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
//   - HTTPS refs: "https://github.com/org/repo" (the scheme in any case) →
//     "https://github.com/org/repo.git", as the remote ref without the scheme
//   - git:// protocol: "git://../../repo" → resolved local path or remote URL
//   - Local paths: "/abs/path" or "../../rel" → resolved to absolute
//
// A ref with another scheme ("http://", "file://", "SSH://") resolves to a
// URL that the remote git commands refuse with a fixed text (checkRemote).
// A ref that CheckRef refuses resolves to refusedURL, with no token: the
// remote git commands refuse it with the text of CheckRef.
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

	// A password or more than one @ in front of the host: b cannot tell the
	// user part from the host or the version, so the URL holds no part of
	// the ref.
	if CheckRef(ref) != nil {
		return ResolvedRef{URL: refusedURL}
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

	// An explicit https scheme, in any case ("https://", "HTTPS://"): the ref
	// is that URL. It resolves as the ref without the scheme, so it gets the
	// same URL and the same token.
	if hasHTTPSScheme(cleanRef) {
		return remoteHTTPS(cleanRef[len("https://"):])
	}

	// Remote ref: "github.com/org/repo" → "https://github.com/org/repo.git".
	// A ref with another scheme ("http://", "file://") gets the URL
	// "https://<scheme>://...", which checkRemote refuses (errScheme).
	return remoteHTTPS(cleanRef)
}

// hasHTTPSScheme reports whether s starts with "https://", in any case.
func hasHTTPSScheme(s string) bool {
	return len(s) >= len("https://") && strings.EqualFold(s[:len("https://")], "https://")
}

// hasScheme reports whether s starts with a scheme and "://": the text in
// front of the first "://" is not empty and has no "/", "?", "#" or "@".
func hasScheme(s string) bool {
	i := strings.Index(s, "://")
	return i > 0 && !strings.ContainsAny(s[:i], "/?#@")
}

// remoteHTTPS resolves a remote ref without a scheme ("github.com/org/repo")
// to its https URL, with the provider auth for its host. The token is
// chosen from the host of that final URL, as parseHTTPSHost parses it, the
// same parse that scopes the header key (headerKey). A URL whose host the
// parse does not accept gets no token, and the remote git commands refuse it
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
	"(no user name, password, wildcard, percent sign or trailing dot); b does not run a remote git command for it")

// errUserinfoAt is the error for a ref that CheckRef refuses, and for a URL
// with more than one raw "@" in its authority (checkRemote). The text does
// not echo the ref: a part of the password can look like the host.
var errUserinfoAt = errors.New("refusing a git ref with a password or more than one @ before its path: remove the user part")

// errScheme is the error for a URL with a scheme that b does not fetch or a
// remote helper, also the URL "https://<scheme>://..." that ResolveGitURL
// builds for a ref with such a scheme (checkRemote). The text does not echo
// the URL.
var errScheme = errors.New("refusing a git ref with a scheme that b does not fetch: use https://, ssh://, git:// or no scheme")

// refusedURL is the URL of a ref that CheckRef refuses (ResolveGitURL). It
// holds no part of the ref. The remote git commands refuse it with
// errUserinfoAt (checkRemote).
const refusedURL = "https://***@***/"

// authorityEnd returns the end of the authority of s, the text after
// "<scheme>://" or a ref without a scheme: the index of the first "/", "?"
// or "#", or len(s), as git and curl read an https URL. It is the one rule
// of checkRemote, CheckRef, RedactUserinfo and IsSSHUserAt (for a ref with
// a scheme).
func authorityEnd(s string) int {
	if end := strings.IndexAny(s, "/?#"); end >= 0 {
		return end
	}
	return len(s)
}

// CheckRef refuses a ref whose user part b cannot tell from the host or
// the version. It returns an error with a fixed text that does not echo
// the ref (errUserinfoAt) for:
//   - a ref without a scheme, also the ref after "git://", whose first part
//     (the text before its first "/") has a ":" before its first "@"
//     ("user:password@host"), or more than one "@" in front of the host
//     (refusedBare);
//   - a ref with a scheme ("https://", "ssh://") with more than one "@" in
//     its authority;
//   - a ref with another "://" after its authority and an "@" in the
//     authority that follows it ("https://a?b://user:pw@host/x").
//
// A local path passes. Call CheckRef with the ref as the user wrote it,
// before RefBase, RefVersion or any message that shows the ref. The remote
// git commands refuse the other forms that b does not fetch (checkRemote).
func CheckRef(ref string) error {
	s, local := ref, isLocalPath(ref) || ref == "." || ref == ".."
	if rest, ok := strings.CutPrefix(ref, "git://"); ok {
		// b's own protocol: the rest is a ref, a local path when it starts
		// with "/" or "." (resolveRepo).
		s, local = rest, strings.HasPrefix(rest, "/") || strings.HasPrefix(rest, ".")
	}
	if local {
		return nil
	}
	start := 0 // the start of the authority
	for hasScheme(s[start:]) {
		start += strings.Index(s[start:], "://") + len("://")
	}
	if start == 0 && refusedBare(s) ||
		start > 0 && strings.Count(s[start:start+authorityEnd(s[start:])], "@") > 1 {
		return errUserinfoAt
	}
	// Another "://" after the authority: git and curl read the text after
	// it as a path, a query or a fragment, but it can hold a user part.
	for rest := s[start:]; ; {
		i := strings.Index(rest, "://")
		if i < 0 {
			return nil
		}
		rest = rest[i+len("://"):]
		if strings.Contains(rest[:authorityEnd(rest)], "@") {
			return errUserinfoAt
		}
	}
}

