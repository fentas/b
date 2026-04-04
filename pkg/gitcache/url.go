package gitcache

import (
	"os"
	"path/filepath"
	"strings"
)

// ResolvedRef holds a parsed and resolved git reference.
type ResolvedRef struct {
	URL     string // clone URL (https://... or local path)
	IsLocal bool   // true for local filesystem repos
}

// ResolveGitURL converts a ref to a clone-ready URL or local path.
// Handles:
//   - Remote refs: "github.com/org/repo" → "https://github.com/org/repo.git"
//   - git:// protocol: "git://../../repo" → resolved local path or "https://host/repo.git"
//   - Local absolute paths: "/home/user/repo" → "/home/user/repo"
//   - Local relative paths: "../../repo" → resolved to absolute (relative to configDir)
//
// Auth tokens are injected into HTTPS URLs when available.
func ResolveGitURL(ref, configDir string) ResolvedRef {
	// Strip fragment label (e.g. #monitoring)
	if i := strings.Index(ref, "#"); i != -1 {
		ref = ref[:i]
	}
	// Strip version (e.g. @v2.0)
	if i := strings.Index(ref, "@"); i != -1 {
		ref = ref[:i]
	}

	// Handle git:// protocol prefix
	if strings.HasPrefix(ref, "git://") {
		raw := strings.TrimPrefix(ref, "git://")
		// Strip colon-separated filepath (git://repo:filepath)
		if i := strings.Index(raw, ":"); i >= 0 {
			raw = raw[:i]
		}
		return resolveRepo(raw, configDir)
	}

	// Handle absolute local paths
	if strings.HasPrefix(ref, "/") {
		return ResolvedRef{URL: ref, IsLocal: true}
	}

	// Handle relative paths (starts with . or ..)
	if strings.HasPrefix(ref, ".") || strings.HasPrefix(ref, "..") {
		abs := filepath.Join(configDir, ref)
		abs = filepath.Clean(abs)
		return ResolvedRef{URL: abs, IsLocal: true}
	}

	// Remote ref: "github.com/org/repo" → "https://github.com/org/repo.git"
	url := "https://" + ref + ".git"
	url = injectAuth(url, ref)
	return ResolvedRef{URL: url, IsLocal: false}
}

// resolveRepo determines if a repo path is local or remote.
func resolveRepo(repo, configDir string) ResolvedRef {
	// Absolute local path
	if strings.HasPrefix(repo, "/") {
		return ResolvedRef{URL: repo, IsLocal: true}
	}

	// Relative path (starts with . or ..)
	if strings.HasPrefix(repo, ".") || strings.HasPrefix(repo, "..") {
		abs := filepath.Join(configDir, repo)
		abs = filepath.Clean(abs)
		return ResolvedRef{URL: abs, IsLocal: true}
	}

	// Remote: "github.com/org/repo"
	url := "https://" + repo + ".git"
	url = injectAuth(url, repo)
	return ResolvedRef{URL: url, IsLocal: false}
}

// injectAuth embeds an auth token into an HTTPS URL if the corresponding
// environment variable is set. Supports GitHub, GitLab, and Gitea/Codeberg.
func injectAuth(url, ref string) string {
	host := ref
	if i := strings.Index(host, "/"); i > 0 {
		host = host[:i]
	}

	var token string
	switch {
	case strings.Contains(host, "github"):
		token = os.Getenv("GITHUB_TOKEN")
	case strings.Contains(host, "gitlab"):
		token = os.Getenv("GITLAB_TOKEN")
	case strings.Contains(host, "gitea") || strings.Contains(host, "codeberg"):
		token = os.Getenv("GITEA_TOKEN")
	}

	if token == "" {
		return url
	}

	// Embed token: https://host/... → https://x-access-token:TOKEN@host/...
	return strings.Replace(url, "https://", "https://x-access-token:"+token+"@", 1)
}
