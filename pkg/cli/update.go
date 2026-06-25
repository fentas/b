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
	EnvsOnly          bool                         // update envs only, skip binaries
	BinariesOnly      bool                         // update binaries only, skip envs
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

			# Update specific env (paste the key 'b env status' prints)
			b update github.com/org/infra
			b update git@github.com:org/infra#main

			# Force env resolution for a key that is also a valid binary ref
			# by appending '#' (works for any env, incl. label-less ones)
			b update github.com/org/infra#

			# Update only envs (skip the toolchain), optionally by group
			b update --envs-only
			b update --group dev

			# Update only binaries (skip env sync)
			b update --binaries-only

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
	cmd.Flags().StringVar(&o.Group, "group", "", "Only update envs in this group (implies --envs-only)")
	cmd.Flags().BoolVar(&o.EnvsOnly, "envs-only", false, "Only update envs, skip binaries")
	cmd.Flags().BoolVar(&o.BinariesOnly, "binaries-only", false, "Only update binaries, skip envs")

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

	// Resolve specified args (binaries or env refs) and store them.
	for _, arg := range args {
		envKey, b, err := o.resolveUpdateArg(arg)
		if err != nil {
			return err
		}
		if envKey != "" {
			o.specifiedEnvRefs = append(o.specifiedEnvRefs, envKey)
			continue
		}
		o.specifiedBinaries = append(o.specifiedBinaries, b)
	}

	return nil
}

// resolveUpdateArg maps a single CLI arg to either an env (returns its config
// key) or a binary (returns the resolved *binary.Binary). Exactly one of the
// two is non-empty on success.
//
// The arg addresses two namespaces (binaries and envs) whose grammars overlap,
// so resolution is driven by config membership and an env marker rather than by
// guessing from shape (issue #166):
//
//	docker:// / oci:// / go://   → always a binary ('#' there is a path/module
//	                               char, never an env label)
//	local path or contains '#'   → env marker: resolve as env, error if no such
//	                               env (the user was explicit)
//	otherwise                    → env-exact first (so the literal key printed by
//	                               `b env status` round-trips, incl. SSH `git@…`
//	                               keys whose '@' the binary parser would split),
//	                               then binary.
func (o *UpdateOptions) resolveUpdateArg(arg string) (envKey string, b *binary.Binary, err error) {
	// Env marker: a local path, or a '#' outside a binary-only protocol. The
	// user was explicit, so short handles (repo basename / org/repo tail) are
	// allowed in addition to exact/canonical keys.
	if !hasBinaryProto(arg) && (isLocalRefPath(arg) || strings.Contains(arg, "#")) {
		key, ok, mErr := o.matchEnv(arg, true)
		if mErr != nil {
			return "", nil, mErr
		}
		if !ok {
			return "", nil, fmt.Errorf("unknown env: %s", arg)
		}
		return key, nil, nil
	}

	// No marker: try envs by exact/canonical key before falling into binary
	// space — otherwise a ref like github.com/org/infra that is an env would be
	// hijacked by the github provider convention and fail as a binary. Short
	// handles are NOT allowed here so a bare name can never shadow a binary;
	// they require the explicit '#'/path marker handled above.
	if key, ok, mErr := o.matchEnv(arg, false); mErr != nil {
		return "", nil, mErr
	} else if ok {
		return key, nil, nil
	}

	name, version := parseBinaryArg(arg)
	bin, ok := o.GetBinary(name)
	if !ok {
		return "", nil, o.unknownArgError(arg)
	}
	if version != "" {
		bin.Version = version
	}
	// If this resolved to an ad-hoc provider binary (not in config) while a
	// configured env targets the same repo, the user probably meant the env
	// (e.g. typed the https path of an SSH-keyed env — issue #166). The ad-hoc
	// binary update is still valid, so just say so rather than refuse.
	if !o.isConfigBinary(name) {
		if hint := o.envHint(arg); hint != "" {
			fmt.Fprintf(o.IO.ErrOut, "  note: updating %q as a binary; %s\n", name, hint)
		}
	}
	return "", bin, nil
}

// envUpdateHint returns a copy-pasteable command that resolves to the env keyed
// `key`. It always suggests the exact key (which round-trips), and adds a short
// handle only when that handle is unambiguous — otherwise the short form would
// itself fail to resolve. It deliberately does NOT suggest "append '#' to what
// you typed" — for an https arg pointing at an SSH-keyed env, that form does not
// match (issue #166 review).
func (o *UpdateOptions) envUpdateHint(key string) string {
	hint := fmt.Sprintf("b update %q", key)
	if short := repoTail(key); short != "" && o.shortHandleUnique(short) {
		hint += fmt.Sprintf(" (or short: b update %s#)", short)
	}
	return hint
}

