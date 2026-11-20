package gitcache

import (
	"os/exec"
	"strings"
	"testing"
)

func TestParseHTTPSHost(t *testing.T) {
	valid := []struct {
		raw        string
		host, port string
	}{
		{"https://github.com/org/repo.git", "github.com", ""},
		{"https://GitHub.COM/org/repo.git", "github.com", ""},
		{"https://api.github.com/org/repo.git", "api.github.com", ""},
		{"https://github.com:8443/org/repo.git", "github.com", "8443"},
		{"https://github.com:0443/org/repo.git", "github.com", "443"},
		{"https://a-b.example/x.git", "a-b.example", ""},
		{"https://10.0.0.1:8080/x.git", "10.0.0.1", "8080"},
		{"https://[::1]/x.git", "::1", ""},
		{"https://[2001:DB8::1]:8443/x.git", "2001:db8::1", "8443"},
		{"https://github.com.git", "github.com.git", ""},
	}
	for _, tt := range valid {
		h, err := parseHTTPSHost(tt.raw)
		if err != nil || h.host != tt.host || h.port != tt.port {
			t.Errorf("parseHTTPSHost(%q) = %+v, %v, want host %q port %q", tt.raw, h, err, tt.host, tt.port)
		}
	}

	refused := []string{
		"https://user@github.com/org/repo.git",          // a user name
		"https://user:pw@github.com/org/repo.git",       // a user name and a password
		"https://github.com:pw@evil.example:443/x.git",  // a user name and a password
		"https://github.com\\@evil.example/x.git",       // a backslash in the user name
		"https:///org/repo.git",                         // no host
		"https://:443/org/repo.git",                     // no host
		"https://github.com:/org/repo.git",              // a ":" with no port
		"https://github.com:0/org/repo.git",             // port 0
		"https://github.com:65536/org/repo.git",         // port out of range
		"https://github.com:443.git",                    // not a port number
		"https://*.github.com/org/repo.git",             // a wildcard label
		"https://gith*ub.com/org/repo.git",              // a "*" in a label
		"https://git%68ub.com/org/repo.git",             // a percent sign
		"https://g%C3%ADthub.com/org/repo.git",          // a percent sign, a byte outside ASCII
		"https://gíthub.com/org/repo.git",               // a byte outside ASCII
		"https://GitHub.com./org/repo.git",              // a trailing dot
		"https://.github.com/org/repo.git",              // an empty label
		"https://github..com/org/repo.git",              // an empty label
		"https://gith_ub.com/org/repo.git",              // an underscore
		"https://[fe80::1%25eth0]/org/repo.git",         // an IPv6 zone
		"https://[10.0.0.1]/org/repo.git",               // IPv4 in brackets
		"https://" + strings.Repeat("a", 64) + ".com/x", // a label over 63 bytes
		"http://github.com/org/repo.git",
		"ssh://git@github.com/org/repo.git",
		"git@github.com:org/repo.git",
		"origin",
		"",
	}
	for _, raw := range refused {
		if h, err := parseHTTPSHost(raw); err == nil {
			t.Errorf("parseHTTPSHost(%q) = %+v, want an error", raw, h)
		}
	}
}

// matchesTrustedHost matches the domain and its subdomains at any depth, in
// any case, and nothing that parseHTTPSHost refuses.
func TestMatchesTrustedHost_Forms(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{"github.com", true},
		{"GitHub.COM", true},
		{"api.github.com", true},
		{"a.b.github.com", true},
		{"github.com.", false},
		{"*.github.com", false},
		{"*", false},
		{"evilgithub.com", false},
		{"github.com.evil.example", false},
		{"github.com:443", false},
		{"github.com/org", false},
		{"git%68ub.com", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := matchesTrustedHost(tt.host, "github.com"); got != tt.want {
			t.Errorf("matchesTrustedHost(%q, github.com) = %v, want %v", tt.host, got, tt.want)
		}
	}
}

