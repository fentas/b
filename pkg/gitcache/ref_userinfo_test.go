package gitcache

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// refPassword and refToken are the password in the refs and the provider
// token of these tests. They are not real values.
const (
	refPassword = "pw-canary-q7z"
	refToken    = "FAKE-TOKEN-x"
)

// remoteErrors returns the errors of ls-remote, clone and fetch for the
// resolved ref r. Call recordGit first, so that no git command runs.
func remoteErrors(t *testing.T, ref string, r ResolvedRef) map[string]error {
	t.Helper()
	root := t.TempDir()
	_, lsErr := ResolveRefAuth(r.URL, RefVersion(ref), r.AuthHeader)
	return map[string]error{
		"ls-remote": lsErr,
		"clone":     EnsureCloneAuth(root, RefBase(ref), r.URL, r.AuthHeader),
		"fetch":     FetchURLAuth(root, RefBase(ref), r.URL, "c0ffee", r.AuthHeader),
	}
}

// formatsHold reports whether err holds s in one of the format verbs.
func formatsHold(err error, s string) bool {
	for _, verb := range []string{"%s", "%v", "%+v", "%q", "%#v"} {
		if strings.Contains(fmt.Sprintf(verb, err), s) {
			return true
		}
	}
	return false
}

// refusedText is the fixed text of CheckRef (errUserinfoAt).
const refusedText = "refusing a git ref with a password or more than one @ before its path: remove the user part"

// A ref with more than one raw @ before its path is refused with a fixed
// text, also with B_GIT_SSH_AS_HTTPS=1. The URL is refusedURL, which holds
// no part of the ref. The text shows no part of the password, the ref gets
// no token, and no git command runs. RefBase and RefVersion read an @ after
// the path as a version.
func TestResolveGitURL_MoreThanOneAtBeforePath(t *testing.T) {
	const want = refusedText
	user := "user:" + refPassword + "@" + refPassword + "@"
	tests := []struct{ ref, base, version string }{
		{user + "github.com/org/repo", user + "github.com/org/repo", ""},
		{user + "github.com/org/repo@v1", user + "github.com/org/repo", "v1"},
		{user + "github.com/org/repo#lbl@v1", user + "github.com/org/repo", "v1"},
		{"tok@" + refPassword + "@github.com/org/repo", "tok@" + refPassword + "@github.com/org/repo", ""},
	}
	for _, knob := range []string{"", "1"} {
		t.Setenv(SSHAsHTTPSEnv, knob)
		t.Setenv("GITHUB_TOKEN", refToken)
		for _, tt := range tests {
			cmds := recordGit(t)
			r := ResolveGitURL(tt.ref, "")
			if r.AuthToken != "" || r.AuthHeader != "" {
				t.Errorf("%s=%q: a token is chosen for a ref with a user part", SSHAsHTTPSEnv, knob)
			}
			if r.URL != refusedURL {
				t.Errorf("%s=%q: the URL is not refusedURL", SSHAsHTTPSEnv, knob)
			}
			if RefBase(tt.ref) != tt.base {
				t.Errorf("RefBase cut the ref at an @ before its path")
			}
			if got := RefVersion(tt.ref); got != tt.version {
				t.Errorf("RefVersion = %q, want %q", strings.ReplaceAll(got, refPassword, "<password>"), tt.version)
			}
			for op, err := range remoteErrors(t, tt.ref, r) {
				if err == nil || err.Error() != want || !errors.Is(err, errUserinfoAt) {
					t.Errorf("%s=%q %s: error %v, want the fixed text", SSHAsHTTPSEnv, knob, op, err != nil)
				}
				if err != nil && formatsHold(err, refPassword) {
					t.Errorf("%s=%q %s: the error shows the password", SSHAsHTTPSEnv, knob, op)
				}
			}
			if len(*cmds) != 0 {
				t.Errorf("%s=%q: %d git commands ran", SSHAsHTTPSEnv, knob, len(*cmds))
			}
		}
	}
}

