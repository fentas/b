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

// A ref with more than one raw @ before its path is refused with a fixed
// text, also with B_GIT_SSH_AS_HTTPS=1. The @ before the path are not a
// version, so the URL keeps the host. The text shows no part of the
// password, the ref gets no token, and no git command runs. An @ after the
// path stays a version.
func TestResolveGitURL_MoreThanOneAtBeforePath(t *testing.T) {
	const want = "refusing a git ref with more than one @ before its path: remove the user part"
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
			// The ref minus its label and version, with the https prefix.
			if wantURL := "https://" + tt.base + ".git"; r.URL != wantURL {
				t.Errorf("%s=%q: the URL is not the ref minus its version", SSHAsHTTPSEnv, knob)
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