// shortHandleUnique reports whether exactly one configured env has the given
// "org/repo" tail — i.e. whether `<tail>#` would resolve unambiguously.
func (o *UpdateOptions) shortHandleUnique(tail string) bool {
	if o.Config == nil {
		return false
	}
	n := 0
	for _, e := range o.Config.Envs {
		if repoTail(e.Key) == tail {
			n++
		}
	}
	return n == 1
}

// isConfigBinary reports whether name is a known binary — a preset or an entry
// in b.yaml's binaries — as opposed to an ad-hoc provider ref synthesized on
// the fly.
func (o *UpdateOptions) isConfigBinary(name string) bool {
	if _, ok := o.lookup[name]; ok {
		return true
	}
	if o.Config == nil {
		return false
	}
	for _, lb := range o.Config.Binaries {
		if lb.Name == name {
			return true
		}
	}
	return false
}

// matchEnv resolves arg to a configured env key. It tries, in order: the
// literal string (so the exact key `b env status` prints round-trips), a
// canonical form with any trailing version stripped and label normalized, and
// — only when allowShort is set — a short handle that suffix-matches an env's
// repo path (e.g. "lok8s" or "org/lok8s" → "git@github.com:org/lok8s#main").
// An ambiguous short handle returns an error listing the candidate keys.
func (o *UpdateOptions) matchEnv(arg string, allowShort bool) (string, bool, error) {
	if o.Config == nil {
		return "", false, nil
	}
	if e := o.Config.Envs.Get(arg); e != nil {
		return e.Key, true, nil
	}
	if key := canonicalEnvKey(arg); key != arg {
		if e := o.Config.Envs.Get(key); e != nil {
			return e.Key, true, nil
		}
	}
	if allowShort {
		key, candidates := o.matchEnvShort(arg)
		if key != "" {
			return key, true, nil
		}
		if len(candidates) > 1 {
			return "", false, fmt.Errorf("ambiguous env %q matches %d keys: %s — use the full key",
				arg, len(candidates), strings.Join(candidates, ", "))
		}
	}
	return "", false, nil
}

