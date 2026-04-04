package gitcache

import (
	"os"
	"path/filepath"
	"strings"
)

// ResolvedRef holds a parsed and resolved git reference.
type ResolvedRef struct {
	URL        string // clone URL (https://, ssh://, git@host:repo, or local path)
	IsLocal    bool   // true for local filesystem repos
	IsSSH      bool   // true for SSH URLs (git@ or ssh://)
	AuthToken  string // auth token for HTTPS operations (empty for SSH/local)
	AuthHeader string // pre-formatted auth header (e.g. "Authorization: Bearer ...", "PRIVATE-TOKEN: ...")
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
// HTTPS URLs use AuthToken injected via GIT_CONFIG_* environment variables.
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
	if i := strings.LastIndex(cleanRef, "@"); i > 0 && !IsSSHUserAt(cleanRef, i) {
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
	auth := detectAuth(cleanRef)
	return ResolvedRef{URL: url, IsLocal: false, AuthToken: auth.token, AuthHeader: auth.header}
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
	auth := detectAuth(repo)
	return ResolvedRef{URL: url, IsLocal: false, AuthToken: auth.token, AuthHeader: auth.header}
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

// IsSSHUserAt checks if the @ at position i is an SSH user separator
// (e.g. "git@host:..." or "ssh://user@host/...") rather than a version separator.
// Used by both ResolveGitURL and RefBase/RefVersion.
func IsSSHUserAt(ref string, atIdx int) bool {
	prefix := ref[:atIdx]
	rest := ref[atIdx+1:]

	// Explicit ssh:// with any user
	if strings.HasPrefix(prefix, "ssh://") {
		return true
	}

	// scp-style: user@host:path — the part after @ must contain a ':'
	// indicating host:path format. Version separators (repo@v2.0) don't.
	// Username must not contain slashes (paths like github.com/org/repo@v2.0).
	if strings.Contains(rest, ":") && !strings.Contains(prefix, "/") {
		return true
	}

	return false
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

// authInfo holds token and header format for a provider.
type authInfo struct {
	token  string
	header string // pre-formatted header value
}

// detectAuth returns the auth token and formatted header for a given ref.
func detectAuth(ref string) authInfo {
	host := ref
	if i := strings.Index(host, "/"); i > 0 {
		host = host[:i]
	}
	if i := strings.Index(host, ":"); i > 0 {
		host = host[:i]
	}

	var token string
	switch {
	case matchesTrustedHost(host, "github.com"):
		token = os.Getenv("GITHUB_TOKEN")
		if token != "" {
			return authInfo{token: token, header: "Authorization: Bearer " + token}
		}
	case matchesTrustedHost(host, "gitlab.com"):
		token = os.Getenv("GITLAB_TOKEN")
		if token != "" {
			return authInfo{token: token, header: "PRIVATE-TOKEN: " + token}
		}
	case matchesTrustedHost(host, "gitea.com"), matchesTrustedHost(host, "codeberg.org"):
		token = os.Getenv("GITEA_TOKEN")
		if token != "" {
			return authInfo{token: token, header: "Authorization: token " + token}
		}
	}
	return authInfo{}
}

// redactToken removes auth tokens from strings (for safe error messages).
func redactToken(s, token string) string {
	if token == "" {
		return s
	}
	return strings.ReplaceAll(s, token, "***")
}

// AuthCmd holds git command args and optional auth environment variables.
// Auth tokens are passed via environment variables (not argv) to avoid
// exposure in process listings.
type AuthCmd struct {
	Args []string // git command arguments
	Env  []string // extra environment variables (e.g. GIT_CONFIG_*)
}

// authCmd builds a git command with optional auth header.
// The header is injected via GIT_CONFIG_* environment variables instead of
// command-line args to prevent exposure in process listings.
// Pass header from ResolvedRef.AuthHeader (provider-specific format).
func authCmd(header string, gitArgs ...string) AuthCmd {
	args := append([]string{"git"}, gitArgs...)
	if header == "" {
		return AuthCmd{Args: args}
	}
	return AuthCmd{
		Args: args,
		Env: []string{
			"GIT_CONFIG_COUNT=1",
			"GIT_CONFIG_KEY_0=http.extraHeader",
			"GIT_CONFIG_VALUE_0=" + header,
		},
	}
}
