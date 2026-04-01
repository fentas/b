package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fentas/goodies/templates"
	"github.com/spf13/cobra"

	"github.com/fentas/b/pkg/envmatch"
	"github.com/fentas/b/pkg/gitcache"
	"github.com/fentas/b/pkg/lock"
	"github.com/fentas/b/pkg/path"
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
				var expectedPerm os.FileMode = 0644
				if f.Mode == "755" {
					expectedPerm = 0755
				}
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

				// Path traversal check: refuse to delete outside project root
				rel, relErr := filepath.Rel(lockDir, destPath)
				if relErr != nil || strings.HasPrefix(rel, "..") {
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

	// Remove from config
	if o.Config != nil {
		if o.Config.Envs.Remove(key) {
			configPath, err := o.getConfigPath()
			if err != nil || configPath == "" {
				configPath = path.GetDefaultConfigPath()
			}
			if err := state.SaveConfig(o.Config, configPath); err != nil {
				return err
			}
			fmt.Fprintf(o.IO.Out, "  Removed %s from b.yaml\n", key)
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
