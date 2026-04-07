package cli

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/fentas/goodies/templates"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/fentas/b/pkg/env"
	"github.com/fentas/b/pkg/envmatch"
	"github.com/fentas/b/pkg/gitcache"
	"github.com/fentas/b/pkg/lock"
	"github.com/fentas/b/pkg/path"
	"github.com/fentas/b/pkg/state"
)

// conflictMarkerScanner is a small line-anchored detector for git
// merge-file conflict regions. It is line-oriented so a markdown
// rule like `=======` or a stray `<<<<<<<` inside a string literal
// can't trip the detector. CRLF endings are tolerated by trimming
// `\r` off the end of each candidate line before comparison.
//
// The scanner is intentionally state-machine based instead of using
// bufio.Scanner. Scanner.Scan() returns false with bufio.ErrTooLong
// when a single line exceeds its buffer cap, and unless the caller
// also checks scanner.Err() the result silently looks like
// "no markers". `b env status` is expected to scan arbitrary user
// files including SOPS-encrypted blobs and minified JSON, so we
// avoid the per-line cap entirely.
type conflictMarkerScanner struct {
	hasStart, hasSep, hasEnd bool
	// pending holds the bytes of the current line so far. We only
	// keep up to the first 64 bytes — the markers we care about are
	// all <= 16 bytes — so a degenerate one-line file with no
	// newlines doesn't grow this slice unbounded.
	pending []byte
}

const conflictPendingMax = 64

// Update feeds another chunk of file bytes to the scanner. Returns
// true once all three marker shapes have been seen (the caller can
// stop checking after that).
func (s *conflictMarkerScanner) Update(chunk []byte) bool {
	if s.allSeen() {
		return true
	}
	for _, b := range chunk {
		if b == '\n' {
			s.consumeLine()
			s.pending = s.pending[:0]
			if s.allSeen() {
				return true
			}
			continue
		}
		if len(s.pending) < conflictPendingMax {
			s.pending = append(s.pending, b)
		}
	}
	return s.allSeen()
}

// Done flushes the final partial line (a file with no trailing
// newline) and returns whether all three markers were seen.
func (s *conflictMarkerScanner) Done() bool {
	if !s.allSeen() {
		s.consumeLine()
	}
	return s.allSeen()
}

func (s *conflictMarkerScanner) consumeLine() {
	line := s.pending
	// Tolerate CRLF: drop a trailing \r so the separator check
	// matches `=======\r` files too.
	if n := len(line); n > 0 && line[n-1] == '\r' {
		line = line[:n-1]
	}
	switch {
	case bytes.HasPrefix(line, conflictStartMarker):
		s.hasStart = true
	case bytes.Equal(line, conflictSepMarker):
		s.hasSep = true
	case bytes.HasPrefix(line, conflictEndMarker):
		s.hasEnd = true
	}
}

// Package-level marker byte slices. Defined once so the
// per-line consumeLine path doesn't allocate a fresh []byte on
// every comparison while scanning large files.
var (
	conflictStartMarker = []byte("<<<<<<< ")
	conflictSepMarker   = []byte("=======")
	conflictEndMarker   = []byte(">>>>>>> ")
)

func (s *conflictMarkerScanner) allSeen() bool {
	return s.hasStart && s.hasSep && s.hasEnd
}

// hasConflictMarkers reports whether the byte slice contains a git
// merge-file conflict region. The check is line-anchored, tolerates
// CRLF endings, and has no maximum line length.
//
// Note: pkg/env/splice.go has a similar `containsConflictMarkers`
// helper that uses substring matching and isn't currently exported.
// Keeping a local copy here to avoid widening the pkg/env API
// surface in this small follow-up; merging that into one shared
// helper is tracked as a separate cleanup.
func hasConflictMarkers(b []byte) bool {
	var s conflictMarkerScanner
	if s.Update(b) {
		return true
	}
	return s.Done()
}