// refusedBare reports whether a ref without a scheme has a user part that b
// cannot tell from the host: its first part, the text before its first "/"
// (the whole ref when it has no "/"), has a ":" before its first "@" (a
// password), or more than one "@" in front of the host. In an scp-like ref
// ("user@host:path") the host ends at the first ":" after the "@", so an @
// after that ":" is in the path ("git@host:org/repo@v1").
func refusedBare(ref string) bool {
	first := ref
	if i := strings.Index(ref, "/"); i >= 0 {
		first = ref[:i]
	}
	at := strings.Index(first, "@")
	if at < 0 {
		return false
	}
	colon := strings.Index(first, ":")
	if colon >= 0 && colon < at {
		return true // "user:password@host"
	}
	if colon > at {
		first = first[:colon] // scp-like "user@host:path"
	}
	return strings.Count(first, "@") > 1
}

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

// IsSSHUserAt reports whether the @ at atIdx separates a user part from the
// host ("git@host:org/repo", "ssh://git@host/org/repo",
// "user:password@host/org/repo"), rather than a version
// ("github.com/org/repo@v2.0", "ssh://git@host/org/repo@v2.0"). It is also
// true for an @ in an SSH path that a ":" follows ("git@host:x@y:z"): a git
// ref name has no ":", so that @ is not a version. ResolveGitURL, RefBase,
// RefVersion and the git provider use it.
//
// The authority of a ref with a scheme ends at its first "/", "?" or "#"
// (authorityEnd), as git and curl read an https URL: an @ after it is a
// version ("https://github.com#lbl@v1"). The first part of a ref without a
// scheme ends at its first "/". An @ before that "/" is never a version,
// also when the user part has an @ of its own ("user:p@ss@host/path"): the
// version comes after the path. In an scp-style ref the authority ends at
// the ":", so an @ after the ":" is in the path ("git@host:repo@release/1.0"
// has the version "release/1.0").
//
// In the first part of a ref without a scheme that CheckRef refuses (a
// password, or more than one @ in front of the host), no @ is a version:
// RefBase and RefVersion then do not cut the ref inside its user part.
func IsSSHUserAt(ref string, atIdx int) bool {
	prefix := ref[:atIdx]
	rest := ref[atIdx+1:]

	// With a scheme, the user part is in the authority: no "/", "?" or "#"
	// follows the scheme before the @ (authorityEnd).
	if i := strings.Index(prefix, "://"); i > 0 && !strings.ContainsAny(prefix[:i], "/@") {
		// A URL in the ref ("git://http://user:pw@host/path"): the user part
		// is in the authority of the inner URL.
		inner := i + len("://")
		if hasScheme(prefix[inner:]) {
			return IsSSHUserAt(ref[inner:], atIdx-inner)
		}
		if authorityEnd(prefix[inner:]) == len(prefix)-inner {
			return true
		}
		return strings.HasPrefix(prefix, "ssh://") && strings.Contains(rest, ":")
	}
	// Without a scheme, an @ after a "/" is a version (github.com/org/repo@v2.0).
	if strings.Contains(prefix, "/") {
		return false
	}
	if refusedBare(ref) {
		return true // in the user part of a refused first part
	}
	// scp-style: user@host:path. The part after the @ has a ":"; a version
	// (repo@v2.0) has none.
	if strings.Contains(rest, ":") {
		return true
	}
	// A user part in front of a host and a path: "user:password@host/path",
	// also "user:p@ss@host/path". When the prefix is an scp-style
	// "user@host:path", this @ is in that path.
	return strings.Contains(rest, "/") && !isSSHImplicit(prefix)
}

// RedactUserinfo returns the ref or URL s with the user part of its
// authority (the text before the last @ of the authority, which ends at the
// first "/", "?" or "#": authorityEnd) replaced by "***", for a message. It keeps the user name of an SSH ref
// ("git@github.com:org/repo", "ssh://git@host/org/repo") that has no
// password: that is not a secret. Text with no user part does not change.
func RedactUserinfo(s string) string {
	start, scheme := 0, ""
	if i := strings.Index(s, "://"); i > 0 && !strings.ContainsAny(s[:i], "/@") {
		start, scheme = i+len("://"), strings.ToLower(s[:i])
		// A URL with a second scheme ("https://http://user:pw@host/path",
		// the URL that b builds for a ref with a scheme that it does not
		// fetch): the user part is in the authority of the inner URL.
		if hasScheme(s[start:]) {
			return s[:start] + RedactUserinfo(s[start:])
		}
	} else if isSSHImplicit(s) {
		return s // scp-style "user@host:path": the user part has no password
	}
	rest := s[start:]
	at := strings.LastIndex(rest[:authorityEnd(rest)], "@")
	if at < 0 {
		return s
	}
	if scheme == "ssh" && !strings.Contains(rest[:at], ":") {
		return s
	}
	return s[:start] + "***" + rest[at:]
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
