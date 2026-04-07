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
	ConfigDir       string                         // directory of b.yaml (for resolving relative paths)
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

	resolved := gitcache.ResolveGitURL(cfg.Ref, cfg.ConfigDir)
	baseRef := gitcache.RefBase(cfg.Ref)
	if resolved.IsLocal {
		baseRef = cfg.Ref // local paths may contain # or @ — use as-is for lock keys
	}

	// Resolve version to commit (or use forced commit)
	var commit string
	var err error
	if cfg.ForceCommit != "" {
		commit = cfg.ForceCommit
	} else if resolved.IsLocal {
		commit, err = gitcache.ResolveLocalRef(resolved.URL, cfg.Version)
		if err != nil {
			return nil, fmt.Errorf("resolving %s@%s: %w", cfg.Ref, cfg.Version, err)
		}
	} else {
		commit, err = gitcache.ResolveRefAuth(resolved.URL, cfg.Version, resolved.AuthHeader)
		if err != nil {
			return nil, fmt.Errorf("resolving %s@%s: %w", cfg.Ref, cfg.Version, err)
		}
	}

	// Check if up-to-date (skip when forcing a specific commit).
	// NOTE: This skips when the commit hasn't changed. If only the local config
	// changed (e.g. select filters), use --force to re-sync.
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

	// For local repos, use the repo directly. For remote, clone/fetch into cache.
	var repoDir string
	if resolved.IsLocal {
		repoDir = resolved.URL
	} else {
		if err := gitcache.EnsureCloneAuth(cacheRoot, baseRef, resolved.URL, resolved.AuthHeader); err != nil {
			return nil, fmt.Errorf("cloning %s: %w", resolved.URL, err)
		}
		if err := gitcache.FetchAuth(cacheRoot, baseRef, commit, resolved.AuthHeader); err != nil {
			return nil, fmt.Errorf("fetching %s: %w", commit, err)
		}
		repoDir = gitcache.CacheDir(cacheRoot, baseRef)
	}

	// List tree with modes and match globs
	treeEntries, err := gitcache.ListTreeWithModesDir(repoDir, commit)
	if err != nil {
		return nil, fmt.Errorf("listing tree for %s@%s: %w", cfg.Ref, safeShort(commit), err)
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
		if strategy == StrategyMerge && previousCommit != "" && !resolved.IsLocal {
			if err := gitcache.FetchAuth(cacheRoot, baseRef, previousCommit, resolved.AuthHeader); err != nil {
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

		// Path traversal check (including symlinks)
		if err := ValidatePathUnderRoot(projectRoot, destPath); err != nil {
			return nil, fmt.Errorf("path traversal rejected for %s: %w", m.DestPath, err)
		}

		// Read upstream content
		content, err := gitcache.ShowFileDir(repoDir, commit, m.SourcePath)
		if err != nil {
			return nil, fmt.Errorf("reading %s from %s@%s: %w", m.SourcePath, cfg.Ref, safeShort(commit), err)
		}

		// Build a reusable filter closure for this file's select spec. The
		// closure is applied to upstream here, and passed to doMerge below so
		// base and local get filtered symmetrically. When no select is set,
		// the closure is nil and doMerge skips filtering.
		var filterFn func([]byte) ([]byte, error)
		if len(m.Select) > 0 {
			sel := m.Select
			src := m.SourcePath
			filterFn = func(raw []byte) ([]byte, error) {
				return filterContent(raw, sel, src)
			}
		}

		// Apply select filter to upstream content. After this point,
		// `content` is the *filtered* upstream view — i.e. only the
		// selected scope. Writing it directly to a destination that
		// holds a full file would wipe out-of-scope content; the
		// strategy branches below handle that by splicing.
		if filterFn != nil {
			content, err = filterFn(content)
			if err != nil {
				return nil, fmt.Errorf("filtering %s: %w", m.SourcePath, err)
			}
		}

		// computeTargetBytes returns the exact bytes that should land on
		// disk for this file under a non-merge strategy. When there is no
		// select filter, that's just the (filtered or full) upstream
		// content. When a select is in play, we splice the filtered
		// upstream into the consumer's full local file so that
		// out-of-scope content (other top-level keys, envs:, profiles:)
		// is preserved across syncs.
		//
		// This is the fix for the #126 review comment: previously, the
		// no-local-changes branch and the StrategyReplace branch wrote
		// `content` directly, which silently dropped out-of-scope local
		// content on every sync after a merge had run.
		computeTargetBytes := func() ([]byte, error) {
			if filterFn == nil {
				return content, nil
			}
			localFull, readErr := os.ReadFile(destPath)
			if readErr != nil && !os.IsNotExist(readErr) {
				return nil, fmt.Errorf("reading local for splice: %w", readErr)
			}
			// localFull is nil for not-exist; pass it through to
			// spliceSelectedScope so YAML still gets the empty-doc
			// fast path AND JSON errors consistently regardless of
			// whether the local file exists yet. Per copilot review
			// on PR #126 round 2: skipping the splice for new files
			// silently bypassed the JSON-not-supported error, so the
			// first sync would succeed and the second would fail.
			spliced, spliceErr := spliceSelectedScope(localFull, content, m.Select, m.SourcePath)
			if spliceErr != nil {
				return nil, fmt.Errorf("splicing %s: %w", m.SourcePath, spliceErr)
			}
			return spliced, nil
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
					// Fast-path: treat as unchanged only when local file
					// matches upstream content. The comparison is meaningful
					// only when there's no select filter — when filterFn is
					// set, `content` is just the scoped slice and the
					// on-disk file is the full document, so the two hashes
					// will never match. In that case we fall through to
					// drift detection, which compares against the lock's
					// recorded hash for the spliced-target view.
					if filterFn == nil && localHash == upstreamHash {
						// Ensure file permissions match upstream even when content is identical
						if !cfg.DryRun {
							if info, statErr := os.Stat(destPath); statErr == nil {
								if info.Mode().Perm() != fileMode {
									if chmodErr := os.Chmod(destPath, fileMode); chmodErr != nil {
										return nil, fmt.Errorf("updating permissions for %s: %w", destPath, chmodErr)
									}
								}
							}
						}
						unchangedStatus := "unchanged"
						if cfg.DryRun {
							unchangedStatus += " (dry-run)"
						}
						lockFiles = append(lockFiles, lock.LockFile{
							Path:   m.SourcePath,
							Dest:   m.DestPath,
							SHA256: upstreamHash,
							Mode:   fileModeStr,
							Status: unchangedStatus,
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
			// No local changes — safe to write upstream. With a select
			// filter we MUST splice rather than write the filtered slice
			// directly, otherwise the consumer's out-of-scope content gets
			// silently dropped on every sync. computeTargetBytes returns
			// the right thing for both filtered and unfiltered cases.
			target, err := computeTargetBytes()
			if err != nil {
				return nil, err
			}
			if !cfg.DryRun {
				if err := writeFile(destPath, target, fileMode); err != nil {
					return nil, err
				}
			}
			finalHash = fmt.Sprintf("%x", sha256.Sum256(target))
		} else {
			switch fileStrategy {
			case StrategyClient:
				// Keep local file, don't write upstream
				status = "kept"
				localHash, _ := lock.SHA256File(destPath)
				if localHash != "" {
					finalHash = localHash
				}
				// Record on-disk mode (not upstream) since we're keeping the local file
				if info, statErr := os.Stat(destPath); statErr == nil {
					fileModeStr = fileModeToString(info.Mode().Perm())
				}

			case StrategyMerge:
				merged, hasConflict, mergeErr := doMerge(repoDir, previousCommit, m.SourcePath, destPath, content, filterFn)
				if mergeErr != nil {
					// Merge failed (e.g. no previous commit). Fall back to
					// the same splice path the no-local-changes branch
					// uses, so we never overwrite out-of-scope content
					// even when degrading to replace.
					target, tErr := computeTargetBytes()
					if tErr != nil {
						return nil, tErr
					}
					status = "replaced (merge failed: " + mergeErr.Error() + ")"
					if !cfg.DryRun {
						if err := writeFile(destPath, target, fileMode); err != nil {
							return nil, err
						}
					}
					finalHash = fmt.Sprintf("%x", sha256.Sum256(target))
				} else {
					// Splice the merged (filtered) scope back into the
					// consumer's full local file so that out-of-scope
					// content (other top-level keys, comments on
					// non-replaced keys, trailing layout) is preserved.
					// When no select is set, splice is a no-op pass-through.
					spliced := merged
					if filterFn != nil {
						localFull, readErr := os.ReadFile(destPath)
						if readErr != nil {
							return nil, fmt.Errorf("reading local for splice: %w", readErr)
						}
						spliceOut, spliceErr := spliceSelectedScope(localFull, merged, m.Select, m.SourcePath)
						if spliceErr != nil {
							return nil, fmt.Errorf("splicing merged %s: %w", m.SourcePath, spliceErr)
						}
						spliced = spliceOut
					}
					if hasConflict {
						status = "conflict"
						conflicts++
					} else {
						status = "merged"
					}
					if !cfg.DryRun {
						if err := writeFile(destPath, spliced, fileMode); err != nil {
							return nil, err
						}
					}
					finalHash = fmt.Sprintf("%x", sha256.Sum256(spliced))
				}

			default: // StrategyReplace
				// Replace with select also goes through the splice so the
				// consumer's out-of-scope content is preserved. The status
				// label still says "local changes overwritten" because the
				// IN-scope local edits will be lost — only the out-of-scope
				// portion is preserved.
				target, err := computeTargetBytes()
				if err != nil {
					return nil, err
				}
				status = "replaced (local changes overwritten)"
				if !cfg.DryRun {
					if err := writeFile(destPath, target, fileMode); err != nil {
						return nil, err
					}
				}
				finalHash = fmt.Sprintf("%x", sha256.Sum256(target))
			}
		}

		if cfg.DryRun {
			status += " (dry-run)"
		}

		// Record actual on-disk mode (may differ from target due to umask)
		if !cfg.DryRun && status != "kept" {
			if info, statErr := os.Stat(destPath); statErr == nil {
				fileModeStr = fileModeToString(info.Mode().Perm())
			}
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
//
// The three sides (local, base, upstream) are all passed through filterFn
// before being fed to the text-based 3-way merge. This is essential when a
// select filter is in play: the upstream content was already filtered in
// SyncEnv, so base and local MUST be filtered by the same selector or the
// merge will see the difference between "whole file" and "filtered subset"
// as deletions and clobber the consumer's out-of-scope content (envs,
// profiles, comments, etc). See issue #122.
//
//   - repoDir:        git repo directory (bare or local work tree)
//   - previousCommit: commit from the lock entry (required, caller checks)
//   - sourcePath:     file path inside the repo
//   - destPath:       local file on disk (the consumer's copy)
//   - upstream:       already-filtered upstream content
//   - filterFn:       applied to base and local to match upstream's shape;
//     may be nil when no select is configured
func doMerge(
	repoDir, previousCommit, sourcePath, destPath string,
	upstream []byte,
	filterFn func(content []byte) ([]byte, error),
) ([]byte, bool, error) {
	// Read local file (full, unfiltered)
	localFull, err := os.ReadFile(destPath)
	if err != nil {
		return nil, false, fmt.Errorf("reading local %s: %w", destPath, err)
	}

	// Read base version (file at previous commit, full, unfiltered)
	if previousCommit == "" {
		return nil, false, fmt.Errorf("no previous commit for three-way merge base")
	}
	baseFull, err := gitcache.ShowFileDir(repoDir, previousCommit, sourcePath)
	if err != nil {
		return nil, false, fmt.Errorf("reading base version: %w", err)
	}

	// Apply the same filter to base and local that was applied to upstream.
	// Without this step, git merge-file would diff "filtered upstream" against
	// "whole base/local" and flag every out-of-scope byte as a deletion.
	local := localFull
	base := baseFull
	if filterFn != nil {
		local, err = filterFn(localFull)
		if err != nil {
			return nil, false, fmt.Errorf("filtering local for merge: %w", err)
		}
		base, err = filterFn(baseFull)
		if err != nil {
			return nil, false, fmt.Errorf("filtering base for merge: %w", err)
		}
	}

	return gitcache.Merge3Way(local, base, upstream)
}

// writeFile ensures the dest directory exists and writes content with the given mode.
// For existing files, chmod is applied to correct permissions. For new files, umask is respected.
func writeFile(destPath string, content []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return fmt.Errorf("creating directory for %s: %w", destPath, err)
	}

	// Check if file already exists so we can respect umask on new files
	existed := false
	if _, err := os.Stat(destPath); err == nil {
		existed = true
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("checking existing file %s: %w", destPath, err)
	}

	if err := os.WriteFile(destPath, content, mode); err != nil {
		return fmt.Errorf("writing %s: %w", destPath, err)
	}
	// Only chmod existing files; new files get mode from os.WriteFile (respecting umask)
	if existed {
		if err := os.Chmod(destPath, mode); err != nil {
			return fmt.Errorf("setting permissions on %s: %w", destPath, err)
		}
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

// ValidatePathUnderRoot checks that destPath stays under root, including
// resolving symlinks on the nearest existing ancestor directory.
func ValidatePathUnderRoot(root, destPath string) error {
	// Basic relative-path check
	rel, err := filepath.Rel(root, destPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("resolves outside project root")
	}

	// Symlink-aware check: resolve the nearest existing ancestor and verify
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("resolving project root: %w", err)
	}

	ancestor := filepath.Dir(destPath)
	for {
		info, statErr := os.Stat(ancestor)
		if statErr == nil {
			if info.IsDir() {
				break
			}
		} else if !os.IsNotExist(statErr) {
			// Fail closed: unreadable ancestor could bypass symlink checks
			return fmt.Errorf("stat ancestor %q: %w", ancestor, statErr)
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			break
		}
		ancestor = parent
	}

	resolvedAncestor, err := filepath.EvalSymlinks(ancestor)
	if err != nil {
		if os.IsNotExist(err) {
			// Ancestor doesn't exist yet — will be created; basic check is sufficient
			return nil
		}
		return fmt.Errorf("resolving ancestor %q: %w", ancestor, err)
	}

	relResolved, err := filepath.Rel(resolvedRoot, resolvedAncestor)
	if err != nil || relResolved == ".." || strings.HasPrefix(relResolved, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("resolves outside project root via symlink")
	}

	// Also check if destPath itself is an existing symlink that escapes root
	if info, statErr := os.Lstat(destPath); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		resolved, err := filepath.EvalSymlinks(destPath)
		if err != nil {
			return fmt.Errorf("resolving symlink %q: %w", destPath, err)
		}
		relTarget, err := filepath.Rel(resolvedRoot, resolved)
		if err != nil || relTarget == ".." || strings.HasPrefix(relTarget, ".."+string(os.PathSeparator)) {
			return fmt.Errorf("symlink %s points outside project root", destPath)
		}
	}

	return nil
}

// safeShort returns the first 12 chars of s, or s itself if shorter.
func safeShort(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
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
			// conflicts are reported separately via the conflicts argument
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