// hashAndScanConflicts reads a file once, computing its SHA-256 hex
// digest and detecting whether the contents include any git
// merge-file conflict region. Bundling the two passes lets `b env
// status` scan every synced file without paying for two reads (one
// for the hash, one for the marker check).
//
// Returns ("", false, err) on read failure. A missing file is an
// error from the caller's perspective and is not handled specially
// here — env.Status's existing missing-file branch checks os.Stat
// before calling this helper.
//
// Implementation note: we deliberately don't use bufio.Scanner.
// Scanner.Scan() returns false with bufio.ErrTooLong when a line
// exceeds its buffer cap; if the caller forgets scanner.Err() the
// result silently looks like "no markers". The conflictMarkerScanner
// state machine has no per-line memory limit — a degenerate single
// long line bounds at conflictPendingMax bytes regardless of file
// size — so SOPS blobs, minified JSON, and lockfiles all scan
// correctly.
func hashAndScanConflicts(path string) (string, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	defer f.Close()

	h := sha256.New()
	var ms conflictMarkerScanner
	buf := make([]byte, 64*1024)
	for {
		n, rerr := f.Read(buf)
		if n > 0 {
			h.Write(buf[:n])
			ms.Update(buf[:n])
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return "", false, rerr
		}
	}
	hasMarkers := ms.Done()
	return fmt.Sprintf("%x", h.Sum(nil)), hasMarkers, nil
}

// NewEnvCmd creates the env parent command with subcommands.
func NewEnvCmd(shared *SharedOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "env",
		Short: "Manage environment file sync",
		Long:  "Commands for inspecting and managing env file syncing.",
	}

	cmd.AddCommand(NewEnvStatusCmd(shared))
	cmd.AddCommand(NewEnvRemoveCmd(shared))
	cmd.AddCommand(NewEnvMatchCmd(shared))
	cmd.AddCommand(NewEnvProfilesCmd(shared))
	cmd.AddCommand(NewEnvAddCmd(shared))

	return cmd
}

// --- env status ---

// EnvStatusOptions holds options for the env status command.
type EnvStatusOptions struct {
	*SharedOptions
}

// NewEnvStatusCmd creates the env status subcommand.
func NewEnvStatusCmd(shared *SharedOptions) *cobra.Command {
	o := &EnvStatusOptions{SharedOptions: shared}

	return &cobra.Command{
		Use:   "status",
		Short: "Show env sync status without writing",
		Long:  "Check upstream for new commits and local files for drift. Does not write anything.",
		Example: templates.Examples(`
			# Show status for all envs
			b env status
		`),
		RunE: func(cmd *cobra.Command, args []string) error {
			return o.Run()
		},
	}
}

