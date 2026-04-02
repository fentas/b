package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/fentas/goodies/templates"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	"github.com/fentas/b/pkg/env"
	"github.com/fentas/b/pkg/envmatch"
	"github.com/fentas/b/pkg/gitcache"
	"github.com/fentas/b/pkg/lock"
	"github.com/fentas/b/pkg/state"
)

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
			hash, err := lock.SHA256File(destPath)
			if err != nil {
				localDrift++
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

		// Build status line
		if upstreamStatus == "" && localDrift == 0 && missingFiles == 0 {
			fmt.Fprintf(o.IO.Out, "  %-40s %s @ %s ✓ up to date\n", entry.Key, version, shortCommit(lockEntry.Commit))
		} else {
			fmt.Fprintf(o.IO.Out, "  %-40s %s @ %s\n", entry.Key, version, shortCommit(lockEntry.Commit))
			if upstreamStatus != "" {
				fmt.Fprintf(o.IO.Out, "    ↑ %s\n", upstreamStatus)
			}
			if localDrift > 0 {
				fmt.Fprintf(o.IO.Out, "    ✎ %d file(s) modified locally\n", localDrift)
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
		Long: `Fetch the upstream repo's b.yaml to list available env profiles (labeled env entries).
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
	if err == nil {
		if len(upstream.Envs) == 0 {
			fmt.Fprintf(o.IO.Out, "No env profiles found in %s's b.yaml\n", ref)
			return nil
		}

		// Sort envs for deterministic output
		sortedEnvs := make([]*state.EnvEntry, len(upstream.Envs))
		copy(sortedEnvs, upstream.Envs)
		sort.Slice(sortedEnvs, func(i, j int) bool {
			return sortedEnvs[i].Key < sortedEnvs[j].Key
		})

		fmt.Fprintf(o.IO.Out, "Available profiles from %s @ %s:\n\n", ref, versionLabel)
		for _, e := range sortedEnvs {
			label := gitcache.RefLabel(e.Key)
			name := label
			if name == "" {
				name = "(default)"
			}
			desc := e.Description
			if desc == "" {
				desc = summarizeFiles(e.Files)
			}
			fmt.Fprintf(o.IO.Out, "  %-24s %s\n", name, desc)
			if e.Files != nil {
				// Sort globs for deterministic output
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
		fmt.Fprintf(o.IO.Out, "Install a profile with:\n  b env add %s#<profile>\n", ref)
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

	fmt.Fprintf(o.IO.Out, "No b.yaml found in %s. Detected directories:\n\n", ref)
	for _, dir := range sortedDirs {
		count := dirs[dir]
		fmt.Fprintf(o.IO.Out, "  %-24s %d file(s)\n", dir+"/", count)
		fmt.Fprintf(o.IO.Out, "    b install %s:/%s/** ./%s\n\n", ref, dir, dir)
	}

	return nil
}

// --- env add ---

// EnvAddOptions holds options for the env add command.
type EnvAddOptions struct {
	*SharedOptions
	Version string
}

// NewEnvAddCmd creates the env add subcommand.
func NewEnvAddCmd(shared *SharedOptions) *cobra.Command {
	o := &EnvAddOptions{SharedOptions: shared}

	cmd := &cobra.Command{
		Use:   "add <ref>[#profile]",
		Short: "Add an env profile from an upstream repo to your b.yaml",
		Long: `Fetch the upstream repo's b.yaml and copy the specified profile into your local b.yaml.
If no profile label is given, adds the default (unlabeled) env entry.`,
		Example: templates.Examples(`
			# Add a specific profile
			b env add github.com/org/infra#monitoring

			# Add with version pin
			b env add --version v2.0 github.com/org/infra#base

			# Add the default profile (no label)
			b env add github.com/org/infra
		`),
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return o.Run(args[0])
		},
	}

	cmd.Flags().StringVar(&o.Version, "version", "", "Pin a specific version (tag/branch)")

	return cmd
}

// Run executes the env add command.
func (o *EnvAddOptions) Run(refArg string) error {
	ref := gitcache.RefBase(refArg)
	label := gitcache.RefLabel(refArg)
	version := gitcache.RefVersion(refArg)
	if o.Version != "" {
		version = o.Version
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

	// Fetch upstream config
	upstream, err := fetchUpstreamConfig(cacheRoot, ref, commit)
	if err != nil {
		return fmt.Errorf("no b.yaml found in %s: %w", ref, err)
	}

	// Build the key to look up in upstream
	lookupKey := ref
	if label != "" {
		lookupKey = ref + "#" + label
	}

	// Find the matching env entry in upstream
	var source *state.EnvEntry
	for _, e := range upstream.Envs {
		if e.Key == lookupKey {
			source = e
			break
		}
	}

	if source == nil {
		// List available profiles
		available := []string{}
		for _, e := range upstream.Envs {
			available = append(available, e.Key)
		}
		if len(available) > 0 {
			return fmt.Errorf("profile %q not found in %s\n  Available: %s\n  Hint: run `b env profiles %s` to see all profiles",
				lookupKey, ref, strings.Join(available, ", "), ref)
		}
		return fmt.Errorf("no env profiles found in %s's b.yaml", ref)
	}

	// Build local entry from upstream
	localKey := lookupKey
	entry := &state.EnvEntry{
		Key:         localKey,
		Description: source.Description,
		Version:     version,
		Ignore:      source.Ignore,
		Strategy:    source.Strategy,
		Group:       source.Group,
		OnPreSync:   source.OnPreSync,
		OnPostSync:  source.OnPostSync,
		Files:       source.Files,
	}
	if entry.Version == "" {
		entry.Version = source.Version
	}

	// Load or create local config
	configPath, err := o.getConfigPath()
	if err != nil {
		return fmt.Errorf("cannot determine config path: %w", err)
	}
	if configPath == "" {
		return fmt.Errorf("cannot save config: config path is not set")
	}

	config := o.Config
	if config == nil {
		config = &state.State{}
	}

	// Check if already exists
	if existing := config.Envs.Get(localKey); existing != nil {
		return fmt.Errorf("%s already exists in b.yaml — remove it first with `b env remove %s`", localKey, localKey)
	}

	config.Envs = append(config.Envs, entry)

	if err := state.SaveConfig(config, configPath); err != nil {
		return err
	}

	fmt.Fprintf(o.IO.Out, "Added %s to b.yaml", localKey)
	if entry.Description != "" {
		fmt.Fprintf(o.IO.Out, " (%s)", entry.Description)
	}
	fmt.Fprintln(o.IO.Out)

	if entry.Files != nil {
		for glob, gc := range entry.Files {
			if gc.Dest != "" {
				fmt.Fprintf(o.IO.Out, "  %s → %s\n", glob, gc.Dest)
			} else {
				fmt.Fprintf(o.IO.Out, "  %s\n", glob)
			}
		}
	}

	fmt.Fprintln(o.IO.Out, "\nRun `b update` to sync files.")
	return nil
}

// --- shared helpers ---

// fetchUpstreamConfig fetches and parses b.yaml (or .bin/b.yaml) from a cached repo.
func fetchUpstreamConfig(cacheRoot, ref, commit string) (*state.State, error) {
	// Try b.yaml
	content, err := gitcache.ShowFile(cacheRoot, ref, commit, "b.yaml")
	if err != nil {
		// Try .bin/b.yaml
		content, err = gitcache.ShowFile(cacheRoot, ref, commit, ".bin/b.yaml")
		if err != nil {
			return nil, fmt.Errorf("b.yaml not found")
		}
	}

	var upstream state.State
	if err := yaml.Unmarshal(content, &upstream); err != nil {
		return nil, fmt.Errorf("parsing b.yaml: %w", err)
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
