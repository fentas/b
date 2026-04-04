package gitcache

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ResolvedRef holds a parsed and resolved git reference.
type ResolvedRef struct {
	URL       string // clone URL (https://, ssh://, git@host:repo, or local path)
	IsLocal   bool   // true for local filesystem repos
	IsSSH     bool   // true for SSH URLs (git@ or ssh://)
	AuthToken string // auth token for HTTPS operations (empty for SSH/local)
}

// ResolveGitURL converts a ref to a clone-ready URL or local path.
// Handles:
//   - SSH refs: "git@github.com:org/repo" → passed through as-is (uses ssh-agent)
//   - SSH protocol: "ssh://git@host/org/repo" → passed through as-is
//   - Remote refs: "github.com/org/repo" → "https://github.com/org/repo.git"
//   - git:// protocol: "git://../../repo" → resolved local path or remote URL
//   - Local paths: "/abs/path" or "../../rel" → resolved to absolute
//
// SSH URLs use the system's SSH agent (SSH_AUTH_SOCK) for authentication.
// HTTPS URLs use AuthToken with git -c http.extraHeader.
func ResolveGitURL(ref, configDir string) ResolvedRef {
	// Detect absolute/relative local paths BEFORE stripping #/@ to avoid
	// truncating paths containing those characters (e.g. /home/me/repo@work).
	if strings.HasPrefix(ref, "/") {
		return ResolvedRef{URL: ref, IsLocal: true}
	}
	if strings.HasPrefix(ref, "./") || strings.HasPrefix(ref, "../") || ref == "." || ref == ".." {
		abs, err := filepath.Abs(filepath.Join(configDir, ref))
		if err != nil {
			return ResolvedRef{URL: filepath.Clean(filepath.Join(configDir, ref)), IsLocal: true}
		}
		return ResolvedRef{URL: abs, IsLocal: true}
	}

	// Strip fragment label (e.g. #monitoring)
	cleanRef := ref
	if i := strings.Index(cleanRef, "#"); i != -1 {
		cleanRef = cleanRef[:i]
	}
	// Strip version (e.g. @v2.0) — careful not to strip git@ prefix
	if i := strings.LastIndex(cleanRef, "@"); i > 0 && !isSSHUserPrefix(cleanRef, i) {
		cleanRef = cleanRef[:i]
	}

	// SSH implicit format: git@host:org/repo.git
	if isSSHImplicit(cleanRef) {
		url := cleanRef
		if !strings.HasSuffix(url, ".git") {
			url += ".git"
		}
		return ResolvedRef{URL: url, IsSSH: true}
	}

	// SSH explicit format: ssh://git@host/org/repo
	if strings.HasPrefix(cleanRef, "ssh://") {
		url := cleanRef
		if !strings.HasSuffix(url, ".git") {
			url += ".git"
		}
		return ResolvedRef{URL: url, IsSSH: true}
	}

	// git:// protocol prefix (custom b protocol for local/remote repos)
	if strings.HasPrefix(cleanRef, "git://") {
		raw := strings.TrimPrefix(cleanRef, "git://")
		// Strip colon-separated filepath (git://repo:filepath),
		// but preserve host:port forms (colon before first slash).
		if colon := strings.Index(raw, ":"); colon >= 0 {
			slash := strings.Index(raw, "/")
			if slash == -1 || colon > slash {
				raw = raw[:colon]
			}
		}
		return resolveRepo(raw, configDir)
	}

	// Remote ref: "github.com/org/repo" → "https://github.com/org/repo.git"
	url := "https://" + cleanRef + ".git"
	token := detectAuthToken(cleanRef)
	return ResolvedRef{URL: url, IsLocal: false, AuthToken: token}
}

// resolveRepo determines if a repo path is local or remote.
func resolveRepo(repo, configDir string) ResolvedRef {
	if strings.HasPrefix(repo, "/") {
		return ResolvedRef{URL: repo, IsLocal: true}
	}
	if strings.HasPrefix(repo, ".") || strings.HasPrefix(repo, "..") {
		abs, err := filepath.Abs(filepath.Join(configDir, repo))
		if err != nil {
			return ResolvedRef{URL: filepath.Clean(filepath.Join(configDir, repo)), IsLocal: true}
		}
		return ResolvedRef{URL: abs, IsLocal: true}
	}
	// Remote
	url := "https://" + repo + ".git"
	token := detectAuthToken(repo)
	return ResolvedRef{URL: url, IsLocal: false, AuthToken: token}
}

// isSSHImplicit detects the "git@host:path" SSH URL format.
// Must have @ before : and no :// protocol prefix.
func isSSHImplicit(ref string) bool {
	if strings.Contains(ref, "://") {
		return false
	}
	at := strings.Index(ref, "@")
	colon := strings.Index(ref, ":")
	return at >= 0 && colon > at
}

// isSSHUserPrefix checks if the @ at position i is part of a git@ user prefix
// (e.g. "git@github.com:...") rather than a version separator (e.g. "repo@v2.0").
func isSSHUserPrefix(ref string, atIdx int) bool {
	prefix := ref[:atIdx]
	return prefix == "git" || prefix == "ssh://git"
}

// IsSSHURL returns true if a URL uses SSH transport.
func IsSSHURL(url string) bool {
	return strings.HasPrefix(url, "ssh://") || isSSHImplicit(url)
}

// matchesTrustedHost reports whether host exactly matches domain or is a subdomain.
func matchesTrustedHost(host, domain string) bool {
	host = strings.ToLower(host)
	return host == domain || strings.HasSuffix(host, "."+domain)
}

// detectAuthToken returns the auth token for a given ref based on trusted host matching.
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
// Skips Bearer header for SSH URLs (SSH uses ssh-agent, not HTTP headers).
func authArgs(token string, gitArgs ...string) []string {
	if token != "" {
		header := fmt.Sprintf("http.extraHeader=Authorization: Bearer %s", token)
		return append([]string{"git", "-c", header}, gitArgs...)
	}
	return append([]string{"git"}, gitArgs...)
}