// Run executes the env status command.
func (o *EnvStatusOptions) Run() error {
	if o.Config == nil || len(o.Config.Envs) == 0 {
		fmt.Fprintln(o.IO.Out, "No envs configured.")
		return nil
	}

	lockDir := o.LockDir()
	lk, err := lock.ReadLock(lockDir)
	if err != nil {
		return err
	}

	for _, entry := range o.Config.Envs {
		label := gitcache.RefLabel(entry.Key)
		ref := gitcache.RefBase(entry.Key)
		url := gitcache.GitURL(entry.Key)

		lockEntry := lk.FindEnv(ref, label)

		version := entry.Version
		if version == "" {
			version = "(HEAD)"
		}

		// Not synced yet
		if lockEntry == nil {
			fmt.Fprintf(o.IO.Out, "  %-40s %s — not synced\n", entry.Key, version)
			continue
		}

		// Check upstream
		upstreamStatus := ""
		upstreamCommit, resolveErr := resolveRefFunc(url, entry.Version)
		if resolveErr != nil {
			upstreamStatus = "cannot check upstream"
		} else if upstreamCommit != lockEntry.Commit {
			upstreamStatus = fmt.Sprintf("upstream changed (%s → %s)", shortCommit(lockEntry.Commit), shortCommit(upstreamCommit))
		}

		// Check local files for drift (content and mode)
		localDrift := 0
		missingFiles := 0
		// Conflicted files are surfaced as a distinct counter so
		// users see them in `b env status` without having to run
		// `b env resolve`. We deliberately do NOT also count them
		// as drift: when env update writes a merge result with
		// conflict markers, the lock SHA records the post-merge
		// bytes (markers and all), so a conflicted file may match
		// its lock SHA exactly. Treating it as drift would mask
		// the more actionable "unresolved conflict markers"
		// message; the conflict counter is the source of truth.
		conflictedFiles := 0
		for _, f := range lockEntry.Files {
			destPath := f.Dest
			if !filepath.IsAbs(destPath) {
				destPath = filepath.Join(lockDir, destPath)
			}
			destPath = filepath.Clean(destPath)

			// Skip paths that escape the project root (including via symlinks)
			if err := env.ValidatePathUnderRoot(lockDir, destPath); err != nil {
				localDrift++
				continue
			}

			info, statErr := os.Stat(destPath)
			if statErr != nil {
				if os.IsNotExist(statErr) {
					missingFiles++
				} else {
					localDrift++
				}
				continue
			}
			// Single-pass: hash the file AND scan it for conflict
			// markers in one read. We must scan EVERY synced file,
			// not just ones whose SHA drifted, because env update
			// records the post-merge bytes (including any conflict
			// markers) into the lock — so a conflicted file can
			// match its lock SHA exactly and would otherwise be
			// reported as "✓ up to date".
			hash, hasMarkers, err := hashAndScanConflicts(destPath)
			if err != nil {
				localDrift++
				continue
			}
			if hasMarkers {
				// Count conflicted files separately so users get
				// the actionable "run b env resolve" message
				// instead of the generic drift number. A
				// conflicted file is NOT also counted as drift,
				// even when its SHA happens to differ from the
				// lock — the conflict counter is the source of
				// truth.
				conflictedFiles++
				continue
			}
			if hash != f.SHA256 {
				localDrift++
				continue
			}
			// Also check file mode drift when lock records a mode
			if f.Mode != "" {
				parsed, parseErr := strconv.ParseUint(f.Mode, 8, 32)
				if parseErr != nil {
					localDrift++
					continue
				}
				expectedPerm := os.FileMode(parsed)
				if info.Mode().Perm() != expectedPerm {
					localDrift++
				}
			}
		}

		// Build status line. Conflicted files block the "up to date"
		// shortcut even when their on-disk SHA matches the lock —
		// the user still needs to resolve the markers.
		if upstreamStatus == "" && localDrift == 0 && missingFiles == 0 && conflictedFiles == 0 {
			fmt.Fprintf(o.IO.Out, "  %-40s %s @ %s ✓ up to date\n", entry.Key, version, shortCommit(lockEntry.Commit))
		} else {
			fmt.Fprintf(o.IO.Out, "  %-40s %s @ %s\n", entry.Key, version, shortCommit(lockEntry.Commit))
			if upstreamStatus != "" {
				fmt.Fprintf(o.IO.Out, "    ↑ %s\n", upstreamStatus)
			}
			if localDrift > 0 {
				fmt.Fprintf(o.IO.Out, "    ✎ %d file(s) modified locally\n", localDrift)
			}
			if conflictedFiles > 0 {
				fmt.Fprintf(o.IO.Out, "    ✗ %d file(s) with unresolved conflict markers — run `b env resolve`\n", conflictedFiles)
			}
			if missingFiles > 0 {
				fmt.Fprintf(o.IO.Out, "    ✗ %d file(s) missing\n", missingFiles)
			}
		}
	}

	return nil
}

// --- env remove ---

// EnvRemoveOptions holds options for the env remove command.
type EnvRemoveOptions struct {
	*SharedOptions
	DeleteFiles bool
}

// NewEnvRemoveCmd creates the env remove subcommand.
func NewEnvRemoveCmd(shared *SharedOptions) *cobra.Command {
	o := &EnvRemoveOptions{SharedOptions: shared}

	cmd := &cobra.Command{
		Use:   "remove <ref>",
		Short: "Remove an env entry from config and lock",
		Long:  "Remove an env from b.yaml and b.lock, optionally deleting synced files.",
		Example: templates.Examples(`
			# Remove env from config and lock
			b env remove github.com/org/infra

			# Remove and delete synced files from disk
			b env remove --delete-files github.com/org/infra
		`),
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return o.Run(args[0])
		},
	}

	cmd.Flags().BoolVar(&o.DeleteFiles, "delete-files", false, "Also delete synced files from disk")

	return cmd
}

