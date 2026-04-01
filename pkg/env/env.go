// Package env handles syncing environment files from upstream git repos.
package env

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/fentas/b/pkg/envmatch"
	"github.com/fentas/b/pkg/gitcache"
	"github.com/fentas/b/pkg/lock"
)

// Strategy constants for env update behavior.
const (
	StrategyReplace = "replace" // default: overwrite with upstream
	StrategyClient  = "client"  // keep local files when modified
	StrategyMerge   = "merge"   // three-way merge via git merge-file
)

// ConflictFunc is called when a file has local changes during an update.
// It receives source path, dest path, and returns the strategy to use for this file.
// Return "replace", "client", "merge", or "diff" (shows diff, caller re-prompts).
type ConflictFunc func(sourcePath, destPath string) string

// EnvConfig is the parsed configuration for a single env entry from b.yaml.
type EnvConfig struct {
	Ref             string                         // e.g. "github.com/org/infra"
	Label           string                         // fragment label (e.g. "monitoring")
	Version         string                         // tag/branch (resolved to commit in lock)
	Ignore          []string                       // global ignore patterns
	Strategy        string                         // replace (default) | client | merge
	Files           map[string]envmatch.GlobConfig // glob → config
	ResolveConflict ConflictFunc                   // optional: called per-file when local changes detected
	DryRun          bool                           // if true, compute changes without writing files
	ForceCommit     string                         // if set, use this commit instead of resolving version
	OnPreSync       string                         // shell command to run before syncing
	OnPostSync      string                         // shell command to run after syncing
	Stdout          io.Writer                      // output for hooks (defaults to os.Stdout)
	Stderr          io.Writer                      // error output for hooks (defaults to os.Stderr)
}

// SyncResult is the result of syncing a single env.
type SyncResult struct {
	Ref            string
	Label          string
	Version        string
	Commit         string
	PreviousCommit string
	Files          []lock.LockFile
	Skipped        bool   // true if already up-to-date
	Message        string // human-readable status
	Conflicts      int    // number of files with merge conflicts
}

