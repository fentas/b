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

// EnvConfig is the parsed configuration for a single env entry from b.yaml.
type EnvConfig struct {
	Ref      string                       // e.g. "github.com/org/infra"
	Label    string                       // fragment label (e.g. "monitoring")
	Version  string                       // tag/branch (resolved to commit in lock)
	Ignore   []string                     // global ignore patterns
	Strategy string                       // replace (default) | client | merge
	Files    map[string]envmatch.GlobConfig // glob → config
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
}

// SyncEnv syncs environment files from an upstream git repo.
// It clones/fetches the repo, matches files via globs, and writes them to dest.
//
// projectRoot is the base directory for resolving dest paths.
// cacheRoot is the git cache directory (defaults to ~/.cache/b/repos).
// lockEntry is the existing lock entry (nil if first sync).
func SyncEnv(cfg EnvConfig, projectRoot, cacheRoot string, lockEntry *lock.EnvEntry) (*SyncResult, error) {
	if cacheRoot == "" {
		cacheRoot = gitcache.DefaultCacheRoot()
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

	// Write files and compute checksums
	var lockFiles []lock.LockFile
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

		// Read file from cache
		content, err := gitcache.ShowFile(cacheRoot, baseRef, commit, m.SourcePath)
		if err != nil {
			return nil, fmt.Errorf("reading %s from %s@%s: %w", m.SourcePath, cfg.Ref, commit[:12], err)
		}

		// Ensure dest directory exists
		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return nil, fmt.Errorf("creating directory for %s: %w", destPath, err)
		}

		// Write file
		if err := os.WriteFile(destPath, content, 0644); err != nil {
			return nil, fmt.Errorf("writing %s: %w", destPath, err)
		}

		// Compute SHA256
		hash := fmt.Sprintf("%x", sha256.Sum256(content))

		lockFiles = append(lockFiles, lock.LockFile{
			Path:   m.SourcePath,
			Dest:   m.DestPath,
			SHA256: hash,
			Mode:   "644",
		})
	}

	previousCommit := ""
	if lockEntry != nil {
		previousCommit = lockEntry.Commit
	}

	return &SyncResult{
		Ref:            baseRef,
		Label:          cfg.Label,
		Version:        cfg.Version,
		Commit:         commit,
		PreviousCommit: previousCommit,
		Files:          lockFiles,
		Message:        fmt.Sprintf("%d file(s) synced", len(lockFiles)),
	}, nil
}