// An https ref, with the scheme in any case, resolves as the same ref
// without the scheme: the same URL, the same token choice and the same
// header key, so the token goes to the same host. B_GIT_SSH_AS_HTTPS does
// not change this.
func TestResolveGitURL_HTTPSRefAsBareRef(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", refToken)
	t.Setenv("GITLAB_TOKEN", refToken)
	t.Setenv("GITEA_TOKEN", "")
	tests := []struct{ ref, bare string }{
		{"https://github.com/org/repo", "github.com/org/repo"},
		{"HTTPS://github.com/org/repo", "github.com/org/repo"},
		{"Https://github.com/org/repo", "github.com/org/repo"},
		{"https://github.com/org/repo@v1", "github.com/org/repo@v1"},
		{"https://github.com/org/repo#lbl@v1", "github.com/org/repo#lbl@v1"},
		{"https://GitHub.com/org/repo", "GitHub.com/org/repo"},
		{"https://gitlab.com/group/project", "gitlab.com/group/project"},
		{"https://host.example:8443/org/repo", "host.example:8443/org/repo"},
	}
	for _, knob := range []string{"", "1"} {
		t.Setenv(SSHAsHTTPSEnv, knob)
		for _, tt := range tests {
			got, want := ResolveGitURL(tt.ref, ""), ResolveGitURL(tt.bare, "")
			if got != want {
				t.Errorf("%s=%q: ResolveGitURL(%q) = URL %q, token set %v; want the result of %q: URL %q, token set %v",
					SSHAsHTTPSEnv, knob, tt.ref, got.URL, got.AuthToken != "", tt.bare, want.URL, want.AuthToken != "")
			}
			if headerKey(got.URL) != headerKey(want.URL) {
				t.Errorf("%q: header key %q, want %q", tt.ref, headerKey(got.URL), headerKey(want.URL))
			}
			if RefVersion(tt.ref) != RefVersion(tt.bare) || RefLabel(tt.ref) != RefLabel(tt.bare) {
				t.Errorf("%q: version %q label %q, want %q %q", tt.ref,
					RefVersion(tt.ref), RefLabel(tt.ref), RefVersion(tt.bare), RefLabel(tt.bare))
			}
			if err := checkRemote(got.URL); err != nil {
				t.Errorf("%q: checkRemote refuses %q: %v", tt.ref, got.URL, err)
			}
		}
	}
	r := ResolveGitURL("https://github.com/org/repo", "")
	if r.URL != "https://github.com/org/repo.git" || r.AuthToken != refToken ||
		headerKey(r.URL) != "http.https://github.com/.extraHeader" {
		t.Errorf("https://github.com/org/repo: URL %q, token set %v, header key %q",
			r.URL, r.AuthToken != "", headerKey(r.URL))
	}
}

// An https ref with a user part is refused: with more than one @ with the
// fixed text of CheckRef, else with the host error, which shows "***" in
// place of the user part. A ref with another scheme, also http, is refused
// with one fixed text. No text shows a part of the password, the URL that
// the callers print shows "***" in place of the user part, no token is
// chosen and no git command runs.
func TestResolveGitURL_SchemeRefusals(t *testing.T) {
	const scheme = "refusing a git ref with a scheme that b does not fetch: use https://, ssh://, git:// or no scheme"
	const at = refusedText
	user := "user:" + refPassword + "@"
	tests := []struct{ ref, refusal string }{
		{"https://" + user + "github.com/org/repo",
			"refusing https://***@github.com/org/repo.git: " + errRemoteHost.Error()},
		{"HTTPS://" + user + "github.com/org/repo",
			"refusing https://***@github.com/org/repo.git: " + errRemoteHost.Error()},
		{"Https://" + user + "github.com/org/repo@v1",
			"refusing https://***@github.com/org/repo.git: " + errRemoteHost.Error()},
		{"https://" + user + refPassword + "@github.com/org/repo", at},
		{"HTTPS://" + user + refPassword + "@github.com/org/repo", at},
		{"http://github.com/org/repo", scheme},
		{"http://" + user + "github.com/org/repo", scheme},
		{"HTTP://" + user + "github.com/org/repo", scheme},
		{"file:///srv/repo", scheme},
		{"ftp://" + user + "h.example/x", scheme},
		{"git+ssh://git@github.com/org/repo", scheme},
		{"SSH://git@github.com/org/repo", scheme},
		{"git://http://" + user + "github.com/org/repo", scheme},
		{"git://https://" + user + "github.com/org/repo", scheme},
	}
	for _, knob := range []string{"", "1"} {
		t.Setenv(SSHAsHTTPSEnv, knob)
		t.Setenv("GITHUB_TOKEN", refToken)
		for _, tt := range tests {
			cmds := recordGit(t)
			r := ResolveGitURL(tt.ref, "")
			if r.AuthToken != "" || r.AuthHeader != "" {
				t.Errorf("%s=%q %q: a token is chosen", SSHAsHTTPSEnv, knob, tt.ref)
			}
			if strings.Contains(RedactUserinfo(r.URL), refPassword) || strings.Contains(RedactUserinfo(tt.ref), refPassword) {
				t.Errorf("%s=%q %q: RedactUserinfo of the ref or the URL shows the password", SSHAsHTTPSEnv, knob, tt.ref)
			}
			for op, err := range remoteErrors(t, tt.ref, r) {
				if err == nil || err.Error() != tt.refusal {
					t.Errorf("%s=%q %q %s: error %q, want %q", SSHAsHTTPSEnv, knob, tt.ref, op,
						strings.ReplaceAll(fmt.Sprint(err), refPassword, "<password>"), tt.refusal)
				}
				if err != nil && formatsHold(err, refPassword) {
					t.Errorf("%s=%q %q %s: the error shows the password", SSHAsHTTPSEnv, knob, tt.ref, op)
				}
			}
			if len(*cmds) != 0 {
				t.Errorf("%s=%q %q: %d git commands ran", SSHAsHTTPSEnv, knob, tt.ref, len(*cmds))
			}
		}
	}
}
