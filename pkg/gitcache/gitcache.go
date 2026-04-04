// Package gitcache manages shallow bare git clones in a local cache directory.
// All operations shell out to the host git CLI — no go-git dependency.
package gitcache

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// DefaultCacheRoot returns ~/.cache/b/repos.
func DefaultCacheRoot() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "b", "repos")
}

// CacheDir returns the cache directory for a given ref.
// The directory name is the SHA-256 of the ref string.
func CacheDir(root, ref string) string {
	h := sha256.Sum256([]byte(ref))
	return filepath.Join(root, fmt.Sprintf("%x", h))
}

// EnsureClone creates a shallow bare clone if the cache directory doesn't exist.
// If it already exists, this is a no-op.
func EnsureClone(root, ref, url string) error {
	return EnsureCloneAuth(root, ref, url, "")
}

// EnsureCloneAuth creates a shallow bare clone with optional auth token.
func EnsureCloneAuth(root, ref, url, authHeader string) error {
	dir := CacheDir(root, ref)
	if _, err := os.Stat(dir); err == nil {
		return nil // already cached
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		return fmt.Errorf("creating cache root %s: %w", root, err)
	}
	ac := authCmd(authHeader, "clone", "--bare", "--depth", "1", url, dir)
	if err := runAuth(ac); err != nil {
		return fmt.Errorf("%s", redactToken(err.Error(), authHeader))
	}
	return nil
}

// Fetch fetches a specific commit or tag into the cache.
func Fetch(root, ref, commitOrTag string) error {
	return FetchAuth(root, ref, commitOrTag, "")
}

// FetchAuth fetches with optional auth token.
func FetchAuth(root, ref, commitOrTag, authHeader string) error {
	dir := CacheDir(root, ref)
	ac := authCmd(authHeader, "-C", dir, "fetch", "--depth", "1", "origin", commitOrTag)
	if err := runAuth(ac); err != nil {
		return fmt.Errorf("%s", redactToken(err.Error(), authHeader))
	}
	return nil
}

// ResolveLocalRef resolves a version to a commit SHA for a local repo.
func ResolveLocalRef(repoPath, version string) (string, error) {
	if version == "" || version == "HEAD" {
		out, err := output("git", "-C", repoPath, "rev-parse", "HEAD")
		if err != nil {
			return "", fmt.Errorf("git rev-parse HEAD in %s: %w", repoPath, err)
		}
		return strings.TrimSpace(out), nil
	}
	// Try as a ref
	out, err := output("git", "-C", repoPath, "rev-parse", version)
	if err != nil {
		return "", fmt.Errorf("git rev-parse %s in %s: %w", version, repoPath, err)
	}
	return strings.TrimSpace(out), nil
}

// ResolveRef resolves a version (tag/branch/HEAD) to a commit SHA via ls-remote.
// If version is empty, it resolves HEAD.
func ResolveRef(url, version string) (string, error) {
	return ResolveRefAuth(url, version, "")
}

// ResolveRefAuth resolves with optional auth token.
func ResolveRefAuth(url, version, authHeader string) (string, error) {
	if version == "" {
		version = "HEAD"
	}
	ac := authCmd(authHeader, "ls-remote", url, version)
	out, err := outputAuth(ac)
	if err != nil {
		return "", fmt.Errorf("git ls-remote %s %s: %s", url, version, redactToken(err.Error(), authHeader))
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for _, line := range lines {
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			return parts[0], nil
		}
	}
	// If version is HEAD and ls-remote returned nothing useful, try refs/heads/main, master
	if version == "HEAD" {
		for _, branch := range []string{"refs/heads/main", "refs/heads/master"} {
			fallbackAC := authCmd(authHeader, "ls-remote", url, branch)
			out, err = outputAuth(fallbackAC)
			if err == nil {
				parts := strings.Fields(strings.TrimSpace(out))
				if len(parts) >= 2 {
					return parts[0], nil
				}
			}
		}
	}
	return "", fmt.Errorf("could not resolve %q for %s", version, url)
}