// Run executes the env remove command.
func (o *EnvRemoveOptions) Run(key string) error {
	label := gitcache.RefLabel(key)
	ref := gitcache.RefBase(key)
	lockDir := o.LockDir()

	// Normalize key to canonical form (ref + optional #label) for config lookup
	configKey := ref
	if label != "" {
		configKey = ref + "#" + label
	}

	// Delete synced files if requested
	if o.DeleteFiles {
		lk, err := lock.ReadLock(lockDir)
		if err != nil {
			return err
		}
		lockEntry := lk.FindEnv(ref, label)
		if lockEntry != nil {
			for _, f := range lockEntry.Files {
				destPath := f.Dest
				if !filepath.IsAbs(destPath) {
					destPath = filepath.Join(lockDir, destPath)
				}
				destPath = filepath.Clean(destPath)

				// Path traversal check (including symlinks): refuse to delete outside project root
				if err := env.ValidatePathUnderRoot(lockDir, destPath); err != nil {
					fmt.Fprintf(o.IO.ErrOut, "  Warning: skipping %s (resolves outside project root)\n", f.Dest)
					continue
				}

				if err := os.Remove(destPath); err != nil && !os.IsNotExist(err) {
					fmt.Fprintf(o.IO.ErrOut, "  Warning: could not delete %s: %v\n", destPath, err)
				} else if err == nil {
					fmt.Fprintf(o.IO.Out, "  Deleted %s\n", f.Dest)
				}
			}
		}
	}

	// Remove from lock
	lk, err := lock.ReadLock(lockDir)
	if err != nil {
		return err
	}
	if lk.RemoveEnv(ref, label) {
		if err := lock.WriteLock(lockDir, lk, o.bVersion); err != nil {
			return err
		}
		fmt.Fprintf(o.IO.Out, "  Removed %s from b.lock\n", key)
	}

	// Remove from config using normalized key
	if o.Config != nil {
		if o.Config.Envs.Remove(configKey) {
			configPath, err := o.getConfigPath()
			if err != nil {
				return fmt.Errorf("cannot determine config path: %w", err)
			}
			if configPath == "" {
				return fmt.Errorf("cannot save updated config: config path is not set")
			}
			if err := state.SaveConfig(o.Config, configPath); err != nil {
				return err
			}
			fmt.Fprintf(o.IO.Out, "  Removed %s from b.yaml\n", configKey)
		}
	}

	return nil
}

// --- env match ---

// EnvMatchOptions holds options for the env match command.
type EnvMatchOptions struct {
	*SharedOptions
}

// NewEnvMatchCmd creates the env match subcommand.
func NewEnvMatchCmd(shared *SharedOptions) *cobra.Command {
	o := &EnvMatchOptions{SharedOptions: shared}

	return &cobra.Command{
		Use:   "match <ref> <glob> [dest]",
		Short: "Preview which files a glob pattern matches in a remote repo",
		Long:  "Clone/fetch the repo into the local cache and show which files match the glob pattern. Does not modify project files or b.lock, but may populate the git cache.",
		Example: templates.Examples(`
			# Preview matched files
			b env match github.com/org/infra "manifests/base/**"

			# Preview with dest mapping
			b env match github.com/org/infra "manifests/base/**" ./base

			# Pin a version
			b env match github.com/org/infra@v2.0 "manifests/**"
		`),
		Args: cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			return o.Run(args)
		},
	}
}

// Run executes the env match command.
func (o *EnvMatchOptions) Run(args []string) error {
	refArg := args[0]
	glob := args[1]
	dest := ""
	if len(args) >= 3 {
		dest = args[2]
	}

	ref := gitcache.RefBase(refArg)
	version := gitcache.RefVersion(refArg)
	if idx := strings.Index(version, "#"); idx != -1 {
		version = version[:idx]
	}
	url := gitcache.GitURL(refArg)

	commit, err := gitcache.ResolveRef(url, version)
	if err != nil {
		return fmt.Errorf("resolving %s: %w", refArg, err)
	}

	cacheRoot := gitcache.DefaultCacheRoot()
	if err := gitcache.EnsureClone(cacheRoot, ref, url); err != nil {
		return fmt.Errorf("cloning %s: %w", url, err)
	}
	if err := gitcache.Fetch(cacheRoot, ref, commit); err != nil {
		return fmt.Errorf("fetching %s: %w", commit, err)
	}

	tree, err := gitcache.ListTree(cacheRoot, ref, commit)
	if err != nil {
		return fmt.Errorf("listing tree: %w", err)
	}

	globs := map[string]envmatch.GlobConfig{
		glob: {Dest: dest},
	}
	matched := envmatch.MatchGlobs(tree, globs, nil)

	if len(matched) == 0 {
		fmt.Fprintf(o.IO.Out, "No files matched %q in %s @ %s\n", glob, ref, shortCommit(commit))
		return nil
	}

	fmt.Fprintf(o.IO.Out, "%d file(s) matched in %s @ %s:\n", len(matched), ref, shortCommit(commit))
	for _, m := range matched {
		if m.DestPath != m.SourcePath {
			fmt.Fprintf(o.IO.Out, "  %s → %s\n", m.SourcePath, m.DestPath)
		} else {
			fmt.Fprintf(o.IO.Out, "  %s\n", m.SourcePath)
		}
	}

	return nil
}

