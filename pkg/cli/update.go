package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
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
	specifiedArgs     []string         // args from CLI (binary names or env refs)
	specifiedBinaries []*binary.Binary // resolved binaries from CLI args
	specifiedEnvRefs  []string         // resolved env refs from CLI args
	Strategy          string           // strategy flag override: replace, client, merge
	stdinReader       io.Reader        // overridden by tests; nil means os.Stdin
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
	return nil
}

// Run executes the update operation
func (o *UpdateOptions) Run() error {
	if len(o.specifiedBinaries) > 0 || len(o.specifiedEnvRefs) > 0 {
		return o.runSpecified()
	}
	return o.runAll()
}

// runAll updates all binaries and envs from config.
func (o *UpdateOptions) runAll() error {
	// Update binaries
	binariesToUpdate := o.GetBinariesFromConfig()
	if len(binariesToUpdate) > 0 {
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
		fmt.Fprintln(o.IO.Out, "No binaries or envs to update")
	}

	return nil
}

// runSpecified updates only the specified binaries/envs.
func (o *UpdateOptions) runSpecified() error {
	if len(o.specifiedBinaries) > 0 {
		if err := o.callUpdateBinaries(o.specifiedBinaries); err != nil {
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

	// Check for dest path conflicts between envs
	o.checkEnvConflicts(refs)

	lockDir := o.LockDir()
	projectRoot := lockDir
	lk, err := lock.ReadLock(lockDir)
	if err != nil {
		return err
	}

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

		label := gitcache.RefLabel(entry.Key)
		ref := gitcache.RefBase(entry.Key)

		// Determine strategy: CLI flag > config > default
		strategy := entry.Strategy
		if o.Strategy != "" {
			strategy = o.Strategy
		}

		cfg := env.EnvConfig{
			Ref:      ref,
			Label:    label,
			Version:  entry.Version,
			Ignore:   entry.Ignore,
			Strategy: strategy,
			Files:    entry.Files,
		}

		// Set up interactive conflict resolver for replace strategy on TTY
		if (strategy == "" || strategy == env.StrategyReplace) && isTTYFunc() {
			cfg.ResolveConflict = o.interactiveConflictResolver(ref, lk)
		}

		lockEntry := lk.FindEnv(ref, label)

		result, err := syncEnvFunc(cfg, projectRoot, "", lockEntry)
		if err != nil {
			fmt.Fprintf(o.IO.ErrOut, "  %-40s ✗ %v\n", entry.Key, err)
			continue
		}

		if result.Skipped {
			fmt.Fprintf(o.IO.Out, "  %-40s %s\n", entry.Key, result.Message)
			continue // don't overwrite lock entry when up-to-date
		}

		fmt.Fprintf(o.IO.Out, "  %-40s %s → %s (%s)\n", entry.Key, shortCommit(result.PreviousCommit), shortCommit(result.Commit), result.Message)
		for _, f := range result.Files {
			o.printFileStatus(f)
		}

		if result.Conflicts > 0 {
			fmt.Fprintf(o.IO.ErrOut, "    ⚠ %d file(s) have merge conflicts — resolve manually\n", result.Conflicts)
		}

		lk.UpsertEnv(lock.EnvEntry{
			Ref:            result.Ref,
			Label:          result.Label,
			Version:        result.Version,
			Commit:         result.Commit,
			PreviousCommit: result.PreviousCommit,
			Files:          result.Files,
		})
	}

	return lock.WriteLock(lockDir, lk, o.bVersion)
}

// printFileStatus prints a single file's sync status.
func (o *UpdateOptions) printFileStatus(f lock.LockFile) {
	switch {
	case f.Status == "kept":
		fmt.Fprintf(o.IO.Out, "    → %-36s ⊘ kept (local changes preserved)\n", f.Dest)
	case f.Status == "merged":
		fmt.Fprintf(o.IO.Out, "    → %-36s ✓ merged\n", f.Dest)
	case f.Status == "conflict":
		fmt.Fprintf(o.IO.ErrOut, "    → %-36s ✗ conflict (markers inserted)\n", f.Dest)
	case strings.Contains(f.Status, "local changes overwritten"):
		fmt.Fprintf(o.IO.ErrOut, "    → %-36s ⚠ replaced (local changes overwritten)\n", f.Dest)
	default:
		fmt.Fprintf(o.IO.Out, "    → %-36s ✓ replaced\n", f.Dest)
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
			if o.Force {
				err = b.DownloadBinary()
			} else {
				err = b.EnsureBinary(true) // Force update
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
	return nil
}

// checkEnvConflicts detects when two env entries write to overlapping dest paths.
// It checks the lock file for existing dest paths across all env entries.
func (o *UpdateOptions) checkEnvConflicts(refs []string) {
	if o.Config == nil || len(o.Config.Envs) < 2 {
		return
	}

	lk, _ := lock.ReadLock(o.LockDir())
	if lk == nil {
		return
	}

	// Build a map of dest → env ref for all env entries in the lock
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