// TreeEntry represents a single entry from git ls-tree with its file mode.
type TreeEntry struct {
	Path string
	Mode string // git mode, e.g. "100644", "100755"
}

// ListTree returns all file paths in the repo at the given commit.
// Uses --name-only for efficiency when modes are not needed.
func ListTree(root, ref, commit string) ([]string, error) {
	dir := CacheDir(root, ref)
	out, err := output("git", "-C", dir, "ls-tree", "-r", "--name-only", commit)
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(strings.TrimSpace(out), "\n"), nil
}

// ListTreeWithModes returns all file entries with their git modes.
func ListTreeWithModes(root, ref, commit string) ([]TreeEntry, error) {
	dir := CacheDir(root, ref)
	return ListTreeWithModesDir(dir, commit)
}

// ShowFile returns the contents of a single file at the given commit.
func ShowFile(root, ref, commit, path string) ([]byte, error) {
	dir := CacheDir(root, ref)
	return ShowFileDir(dir, commit, path)
}

// ShowFileDir returns the contents of a single file at the given commit using a direct directory path.
func ShowFileDir(dir, commit, path string) ([]byte, error) {
	return outputBytes("git", "-C", dir, "show", commit+":"+path)
}

// ListTreeWithModesDir returns all file entries with their git modes, using a direct directory path.
func ListTreeWithModesDir(dir, commit string) ([]TreeEntry, error) {
	out, err := output("git", "-C", dir, "ls-tree", "-r", commit)
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	entries := make([]TreeEntry, 0, len(lines))
	for _, line := range lines {
		tabIdx := strings.IndexByte(line, '\t')
		if tabIdx == -1 {
			return nil, fmt.Errorf("git ls-tree: unexpected line format: %q", line)
		}
		path := line[tabIdx+1:]
		fields := strings.Fields(line[:tabIdx])
		mode := "100644"
		if len(fields) >= 1 {
			mode = fields[0]
		}
		entries = append(entries, TreeEntry{Path: path, Mode: mode})
	}
	return entries, nil
}

// runAuth executes a git command with optional auth env vars.
func runAuth(ac AuthCmd) error {
	cmd := exec.Command(ac.Args[0], ac.Args[1:]...)
	if len(ac.Env) > 0 {
		cmd.Env = append(os.Environ(), ac.Env...)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w\n%s", strings.Join(ac.Args, " "), err, stderr.String())
	}
	return nil
}

// output executes a git command and returns stdout as a string.
func output(args ...string) (string, error) {
	return outputAuth(AuthCmd{Args: args})
}

// outputAuth executes a git command with optional auth env vars.
func outputAuth(ac AuthCmd) (string, error) {
	cmd := exec.Command(ac.Args[0], ac.Args[1:]...)
	if len(ac.Env) > 0 {
		cmd.Env = append(os.Environ(), ac.Env...)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %w\n%s", strings.Join(ac.Args, " "), err, stderr.String())
	}
	return stdout.String(), nil
}

