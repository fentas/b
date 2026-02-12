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
	dir := CacheDir(root, ref)
	if _, err := os.Stat(dir); err == nil {
		return nil // already cached
	}
	// git clone --bare --depth 1 <url> <dir>
	return run("git", "clone", "--bare", "--depth", "1", url, dir)
}

// Fetch fetches a specific commit or tag into the cache.
func Fetch(root, ref, commitOrTag string) error {
	dir := CacheDir(root, ref)
	return run("git", "-C", dir, "fetch", "--depth", "1", "origin", commitOrTag)
}

// ResolveRef resolves a version (tag/branch/HEAD) to a commit SHA via ls-remote.
// If version is empty, it resolves HEAD.
func ResolveRef(url, version string) (string, error) {
	if version == "" {
		version = "HEAD"
	}
	out, err := output("git", "ls-remote", url, version)
	if err != nil {
		return "", fmt.Errorf("git ls-remote %s %s: %w", url, version, err)
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
			out, err = output("git", "ls-remote", url, branch)
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

// ListTree returns all file paths in the repo at the given commit.
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

// ShowFile returns the contents of a single file at the given commit.
func ShowFile(root, ref, commit, path string) ([]byte, error) {
	dir := CacheDir(root, ref)
	return outputBytes("git", "-C", dir, "show", commit+":"+path)
}

// run executes a git command, returning an error that includes stderr.
func run(args ...string) error {
	cmd := exec.Command(args[0], args[1:]...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return nil
}

// output executes a git command and returns stdout as a string.
func output(args ...string) (string, error) {
	cmd := exec.Command(args[0], args[1:]...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %w\n%s", strings.Join(args, " "), err, stderr.String())
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

// GitURL converts a ref like "github.com/org/repo" or "github.com/org/repo#label"
// to a clone URL like "https://github.com/org/repo.git".
func GitURL(ref string) string {
	// Strip fragment label (e.g. #monitoring)
	if i := strings.Index(ref, "#"); i != -1 {
		ref = ref[:i]
	}
	// Strip version
	if i := strings.Index(ref, "@"); i != -1 {
		ref = ref[:i]
	}
	return "https://" + ref + ".git"
}

// RefBase strips version and fragment from a ref, returning the bare repo ref.
// e.g. "github.com/org/repo@v2.0" → "github.com/org/repo"
// e.g. "github.com/org/repo#label" → "github.com/org/repo"
func RefBase(ref string) string {
	if i := strings.Index(ref, "#"); i != -1 {
		ref = ref[:i]
	}
	if i := strings.Index(ref, "@"); i != -1 {
		ref = ref[:i]
	}
	return ref
}

// RefLabel extracts the fragment label from a ref (after #).
// Returns empty string if no label.
func RefLabel(ref string) string {
	if i := strings.Index(ref, "#"); i != -1 {
		rest := ref[i+1:]
		// Strip version if present after label
		if j := strings.Index(rest, "@"); j != -1 {
			return rest[:j]
		}
		return rest
	}
	return ""
}

// RefVersion extracts the version from a ref (after @).
// Returns empty string if no version.
func RefVersion(ref string) string {
	if i := strings.Index(ref, "@"); i != -1 {
		return ref[i+1:]
	}
	return ""
}
