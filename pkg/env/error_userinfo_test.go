package env

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/fentas/b/pkg/envmatch"
	"github.com/fentas/b/pkg/gitcache"
)

// fakePassword is the password in the refs of these tests. It is not a real
// value.
const fakePassword = "fake-pw-value-for-tests"

// canaryPassword and canaryToken are the password and the provider token of
// the refused-ref tests. They are not real values.
const (
	canaryPassword = "pw-canary-q7z"
	canaryToken    = "FAKE-TOKEN-x"
)

// gitSpy makes a fake git the only program on the PATH. The fake git writes
// its arguments to a file and fails. The returned function gives the
// arguments of each call, one call on each line.
func gitSpy(t *testing.T) func() string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake git is a shell script")
	}
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + calls + "'\nexit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	return func() string {
		b, err := os.ReadFile(calls)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		return string(b)
	}
}

// SyncEnv refuses a ref whose user part b cannot show safely with a fixed
// text: at the version resolve, and with a forced commit at the clone. No
// part of the password and no token shows in the error, no token is chosen
// and no git command runs. This holds with and without
// B_GIT_SSH_AS_HTTPS=1.
func TestSyncEnv_RefusedRefShowsNoPassword(t *testing.T) {
	isolateGit(t)
	t.Setenv("GITHUB_TOKEN", canaryToken)
	calls := gitSpy(t)
	const (
		atText     = "refusing a git ref with more than one @ before its path: remove the user part"
		schemeText = "refusing a git ref with a scheme that b does not fetch: use https://, ssh://, git:// or no scheme"
		hostText   = "refusing https://***@github.com/org/repo.git: the host part is not a plain host name or IP address " +
			"with an optional port (no user name, password, wildcard, percent sign or trailing dot); " +
			"b does not run a remote git command for it"
	)
	user := "user:" + canaryPassword + "@"
	tests := []struct{ ref, refusal string }{
		{user + canaryPassword + "@github.com/org/repo", atText},
		{"https://" + user + canaryPassword + "@github.com/org/repo", atText},
		{"https://" + user + "github.com/org/repo", hostText},
		{"HTTPS://" + user + "github.com/org/repo", hostText},
		{"Https://" + user + "github.com/org/repo", hostText},
		{"http://" + user + "github.com/org/repo", schemeText},
		{"ftp://" + user + "h.example/x", schemeText},
		{"git://http://" + user + "github.com/org/repo", schemeText},
	}
	for _, knob := range []string{"", "1"} {
		t.Setenv(gitcache.SSHAsHTTPSEnv, knob)
		for _, tt := range tests {
			if gitcache.ResolveGitURL(tt.ref, "").AuthToken != "" {
				t.Errorf("%s=%q: a token is chosen", gitcache.SSHAsHTTPSEnv, knob)
			}
			for _, force := range []string{"", "c0ffee"} {
				cfg := EnvConfig{
					Ref:         tt.ref,
					Strategy:    StrategyReplace,
					Files:       map[string]envmatch.GlobConfig{"cfg/a.yaml": {}},
					ForceCommit: force,
				}
				_, err := SyncEnv(cfg, t.TempDir(), t.TempDir(), nil)
				if err == nil {
					t.Fatalf("%s=%q forced commit %q: SyncEnv passed", gitcache.SSHAsHTTPSEnv, knob, force)
				}
				for _, verb := range []string{"%s", "%v", "%+v", "%q", "%#v"} {
					text := fmt.Sprintf(verb, err)
					if strings.Contains(text, canaryPassword) || strings.Contains(text, canaryToken) {
						t.Errorf("%s=%q forced commit %q: the %s text shows the password or the token",
							gitcache.SSHAsHTTPSEnv, knob, force, verb)
					}
				}
				if !strings.HasSuffix(err.Error(), ": "+tt.refusal) {
					t.Errorf("%s=%q forced commit %q: error %q, want the fixed text %q",
						gitcache.SSHAsHTTPSEnv, knob, force, strings.ReplaceAll(err.Error(), canaryPassword, "<password>"), tt.refusal)
				}
			}
		}
	}
	if c := calls(); c != "" {
		t.Errorf("git ran:\n%s", strings.ReplaceAll(c, canaryPassword, "<password>"))
	}
}

// For a ref with a user part, the SyncEnv error shows "***@" in place of
// the user part, in its own text and in the text of gitcache: at the
// version resolve (env.go, "resolving") and, with a forced commit, at the
// clone (env.go, "cloning"). b refuses the URL, so no remote git command
// runs.
func TestSyncEnv_ErrorHidesTheUserPart(t *testing.T) {
	isolateGit(t)
	t.Setenv("GITHUB_TOKEN", "")
	ref := "user:" + fakePassword + "@github.com/org/repo"
	for _, tt := range []struct {
		force, prefix string
	}{
		{"", "resolving ***@github.com/org/repo@"},
		{"c0ffee", "cloning https://***@github.com/org/repo.git"},
	} {
		cfg := EnvConfig{
			Ref:         ref,
			Strategy:    StrategyReplace,
			Files:       map[string]envmatch.GlobConfig{"cfg/a.yaml": {}},
			ForceCommit: tt.force,
		}
		_, err := SyncEnv(cfg, t.TempDir(), t.TempDir(), nil)
		if err == nil {
			t.Fatalf("forced commit %q: SyncEnv passed for a ref with a password", tt.force)
		}
		msg := err.Error()
		if strings.Contains(msg, fakePassword) {
			t.Errorf("forced commit %q: the error shows the password", tt.force)
		}
		if !strings.HasPrefix(msg, tt.prefix) || !strings.Contains(msg, "refusing https://***@github.com/org/repo.git") {
			t.Errorf("forced commit %q: error %q, want the prefix %q and the refused URL with ***@", tt.force, msg, tt.prefix)
		}
	}
}
