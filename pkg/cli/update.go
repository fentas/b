package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fatih/color"
	"github.com/fentas/goodies/progress"
	"github.com/fentas/goodies/templates"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/fentas/b/pkg/binary"
	"github.com/fentas/b/pkg/env"
	"github.com/fentas/b/pkg/gitcache"
	"github.com/fentas/b/pkg/lock"
	"github.com/fentas/b/pkg/provider"
	"github.com/fentas/b/pkg/state"
)

// Test hooks — production code uses the defaults; tests can override.
var (
	syncEnvFunc    = env.SyncEnv
	resolveRefFunc = gitcache.ResolveRef
	ensureCloneF   = gitcache.EnsureClone
	fetchFunc      = gitcache.Fetch
	showFileFunc   = gitcache.ShowFile
	diffNoIndexF   = gitcache.DiffNoIndex
	isTTYFunc      = isTTY
)

// UpdateOptions holds options for the update command
type UpdateOptions struct {
	*SharedOptions
	specifiedArgs     []string                     // args from CLI (binary names or env refs)
	specifiedBinaries []*binary.Binary             // resolved binaries from CLI args
	specifiedEnvRefs  []string                     // resolved env refs from CLI args
	Strategy          string                       // strategy flag override: replace, client, merge
	Safety            string                       // safety flag override: strict, prompt, auto
	DryRun            bool                         // show what would change without writing
	PlanJSON          bool                         // emit machine-readable plan and exit (implies dry-run)
	Yes               bool                         // skip prompt confirmations (treat prompt as auto)
	Rollback          bool                         // rollback to previous commit from lock
	Group             string                       // only update envs in this group
	stdinReader       io.Reader                    // overridden by tests; nil means os.Stdin
	updateBinariesF   func([]*binary.Binary) error // overridden by tests; nil means o.updateBinaries
}

// NewUpdateCmd creates the update subcommand
func NewUpdateCmd(shared *SharedOptions) *cobra.Command {
	o := &UpdateOptions{
		SharedOptions: shared,
	}

	cmd := &cobra.Command{
		Use:     "update [binary|env...]",
		Aliases: []string{"u"},
		Short:   "Update binaries and env files",
		Long:    "Update binaries and env files. If no arguments are given, updates all from b.yaml.",
		Example: templates.Examples(`
			# Update all binaries and envs from b.yaml
			b update

			# Update specific binary
			b update jq

			# Update specific env
			b update github.com/org/infra

			# Force update (overwrite existing)
			b update --force kubectl

			# Update with merge strategy (three-way merge on local changes)
			b update --strategy=merge

			# Update keeping local changes
			b update --strategy=client
		`),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := o.Complete(args); err != nil {
				return err
			}
			if err := o.Validate(); err != nil {
				return err
			}
			return o.Run()
		},
	}

	cmd.Flags().StringVar(&o.Strategy, "strategy", "", "Conflict strategy: replace (default), client, merge")
	cmd.Flags().StringVar(&o.Safety, "safety", "", "Override per-env safety: strict, prompt, auto")
	cmd.Flags().BoolVar(&o.DryRun, "dry-run", false, "Show what would change without writing files")
	cmd.Flags().BoolVar(&o.PlanJSON, "plan-json", false, "Emit a machine-readable plan as JSON (implies --dry-run)")
	cmd.Flags().BoolVarP(&o.Yes, "yes", "y", false, "Skip prompt confirmation (treat prompt as auto)")
	cmd.Flags().BoolVar(&o.Rollback, "rollback", false, "Rollback envs to previous commit from lock")
	cmd.Flags().StringVar(&o.Group, "group", "", "Only update envs in this group")

	return cmd
}

// Complete sets up the update operation
func (o *UpdateOptions) Complete(args []string) error {
	if err := o.ValidateBinaryPath(); err != nil {
		return err
	}

	// Reset from any previous invocation
	o.specifiedArgs = nil
	o.specifiedBinaries = nil
	o.specifiedEnvRefs = nil

	if len(args) == 0 {
		// Update all from config
		if o.Config == nil {
			return fmt.Errorf("no b.yaml configuration found and no binaries specified")
		}
		return nil
	}

	o.specifiedArgs = args

	// Resolve specified args (binaries or env refs) and store them
	for _, arg := range args {
		name, version := parseBinaryArg(arg)

		// Check if it's an env ref
		if o.Config != nil && o.Config.Envs.Get(name) != nil {
			o.specifiedEnvRefs = append(o.specifiedEnvRefs, name)
			continue
		}

		// Resolve binary once and keep the reference
		b, ok := o.GetBinary(name)
		if !ok {
			return fmt.Errorf("unknown binary or env: %s", name)
		}
		if version != "" {
			b.Version = version
		}
		o.specifiedBinaries = append(o.specifiedBinaries, b)
	}

	return nil
}