// --- env profiles ---

// EnvProfilesOptions holds options for the env profiles command.
type EnvProfilesOptions struct {
	*SharedOptions
}

// NewEnvProfilesCmd creates the env profiles subcommand.
func NewEnvProfilesCmd(shared *SharedOptions) *cobra.Command {
	o := &EnvProfilesOptions{SharedOptions: shared}

	return &cobra.Command{
		Use:   "profiles <ref>",
		Short: "Discover available env profiles from an upstream repo",
		Long: `Fetch the upstream repo's b.yaml to list available profiles from the profiles section.
If no b.yaml is found, shows the directory structure as suggested profiles.`,
		Example: templates.Examples(`
			# List profiles from upstream
			b env profiles github.com/org/infra

			# List profiles from a specific version
			b env profiles github.com/org/infra@v2.0
		`),
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return o.Run(args[0])
		},
	}
}

// Run executes the env profiles command.
func (o *EnvProfilesOptions) Run(refArg string) error {
	ref := gitcache.RefBase(refArg)
	version := gitcache.RefVersion(refArg)
	if idx := strings.Index(version, "#"); idx != -1 {
		version = version[:idx]
	}
	url := gitcache.GitURL(refArg)

	commit, err := gitcache.ResolveRef(url, version)
	if err != nil {
		return fmt.Errorf("resolving %s: %w", refArg, err)
	}

	cacheRoot := gitcache.DefaultCacheRoot()
	if err := gitcache.EnsureClone(cacheRoot, ref, url); err != nil {
		return fmt.Errorf("cloning %s: %w", url, err)
	}
	if err := gitcache.Fetch(cacheRoot, ref, commit); err != nil {
		return fmt.Errorf("fetching %s: %w", commit, err)
	}

	versionLabel := version
	if versionLabel == "" {
		versionLabel = shortCommit(commit)
	}

	// Try to find and parse upstream b.yaml
	upstream, err := fetchUpstreamConfig(cacheRoot, ref, commit)
	if err != nil && !errors.Is(err, errConfigNotFound) {
		return fmt.Errorf("loading upstream config: %w", err)
	}
	if err == nil {
		if len(upstream.Profiles) == 0 {
			fmt.Fprintf(o.IO.Out, "No profiles found in %s's b.yaml\n", ref)
			return nil
		}

		profiles := upstream.Profiles

		// Sort for deterministic output
		sorted := make([]*state.EnvEntry, len(profiles))
		copy(sorted, profiles)
		sort.Slice(sorted, func(i, j int) bool {
			return sorted[i].Key < sorted[j].Key
		})

		fmt.Fprintf(o.IO.Out, "Available profiles from %s @ %s:\n\n", ref, versionLabel)
		for _, e := range sorted {
			name := e.Key
			desc := e.Description
			if desc == "" {
				desc = summarizeFiles(e.Files)
			}
			fmt.Fprintf(o.IO.Out, "  %-24s %s\n", name, desc)
			if len(e.Includes) > 0 {
				fmt.Fprintf(o.IO.Out, "    includes: %s\n", strings.Join(e.Includes, ", "))
			}
			if e.Files != nil {
				globs := make([]string, 0, len(e.Files))
				for g := range e.Files {
					globs = append(globs, g)
				}
				sort.Strings(globs)
				for _, glob := range globs {
					gc := e.Files[glob]
					if gc.Dest != "" {
						fmt.Fprintf(o.IO.Out, "    %s → %s\n", glob, gc.Dest)
					} else {
						fmt.Fprintf(o.IO.Out, "    %s\n", glob)
					}
				}
			}
			fmt.Fprintln(o.IO.Out)
		}
		if version != "" {
			fmt.Fprintf(o.IO.Out, "Install a profile with:\n  b env add --version %s %s#<name>\n", version, ref)
		} else {
			fmt.Fprintf(o.IO.Out, "Install a profile with:\n  b env add %s#<name>\n", ref)
		}
		return nil
	}

	// Fallback: auto-detect from directory structure
	tree, err := gitcache.ListTree(cacheRoot, ref, commit)
	if err != nil {
		return fmt.Errorf("listing tree: %w", err)
	}

	dirs := make(map[string]int)
	for _, p := range tree {
		parts := strings.SplitN(p, "/", 2)
		if len(parts) > 1 {
			dirs[parts[0]]++
		}
	}

	if len(dirs) == 0 {
		fmt.Fprintf(o.IO.Out, "No profiles or directories found in %s\n", ref)
		return nil
	}

	// Sort directory names
	sortedDirs := make([]string, 0, len(dirs))
	for d := range dirs {
		sortedDirs = append(sortedDirs, d)
	}
	sort.Strings(sortedDirs)

	// Use the original ref (with version) in install hints
	installRef := ref
	if version != "" {
		installRef = ref + "@" + version
	}

	fmt.Fprintf(o.IO.Out, "No b.yaml found in %s. Detected directories:\n\n", ref)
	for _, dir := range sortedDirs {
		count := dirs[dir]
		fmt.Fprintf(o.IO.Out, "  %-24s %d file(s)\n", dir+"/", count)
		fmt.Fprintf(o.IO.Out, "    b install %s:/%s/** ./%s\n\n", installRef, dir, dir)
	}

	return nil
}

