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
	return strings.HasPrefix(ref, "git://") || gitcache.IsSSHURL(ref)
}

// LatestVersion returns the HEAD commit SHA for the repo.
func (g *Git) LatestVersion(ref string) (string, error) {
	repo, _, err := parseGitRef(ref)
	if err != nil {
		return "", err
	}

	if isLocalRepo(repo) {
		return gitcache.ResolveLocalRef(repo, "HEAD")
	}

	resolved := gitcache.ResolveGitURL(repo, "")
	return gitcache.ResolveRefAuth(resolved.URL, "HEAD", resolved.AuthToken)
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
	resolved := gitcache.ResolveGitURL(repo, "")

	if err := gitcache.EnsureCloneAuth(cacheRoot, repo, resolved.URL, resolved.AuthToken); err != nil {
		return "", fmt.Errorf("cloning %s: %w", resolved.URL, err)
	}

	commit := version
	if commit == "" {
		var err error
		commit, err = gitcache.ResolveRefAuth(resolved.URL, "HEAD", resolved.AuthToken)
		if err != nil {
			return "", err
		}
	}

	// Fetch the specific ref if not already present
	if err := gitcache.FetchAuth(cacheRoot, repo, commit, resolved.AuthToken); err != nil {
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

	// Strip version suffix (@v1.0.0) before parsing — but preserve git@ SSH prefix
	if i := strings.LastIndex(raw, "@"); i > 0 {
		prefix := raw[:i]
		if prefix != "git" && prefix != "ssh://git" {
			raw = raw[:i] // strip version
		}
	}

	// SSH implicit format: git@host:org/repo:filepath
	// Has two colons — first is SSH host separator, last is our filepath separator.
	if gitcache.IsSSHURL(raw) {
		idx := strings.LastIndex(raw, ":")
		if idx < 0 {
			return "", "", fmt.Errorf("git ref missing filepath separator ':' — expected <ssh-url>:<filepath>, got %s", ref)
		}
		// For SSH implicit (git@host:org/repo:file), we need at least 2 colons
		// First colon is after host, last colon separates filepath
		firstColon := strings.Index(raw, ":")
		if firstColon == idx {
			// Only one colon — it's the SSH host separator, no filepath
			return "", "", fmt.Errorf("git ref missing filepath separator — expected <ssh-url>:<filepath>, got %s", ref)
		}
		return raw[:idx], raw[idx+1:], nil
	}

	// Local absolute path: /home/user/repo:.scripts/lo
	if strings.HasPrefix(raw, "/") {
		idx := strings.Index(raw, ":")
		if idx < 0 {
			return "", "", fmt.Errorf("git ref missing filepath separator ':' — expected git://<repo>:<filepath>, got %s", ref)
		}
		return raw[:idx], raw[idx+1:], nil
	}

	// Relative local path: ../../repo:filepath
	if strings.HasPrefix(raw, ".") {
		idx := strings.Index(raw, ":")
		if idx < 0 {
			return "", "", fmt.Errorf("git ref missing filepath separator ':' — expected git://<repo>:<filepath>, got %s", ref)
		}
		return raw[:idx], raw[idx+1:], nil
	}

	// Remote: github.com/org/repo:scripts/tool.sh
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