// SyncEnv syncs environment files from an upstream git repo.
// It clones/fetches the repo, matches files via globs, and writes them to dest.
// Strategy-aware: respects cfg.Strategy for files with local changes.
//
// projectRoot is the base directory for resolving dest paths.
// cacheRoot is the git cache directory (defaults to ~/.cache/b/repos).
// lockEntry is the existing lock entry (nil if first sync).
func SyncEnv(cfg EnvConfig, projectRoot, cacheRoot string, lockEntry *lock.EnvEntry) (*SyncResult, error) {
	if cacheRoot == "" {
		cacheRoot = gitcache.DefaultCacheRoot()
	}

	strategy := cfg.Strategy
	if strategy == "" {
		strategy = StrategyReplace
	}

	baseRef := gitcache.RefBase(cfg.Ref)
	url := gitcache.GitURL(cfg.Ref)

	// Resolve version to commit (or use forced commit)
	var commit string
	var err error
	if cfg.ForceCommit != "" {
		commit = cfg.ForceCommit
	} else {
		commit, err = gitcache.ResolveRef(url, cfg.Version)
		if err != nil {
			return nil, fmt.Errorf("resolving %s@%s: %w", cfg.Ref, cfg.Version, err)
		}
	}

	// Check if up-to-date (skip when forcing a specific commit)
	if cfg.ForceCommit == "" && lockEntry != nil && lockEntry.Commit == commit {
		return &SyncResult{
			Ref:     baseRef,
			Label:   cfg.Label,
			Version: cfg.Version,
			Commit:  commit,
			Skipped: true,
			Message: "(up to date)",
		}, nil
	}

	// Run pre-sync hook (skip in dry-run mode)
	if cfg.OnPreSync != "" && !cfg.DryRun {
		if err := runHook(cfg.OnPreSync, projectRoot, hookStdout(cfg), hookStderr(cfg)); err != nil {
			return nil, fmt.Errorf("pre-sync hook failed: %w", err)
		}
	}

	// Clone and fetch
	if err := gitcache.EnsureClone(cacheRoot, baseRef, url); err != nil {
		return nil, fmt.Errorf("cloning %s: %w", url, err)
	}
	if err := gitcache.Fetch(cacheRoot, baseRef, commit); err != nil {
		return nil, fmt.Errorf("fetching %s: %w", commit, err)
	}

	// List tree with modes and match globs
	treeEntries, err := gitcache.ListTreeWithModes(cacheRoot, baseRef, commit)
	if err != nil {
		return nil, fmt.Errorf("listing tree for %s@%s: %w", cfg.Ref, commit[:12], err)
	}

	// Build paths list and mode map
	treePaths := make([]string, len(treeEntries))
	modeMap := make(map[string]string, len(treeEntries))
	for i, e := range treeEntries {
		treePaths[i] = e.Path
		modeMap[e.Path] = e.Mode
	}

	matched := envmatch.MatchGlobs(treePaths, cfg.Files, cfg.Ignore)
	if len(matched) == 0 {
		return nil, fmt.Errorf("no files matched for %s — check your glob patterns", cfg.Ref)
	}

	// If merge strategy needs the previous commit, fetch it too
	previousCommit := ""
	if lockEntry != nil {
		previousCommit = lockEntry.Commit
		if strategy == StrategyMerge && previousCommit != "" {
			if err := gitcache.Fetch(cacheRoot, baseRef, previousCommit); err != nil {
				// Non-fatal: if we can't fetch the old commit, fall back to replace
				previousCommit = ""
			}
		}
	}

	// Write files and compute checksums
	var lockFiles []lock.LockFile
	conflicts := 0

	for _, m := range matched {
		// Resolve dest relative to project root
		destPath := m.DestPath
		if !filepath.IsAbs(destPath) {
			destPath = filepath.Join(projectRoot, destPath)
		}
		destPath = filepath.Clean(destPath)

		// Path traversal check: ensure resolved path stays under projectRoot
		rel, err := filepath.Rel(projectRoot, destPath)
		if err != nil || strings.HasPrefix(rel, "..") {
			return nil, fmt.Errorf("path traversal rejected: %s resolves outside project root", m.DestPath)
		}

		// Read upstream content
		content, err := gitcache.ShowFile(cacheRoot, baseRef, commit, m.SourcePath)
		if err != nil {
			return nil, fmt.Errorf("reading %s from %s@%s: %w", m.SourcePath, cfg.Ref, commit[:12], err)
		}

		upstreamHash := fmt.Sprintf("%x", sha256.Sum256(content))

		// Determine file mode from upstream
		fileMode := gitModeToFileMode(modeMap[m.SourcePath])
		fileModeStr := fileModeToString(fileMode)

		// Detect local changes
		localChanged := false
		if lockEntry != nil {
			if oldFile := findLockFile(lockEntry, m.SourcePath); oldFile != nil {
				localHash, hashErr := lock.SHA256File(destPath)
				if hashErr == nil {
					// Fast-path: treat as unchanged only when local file matches upstream content
					if localHash == upstreamHash {
						lockFiles = append(lockFiles, lock.LockFile{
							Path:   m.SourcePath,
							Dest:   m.DestPath,
							SHA256: upstreamHash,
							Mode:   fileModeStr,
							Status: "unchanged",
						})
						continue
					}
					// Otherwise, detect local drift relative to the previous lock entry
					if localHash != oldFile.SHA256 {
						localChanged = true
					}
				}
			}
		}

		// Determine per-file strategy
		fileStrategy := strategy
		if localChanged && cfg.ResolveConflict != nil {
			fileStrategy = cfg.ResolveConflict(m.SourcePath, destPath)
		}

		// Apply strategy
		status := "replaced"
		finalHash := upstreamHash

		if !localChanged {
			// No local changes — always safe to replace
			if !cfg.DryRun {
				if err := writeFile(destPath, content, fileMode); err != nil {
					return nil, err
				}
			}
		} else {
			switch fileStrategy {
			case StrategyClient:
				// Keep local file, don't write upstream
				status = "kept"
				localHash, _ := lock.SHA256File(destPath)
				if localHash != "" {
					finalHash = localHash
				}

			case StrategyMerge:
				merged, hasConflict, mergeErr := doMerge(cacheRoot, baseRef, previousCommit, m.SourcePath, destPath, content)
				if mergeErr != nil {
					// Merge failed, fall back to replace with warning
					status = "replaced (merge failed: " + mergeErr.Error() + ")"
					if !cfg.DryRun {
						if err := writeFile(destPath, content, fileMode); err != nil {
							return nil, err
						}
					}
				} else if hasConflict {
					status = "conflict"
					conflicts++
					if !cfg.DryRun {
						if err := writeFile(destPath, merged, fileMode); err != nil {
							return nil, err
						}
					}
					finalHash = fmt.Sprintf("%x", sha256.Sum256(merged))
				} else {
					status = "merged"
					if !cfg.DryRun {
						if err := writeFile(destPath, merged, fileMode); err != nil {
							return nil, err
						}
					}
					finalHash = fmt.Sprintf("%x", sha256.Sum256(merged))
				}

			default: // StrategyReplace
				status = "replaced (local changes overwritten)"
				if !cfg.DryRun {
					if err := writeFile(destPath, content, fileMode); err != nil {
						return nil, err
					}
				}
			}
		}

		if cfg.DryRun {
			status += " (dry-run)"
		}

		lockFiles = append(lockFiles, lock.LockFile{
			Path:   m.SourcePath,
			Dest:   m.DestPath,
			SHA256: finalHash,
			Mode:   fileModeStr,
			Status: status,
		})
	}

	// Run post-sync hook (skip in dry-run mode)
	if cfg.OnPostSync != "" && !cfg.DryRun {
		if err := runHook(cfg.OnPostSync, projectRoot, hookStdout(cfg), hookStderr(cfg)); err != nil {
			return nil, fmt.Errorf("post-sync hook failed: %w", err)
		}
	}

	return &SyncResult{
		Ref:            baseRef,
		Label:          cfg.Label,
		Version:        cfg.Version,
		Commit:         commit,
		PreviousCommit: previousCommit,
		Files:          lockFiles,
		Message:        syncMessage(lockFiles, conflicts),
		Conflicts:      conflicts,
	}, nil
}