// --- env add ---

// EnvAddOptions holds options for the env add command.
type EnvAddOptions struct {
	*SharedOptions
	Version     string
	Interactive bool
	stdinReader io.Reader // overridden by tests; nil means os.Stdin
}

// NewEnvAddCmd creates the env add subcommand.
func NewEnvAddCmd(shared *SharedOptions) *cobra.Command {
	o := &EnvAddOptions{SharedOptions: shared}

	cmd := &cobra.Command{
		Use:   "add <ref>#<profile> | add -i <ref>",
		Short: "Add an env profile from an upstream repo to your b.yaml",
		Long: `Fetch the upstream repo's b.yaml and copy the specified profile into your local b.yaml.
The profile name (after #) must match an entry in the upstream profiles section.
Use -i for interactive selection.`,
		Example: templates.Examples(`
			# Add a profile
			b env add github.com/org/infra#monitoring

			# Add with version pin
			b env add --version v2.0 github.com/org/infra#base

			# Interactive selection
			b env add -i github.com/org/infra
		`),
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			arg := ""
			if len(args) > 0 {
				arg = args[0]
			}
			return o.Run(arg)
		},
	}

	cmd.Flags().StringVar(&o.Version, "version", "", "Pin a specific version (tag/branch)")
	cmd.Flags().BoolVarP(&o.Interactive, "interactive", "i", false, "Interactively select profiles")

	return cmd
}

// Run executes the env add command.
func (o *EnvAddOptions) Run(refArg string) error {
	ref := gitcache.RefBase(refArg)
	label := gitcache.RefLabel(refArg)
	version := gitcache.RefVersion(refArg)
	if idx := strings.Index(version, "#"); idx != -1 {
		version = version[:idx]
	}
	if o.Version != "" {
		version = o.Version
	}

	if o.Interactive {
		return o.runInteractive(ref, version, refArg)
	}

	if label == "" {
		return fmt.Errorf("profile name required — use %s#<name>\n  Hint: run `b env profiles %s` to see available profiles\n  Hint: use -i for interactive selection", ref, ref)
	}

	// Fail fast if already in local config
	localKey := ref + "#" + label
	if o.Config != nil {
		if existing := o.Config.Envs.Get(localKey); existing != nil {
			return fmt.Errorf("%s already exists in b.yaml — remove it first with `b env remove %s`", localKey, localKey)
		}
	}

	upstream, err := o.fetchUpstream(ref, version, refArg)
	if err != nil {
		return err
	}

	source, err := o.findProfile(label, ref, upstream)
	if err != nil {
		return err
	}

	return o.addProfile(ref, label, version, source, upstream)
}

