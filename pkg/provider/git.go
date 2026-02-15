package provider

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/fentas/b/pkg/gitcache"
)

func init() {
	Register(&Git{})
}

// Git sources binaries from git repositories (local or remote).
//
// Ref format: git://<repo>:<filepath>
//   - Local:  git:///absolute/path/to/repo:.scripts/lo
//   - Remote: git://github.com/org/repo:scripts/tool.sh
type Git struct{}

func (g *Git) Name() string { return "git" }

func (g *Git) Match(ref string) bool {
	return strings.HasPrefix(ref, "git://")
}

// LatestVersion returns the HEAD commit SHA for the repo.
func (g *Git) LatestVersion(ref string) (string, error) {
	repo, _, err := parseGitRef(ref)
	if err != nil {
		return "", err
	}

	if isLocalRepo(repo) {
		out, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
		if err != nil {
			return "", fmt.Errorf("git rev-parse HEAD in %s: %w", repo, err)
		}
		return strings.TrimSpace(string(out)), nil
	}

	url := "https://" + repo + ".git"
	return gitcache.ResolveRef(url, "HEAD")
}

// FetchRelease is not used for git — use Install instead.
func (g *Git) FetchRelease(ref, version string) (*Release, error) {
	return nil, fmt.Errorf("git provider does not use FetchRelease; use Install()")
}

// Install extracts a single file from a git repo and copies it to destDir.
func (g *Git) Install(ref, version, destDir string) (string, error) {
	repo, filePath, err := parseGitRef(ref)
	if err != nil {
		return "", err
	}

	name := filepath.Base(filePath)
	dest := filepath.Join(destDir, name)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return "", err
	}

	if isLocalRepo(repo) {
		return g.installFromLocal(repo, filePath, version, dest)
	}
	return g.installFromRemote(repo, filePath, version, dest)
}

// installFromLocal extracts a file from a local git repo.
func (g *Git) installFromLocal(repo, filePath, version, dest string) (string, error) {
	treeish := version
	if treeish == "" {
		treeish = "HEAD"
	}

	// Use git show to read the file at the given revision
	obj := treeish + ":" + filePath
	data, err := exec.Command("git", "-C", repo, "show", obj).Output()
	if err != nil {
		return "", fmt.Errorf("git show %s in %s: %w", obj, repo, err)
	}

	if err := os.WriteFile(dest, data, 0755); err != nil {
		return "", err
	}
	return dest, nil
}

// installFromRemote clones/caches a remote repo and extracts the file.
func (g *Git) installFromRemote(repo, filePath, version, dest string) (string, error) {
	cacheRoot := gitcache.DefaultCacheRoot()
	url := "https://" + repo + ".git"

	if err := gitcache.EnsureClone(cacheRoot, repo, url); err != nil {
		return "", fmt.Errorf("cloning %s: %w", url, err)
	}

	commit := version
	if commit == "" {
		var err error
		commit, err = gitcache.ResolveRef(url, "HEAD")
		if err != nil {
			return "", err
		}
	}

	// Fetch the specific ref if not already present
	if err := gitcache.Fetch(cacheRoot, repo, commit); err != nil {
		// Ignore fetch errors if the commit is already cached
		_ = err
	}

	data, err := gitcache.ShowFile(cacheRoot, repo, commit, filePath)
	if err != nil {
		return "", fmt.Errorf("reading %s at %s from %s: %w", filePath, commit, repo, err)
	}

	if err := os.WriteFile(dest, data, 0755); err != nil {
		return "", err
	}
	return dest, nil
}

// parseGitRef splits "git://<repo>:<filepath>" into repo and file path.
// For local repos: git:///absolute/path:.scripts/lo -> ("/absolute/path", ".scripts/lo")
// For remote repos: git://github.com/org/repo:path/file -> ("github.com/org/repo", "path/file")
func parseGitRef(ref string) (repo, filePath string, err error) {
	raw := strings.TrimPrefix(ref, "git://")
	if raw == "" {
		return "", "", fmt.Errorf("empty git ref: %s", ref)
	}

	// Strip version suffix (@v1.0.0) before parsing
	raw, _ = ParseRef("git://" + raw)
	raw = strings.TrimPrefix(raw, "git://")

	// Local absolute path: starts with /
	// The colon separator between repo and filepath must be found carefully.
	// For local: /home/user/repo:.scripts/lo — first colon after the path
	// For remote: github.com/org/repo:scripts/tool.sh — first colon
	if strings.HasPrefix(raw, "/") {
		// Local path: find colon that separates repo path from file path
		idx := strings.Index(raw, ":")
		if idx < 0 {
			return "", "", fmt.Errorf("git ref missing filepath separator ':' — expected git://<repo>:<filepath>, got %s", ref)
		}
		return raw[:idx], raw[idx+1:], nil
	}

	// Remote: find the colon separator
	idx := strings.Index(raw, ":")
	if idx < 0 {
		return "", "", fmt.Errorf("git ref missing filepath separator ':' — expected git://<repo>:<filepath>, got %s", ref)
	}
	return raw[:idx], raw[idx+1:], nil
}

// isLocalRepo returns true if the repo path is an absolute local path.
func isLocalRepo(repo string) bool {
	return strings.HasPrefix(repo, "/")
}