// outputBytes executes a git command and returns stdout as bytes.
func outputBytes(args ...string) ([]byte, error) {
	cmd := exec.Command(args[0], args[1:]...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s: %w\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return out, nil
}

// Merge3Way performs a three-way merge using git merge-file.
// Returns the merged content, whether conflicts exist, and any error.
// Exit code 0 = clean merge, 1 = conflicts (markers in result), 2+ = error.
func Merge3Way(local, base, upstream []byte) ([]byte, bool, error) {
	tmpDir, err := os.MkdirTemp("", "b-merge-*")
	if err != nil {
		return nil, false, fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	localPath := filepath.Join(tmpDir, "local")
	basePath := filepath.Join(tmpDir, "base")
	upstreamPath := filepath.Join(tmpDir, "upstream")

	if err := os.WriteFile(localPath, local, 0644); err != nil {
		return nil, false, err
	}
	if err := os.WriteFile(basePath, base, 0644); err != nil {
		return nil, false, err
	}
	if err := os.WriteFile(upstreamPath, upstream, 0644); err != nil {
		return nil, false, err
	}

	cmd := exec.Command("git", "merge-file", "--diff3", localPath, basePath, upstreamPath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	mergeErr := cmd.Run()

	result, readErr := os.ReadFile(localPath)
	if readErr != nil {
		return nil, false, fmt.Errorf("reading merge result: %w", readErr)
	}

	if mergeErr != nil {
		if exitErr, ok := mergeErr.(*exec.ExitError); ok {
			code := exitErr.ExitCode()
			if code > 0 && code < 128 {
				// Exit code 1..127 = number of conflicts (markers in result)
				// Exit code >= 128 = killed by signal (real error)
				return result, true, nil
			}
		}
		return nil, false, fmt.Errorf("git merge-file: %w\n%s", mergeErr, stderr.String())
	}

	return result, false, nil
}

// DiffNoIndex returns a unified diff between two byte slices using git diff --no-index.
func DiffNoIndex(a, b []byte, labelA, labelB string) (string, error) {
	tmpDir, err := os.MkdirTemp("", "b-diff-*")
	if err != nil {
		return "", fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	pathA := filepath.Join(tmpDir, "a")
	pathB := filepath.Join(tmpDir, "b")
	if err := os.WriteFile(pathA, a, 0644); err != nil {
		return "", err
	}
	if err := os.WriteFile(pathB, b, 0644); err != nil {
		return "", err
	}

	cmd := exec.Command("git", "diff", "--no-index",
		fmt.Sprintf("--src-prefix=%s/", labelA),
		fmt.Sprintf("--dst-prefix=%s/", labelB),
		pathA, pathB)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// Exit code 1 = differences found (normal for diff)
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return stdout.String(), nil
		}
		return "", fmt.Errorf("git diff --no-index: %w\n%s", err, stderr.String())
	}

	return stdout.String(), nil
}

// GitURL converts a ref to a clone-ready URL (without auth credentials).
// Delegates to ResolveGitURL with empty configDir. For local/relative paths
// or auth token support, use ResolveGitURL directly.
func GitURL(ref string) string {
	resolved := ResolveGitURL(ref, "")
	return resolved.URL
}

// RefBase strips version and fragment from a ref, returning the bare repo ref.
// Local paths (starting with / or ./ or ../) are returned unchanged.
func RefBase(ref string) string {
	// Local paths may contain # or @ — return as-is
	if strings.HasPrefix(ref, "/") || strings.HasPrefix(ref, "./") || strings.HasPrefix(ref, "../") {
		return ref
	}
	if i := strings.Index(ref, "#"); i != -1 {
		ref = ref[:i]
	}
	if i := strings.LastIndex(ref, "@"); i > 0 && !IsSSHUserAt(ref, i) {
		ref = ref[:i]
	}
	return ref
}

// RefLabel extracts the fragment label from a ref (after #).
// Returns empty string if no label. Local paths return empty (# is part of path).
func RefLabel(ref string) string {
	if isLocalPath(ref) {
		return ""
	}
	if i := strings.Index(ref, "#"); i != -1 {
		rest := ref[i+1:]
		if j := strings.LastIndex(rest, "@"); j != -1 {
			return rest[:j]
		}
		return rest
	}
	return ""
}

// RefVersion extracts the version from a ref (after last @).
// Returns empty string if no version. Skips SSH user@ prefix and local paths.
func RefVersion(ref string) string {
	if isLocalPath(ref) {
		return ""
	}
	if i := strings.LastIndex(ref, "@"); i > 0 && !IsSSHUserAt(ref, i) {
		return ref[i+1:]
	}
	return ""
}

// isLocalPath returns true if the ref looks like a local filesystem path.
func isLocalPath(ref string) bool {
	return strings.HasPrefix(ref, "/") || strings.HasPrefix(ref, "./") || strings.HasPrefix(ref, "../")
}