// runInteractive shows a numbered list of profiles and lets the user select.
func (o *EnvAddOptions) runInteractive(ref, version, refArg string) error {
	if !isTTYFunc() {
		return fmt.Errorf("interactive mode requires a terminal — use %s#<name> syntax instead", ref)
	}

	upstream, err := o.fetchUpstream(ref, version, refArg)
	if err != nil {
		return err
	}

	if len(upstream.Profiles) == 0 {
		return fmt.Errorf("no profiles found in %s's b.yaml", ref)
	}

	// Sort for stable display
	sorted := make([]*state.EnvEntry, len(upstream.Profiles))
	copy(sorted, upstream.Profiles)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Key < sorted[j].Key
	})

	fmt.Fprintf(o.IO.Out, "Available profiles from %s:\n\n", ref)
	for i, e := range sorted {
		desc := e.Description
		if desc == "" {
			desc = summarizeFiles(e.Files)
		}
		fmt.Fprintf(o.IO.Out, "  [%d] %-20s %s\n", i+1, e.Key, desc)
	}

	fmt.Fprintf(o.IO.ErrOut, "\nSelect profiles (space-separated numbers, e.g. \"1 3\"): ")
	reader := o.stdinReader
	if reader == nil {
		reader = os.Stdin
	}
	input, err := bufio.NewReader(reader).ReadString('\n')
	if err != nil {
		return fmt.Errorf("reading input: %w", err)
	}

	// Parse selection
	parts := strings.Fields(strings.TrimSpace(input))
	if len(parts) == 0 {
		return fmt.Errorf("no profiles selected")
	}

	added := 0
	for _, part := range parts {
		idx, err := strconv.Atoi(part)
		if err != nil {
			fmt.Fprintf(o.IO.ErrOut, "  Skipping invalid input: %s\n", part)
			continue
		}
		if idx < 1 || idx > len(sorted) {
			fmt.Fprintf(o.IO.ErrOut, "  Skipping out-of-range selection: %d (valid: 1-%d)\n", idx, len(sorted))
			continue
		}
		selected := sorted[idx-1]
		if err := o.addProfile(ref, selected.Key, version, selected, upstream); err != nil {
			fmt.Fprintf(o.IO.ErrOut, "  Error adding %s: %v\n", selected.Key, err)
			continue
		}
		added++
	}

	if added == 0 {
		return fmt.Errorf("no profiles were added")
	}
	fmt.Fprintf(o.IO.Out, "\nRun `b update` to sync files.\n")
	return nil
}

// fetchUpstream resolves, clones, fetches, and loads the upstream config.
func (o *EnvAddOptions) fetchUpstream(ref, version, refArg string) (*state.State, error) {
	url := gitcache.GitURL(refArg)
	commit, err := gitcache.ResolveRef(url, version)
	if err != nil {
		return nil, fmt.Errorf("resolving %s: %w", refArg, err)
	}

	cacheRoot := gitcache.DefaultCacheRoot()
	if err := gitcache.EnsureClone(cacheRoot, ref, url); err != nil {
		return nil, fmt.Errorf("cloning %s: %w", url, err)
	}
	if err := gitcache.Fetch(cacheRoot, ref, commit); err != nil {
		return nil, fmt.Errorf("fetching %s: %w", commit, err)
	}

	upstream, err := fetchUpstreamConfig(cacheRoot, ref, commit)
	if err != nil {
		return nil, fmt.Errorf("failed to load upstream b.yaml for %s: %w", ref, err)
	}
	return upstream, nil
}

// findProfile looks up a profile by name in the upstream config.
func (o *EnvAddOptions) findProfile(label, ref string, upstream *state.State) (*state.EnvEntry, error) {
	for _, e := range upstream.Profiles {
		if e.Key == label {
			return e, nil
		}
	}

	available := []string{}
	for _, e := range upstream.Profiles {
		available = append(available, e.Key)
	}
	sort.Strings(available)
	if len(available) > 0 {
		return nil, fmt.Errorf("profile %q not found in %s\n  Available: %s\n  Hint: run `b env profiles %s` to see all profiles",
			label, ref, strings.Join(available, ", "), ref)
	}
	return nil, fmt.Errorf("no profiles found in %s's b.yaml", ref)
}