// Validate checks if the update operation is valid
func (o *UpdateOptions) Validate() error {
	if o.Strategy != "" {
		switch o.Strategy {
		case env.StrategyReplace, env.StrategyClient, env.StrategyMerge:
			// valid
		default:
			return fmt.Errorf("invalid strategy %q: must be replace, client, or merge", o.Strategy)
		}
	}
	// `--safety` materially changes non-TTY behavior, so a typo (e.g.
	// `--safety=autp`) must error rather than silently fall back to
	// prompt. Validation is case-insensitive and trims whitespace,
	// matching the NormalizeSafety contract used by config-loaded
	// values.
	if o.Safety != "" {
		switch strings.ToLower(strings.TrimSpace(o.Safety)) {
		case state.SafetyAuto, state.SafetyPrompt, state.SafetyStrict:
			// valid
		default:
			return fmt.Errorf("invalid --safety value %q: must be %s, %s, or %s",
				o.Safety, state.SafetyStrict, state.SafetyPrompt, state.SafetyAuto)
		}
	}
	return nil
}

// Run executes the update operation
func (o *UpdateOptions) Run() error {
	if len(o.specifiedBinaries) > 0 || len(o.specifiedEnvRefs) > 0 {
		return o.runSpecified()
	}
	return o.runAll()
}

// effectiveDryRun reports whether this update invocation should be
// treated as dry-run by callers that route through this helper.
// `--dry-run` is the obvious case. `--plan-json` is also dry-run-like
// because the user only wants plan output and side effects such as
// file writes, hooks, or lock writes would be surprising.
//
// Today the helper centralizes:
//   - the per-env `cfg.DryRun` decision in `updateEnvs`
//   - the lock-write suppression at the end of `updateEnvs`
//
// New dry-run-like flags should be added here so future code paths
// that rely on this helper interpret them consistently.
func (o *UpdateOptions) effectiveDryRun() bool {
	return o.DryRun || o.PlanJSON
}

// runAll updates all binaries and envs from config.
func (o *UpdateOptions) runAll() error {
	// Update binaries — but NOT in plan-json mode, where binary
	// progress output would corrupt the JSON document on stdout.
	//.
	binariesToUpdate := o.GetBinariesFromConfig()
	if len(binariesToUpdate) > 0 && !o.PlanJSON {
		if err := o.callUpdateBinaries(binariesToUpdate); err != nil {
			return err
		}
	}

	// Update envs
	if o.Config != nil && len(o.Config.Envs) > 0 {
		if err := o.updateEnvs(nil); err != nil {
			return err
		}
	}

	if len(binariesToUpdate) == 0 && (o.Config == nil || len(o.Config.Envs) == 0) {
		// In plan-json mode the human-readable line would corrupt
		// the JSON output. Emit an empty array instead so consumers
		// always get valid JSON.
		if o.PlanJSON {
			return env.RenderPlansJSON(o.IO.Out, nil)
		}
		fmt.Fprintln(o.IO.Out, "No binaries or envs to update")
	}

	return nil
}

// runSpecified updates only the specified binaries/envs.
func (o *UpdateOptions) runSpecified() error {
	if len(o.specifiedBinaries) > 0 {
		// Same as runAll: in plan-json mode binary progress would
		// corrupt stdout. Skip binaries entirely; if the user
		// explicitly listed binaries, warn on stderr so they know
		// the binary args were ignored.
		if o.PlanJSON {
			fmt.Fprintf(o.IO.ErrOut,
				"  warning: --plan-json suppresses binary updates; %d binary arg(s) ignored\n",
				len(o.specifiedBinaries))
		} else if err := o.callUpdateBinaries(o.specifiedBinaries); err != nil {
			return err
		}
	}

	if len(o.specifiedEnvRefs) > 0 {
		if err := o.updateEnvs(o.specifiedEnvRefs); err != nil {
			return err
		}
	}

	return nil
}

// callUpdateBinaries delegates to the test hook or the real implementation.
func (o *UpdateOptions) callUpdateBinaries(binaries []*binary.Binary) error {
	if o.updateBinariesF != nil {
		return o.updateBinariesF(binaries)
	}
	return o.updateBinaries(binaries)
}