// doMerge performs a three-way merge for a single file.
// base = file at previousCommit, local = current file on disk, upstream = new content.
func doMerge(cacheRoot, baseRef, previousCommit, sourcePath, destPath string, upstream []byte) ([]byte, bool, error) {
	// Read local file
	local, err := os.ReadFile(destPath)
	if err != nil {
		return nil, false, fmt.Errorf("reading local %s: %w", destPath, err)
	}

	// Get base version (file at previous commit)
	var base []byte
	if previousCommit != "" {
		base, err = gitcache.ShowFile(cacheRoot, baseRef, previousCommit, sourcePath)
		if err != nil {
			// Can't get base — treat as new file, fall back
			return nil, false, fmt.Errorf("reading base version: %w", err)
		}
	} else {
		// No previous commit — can't do three-way merge
		return nil, false, fmt.Errorf("no previous commit for three-way merge base")
	}

	return gitcache.Merge3Way(local, base, upstream)
}

// writeFile ensures the dest directory exists and writes content with the given mode.
func writeFile(destPath string, content []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return fmt.Errorf("creating directory for %s: %w", destPath, err)
	}
	if err := os.WriteFile(destPath, content, mode); err != nil {
		return fmt.Errorf("writing %s: %w", destPath, err)
	}
	// os.WriteFile only applies mode on create; explicitly chmod for existing files
	if err := os.Chmod(destPath, mode); err != nil {
		return fmt.Errorf("setting permissions on %s: %w", destPath, err)
	}
	return nil
}

// findLockFile finds a lock file entry by source path.
func findLockFile(entry *lock.EnvEntry, sourcePath string) *lock.LockFile {
	if entry == nil {
		return nil
	}
	for i := range entry.Files {
		if entry.Files[i].Path == sourcePath {
			return &entry.Files[i]
		}
	}
	return nil
}

// hookStdout returns the configured stdout writer, defaulting to os.Stdout.
func hookStdout(cfg EnvConfig) io.Writer {
	if cfg.Stdout != nil {
		return cfg.Stdout
	}
	return os.Stdout
}

// hookStderr returns the configured stderr writer, defaulting to os.Stderr.
func hookStderr(cfg EnvConfig) io.Writer {
	if cfg.Stderr != nil {
		return cfg.Stderr
	}
	return os.Stderr
}

// runHook executes a shell command in the given directory.
func runHook(command, dir string, stdout, stderr io.Writer) error {
	cmd := exec.Command("sh", "-c", command)
	cmd.Dir = dir
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

// gitModeToFileMode converts a git tree mode to an os.FileMode.
// Returns 0755 for executable files (100755), 0644 otherwise.
func gitModeToFileMode(gitMode string) os.FileMode {
	if gitMode == "100755" {
		return 0755
	}
	return 0644
}

// fileModeToString converts an os.FileMode to a short string like "644" or "755".
func fileModeToString(mode os.FileMode) string {
	return strconv.FormatUint(uint64(mode.Perm()), 8)
}

// syncMessage builds a human-readable sync message from file results.
func syncMessage(files []lock.LockFile, conflicts int) string {
	replaced, kept, merged, unchanged := 0, 0, 0, 0
	for _, f := range files {
		// Strip dry-run suffix for counting
		status := strings.TrimSuffix(f.Status, " (dry-run)")
		switch {
		case status == "unchanged":
			unchanged++
		case strings.HasPrefix(status, "replaced"):
			replaced++
		case status == "kept":
			kept++
		case status == "merged":
			merged++
		case status == "conflict":
			merged++ // conflicts are a type of merge
		}
	}

	parts := []string{}
	total := len(files)
	if replaced == total {
		return fmt.Sprintf("%d file(s) synced", total)
	}
	if unchanged == total {
		return fmt.Sprintf("%d file(s) unchanged", total)
	}
	if replaced > 0 {
		parts = append(parts, fmt.Sprintf("%d replaced", replaced))
	}
	if kept > 0 {
		parts = append(parts, fmt.Sprintf("%d kept", kept))
	}
	if merged > 0 {
		parts = append(parts, fmt.Sprintf("%d merged", merged))
	}
	if conflicts > 0 {
		parts = append(parts, fmt.Sprintf("%d conflict(s)", conflicts))
	}
	if unchanged > 0 {
		parts = append(parts, fmt.Sprintf("%d unchanged", unchanged))
	}
	return strings.Join(parts, ", ")
}