// addProfile resolves includes and adds a single profile to the local config.
func (o *EnvAddOptions) addProfile(ref, label, version string, source *state.EnvEntry, upstream *state.State) error {
	localKey := ref + "#" + label

	// Check if already exists
	if o.Config == nil {
		o.Config = &state.State{}
	}
	config := o.Config
	if existing := config.Envs.Get(localKey); existing != nil {
		return fmt.Errorf("%s already exists in b.yaml — remove it first with `b env remove %s`", localKey, localKey)
	}

	// Resolve includes
	resolved := source
	if len(source.Includes) > 0 {
		var err error
		resolved, err = state.ResolveProfileIncludes(source, upstream.Profiles)
		if err != nil {
			return fmt.Errorf("resolving includes for %q: %w", label, err)
		}
	}

	if len(resolved.Files) == 0 {
		return fmt.Errorf("profile %q has no file globs — nothing to sync", label)
	}

	// Use only the user-specified version. If empty, the config was loaded
	// from HEAD and leaving version empty keeps that consistent.
	effectiveVersion := version

	entry := &state.EnvEntry{
		Key:         localKey,
		Description: resolved.Description,
		Version:     effectiveVersion,
		Ignore:      resolved.Ignore,
		Strategy:    resolved.Strategy,
		Group:       resolved.Group,
		OnPreSync:   resolved.OnPreSync,
		OnPostSync:  resolved.OnPostSync,
		Files:       resolved.Files,
	}

	configPath, err := o.getConfigPath()
	if err != nil || configPath == "" {
		configPath = path.GetDefaultConfigPath()
	}

	originalLen := len(config.Envs)
	config.Envs = append(config.Envs, entry)

	if err := state.SaveConfig(config, configPath); err != nil {
		config.Envs = config.Envs[:originalLen] // rollback on save failure
		return err
	}

	fmt.Fprintf(o.IO.Out, "Added %s to b.yaml", localKey)
	if entry.Description != "" {
		fmt.Fprintf(o.IO.Out, " (%s)", entry.Description)
	}
	fmt.Fprintln(o.IO.Out)

	if entry.Files != nil {
		globs := make([]string, 0, len(entry.Files))
		for g := range entry.Files {
			globs = append(globs, g)
		}
		sort.Strings(globs)
		for _, glob := range globs {
			gc := entry.Files[glob]
			if gc.Dest != "" {
				fmt.Fprintf(o.IO.Out, "  %s → %s\n", glob, gc.Dest)
			} else {
				fmt.Fprintf(o.IO.Out, "  %s\n", glob)
			}
		}
	}

	return nil
}

// --- shared helpers ---

// errConfigNotFound is returned when neither b.yaml nor .bin/b.yaml exists upstream.
var errConfigNotFound = errors.New("b.yaml not found")

// isGitNotFound checks if a git error indicates a missing path or repo.
func isGitNotFound(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "does not exist") || strings.Contains(msg, "No such file or directory")
}

// fetchUpstreamConfig fetches and parses b.yaml (or .bin/b.yaml) from a cached repo.
func fetchUpstreamConfig(cacheRoot, ref, commit string) (*state.State, error) {
	// Try b.yaml, then .bin/b.yaml
	configPath := "b.yaml"
	content, err := gitcache.ShowFile(cacheRoot, ref, commit, configPath)
	if err != nil {
		if !isGitNotFound(err) {
			return nil, fmt.Errorf("fetching %s: %w", configPath, err)
		}
		configPath = ".bin/b.yaml"
		content, err = gitcache.ShowFile(cacheRoot, ref, commit, configPath)
		if err != nil {
			if !isGitNotFound(err) {
				return nil, fmt.Errorf("fetching %s: %w", configPath, err)
			}
			return nil, errConfigNotFound
		}
	}

	var upstream state.State
	if err := yaml.Unmarshal(content, &upstream); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", configPath, err)
	}
	return &upstream, nil
}

// summarizeFiles builds a short summary from a files map by returning a
// comma-separated list of shortened glob suffixes (e.g. "hetzner/**, base/**").
// For more than three entries, returns the first two followed by "... (N total)".
func summarizeFiles(files map[string]envmatch.GlobConfig) string {
	if len(files) == 0 {
		return ""
	}
	globs := make([]string, 0, len(files))
	for g := range files {
		// Shorten: "manifests/hetzner/**" → "hetzner/**"
		parts := strings.Split(g, "/")
		if len(parts) > 1 {
			globs = append(globs, strings.Join(parts[len(parts)-2:], "/"))
		} else {
			globs = append(globs, g)
		}
	}
	sort.Strings(globs)
	if len(globs) <= 3 {
		return strings.Join(globs, ", ")
	}
	return fmt.Sprintf("%s, ... (%d total)", strings.Join(globs[:2], ", "), len(globs))
}