// updateEnvs updates env entries from config. If refs is nil, updates all.
func (o *UpdateOptions) updateEnvs(refs []string) error {
	if o.Config == nil {
		return nil
	}

	// Check for dest path conflicts between envs (filtered by refs/group)
	o.checkEnvConflicts(refs, o.Group)

	lockDir := o.LockDir()
	projectRoot := o.ProjectRoot()
	lk, err := lock.ReadLock(lockDir)
	if err != nil {
		return err
	}

	// Tracks any per-env safety gate refusals so we can return a
	// non-zero exit at the end.: silent
	// refusal contradicts the documented "CI pipelines will fail"
	// promise. Per-env apply work continues for non-refused envs so a
	// single bad apple doesn't block the rest of the run.
	var refusedEnvs []string

	// Tracks per-env hard sync failures (network errors, missing
	// previous commits for rollback, real apply errors, etc.) for the
	// same reason: any failure must turn into a non-zero exit so CI
	// notices.
	var failedEnvs []string

	// Collected plans for --plan-json. Previously, emitting one JSON
	// document per env produced concatenated output that isn't valid
	// JSON for typical parsers. We now collect plans in this slice and
	// emit a single JSON array at the end.
	var planJSONOut []*env.Plan

	for _, entry := range o.Config.Envs {
		if refs != nil {
			found := false
			for _, r := range refs {
				if entry.Key == r {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}

		// Filter by group if specified
		if o.Group != "" && entry.Group != o.Group {
			continue
		}

		label := gitcache.RefLabel(entry.Key)
		ref := gitcache.RefBase(entry.Key)

		// Determine strategy: CLI flag > config > default
		strategy := entry.Strategy
		if o.Strategy != "" {
			strategy = o.Strategy
		}

		// Determine safety: CLI flag > config > default (prompt). Defaulted
		// via state.NormalizeSafety so unknown values fall back safely.
		safety := state.NormalizeSafety(entry.Safety)
		if o.Safety != "" {
			safety = state.NormalizeSafety(o.Safety)
		}
		// `--plan-json` implies `--dry-run` — the helper hides this
		// dependency from the rest of the loop so future dry-run-like
		// flags only need to be added in one place.
		isDryRun := o.effectiveDryRun()

		// We only need a dry-run "plan" pass when the safety mode might
		// reject the apply (strict, or prompt where the user has to be
		// asked). For SafetyAuto and for explicit --yes we can go straight
		// to the real apply and render the plan from its result — no
		// double work, no second clone-cache hit.
		needsPlanFirst := isDryRun ||
			safety == state.SafetyStrict ||
			(safety == state.SafetyPrompt && !o.Yes)

		cfg := env.EnvConfig{
			Ref:        ref,
			Label:      label,
			Version:    entry.Version,
			ConfigDir:  lockDir,
			Ignore:     entry.Ignore,
			Strategy:   strategy,
			Files:      entry.Files,
			DryRun:     needsPlanFirst,
			OnPreSync:  entry.OnPreSync,
			OnPostSync: entry.OnPostSync,
			Stdout:     o.IO.Out,
			Stderr:     o.IO.ErrOut,
		}
		// Attach the interactive conflict resolver only when this very
		// pass will actually apply to disk (auto / --yes path). The
		// resolver is interactive — running it during a dry-run plan
		// pass would prompt the user before they've even approved the
		// plan. The plan-first path sets it on the second pass instead.
		if !needsPlanFirst && (strategy == "" || strategy == env.StrategyReplace) && isTTYFunc() {
			cfg.ResolveConflict = o.interactiveConflictResolver(ref, lk)
		}

		lockEntry := lk.FindEnv(ref, label)

		// Handle rollback: use previous commit from lock
		if o.Rollback {
			if lockEntry == nil || lockEntry.PreviousCommit == "" {
				fmt.Fprintf(o.IO.ErrOut, "  %-40s ✗ no previous commit to rollback to\n", entry.Key)
				failedEnvs = append(failedEnvs, entry.Key)
				continue
			}
			cfg.ForceCommit = lockEntry.PreviousCommit
		}

		// First pass — dry-run when we need a plan to gate on, real
		// apply when safety is auto / --yes.
		firstResult, err := syncEnvFunc(cfg, projectRoot, "", lockEntry)
		if err != nil {
			fmt.Fprintf(o.IO.ErrOut, "  %-40s ✗ %s\n", entry.Key, firstLine(err.Error()))
			failedEnvs = append(failedEnvs, entry.Key)
			continue
		}

		if firstResult.Skipped {
			// Plan-json mode: emit an explicit empty plan for the
			// skipped env so consumers can distinguish "all envs are
			// up to date" from "no envs configured" — both used to
			// produce [].
			// Plain dry-run / plan-text mode prints just the cheap
			// "(up to date)" line; no plan table or summary is
			// rendered for skipped envs in text mode.
			if o.PlanJSON {
				planJSONOut = append(planJSONOut, &env.Plan{
					Ref:     ref,
					Label:   label,
					Version: entry.Version,
					Commit:  firstResult.Commit,
				})
			} else {
				fmt.Fprintf(o.IO.Out, "  %-40s %s\n", entry.Key, firstResult.Message)
			}
			continue // don't overwrite lock entry when up-to-date
		}

		plan := env.PlanFromResult(firstResult, lockEntry)

		// --plan-json: collect the plan for batched JSON output below.
		// We never apply in plan-json mode (it implies dry-run).
		if o.PlanJSON {
			planJSONOut = append(planJSONOut, plan)
			continue
		}

		// Header line + plan table.
		fmt.Fprintf(o.IO.Out, "  %-40s %s → %s\n", entry.Key,
			shortCommit(firstResult.PreviousCommit), shortCommit(firstResult.Commit))
		env.RenderPlanText(o.IO.Out, plan)

		// If the first pass was already a real apply (auto / --yes path),
		// just write the lock and move on. No gate, no second SyncEnv.
		if !needsPlanFirst {
			if firstResult.Conflicts > 0 {
				printConflictHint(o.IO.ErrOut, firstResult, projectRoot)
			}
			lk.UpsertEnv(lock.EnvEntry{
				Ref:            firstResult.Ref,
				Label:          firstResult.Label,
				Version:        firstResult.Version,
				Commit:         firstResult.Commit,
				PreviousCommit: firstResult.PreviousCommit,
				Files:          firstResult.Files,
			})
			continue
		}

		// Plan-first path: gate on safety, then apply if approved.
		apply, gateErr := o.gateApply(safety, plan, isDryRun)
		if gateErr != nil {
			fmt.Fprintf(o.IO.ErrOut, "  %-40s ✗ %s\n", entry.Key, gateErr)
			refusedEnvs = append(refusedEnvs, entry.Key)
			continue
		}
		if !apply {
			// Dry-run or user declined the prompt — not an error,
			// just nothing to do for this env.
			continue
		}

		// Second pass: real apply. The gitcache is hot from the first
		// pass, so only the actual file writes hit disk newly.
		//
		// Notably we do NOT attach the per-file
		// interactiveConflictResolver here, even on TTY+replace.
		// In the plan-first flow the user has already approved (or
		// rejected) the entire plan via the safety gate. Attaching
		// the legacy per-file resolver would (a) show a second
		// round of interactive prompts after they already accepted
		// the plan, and (b) create a plan-vs-reality skew because
		// the dry-run pass that produced the plan ran without the
		// resolver, so its destructiveness verdict (and the strict
		// gate's decision) was based on "unconditional overwrite"
		// while the apply pass would actually call the resolver
		// and might pick keep/merge/diff per file.
		//
		// Auto / --yes mode is the only path where the legacy
		// resolver is still attached (handled at the top of the
		// loop where !needsPlanFirst).
		applyCfg := cfg
		applyCfg.DryRun = false
		realResult, err := syncEnvFunc(applyCfg, projectRoot, "", lockEntry)
		if err != nil {
			fmt.Fprintf(o.IO.ErrOut, "  %-40s ✗ %s\n", entry.Key, firstLine(err.Error()))
			failedEnvs = append(failedEnvs, entry.Key)
			continue
		}

		if realResult.Conflicts > 0 {
			printConflictHint(o.IO.ErrOut, realResult, projectRoot)
		}

		lk.UpsertEnv(lock.EnvEntry{
			Ref:            realResult.Ref,
			Label:          realResult.Label,
			Version:        realResult.Version,
			Commit:         realResult.Commit,
			PreviousCommit: realResult.PreviousCommit,
			Files:          realResult.Files,
		})
	}

	if o.PlanJSON {
		// Emit the collected plans as a single JSON array so PR
		// comment bots / CI summary jobs can parse with a single
		// invocation.
		if err := env.RenderPlansJSON(o.IO.Out, planJSONOut); err != nil {
			return err
		}
		// In plan-json mode we still need a non-zero exit when some
		// envs were refused or failed, otherwise automation sees
		// partial plan generation as success.
		return aggregateEnvErrors(refusedEnvs, failedEnvs)
	}
	if o.effectiveDryRun() {
		// Don't write the lock in any dry-run-like mode, but still
		// surface any per-env refusals or failures so CI and users
		// can detect that planning was only partially successful.
		// Routed through effectiveDryRun() so future dry-run-like
		// flags get the same lock-write suppression for free.
		return aggregateEnvErrors(refusedEnvs, failedEnvs)
	}

	if err := lock.WriteLock(lockDir, lk, o.bVersion); err != nil {
		return err
	}
	return aggregateEnvErrors(refusedEnvs, failedEnvs)
}

// aggregateEnvErrors returns a single error summarizing safety refusals
// and hard sync failures, or nil when neither happened. Both lists are
// reported when both are non-empty so the user sees the full story in
// one error message. (refusals: round 1,
// failures: round 5, plan-json path: round 6).
func aggregateEnvErrors(refused, failed []string) error {
	switch {
	case len(refused) > 0 && len(failed) > 0:
		return fmt.Errorf("safety refused %d env(s): %s; %d env(s) failed: %s",
			len(refused), strings.Join(refused, ", "),
			len(failed), strings.Join(failed, ", "))
	case len(refused) > 0:
		return fmt.Errorf("safety refused %d env(s): %s", len(refused), strings.Join(refused, ", "))
	case len(failed) > 0:
		return fmt.Errorf("%d env(s) failed: %s", len(failed), strings.Join(failed, ", "))
	}
	return nil
}

// printConflictHint emits the legacy "needs manual resolution" footer for
// any files that came back with conflict status.
func printConflictHint(w io.Writer, result *env.SyncResult, projectRoot string) {
	fmt.Fprintf(w, "    ⚠ %d file(s) have merge conflicts — resolve manually:\n", result.Conflicts)
	for _, f := range result.Files {
		status := strings.TrimSuffix(f.Status, " (dry-run)")
		if status == "conflict" {
			destPath := f.Dest
			if !filepath.IsAbs(destPath) {
				destPath = filepath.Join(projectRoot, destPath)
			}
			fmt.Fprintf(w, "      %s\n", destPath)
		}
	}
}

// gateApply implements the #125 safety + plan flow. It returns
// (applyNow, error). applyNow=false means "do not run the real sync for
// this env, but the loop should continue".
func (o *UpdateOptions) gateApply(safety string, plan *env.Plan, isDryRun bool) (bool, error) {
	// Dry-run is the simplest case: never apply, never error. The plan
	// has already been printed for the user.
	if isDryRun {
		return false, nil
	}

	destructiveRows := plan.DestructiveRows()
	destructive := len(destructiveRows) > 0

	switch safety {
	case state.SafetyAuto:
		// Trust the upstream and apply silently.
		return true, nil

	case state.SafetyStrict:
		// Refuse if any destructive row is present.
		if destructive {
			return false, destructiveRefusalError("strict safety", destructiveRows,
				"use --safety=prompt or --safety=auto to apply, or --dry-run to preview")
		}
		return true, nil

	case state.SafetyPrompt:
		// --yes overrides the prompt and behaves like auto.
		if o.Yes {
			return true, nil
		}
		// On non-TTY, prompt collapses to strict — refuse on destructive.
		if !isTTYFunc() {
			if destructive {
				return false, destructiveRefusalError("prompt safety on non-TTY", destructiveRows,
					"re-run with --yes or --safety=auto, set safety: auto in b.yaml, or --dry-run to preview")
			}
			return true, nil
		}
		// Interactive prompt.
		return o.confirmApply(destructive), nil
	}

	// Unknown safety value (shouldn't happen — NormalizeSafety covers it,
	// but be defensive).
	return false, fmt.Errorf("unknown safety value %q", safety)
}

// destructiveRefusalError builds the user-facing error string for a
// safety gate refusal. The message has three parts to make CI failure
// triage fast:
//
//  1. Which gate refused (so users know what to flip).
//  2. A breakdown of WHAT is destructive: count by action type, plus
//     the first row's path so the user has a concrete file to look at
//     without scrolling back through the printed plan.
//  3. The recovery hint (which flag/setting to use).
//
// Example:
//
//	strict safety: plan contains 2 overwrite, 1 conflict
//	  (first: hetzner/legacy.yaml) — use --safety=prompt or
//	  --safety=auto to apply, or --dry-run to preview
//
// Per N1 from PR #128 reviewer feedback.
func destructiveRefusalError(gate string, rows []env.PlanRow, recovery string) error {
	if len(rows) == 0 {
		return fmt.Errorf("%s: plan contains destructive changes — %s", gate, recovery)
	}
	// Count by action.
	var overwrite, conflict int
	for _, r := range rows {
		switch r.Action {
		case env.PlanOverwrite:
			overwrite++
		case env.PlanConflict:
			conflict++
		}
	}
	var parts []string
	if overwrite > 0 {
		parts = append(parts, fmt.Sprintf("%d overwrite", overwrite))
	}
	if conflict > 0 {
		parts = append(parts, fmt.Sprintf("%d conflict", conflict))
	}
	breakdown := strings.Join(parts, ", ")
	first := rows[0].Dest
	return fmt.Errorf("%s: plan contains %s (first: %s) — %s", gate, breakdown, first, recovery)
}

// confirmApply prompts the user with a y/N question. Default is N (safer).
func (o *UpdateOptions) confirmApply(destructive bool) bool {
	r := o.stdinReader
	if r == nil {
		r = os.Stdin
	}
	reader := bufio.NewReader(r)
	if destructive {
		fmt.Fprint(o.IO.ErrOut, "  Plan contains destructive changes. Apply? [y/N] ")
	} else {
		fmt.Fprint(o.IO.ErrOut, "  Apply plan? [y/N] ")
	}
	input, err := reader.ReadString('\n')
	if err != nil {
		return false
	}
	input = strings.TrimSpace(strings.ToLower(input))
	return input == "y" || input == "yes"
}

// printFileStatus prints a single file's sync status.
func (o *UpdateOptions) printFileStatus(f lock.LockFile) {
	// Strip dry-run suffix for display matching
	status := strings.TrimSuffix(f.Status, " (dry-run)")
	dryRun := strings.HasSuffix(f.Status, " (dry-run)")
	suffix := ""
	if dryRun {
		suffix = " (dry-run)"
	}

	switch {
	case status == "unchanged":
		fmt.Fprintf(o.IO.Out, "    → %-36s ⊘ unchanged%s\n", f.Dest, suffix)
	case status == "kept":
		fmt.Fprintf(o.IO.Out, "    → %-36s ⊘ kept (local changes preserved)%s\n", f.Dest, suffix)
	case status == "merged":
		fmt.Fprintf(o.IO.Out, "    → %-36s ✓ merged%s\n", f.Dest, suffix)
	case status == "conflict":
		fmt.Fprintf(o.IO.ErrOut, "    → %-36s ✗ conflict (markers inserted)%s\n", f.Dest, suffix)
	case strings.Contains(status, "local changes overwritten"):
		fmt.Fprintf(o.IO.ErrOut, "    → %-36s ⚠ replaced (local changes overwritten)%s\n", f.Dest, suffix)
	default:
		fmt.Fprintf(o.IO.Out, "    → %-36s ✓ replaced%s\n", f.Dest, suffix)
	}
}

// interactiveConflictResolver returns a ConflictFunc that prompts the user per-file.
func (o *UpdateOptions) interactiveConflictResolver(ref string, lk *lock.Lock) env.ConflictFunc {
	r := o.stdinReader
	if r == nil {
		r = os.Stdin
	}
	reader := bufio.NewReader(r)
	return func(sourcePath, destPath string) string {
		for {
			fmt.Fprintf(o.IO.ErrOut, "    %s has local changes.\n", destPath)
			fmt.Fprintf(o.IO.ErrOut, "      [r]eplace  [k]eep  [m]erge  [d]iff > ")

			input, err := reader.ReadString('\n')
			if err != nil {
				return env.StrategyReplace // default on read error
			}
			input = strings.TrimSpace(strings.ToLower(input))

			switch input {
			case "r", "replace":
				return env.StrategyReplace
			case "k", "keep":
				return env.StrategyClient
			case "m", "merge":
				return env.StrategyMerge
			case "d", "diff":
				o.showDiff(ref, sourcePath, destPath, lk)
				continue // re-prompt
			default:
				fmt.Fprintf(o.IO.ErrOut, "      Invalid choice. Try r, k, m, or d.\n")
				continue
			}
		}
	}
}

// showDiff shows a unified diff between local file and upstream content.
func (o *UpdateOptions) showDiff(ref, sourcePath, destPath string, lk *lock.Lock) {
	local, err := os.ReadFile(destPath)
	if err != nil {
		fmt.Fprintf(o.IO.ErrOut, "      Error reading local file: %v\n", err)
		return
	}

	// Find the env entry to get the new commit
	// We can't easily get the upstream content here without the commit,
	// so we show local vs lock SHA for context
	fmt.Fprintf(o.IO.ErrOut, "\n--- local: %s\n", destPath)
	fmt.Fprintf(o.IO.ErrOut, "+++ upstream: %s:%s\n", ref, sourcePath)

	// Read upstream from cache (best effort — use HEAD of the cache)
	baseRef := gitcache.RefBase(ref)
	url := gitcache.GitURL(ref)
	commit, err := resolveRefFunc(url, "")
	if err != nil {
		fmt.Fprintf(o.IO.ErrOut, "      Cannot resolve upstream for diff: %v\n", err)
		return
	}

	cacheRoot := gitcache.DefaultCacheRoot()
	if err := ensureCloneF(cacheRoot, baseRef, url); err != nil {
		fmt.Fprintf(o.IO.ErrOut, "      Cannot clone upstream for diff: %v\n", err)
		return
	}
	if err := fetchFunc(cacheRoot, baseRef, commit); err != nil {
		fmt.Fprintf(o.IO.ErrOut, "      Cannot fetch upstream for diff: %v\n", err)
		return
	}

	upstream, err := showFileFunc(cacheRoot, baseRef, commit, sourcePath)
	if err != nil {
		fmt.Fprintf(o.IO.ErrOut, "      Cannot read upstream file for diff: %v\n", err)
		return
	}

	diff, err := diffNoIndexF(local, upstream, "local", "upstream")
	if err != nil {
		fmt.Fprintf(o.IO.ErrOut, "      Error computing diff: %v\n", err)
		return
	}

	if diff == "" {
		fmt.Fprintf(o.IO.ErrOut, "      (no differences)\n\n")
	} else {
		fmt.Fprintf(o.IO.ErrOut, "%s\n", diff)
	}
}

// isTTY returns true if stdout is a terminal (not piped/redirected).
func isTTY() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}

// updateBinaries updates the specified binaries with progress tracking
func (o *UpdateOptions) updateBinaries(binaries []*binary.Binary) error {
	// Pre-resolve ambiguous assets before starting progress bars.
	// Updates always attempt to check for newer versions, so always pre-resolve.
	resolveAmbiguousAssets(binaries, o.Quiet, o.IO)

	// Wire fallback selector for any remaining ambiguous cases
	var promptMu sync.Mutex
	for _, b := range binaries {
		if b.AutoDetect && b.SelectAsset == nil {
			b.SelectAsset = guardedAssetSelector(&promptMu, b, o.Quiet, o.IO)
		}
	}

	// Load the current lockfile so digest-resolver providers (docker://, oci://)
	// can short-circuit when the tag's manifest digest hasn't changed upstream.
	// Not having a lock (fresh project) or read errors are non-fatal: we just
	// fall through to the normal update path.
	var lk *lock.Lock
	if readLk, err := lock.ReadLock(o.LockDir()); err == nil {
		lk = readLk
	}

	// Resolve digests once per digest-capable binary up-front. We reuse these
	// values both for the skip decision and the post-update lock refresh, so
	// each registry HEAD only happens once per run.
	//
	// ResolveDigest distinguishes two error shapes:
	//   - transient/registry/auth/404 → ("", nil): treat as "don't know" and
	//     fall through to a normal download.
	//   - malformed ref (hard error)  → ("", err): surface as a warning so
	//     the user sees the actionable problem instead of seeing b silently
	//     fall back to re-downloading a broken ref.
	freshDigests := make(map[string]string, len(binaries))
	for _, b := range binaries {
		if !b.AutoDetect || b.ProviderRef == "" {
			continue
		}
		if dr, ok := providerDigestResolver(b.ProviderRef); ok {
			d, err := dr.ResolveDigest(b.ProviderRef, b.Version)
			if err != nil {
				fmt.Fprintf(o.IO.ErrOut, "Warning: resolving digest for %s (%s): %v\n", b.Name, b.ProviderRef, err)
				continue
			}
			if d != "" {
				freshDigests[b.Name] = d
			}
		}
	}
	// preSHA lets refreshLockDigests tell which binaries actually re-downloaded:
	// if the file's SHA256 changed from this value, the download succeeded.
	preSHA := make(map[string]string, len(binaries))
	for _, b := range binaries {
		if b.File == "" {
			continue
		}
		if h, err := lock.SHA256File(b.File); err == nil {
			preSHA[b.Name] = h
		}
	}

	// Track download outcomes per binary so the post-update lock refresh
	// can distinguish "skipped on purpose" (digest already matched, safe
	// to keep the locked digest in sync) from "attempted + failed" (don't
	// advance the lock, otherwise future skips would be based on a
	// digest whose on-disk file we never actually installed).
	var outcomeMu sync.Mutex
	downloadAttempted := make(map[string]bool, len(binaries))
	downloadFailed := make(map[string]bool, len(binaries))

	wg := sync.WaitGroup{}
	pw := progress.NewWriter(progress.StyleDownload, o.IO.Out)
	pw.Style().Visibility.Percentage = true
	go pw.Render()
	defer pw.Stop()

	for _, b := range binaries {
		wg.Add(1)

		go func(b *binary.Binary) {
			defer wg.Done()

			name := b.Name
			if b.Alias != "" {
				name = b.Alias
			}

			tracker := pw.AddTracker(fmt.Sprintf("Updating %s", name), 0)
			b.Tracker = tracker
			b.Writer = pw

			var err error
			attempted := false
			switch {
			case o.Force:
				attempted = true
				err = b.DownloadBinary()
			case digestMatchesLock(b, lk, freshDigests[b.Name]):
				// Manifest digest matches the locked one: upstream hasn't
				// moved since the last install. Nothing to do.
				err = nil
			case b.AutoDetect && isDigestProvider(b.ProviderRef):
				// Digest-resolver provider but either no lock digest, or the
				// digest differs — always re-download. Bypasses
				// EnsureBinary's Version==Enforced short-circuit that would
				// otherwise keep the old binary for mutable tags like 'cli'.
				attempted = true
				err = b.DownloadBinary()
			default:
				// EnsureBinary's internal skip check may or may not
				// download; treat it as "attempted" only on error so a
				// failed preset update doesn't poison the lock either.
				err = b.EnsureBinary(true)
				if err != nil {
					attempted = true
				}
			}
			outcomeMu.Lock()
			downloadAttempted[b.Name] = attempted
			downloadFailed[b.Name] = attempted && err != nil
			outcomeMu.Unlock()

			doneLabel := name + " updated"
			if b.Alias != "" {
				doneLabel = b.Alias + " (" + color.New(color.FgYellow).Sprint(b.Name) + ") updated"
			}
			progress.ProgressDone(
				b.Tracker,
				doneLabel,
				err,
			)
		}(b)
	}

	wg.Wait()
	time.Sleep(200 * time.Millisecond)

	// Record freshly-resolved digests in the lockfile so subsequent `b update`
	// runs can skip when the tag hasn't moved. Only touches digest-resolver
	// providers; non-digest entries and the rest of the lock are left alone.
	if !o.effectiveDryRun() {
		o.refreshLockDigests(binaries, freshDigests, preSHA, downloadFailed)
	}
	return nil
}

// refreshLockDigests re-reads b.lock and updates the Digest + SHA256 for
// every digest-resolver binary that actually changed on disk during this
// update run. Failed downloads are identified via downloadFailed and
// skipped entirely — otherwise the lock could advance to an upstream
// digest whose bytes never made it to disk, and a future `b update`
// would wrongly skip.
//
// freshDigests is reused from updateBinaries so each registry is HEAD-ed
// only once per run. Best-effort: any error is surfaced as a warning
// without failing the update — the binaries are already on disk.
func (o *UpdateOptions) refreshLockDigests(binaries []*binary.Binary, freshDigests, preSHA map[string]string, downloadFailed map[string]bool) {
	lk, err := lock.ReadLock(o.LockDir())
	if err != nil {
		fmt.Fprintf(o.IO.ErrOut, "Warning: can't read b.lock to refresh digests: %v\n", err)
		return
	}
	changed := false
	for _, b := range binaries {
		if !b.AutoDetect || b.ProviderRef == "" || b.File == "" {
			continue
		}
		if _, ok := providerDigestResolver(b.ProviderRef); !ok {
			continue
		}
		// Download was attempted and failed: don't touch the lock. The
		// previous digest/SHA still match the previous binary, which is
		// what's on disk.
		if downloadFailed[b.Name] {
			continue
		}
		entry := lk.FindBinary(b.Name)
		if entry == nil {
			continue
		}
		// Use the captured digest from the update pre-resolve; if we
		// didn't have one (transient registry failure or auth issue),
		// don't touch the lock — keeping the previous digest lets a
		// future `b update` still consult it instead of being forced
		// to re-download blindly.
		digest := freshDigests[b.Name]
		if digest == "" {
			continue
		}
		hash, err := lock.SHA256File(b.File)
		if err != nil {
			continue
		}
		// Unchanged on-disk hash means the download branch was NOT taken
		// for this binary (we already skipped failed downloads above).
		// The binary on disk is still the one the lock pointed at, so
		// both Digest and SHA256 remain valid — only refresh Digest to
		// whatever the registry reports now, so future runs can detect
		// the next upstream change (SHA256 would just duplicate the
		// on-disk state).
		if pre, ok := preSHA[b.Name]; ok && pre == hash {
			if entry.Digest != digest {
				entry.Digest = digest
				changed = true
			}
			continue
		}
		// Download branch ran AND the on-disk bytes moved: both Digest
		// and SHA256 may advance.
		if entry.Digest != digest {
			entry.Digest = digest
			changed = true
		}
		if entry.SHA256 != hash {
			entry.SHA256 = hash
			changed = true
		}
	}
	if !changed {
		return
	}
	if err := lock.WriteLock(o.LockDir(), lk, o.bVersion); err != nil {
		fmt.Fprintf(o.IO.ErrOut, "Warning: can't write b.lock digest updates: %v\n", err)
	}
}

// providerDigestResolver returns the provider behind ref as a
// DigestResolver, or false if the provider isn't digest-capable.
func providerDigestResolver(ref string) (provider.DigestResolver, bool) {
	p, err := provider.Detect(ref)
	if err != nil {
		return nil, false
	}
	dr, ok := p.(provider.DigestResolver)
	return dr, ok
}

// isDigestProvider reports whether the provider behind ref implements
// DigestResolver (currently docker://, oci://).
func isDigestProvider(ref string) bool {
	_, ok := providerDigestResolver(ref)
	return ok
}

// digestMatchesLock reports whether the freshly-resolved digest for b
// (supplied by the caller so we don't HEAD the registry twice) matches
// the one recorded in the lockfile for the same source. Returns false
// for every "can't prove it" case — missing lock, missing stored
// digest, mismatched Source (user changed the docker/oci ref or
// in-container path but kept the derived binary name), no fresh
// digest, non-digest provider, absent ProviderRef — so the caller
// still attempts an update.
func digestMatchesLock(b *binary.Binary, lk *lock.Lock, fresh string) bool {
	if lk == nil || b.ProviderRef == "" || fresh == "" {
		return false
	}
	entry := lk.FindBinary(b.Name)
	if entry == nil || entry.Digest == "" {
		return false
	}
	if entry.Source != b.ProviderRef {
		return false
	}
	return entry.Digest == fresh
}

// checkEnvConflicts detects when two env entries write to overlapping dest paths.
// It checks the lock file for existing dest paths, filtered by refs and group.
func (o *UpdateOptions) checkEnvConflicts(refs []string, group string) {
	if o.Config == nil || len(o.Config.Envs) < 2 {
		return
	}

	lk, _ := lock.ReadLock(o.LockDir())
	if lk == nil {
		return
	}

	// Build set of active env keys (matching refs/group filter)
	activeKeys := make(map[string]bool)
	for _, entry := range o.Config.Envs {
		if refs != nil {
			found := false
			for _, r := range refs {
				if entry.Key == r {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		if group != "" && entry.Group != group {
			continue
		}
		ref := gitcache.RefBase(entry.Key)
		label := gitcache.RefLabel(entry.Key)
		key := ref
		if label != "" {
			key += "#" + label
		}
		activeKeys[key] = true
	}

	// Build a map of dest → env ref for active env entries in the lock
	type destOwner struct {
		ref  string
		path string // source path
	}
	destMap := make(map[string]destOwner)

	for _, envEntry := range lk.Envs {
		key := envEntry.Ref
		if envEntry.Label != "" {
			key += "#" + envEntry.Label
		}
		if !activeKeys[key] {
			continue
		}
		for _, f := range envEntry.Files {
			if existing, ok := destMap[f.Dest]; ok {
				fmt.Fprintf(o.IO.ErrOut, "  ⚠ Conflict: %s is written by both %s (%s) and %s (%s)\n",
					f.Dest, existing.ref, existing.path, key, f.Path)
				fmt.Fprintf(o.IO.ErrOut, "    Hint: use 'dest' or 'ignore' in b.yaml to resolve\n")
			}
			destMap[f.Dest] = destOwner{ref: key, path: f.Path}
		}
	}
}
