package gitcache

import (
	"errors"
	"net"
	neturl "net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ResolvedRef holds a parsed and resolved git reference.
//
// AuthToken and AuthHeader are set only for an https URL whose host
// parseHTTPSHost accepts and that is a provider host (providerAuth).
type ResolvedRef struct {
	URL        string // clone URL (https://, ssh://, git@host:repo, or local path)
	IsLocal    bool   // true for local filesystem repos
	IsSSH      bool   // true for SSH URLs (git@ or ssh://)
	AuthToken  string // auth token for HTTPS operations (empty for SSH/local)
	AuthHeader string // pre-formatted auth header (e.g. "Authorization: Bearer ...", "PRIVATE-TOKEN: ...")
}

// SSHAsHTTPSEnv names the environment variable that fetches an SSH ref for
// github.com over https. Only the exact value "1" turns it on.
const SSHAsHTTPSEnv = "B_GIT_SSH_AS_HTTPS"

// sshAsHTTPS reports whether SSHAsHTTPSEnv is on.
func sshAsHTTPS() bool {
	return os.Getenv(SSHAsHTTPSEnv) == "1"
}

// githubSSHPath returns "org/repo" for an SSH ref to github.com with the user
// git: "git@github.com:org/repo[.git]" or "ssh://git@github.com/org/repo[.git]".
// It returns "" for every other ref, so another host, a port or another user
// stays SSH.
func githubSSHPath(cleanRef string) string {
	var host, path string
	switch {
	case strings.HasPrefix(cleanRef, "ssh://git@"):
		rest := strings.TrimPrefix(cleanRef, "ssh://git@")
		slash := strings.Index(rest, "/")
		if slash < 0 {
			return ""
		}
		host, path = rest[:slash], rest[slash+1:]
	case strings.HasPrefix(cleanRef, "git@") && isSSHImplicit(cleanRef):
		rest := strings.TrimPrefix(cleanRef, "git@")
		colon := strings.Index(rest, ":")
		host, path = rest[:colon], rest[colon+1:]
	default:
		return ""
	}
	if !strings.EqualFold(host, "github.com") {
		return ""
	}
	return strings.Trim(strings.TrimSuffix(strings.Trim(path, "/"), ".git"), "/")
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
//
// With B_GIT_SSH_AS_HTTPS=1, an SSH ref for github.com resolves as the https
// ref "github.com/org/repo", with the same auth as that ref. SSH refs for
// other hosts do not change. There is no fallback: with the variable off, an
// SSH ref stays SSH, also when SSH fails.
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

	// Opt-in: an SSH ref for github.com resolves as the https ref. It returns
	// here: the path can hold "@" and ":" ("git@github.com:x@y:z"), and the
	// SSH checks below would read "github.com/x@y:z" as SSH again.
	if sshAsHTTPS() {
		if path := githubSSHPath(cleanRef); path != "" {
			return remoteHTTPS("github.com/" + path)
		}
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
	return remoteHTTPS(cleanRef)
}

// remoteHTTPS resolves a remote ref without a scheme ("github.com/org/repo")
// to its https URL, with the provider auth for its host. The token is
// chosen from the host of that final URL, as parseHTTPSHost parses it, the
// same parse that scopes the header key (headerKey). A URL whose host the
// parse does not accept gets no token, and the git commands refuse it
// (checkRemote).
func remoteHTTPS(ref string) ResolvedRef {
	u := "https://" + ref + ".git"
	h, err := parseHTTPSHost(u)
	if err != nil {
		return ResolvedRef{URL: u}
	}
	auth := providerAuth(h.host)
	return ResolvedRef{URL: u, AuthToken: auth.token, AuthHeader: auth.header}
}

// errRemoteHost is the error for an https URL whose host parseHTTPSHost
// does not accept.
var errRemoteHost = errors.New("the host part is not a plain host name or IP address with an optional port " +
	"(no user name, password, wildcard, percent sign or trailing dot); b does not run git for it")

// remoteHost is the host and port of an https URL, as parseHTTPSHost
// parses them.
type remoteHost struct {
	host string // lowercase DNS name or IP address (an IPv6 address has no brackets)
	port string // "" or a port number from 1 to 65535, without leading zeros
}

// authority returns the host and port as an http.<url>.* key writes them.
func (h remoteHost) authority() string {
	a := h.host
	if strings.Contains(a, ":") {
		a = "[" + a + "]"
	}
	if h.port != "" {
		a += ":" + h.port
	}
	return a
}

// parseHTTPSHost parses the host of an https URL. It is the one parser for
// the choice of the token (remoteHTTPS) and for the scope of the header key
// (headerKey), so the two always name the same host. It accepts a DNS name
// (isDNSName) or an IPv6 address in brackets, with an optional port. It
// refuses a URL with a user name or password, an empty host, a port that is
// not a number from 1 to 65535, and a host with any other byte (a "*", a
// "%", a byte outside ASCII).
func parseHTTPSHost(rawURL string) (remoteHost, error) {
	u, err := neturl.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.Opaque != "" {
		return remoteHost{}, errRemoteHost
	}
	if u.User != nil {
		return remoteHost{}, errRemoteHost // a user name or a password
	}
	host, port := u.Hostname(), u.Port()
	if strings.HasSuffix(u.Host, ":") {
		return remoteHost{}, errRemoteHost // a ":" with no port number
	}
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 || strings.Trim(port, "0123456789") != "" {
			return remoteHost{}, errRemoteHost
		}
		port = strconv.Itoa(n)
	}
	if strings.HasPrefix(u.Host, "[") {
		if ip := net.ParseIP(host); ip == nil || !strings.Contains(host, ":") {
			return remoteHost{}, errRemoteHost // not an IPv6 address (a zone has a "%")
		}
	} else if !isDNSName(host) {
		return remoteHost{}, errRemoteHost
	}
	return remoteHost{host: strings.ToLower(host), port: port}, nil
}

// isDNSName reports whether s is a host name of ASCII letters, digits and
// hyphens, in labels of 1 to 63 bytes between single dots. There is no
// empty label, so no trailing dot. A "*" is not a label byte: in an
// http.<url>.* key, git's URL matcher reads a "*" label as a wildcard.
func isDNSName(s string) bool {
	if s == "" || len(s) > 253 {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if label == "" || len(label) > 63 {
			return false
		}
		for i := 0; i < len(label); i++ {
			if !isLabelByte(label[i]) {
				return false
			}
		}
	}
	return true
}

// isLabelByte reports whether c is an ASCII letter, a digit or a hyphen.
func isLabelByte(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || c == '-'
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
	return remoteHTTPS(repo)
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

// matchesTrustedHost reports whether host is domain or a subdomain of it at
// any depth ("api.github.com", "a.b.github.com"), in any case. host is a
// host name as parseHTTPSHost accepts it: a host with a trailing dot, a
// port, a wildcard or any other byte outside a DNS name never matches.
func matchesTrustedHost(host, domain string) bool {
	if !isDNSName(host) {
		return false
	}
	host = strings.ToLower(host)
	return host == domain || strings.HasSuffix(host, "."+domain)
}

// authInfo holds token and header format for a provider.
type authInfo struct {
	token  string
	header string // pre-formatted header value
}

// providerAuth returns the auth token and formatted header for a host that
// parseHTTPSHost returned. It does not parse a ref: the caller gives the
// parsed host of the final URL.
func providerAuth(host string) authInfo {
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

// redactAuth removes auth secrets from strings (for safe error messages).
// Accepts either a raw token or a full auth header — extracts and redacts
// the secret part in both cases.
func redactAuth(s, authHeader string) string {
	if authHeader == "" {
		return s
	}
	// Extract the raw token from common header formats
	for _, prefix := range []string{"Authorization: Bearer ", "Authorization: token ", "PRIVATE-TOKEN: "} {
		if strings.HasPrefix(authHeader, prefix) {
			token := strings.TrimPrefix(authHeader, prefix)
			if token != "" {
				return strings.ReplaceAll(s, token, "***")
			}
		}
	}
	// Fallback: redact the whole header value
	return strings.ReplaceAll(s, authHeader, "***")
}

// AuthCmd holds git command args and optional auth environment variables.
// Auth tokens are passed via environment variables (not argv) to avoid
// exposure in process listings.
type AuthCmd struct {
	Args []string // git command arguments
	Env  []string // extra environment variables (e.g. GIT_CONFIG_*)
}

// authCmd builds a git command that can reach the remote at remote (clone,
// fetch, ls-remote), with an optional auth header.
//
// The header goes to git in GIT_CONFIG_* environment variables, not in argv,
// so a process listing does not show it. The config key is scoped to the
// host of remote (headerKey), so git sends the header to that host only.
// Pass header from ResolvedRef.AuthHeader (provider-specific format).
//
// GIT_TERMINAL_PROMPT=0 stops git from asking for a user name or a password
// on the terminal: a private repo with no token and no credential helper
// then fails at once and does not wait for input.
func authCmd(remote, header string, gitArgs ...string) AuthCmd {
	args := append([]string{"git"}, gitArgs...)
	env := []string{"GIT_TERMINAL_PROMPT=0"}
	if key := headerKey(remote); header != "" && key != "" {
		env = append(env,
			"GIT_CONFIG_COUNT=1",
			"GIT_CONFIG_KEY_0="+key,
			"GIT_CONFIG_VALUE_0="+header,
		)
	}
	return AuthCmd{Args: args, Env: env}
}

// headerKey returns the git config key that adds an HTTP header only for the
// host of remote: "http.https://<host>[:<port>]/.extraHeader", from the
// same parse that chose the token (parseHTTPSHost). git compares an
// http.<url>.* key with the URL that it connects to, after its own
// url.<base>.insteadOf rewrite, so a rewrite to another host does not get
// the header. git does not compare the key with the target of a redirect.
// It returns "" when parseHTTPSHost refuses remote; then b gives git no
// header.
func headerKey(remote string) string {
	h, err := parseHTTPSHost(remote)
	if err != nil {
		return ""
	}
	return "http.https://" + h.authority() + "/.extraHeader"
}