// The token is chosen from the host of the final URL, as parseHTTPSHost
// parses it, and the header key names that same host. A ref whose host the
// parse refuses gets no token and no key, and b runs no remote git command
// for it.
func TestResolveGitURL_TokenFromParsedHost(t *testing.T) {
	gh := "Authorization: Bearer " + fakeGH
	gl := "PRIVATE-TOKEN: " + fakeGL
	tests := []struct {
		ref     string
		wantURL string
		wantHdr string
		wantKey string // headerKey of the URL; "" when the parse refuses it
	}{
		{"github.com:pw@evil.example:443/org/repo", "https://github.com:pw@evil.example:443/org/repo.git", "", ""},
		{"github.com@evil.example/org/repo", "https://github.com@evil.example/org/repo.git", "", ""},
		{"github.com:443@evil.example/x", "https://github.com:443@evil.example/x.git", "", ""},
		{"user:pw@github.com:443/org/repo", "https://user:pw@github.com:443/org/repo.git", "", ""},
		{"gitlab.com:pw@evil.example/group/project", "https://gitlab.com:pw@evil.example/group/project.git", "", ""},
		{"user:pw@github.com/org/repo", "https://user:pw@github.com/org/repo.git", "", ""},
		{"tok@github.com/org/repo", "https://tok@github.com/org/repo.git", "", ""},
		{"GitHub.com./org/repo", "https://GitHub.com./org/repo.git", "", ""},
		{"*.github.com/org/repo", "https://*.github.com/org/repo.git", "", ""},
		{"git%68ub.com/org/repo", "https://git%68ub.com/org/repo.git", "", ""},
		{"[fe80::1%25eth0]/org/repo", "https://[fe80::1%25eth0]/org/repo.git", "", ""},
		{"[::1]:8080/org/repo", "https://[::1]:8080/org/repo.git", "", "http.https://[::1]:8080/.extraHeader"},
		{"github.com.evil.example/org/repo", "https://github.com.evil.example/org/repo.git", "", "http.https://github.com.evil.example/.extraHeader"},
		{"evilgithub.com/org/repo", "https://evilgithub.com/org/repo.git", "", "http.https://evilgithub.com/.extraHeader"},
		{"github.com/org/repo", "https://github.com/org/repo.git", gh, "http.https://github.com/.extraHeader"},
		{"GitHub.com/org/repo", "https://GitHub.com/org/repo.git", gh, "http.https://github.com/.extraHeader"},
		{"api.github.com/org/repo", "https://api.github.com/org/repo.git", gh, "http.https://api.github.com/.extraHeader"},
		{"github.com:8443/org/repo", "https://github.com:8443/org/repo.git", gh, "http.https://github.com:8443/.extraHeader"},
		{"github.com:0443/org/repo", "https://github.com:0443/org/repo.git", gh, "http.https://github.com:443/.extraHeader"},
		{"gitlab.com/group/project", "https://gitlab.com/group/project.git", gl, "http.https://gitlab.com/.extraHeader"},
		// B_GIT_SSH_AS_HTTPS: the authority is github.com; text in the path does not change it.
		{"git@github.com:x@evil.example:443/y", "https://github.com/x@evil.example:443/y.git", gh, "http.https://github.com/.extraHeader"},
	}
	for _, tt := range tests {
		t.Run(tt.ref, func(t *testing.T) {
			setProviderEnv(t, "1")
			r := ResolveGitURL(tt.ref, "")
			if r.URL != tt.wantURL {
				t.Errorf("URL = %q, want %q", r.URL, tt.wantURL)
			}
			if r.AuthHeader != tt.wantHdr {
				t.Errorf("header set = %v (%d bytes), want %v", r.AuthHeader != "", len(r.AuthHeader), tt.wantHdr != "")
			}
			if got := headerKey(r.URL); got != tt.wantKey {
				t.Errorf("headerKey = %q, want %q", got, tt.wantKey)
			}
			if _, err := parseHTTPSHost(r.URL); (err != nil) != (tt.wantKey == "") {
				t.Errorf("parseHTTPSHost error = %v, want an error: %v", err, tt.wantKey == "")
			}
		})
	}
}

// For a URL whose host parseHTTPSHost refuses, ls-remote, clone and fetch
// return a fixed error that names the URL without its user name and
// password, and no remote git command runs.
func TestGitCommands_RefuseUnparsedHost(t *testing.T) {
	setProviderEnv(t, "")
	cmds := recordGit(t)
	root := t.TempDir()
	hdr := "Authorization: Bearer " + fakeGH
	for _, u := range []string{
		"https://github.com:pw@evil.example:443/org/repo.git",
		"https://*.github.com/org/repo.git",
		"https://git%68ub.com/org/repo.git",
		"https://github.com:443.git",
		"https://GitHub.com./org/repo.git",
		"https://github.com:/org/repo.git",
	} {
		_, lsErr := ResolveRefAuth(u, "", hdr)
		for i, err := range []error{
			lsErr,
			EnsureCloneAuth(root, u, u, hdr),
			FetchURLAuth(root, u, u, "c0ffee", hdr),
		} {
			if err == nil {
				t.Errorf("%s: command %d: no error", u, i)
				continue
			}
			msg := err.Error()
			if !strings.Contains(msg, "the host part is not a plain host name") {
				t.Errorf("%s: command %d: error %q, want the host error", u, i, msg)
			}
			if strings.Contains(msg, ":pw@") || strings.Contains(msg, fakeGH) {
				t.Errorf("%s: command %d: the error shows a password or the token", u, i)
			}
		}
	}
	if len(*cmds) != 0 {
		t.Errorf("%d git commands ran for refused URLs, want none", len(*cmds))
	}
}

