// Package env handles syncing environment files from upstream git repos.
package env

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
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
	Ref             string                        // e.g. "github.com/org/infra"
	Label           string                        // fragment label (e.g. "monitoring")
	Version         string                        // tag/branch (resolved to commit in lock)
	Ignore          []string                      // global ignore patterns
	Strategy        string                        // replace (default) | client | merge
	Files           map[string]envmatch.GlobConfig // glob → config
	ResolveConflict ConflictFunc                  // optional: called per-file when local changes detected
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

	// Resolve version to commit
	commit, err := gitcache.ResolveRef(url, cfg.Version)
	if err != nil {
		return nil, fmt.Errorf("resolving %s@%s: %w", cfg.Ref, cfg.Version, err)
	}

	// Check if up-to-date
	if lockEntry != nil && lockEntry.Commit == commit {
		return &SyncResult{
			Ref:     baseRef,
			Label:   cfg.Label,
			Version: cfg.Version,
			Commit:  commit,
			Skipped: true,
			Message: "(up to date)",
		}, nil
	}

	// Clone and fetch
	if err := gitcache.EnsureClone(cacheRoot, baseRef, url); err != nil {
		return nil, fmt.Errorf("cloning %s: %w", url, err)
	}
	if err := gitcache.Fetch(cacheRoot, baseRef, commit); err != nil {
		return nil, fmt.Errorf("fetching %s: %w", commit, err)
	}

	// List tree and match globs
	tree, err := gitcache.ListTree(cacheRoot, baseRef, commit)
	if err != nil {
		return nil, fmt.Errorf("listing tree for %s@%s: %w", cfg.Ref, commit[:12], err)
	}

	matched := envmatch.MatchGlobs(tree, cfg.Files, cfg.Ignore)
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
		// Path traversal check
		if strings.Contains(m.DestPath, "..") {
			return nil, fmt.Errorf("path traversal rejected: %s", m.DestPath)
		}

		// Resolve dest relative to project root
		destPath := m.DestPath
		if !filepath.IsAbs(destPath) {
			destPath = filepath.Join(projectRoot, destPath)
		}

		// Read upstream content
		content, err := gitcache.ShowFile(cacheRoot, baseRef, commit, m.SourcePath)
		if err != nil {
			return nil, fmt.Errorf("reading %s from %s@%s: %w", m.SourcePath, cfg.Ref, commit[:12], err)
		}

		upstreamHash := fmt.Sprintf("%x", sha256.Sum256(content))

		// Detect local changes
		localChanged := false
		if lockEntry != nil {
			if oldFile := findLockFile(lockEntry, m.SourcePath); oldFile != nil {
				localHash, hashErr := lock.SHA256File(destPath)
				if hashErr == nil && localHash != oldFile.SHA256 {
					localChanged = true
				}
			}
		}

		// Determine per-file strategy
		fileStrategy := strategy
		if localChanged && cfg.ResolveConflict != nil {
			fileStrategy = cfg.ResolveConflict(m.SourcePath, m.DestPath)
		}

		// Apply strategy
		status := "replaced"
		finalHash := upstreamHash

		if !localChanged {
			// No local changes — always safe to replace
			if err := writeFile(destPath, content); err != nil {
				return nil, err
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
					if err := writeFile(destPath, content); err != nil {
						return nil, err
					}
				} else if hasConflict {
					status = "conflict"
					conflicts++
					if err := writeFile(destPath, merged); err != nil {
						return nil, err
					}
					finalHash = fmt.Sprintf("%x", sha256.Sum256(merged))
				} else {
					status = "merged"
					if err := writeFile(destPath, merged); err != nil {
						return nil, err
					}
					finalHash = fmt.Sprintf("%x", sha256.Sum256(merged))
				}

			default: // StrategyReplace
				status = "replaced (local changes overwritten)"
				if err := writeFile(destPath, content); err != nil {
					return nil, err
				}
			}
		}

		lockFiles = append(lockFiles, lock.LockFile{
			Path:   m.SourcePath,
			Dest:   m.DestPath,
			SHA256: finalHash,
			Mode:   "644",
			Status: status,
		})
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

// writeFile ensures the dest directory exists and writes content.
func writeFile(destPath string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return fmt.Errorf("creating directory for %s: %w", destPath, err)
	}
	if err := os.WriteFile(destPath, content, 0644); err != nil {
		return fmt.Errorf("writing %s: %w", destPath, err)
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

// syncMessage builds a human-readable sync message from file results.
func syncMessage(files []lock.LockFile, conflicts int) string {
	replaced, kept, merged := 0, 0, 0
	for _, f := range files {
		switch {
		case strings.HasPrefix(f.Status, "replaced"):
			replaced++
		case f.Status == "kept":
			kept++
		case f.Status == "merged":
			merged++
		case f.Status == "conflict":
			merged++ // conflicts are a type of merge
		}
	}

	parts := []string{}
	total := len(files)
	if replaced == total {
		return fmt.Sprintf("%d file(s) synced", total)
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
	return strings.Join(parts, ", ")
}
