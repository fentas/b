package gitcache

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ResolvedRef holds a parsed and resolved git reference.
type ResolvedRef struct {
	URL       string // clone URL (https://... or local path) — never contains credentials
	IsLocal   bool   // true for local filesystem repos
	AuthToken string // auth token for HTTPS operations (empty if none)
}

// ResolveGitURL converts a ref to a clone-ready URL or local path.
// Handles:
//   - Remote refs: "github.com/org/repo" → "https://github.com/org/repo.git"
//   - git:// protocol: "git://../../repo" → resolved local path or "https://host/repo.git"
//   - Local absolute paths: "/home/user/repo" → "/home/user/repo"
//   - Local relative paths: "../../repo" → resolved to absolute (relative to configDir)
//
// Auth tokens are detected from environment variables but NOT embedded in URLs.
// Use AuthToken with git -c http.extraHeader for authenticated operations.
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
		abs, err := filepath.Abs(filepath.Join(configDir, ref))
		if err != nil {
			return ResolvedRef{URL: filepath.Clean(filepath.Join(configDir, ref)), IsLocal: true}
		}
		return ResolvedRef{URL: abs, IsLocal: true}
	}

	// Remote ref: "github.com/org/repo" → "https://github.com/org/repo.git"
	url := "https://" + ref + ".git"
	token := detectAuthToken(ref)
	return ResolvedRef{URL: url, IsLocal: false, AuthToken: token}
}

// resolveRepo determines if a repo path is local or remote.
func resolveRepo(repo, configDir string) ResolvedRef {
	// Absolute local path
	if strings.HasPrefix(repo, "/") {
		return ResolvedRef{URL: repo, IsLocal: true}
	}

	// Relative path (starts with . or ..)
	if strings.HasPrefix(repo, ".") || strings.HasPrefix(repo, "..") {
		abs, err := filepath.Abs(filepath.Join(configDir, repo))
		if err != nil {
			return ResolvedRef{URL: filepath.Clean(filepath.Join(configDir, repo)), IsLocal: true}
		}
		return ResolvedRef{URL: abs, IsLocal: true}
	}

	// Remote: "github.com/org/repo"
	url := "https://" + repo + ".git"
	token := detectAuthToken(repo)
	return ResolvedRef{URL: url, IsLocal: false, AuthToken: token}
}

// matchesTrustedHost reports whether host exactly matches domain or is a subdomain.
func matchesTrustedHost(host, domain string) bool {
	host = strings.ToLower(host)
	return host == domain || strings.HasSuffix(host, "."+domain)
}

// detectAuthToken returns the auth token for a given ref based on trusted host matching.
// Only matches exact domains or subdomains to prevent token leakage to attacker-controlled hosts.
func detectAuthToken(ref string) string {
	host := ref
	if i := strings.Index(host, "/"); i > 0 {
		host = host[:i]
	}
	if i := strings.Index(host, ":"); i > 0 {
		host = host[:i]
	}

	switch {
	case matchesTrustedHost(host, "github.com"):
		return os.Getenv("GITHUB_TOKEN")
	case matchesTrustedHost(host, "gitlab.com"):
		return os.Getenv("GITLAB_TOKEN")
	case matchesTrustedHost(host, "gitea.com"), matchesTrustedHost(host, "codeberg.org"):
		return os.Getenv("GITEA_TOKEN")
	}
	return ""
}

// redactToken removes auth tokens from strings (for safe error messages).
func redactToken(s, token string) string {
	if token == "" {
		return s
	}
	return strings.ReplaceAll(s, token, "***")
}

// authArgs prepends git auth header config when token is non-empty.
// NOTE: Error messages from run()/output() will contain the token in argv.
// Use redactToken() on any error before surfacing to users.
func authArgs(token string, gitArgs ...string) []string {
	if token != "" {
		header := fmt.Sprintf("http.extraHeader=Authorization: Bearer %s", token)
		return append([]string{"git", "-c", header}, gitArgs...)
	}
	return append([]string{"git"}, gitArgs...)
}