// FetchAuth (deprecated) sends a header only when b chooses that header for
// the host of the stored URL.
func TestFetchAuth_HeaderOnlyForTheStoredHost(t *testing.T) {
	isolateGit(t)
	setProviderEnv(t, "")
	gh := "Authorization: Bearer " + fakeGH
	prompt := []string{"GIT_TERMINAL_PROMPT=0"}
	root := t.TempDir()
	for _, tt := range []struct {
		origin string
		want   []string
	}{
		{"https://github.com/org/repo.git", headerEnv("github.com", gh)},
		{"https://evil.example/org/repo.git", prompt},
		{"https://gitlab.com/group/project.git", prompt},
		{"git@github.com:org/repo.git", prompt},
	} {
		dir := CacheDir(root, tt.origin)
		if out, err := exec.Command("git", "init", "-q", "--bare", dir).CombinedOutput(); err != nil {
			t.Fatalf("git init: %v\n%s", err, out)
		}
		if out, err := exec.Command("git", "-C", dir, "config", "remote.origin.url", tt.origin).CombinedOutput(); err != nil {
			t.Fatalf("set origin: %v\n%s", err, out)
		}
		cmds := recordGit(t)
		if err := FetchAuth(root, tt.origin, "c0ffee", gh); err != nil {
			t.Fatalf("%s: FetchAuth: %v", tt.origin, err)
		}
		if len(*cmds) != 1 || strings.Join((*cmds)[0].Env, "\n") != strings.Join(tt.want, "\n") {
			t.Errorf("%s: env is not the wanted env (%d commands)", tt.origin, len(*cmds))
		}
	}
}

// With real git: the header env of a ref whose host b refuses selects no
// header for the other host in that ref, also when a header is passed in.
func TestHeaderEnv_RealGitSelectsNothingForOtherHost(t *testing.T) {
	isolateGit(t)
	setProviderEnv(t, "1")
	forced := "Authorization: Bearer " + fakeGH
	evil := []string{"https://evil.example/org/repo.git", "https://evil.example:443/org/repo.git"}
	for _, ref := range []string{
		"github.com:pw@evil.example:443/org/repo",
		"github.com@evil.example/org/repo",
		"github.com:443@evil.example/x",
		"user:pw@evil.example:443/org/repo",
	} {
		r := ResolveGitURL(ref, "")
		if r.AuthHeader != "" {
			t.Errorf("%s: a token is chosen", ref)
		}
		for _, hdr := range []string{r.AuthHeader, forced} {
			ac := authCmd(r.URL, hdr, "ls-remote", "--", r.URL)
			for _, u := range evil {
				if h := urlMatchHeader(t, ac, u); h != "" {
					t.Errorf("%s (header passed: %v): git selects a header for %s", ref, hdr != "", u)
				}
			}
		}
	}
}

// git's URL matcher reads a "*" label in an http.<url>.* key as a wildcard.
// b builds no such key and chooses no token for such a host.
func TestHeaderKey_RealGitNoWildcard(t *testing.T) {
	isolateGit(t)
	wild := AuthCmd{Env: []string{
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=http.https://*.github.com/.extraHeader",
		"GIT_CONFIG_VALUE_0=X-Probe: on",
	}}
	if h := urlMatchHeader(t, wild, "https://some.github.com/org/repo.git"); h != "X-Probe: on" {
		t.Fatalf("git's matcher did not read the \"*\" label as a wildcard (got %q); the test below shows nothing", h)
	}

	setProviderEnv(t, "")
	r := ResolveGitURL("*.github.com/org/repo", "")
	if r.AuthHeader != "" {
		t.Error("a token is chosen for a wildcard host")
	}
	ac := authCmd(r.URL, "Authorization: Bearer "+fakeGH, "ls-remote", "--", r.URL)
	for _, u := range []string{"https://some.github.com/org/repo.git", "https://github.com/org/repo.git"} {
		if h := urlMatchHeader(t, ac, u); h != "" {
			t.Errorf("git selects a header for %s", u)
		}
	}
}

// With real git: the key that b builds from the parsed host and port
// selects the header for the URL itself, and for no URL of another host or
// port.
func TestHeaderKey_RealGitMatchesTheParsedHost(t *testing.T) {
	isolateGit(t)
	hdr := "X-Probe: on"
	for _, tt := range []struct {
		remote string
		other  []string
	}{
		{"https://github.com/org/repo.git", []string{"https://github.com:8443/x.git", "https://api.github.com/x.git", "https://github.com.evil.example/x.git"}},
		{"https://GitHub.COM/org/repo.git", []string{"https://evil.example/x.git"}},
		{"https://github.com:0443/org/repo.git", []string{"https://github.com:8443/x.git"}},
		{"https://github.com:8443/org/repo.git", []string{"https://github.com/x.git", "https://github.com:443/x.git"}},
		{"https://api.github.com/org/repo.git", []string{"https://github.com/x.git", "https://x.api.github.com/x.git"}},
		{"https://10.0.0.1:8080/x.git", []string{"https://10.0.0.1/x.git", "https://10.0.0.2:8080/x.git"}},
		{"https://[2001:DB8::1]:8443/x.git", []string{"https://[2001:db8::2]:8443/x.git", "https://[2001:db8::1]/x.git"}},
	} {
		ac := authCmd(tt.remote, hdr, "ls-remote", "--", tt.remote)
		if h := urlMatchHeader(t, ac, tt.remote); h != hdr {
			t.Errorf("%s: git selects %q for the URL itself, want the header", tt.remote, h)
		}
		for _, u := range tt.other {
			if h := urlMatchHeader(t, ac, u); h != "" {
				t.Errorf("%s: git selects the header for %s", tt.remote, u)
			}
		}
	}
}