// matchEnvShort matches arg as a short handle against configured env keys: the
// arg's cleaned repo path must be a trailing segment-suffix of an env's. So
// "lok8s" or "org/lok8s" both reach an env keyed "git@github.com:org/lok8s".
// Returns (key, nil) on a unique hit, or ("", candidates) when zero or
// multiple envs match so the caller can report ambiguity.
func (o *UpdateOptions) matchEnvShort(arg string) (string, []string) {
	want := gitcache.RepoPath(arg)
	if len(want) == 0 {
		return "", nil
	}
	var matches []string
	for _, e := range o.Config.Envs {
		if pathHasSuffix(gitcache.RepoPath(e.Key), want) {
			matches = append(matches, e.Key)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	return "", matches
}

// pathHasSuffix reports whether suf is a trailing whole-segment suffix of full
// (case-insensitive), e.g. ["org","repo"] is a suffix of ["host","org","repo"].
func pathHasSuffix(full, suf []string) bool {
	if len(suf) == 0 || len(suf) > len(full) {
		return false
	}
	for i := 1; i <= len(suf); i++ {
		if !strings.EqualFold(full[len(full)-i], suf[len(suf)-i]) {
			return false
		}
	}
	return true
}

// unknownArgError builds the not-found error, adding a "did you mean env …"
// hint when a configured env points at the same repo via a different ref form
// (e.g. the user typed the https path for an SSH-keyed env — issue #166).
func (o *UpdateOptions) unknownArgError(arg string) error {
	if hint := o.envHint(arg); hint != "" {
		return fmt.Errorf("unknown binary or env: %s — did you mean %s", arg, hint)
	}
	return fmt.Errorf("unknown binary or env: %s", arg)
}

// suggestEnvs returns all configured env keys that share arg's trailing
// org/repo path (transport-independent), so callers can hint at the env(s) the
// user may have meant. Empty when none match. Used only to enrich messages —
// it never changes resolution.
func (o *UpdateOptions) suggestEnvs(arg string) []string {
	if o.Config == nil {
		return nil
	}
	want := repoTail(arg)
	if want == "" {
		return nil
	}
	var keys []string
	for _, e := range o.Config.Envs {
		if repoTail(e.Key) == want {
			keys = append(keys, e.Key)
		}
	}
	return keys
}

// envHint builds a "did you mean env" clause for the env(s) matching arg, or ""
// when none match. A single match names the key with a copy-paste command; when
// several envs share the same repo tail (e.g. github + gitlab mirrors) it lists
// the candidates instead of arbitrarily picking the first — pointing at the
// wrong key would be misleading (issue #166 review).
func (o *UpdateOptions) envHint(arg string) string {
	keys := o.suggestEnvs(arg)
	switch len(keys) {
	case 0:
		return ""
	case 1:
		return fmt.Sprintf("env %q targets the same repo — to update it run: %s", keys[0], o.envUpdateHint(keys[0]))
	default:
		return fmt.Sprintf("%d envs target the same repo (%s) — pass the exact key to update one",
			len(keys), strings.Join(keys, ", "))
	}
}

// repoTail returns the "org/repo" tail of a git ref, lowercased and transport-
// independent, so https and SSH forms of the same repo compare equal. Returns
// "" when the ref has fewer than two path segments.
func repoTail(ref string) string {
	parts := gitcache.RepoPath(ref)
	if len(parts) < 2 {
		return ""
	}
	return strings.ToLower(strings.Join(parts[len(parts)-2:], "/"))
}

// canonicalEnvKey normalizes a ref to the form used as an env map key: the ref
// base (version stripped) plus "#label" when a label is present. Local paths
// are returned as-is (their '#'/'@' are literal path characters).
func canonicalEnvKey(arg string) string {
	base := gitcache.RefBase(arg)
	if label := gitcache.RefLabel(arg); label != "" {
		return base + "#" + label
	}
	return base
}

// hasBinaryProto reports whether s carries a protocol for which a '#' must NOT
// be treated as an env-label marker. docker:// / oci:// / go:// are binary-only.
// git:// is ambiguous (a git-sourced binary's in-repo file path may legally
// contain '#', e.g. git://repo:bin/we#rd), so we exclude it from the marker
// rule too; git:// envs are still addressable by their exact/canonical key.
func hasBinaryProto(s string) bool {
	return strings.HasPrefix(s, "docker://") ||
		strings.HasPrefix(s, "oci://") ||
		strings.HasPrefix(s, "go://") ||
		strings.HasPrefix(s, "git://")
}

// isLocalRefPath reports whether s is a local filesystem ref (an env source),
// matching gitcache's own local-path detection.
func isLocalRefPath(s string) bool {
	return strings.HasPrefix(s, "/") ||
		strings.HasPrefix(s, "./") ||
		strings.HasPrefix(s, "../")
}

// Validate checks if the update operation is valid
func (o *UpdateOptions) Validate() error {
	// Scope flags select which namespace the "update all" form touches; they
	// are contradictory together and meaningless once specific args narrow the
	// scope already.
	if o.EnvsOnly && o.BinariesOnly {
		return fmt.Errorf("--envs-only and --binaries-only are mutually exclusive")
	}
	if o.BinariesOnly && o.Group != "" {
		return fmt.Errorf("--group filters envs and cannot be combined with --binaries-only")
	}
	if o.BinariesOnly && o.PlanJSON {
		return fmt.Errorf("--plan-json describes env changes only and has no effect with --binaries-only")
	}
	// --group is env-only, so naming binaries/envs explicitly contradicts it the
	// same way the scope flags do — and a non-matching --group would otherwise
	// silently drop the named env to a no-op.
	if (o.EnvsOnly || o.BinariesOnly || o.Group != "") && len(o.specifiedArgs) > 0 {
		return fmt.Errorf("--group/--envs-only/--binaries-only apply to 'b update' with no arguments; remove them when naming binaries or envs explicitly")
	}
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
	// Scope which namespaces this run touches. `group` is an env-only concept,
	// so --group implies env scope (binaries have no groups — issue #166).
	doBinaries := !o.EnvsOnly && o.Group == ""
	doEnvs := !o.BinariesOnly

	// Update binaries — but NOT in plan-json mode, where binary
	// progress output would corrupt the JSON document on stdout.
	var binariesToUpdate []*binary.Binary
	if doBinaries {
		binariesToUpdate = o.GetBinariesFromConfig()
		if len(binariesToUpdate) > 0 && !o.PlanJSON {
			if err := o.callUpdateBinaries(binariesToUpdate); err != nil {
				return err
			}
		}
	}

	// Update envs
	hasEnvs := o.Config != nil && len(o.Config.Envs) > 0
	if doEnvs && hasEnvs {
		if err := o.updateEnvs(nil); err != nil {
			return err
		}
	}

	// In plan-json mode binaries never produce output, so they count as
	// "nothing" for the empty-array fallback — otherwise a binaries-only project
	// run with --plan-json would print no JSON at all (the opposite of the
	// "consumers always get valid JSON" contract).
	nothingBinaries := !doBinaries || len(binariesToUpdate) == 0 || o.PlanJSON
	nothingEnvs := !doEnvs || !hasEnvs
	if nothingBinaries && nothingEnvs {
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

	// Reconcile the lock to the config before writing: drop env entries no
	// longer in b.yaml (e.g. an env whose label was renamed). Such orphans are
	// never re-synced (FindEnv/UpsertEnv key on the configured ref+label) yet
	// `b verify` still checks them, so their stale hashes report mismatches
	// forever for dests a live env now owns.
	o.pruneOrphanedEnvs(lk)

	if err := lock.WriteLock(lockDir, lk, o.bVersion); err != nil {
		return err
	}
	return aggregateEnvErrors(refusedEnvs, failedEnvs)
}

// pruneOrphanedEnvs removes lock env entries whose (ref,label) no longer
// corresponds to any configured env, returning the number removed. The keys are
// derived exactly as updateEnvs writes them (gitcache.RefBase/RefLabel of the
// config key), so a live config env — even one not synced this run (group/arg
// filtered, or up-to-date) — is never pruned. No-op when no env is configured.
func (o *UpdateOptions) pruneOrphanedEnvs(lk *lock.Lock) int {
	if lk == nil || o.Config == nil || len(o.Config.Envs) == 0 {
		return 0
	}
	configured := make(map[string]bool, len(o.Config.Envs))
	for _, e := range o.Config.Envs {
		configured[gitcache.RefBase(e.Key)+"\x00"+gitcache.RefLabel(e.Key)] = true
	}
	kept := lk.Envs[:0]
	var pruned []string
	for _, e := range lk.Envs {
		if configured[e.Ref+"\x00"+e.Label] {
			kept = append(kept, e)
			continue
		}
		key := e.Ref
		if e.Label != "" {
			key += "#" + e.Label
		}
		pruned = append(pruned, key)
	}
	if len(pruned) == 0 {
		return 0
	}
	lk.Envs = kept
	fmt.Fprintf(o.IO.ErrOut, "  pruned %d orphaned env entry(ies) from b.lock (not in b.yaml): %s\n",
		len(pruned), strings.Join(pruned, ", "))
	return len(pruned)
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
	// ReadLock returns an empty Lock (not nil) when b.lock is missing, so a
	// fresh project is the no-op case — every FindBinary returns nil and the
	// digest-match check simply falls through. A real read/parse error is
	// also non-fatal here: we drop to nil and fall through to the normal
	// update path.
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
	// digestCapable tracks which binaries have a digest-aware provider.
	// Only these get digest resolution AND pre-SHA hashing — skipping
	// SHA256File for everything else keeps `b update` cheap on projects
	// that don't use docker:// / oci://.
	digestCapable := make(map[string]bool, len(binaries))
	freshDigests := make(map[string]string, len(binaries))
	for _, b := range binaries {
		if !b.AutoDetect || b.ProviderRef == "" {
			continue
		}
		dr, ok := providerDigestResolver(b.ProviderRef)
		if !ok {
			continue
		}
		digestCapable[b.Name] = true
		d, err := dr.ResolveDigest(b.ProviderRef, b.Version)
		if err != nil {
			fmt.Fprintf(o.IO.ErrOut, "Warning: resolving digest for %s (%s): %v\n", b.Name, b.ProviderRef, err)
			continue
		}
		if d != "" {
			freshDigests[b.Name] = d
		}
	}
	// preSHA lets refreshLockDigests tell which binaries actually re-downloaded:
	// if the file's SHA256 changed from this value, the download succeeded.
	// Only compute for digest-capable binaries — hashing on-disk bytes is
	// otherwise wasted work for github/gitlab/go provider binaries we'll
	// never consult preSHA for.
	preSHA := make(map[string]string, len(digestCapable))
	for _, b := range binaries {
		if b.File == "" || !digestCapable[b.Name] {
			continue
		}
		if h, err := lock.SHA256File(b.File); err == nil {
			preSHA[b.Name] = h
		}
	}

	// Track which downloads were attempted and failed so the post-update
	// lock refresh can avoid advancing Digest/SHA256 for binaries whose
	// on-disk bytes didn't actually update.
	var outcomeMu sync.Mutex
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
			downloaded := false
			switch {
			case o.Force:
				attempted = true
				err = b.DownloadBinary()
				downloaded = err == nil
			case digestMatchesLock(b, lk, freshDigests[b.Name]) && b.BinaryExists():
				// Manifest digest matches the locked one AND the binary
				// is actually on disk: upstream hasn't moved since the
				// last install. Nothing to do. The BinaryExists() check
				// catches the case where the user deleted the file —
				// the digest match alone is not enough to declare the
				// binary up to date.
				err = nil
			case b.AutoDetect && isDigestProvider(b.ProviderRef):
				// Digest-resolver provider but either no lock digest, or the
				// digest differs — always re-download. Bypasses
				// EnsureBinary's Version==Enforced short-circuit that would
				// otherwise keep the old binary for mutable tags like 'cli'.
				attempted = true
				err = b.DownloadBinary()
				downloaded = err == nil
			default:
				// EnsureBinary's internal skip check may or may not
				// download; treat it as "attempted" only on error so a
				// failed preset update doesn't poison the lock either.
				// Detect whether a download happened via two signals:
				//   - binary was missing → any successful EnsureBinary
				//     must have downloaded it
				//   - binary existed → compare SHA before/after
				// Only compute hashes when a hook might run — avoids
				// O(file-size) work for binaries without hooks.
				wasMissing := !b.BinaryExists()
				var beforeHash string
				needHookCheck := b.OnPost != "" && !o.effectiveDryRun()
				if !wasMissing && needHookCheck {
					beforeHash, _ = lock.SHA256File(b.BinaryPath())
				}
				err = b.EnsureBinary(true)
				if err != nil {
					attempted = true
				} else if wasMissing {
					downloaded = true
				} else if needHookCheck {
					// If we can't hash, assume the file changed — better
					// to run the hook unnecessarily than silently skip it
					// due to an unrelated I/O error.
					afterHash, shaErr := lock.SHA256File(b.BinaryPath())
					if shaErr != nil || beforeHash == "" {
						downloaded = true
					} else {
						downloaded = beforeHash != afterHash
					}
				}
			}
			outcomeMu.Lock()
			downloadFailed[b.Name] = attempted && err != nil
			outcomeMu.Unlock()

			// Run onPost hook only when a download actually changed the
			// binary, and not in dry-run mode.
			if downloaded && b.OnPost != "" && !o.effectiveDryRun() {
				if hookErr := binary.RunHook(b.OnPost, o.ProjectRoot(), "update", b.Name, b.Version, b.BinaryPath(), o.IO.ErrOut, o.IO.ErrOut); hookErr != nil {
					fmt.Fprintf(o.IO.ErrOut, "Warning: onPost hook for %s failed: %v\n", b.Name, hookErr)
				}
			}

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
		// If the lock entry's Source drifted from the configured
		// ProviderRef (e.g. the user edited b.yaml in place and changed
		// the ref but kept the derived binary name), don't rewrite the
		// entry's Digest/SHA256 here — that would mix a stale Source
		// with fresh content identity and break future skip checks via
		// digestMatchesLock. An 'install --add' flow is the correct
		// way to update Source/Provider; this refresh is conservative
		// on purpose.
		if entry.Source != "" && entry.Source != b.ProviderRef {
			continue
		}
		hash, err := lock.SHA256File(b.File)
		if err != nil {
			continue
		}

		// Digest and SHA256 are independent: a transient HEAD failure
		// (freshDigests empty) doesn't stop us from refreshing SHA256
		// when the download still succeeded via cache/retry. Conversely,
		// a pure skip (digest matched, hash unchanged) refreshes only
		// Digest. Decide each one on its own.
		digest := freshDigests[b.Name]
		pre := preSHA[b.Name]
		hashChanged := pre != hash

		// SHA256: refresh whenever the on-disk bytes actually moved.
		// downloadFailed is already filtered out above, so a changed
		// hash here proves a successful download.
		if hashChanged && entry.SHA256 != hash {
			entry.SHA256 = hash
			changed = true
		}
		// Digest: refresh whenever we have a fresh value to store.
		// Empty means ResolveDigest didn't know — keep the previous
		// digest in that case. Non-empty means the registry told us
		// the current manifest identity for this tag; record it so
		// the next `b update` can short-circuit when it matches.
		if digest != "" && entry.Digest != digest {
			entry.Digest = digest
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
