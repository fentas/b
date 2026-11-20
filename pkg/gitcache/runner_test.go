package gitcache

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fakeGitOutEnv makes the test binary act as git (TestMain): it writes its
// argv and env as JSON to the file that the variable names. With
// fakeGitFailEnv=1 it then writes the header value of GIT_CONFIG_VALUE_0 to
// stderr and exits 128.
const (
	fakeGitOutEnv  = "B_TEST_FAKE_GIT_OUT"
	fakeGitFailEnv = "B_TEST_FAKE_GIT_FAIL"
)

func TestMain(m *testing.M) {
	if out := os.Getenv(fakeGitOutEnv); out != "" {
		os.Exit(fakeGitMain(out))
	}
	os.Exit(m.Run())
}

// fakeGitCall is what the fake git received.
type fakeGitCall struct {
	Args []string
	Env  []string
}

func fakeGitMain(out string) int {
	b, err := json.Marshal(fakeGitCall{Args: os.Args[1:], Env: os.Environ()})
	if err != nil {
		return 3
	}
	if err := os.WriteFile(out, b, 0o600); err != nil {
		return 3
	}
	if os.Getenv(fakeGitFailEnv) == "1" {
		fmt.Fprintf(os.Stderr, "fatal: the remote refused the request; sent %s\n", os.Getenv("GIT_CONFIG_VALUE_0"))
		return 128
	}
	if len(os.Args) > 1 && os.Args[1] == "ls-remote" {
		fmt.Println("c0ffee\trefs/tags/v1")
	}
	return 0
}

// useFakeGit puts the test binary as "git" in a new directory and makes
// that directory the only PATH entry of the test process, so the production
// runners (runAuth, outputAuth) start the fake git. The git config env that
// the child inherits has known values. The returned function reads what the
// last fake git received.
func useFakeGit(t *testing.T, fail bool) func() fakeGitCall {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.Symlink(exe, filepath.Join(bin, "git")); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "call.json")
	t.Setenv("PATH", bin)
	t.Setenv(fakeGitOutEnv, out)
	t.Setenv(fakeGitFailEnv, map[bool]string{true: "1", false: ""}[fail])
	t.Setenv("GIT_CONFIG_COUNT", "0")
	t.Setenv("GIT_CONFIG_PARAMETERS", "")
	t.Setenv("GIT_TERMINAL_PROMPT", "")
	return func() fakeGitCall {
		t.Helper()
		b, err := os.ReadFile(out)
		if err != nil {
			t.Fatalf("the fake git did not run: %v", err)
		}
		if err := os.Remove(out); err != nil {
			t.Fatal(err)
		}
		var c fakeGitCall
		if err := json.Unmarshal(b, &c); err != nil {
			t.Fatal(err)
		}
		return c
	}
}

// childEnv returns the env of a fake git call as a map. It fails the test
// for a key that is in the env more than once.
func childEnv(t *testing.T, env []string) map[string]string {
	t.Helper()
	m := make(map[string]string, len(env))
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		if _, dup := m[k]; dup {
			t.Errorf("the child env has %s more than once", k)
		}
		m[k] = v
	}
	return m
}

// The production runners give the git child what authCmd built: for clone
// and fetch (runAuth) and ls-remote (outputAuth), the child env has
// GIT_TERMINAL_PROMPT=0 and one GIT_CONFIG_* entry, the header key scoped to
// the host of the URL with the header as its value. No key is the unscoped
// http.extraHeader, and argv has no token.
func TestProductionRunners_ChildGetsScopedHeaderEnv(t *testing.T) {
	setProviderEnv(t, "1")
	last := useFakeGit(t, false)
	const ref = "git@github.com:org/repo"
	r := ResolveGitURL(ref, "")
	hdr := "Authorization: Bearer " + fakeGH
	if r.URL != "https://github.com/org/repo.git" || r.AuthHeader != hdr {
		t.Fatalf("ResolveGitURL = %q, header set %v", r.URL, r.AuthHeader != "")
	}
	root := t.TempDir()
	dir := CacheDir(root, ref)
	steps := []struct {
		name string
		run  func() error
		args []string
	}{
		{"ls-remote", func() error { _, err := ResolveRefAuth(r.URL, "v1", r.AuthHeader); return err },
			[]string{"ls-remote", "--", r.URL, "v1"}},
		{"clone", func() error { return EnsureCloneAuth(root, ref, r.URL, r.AuthHeader) },
			[]string{"clone", "--bare", "--depth", "1", "--", r.URL, dir}},
		{"fetch", func() error { return FetchURLAuth(root, ref, r.URL, "c0ffee", r.AuthHeader) },
			[]string{"-C", dir, "fetch", "--depth", "1", "--", r.URL, "c0ffee"}},
	}
	want := map[string]string{
		"GIT_TERMINAL_PROMPT": "0",
		"GIT_CONFIG_COUNT":    "1",
		"GIT_CONFIG_KEY_0":    "http.https://github.com/.extraHeader",
		"GIT_CONFIG_VALUE_0":  hdr,
	}
	for _, s := range steps {
		if err := s.run(); err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
		c := last()
		if !slices.Equal(c.Args, s.args) {
			t.Errorf("%s: argv %q, want %q", s.name, c.Args, s.args)
		}
		env := childEnv(t, c.Env)
		for k, v := range want {
			if env[k] != v {
				t.Errorf("%s: child env %s is not the wanted value (set: %v)", s.name, k, env[k] != "")
			}
		}
		for k, v := range env {
			if strings.HasPrefix(k, "GIT_CONFIG_KEY_") && strings.EqualFold(v, "http.extraHeader") {
				t.Errorf("%s: child env %s is the unscoped http.extraHeader", s.name, k)
			}
		}
		if env["GIT_CONFIG_PARAMETERS"] != "" {
			t.Errorf("%s: child env has GIT_CONFIG_PARAMETERS", s.name)
		}
		for _, a := range c.Args {
			if strings.Contains(a, fakeGH) {
				t.Errorf("%s: argv has the token", s.name)
			}
		}
	}

	// An SSH URL gets GIT_TERMINAL_PROMPT=0 and no header entry.
	t.Setenv(SSHAsHTTPSEnv, "")
	s := ResolveGitURL(ref, "")
	if _, err := ResolveRefAuth(s.URL, "v1", s.AuthHeader); err != nil {
		t.Fatal(err)
	}
	env := childEnv(t, last().Env)
	if env["GIT_TERMINAL_PROMPT"] != "0" || env["GIT_CONFIG_COUNT"] != "0" || env["GIT_CONFIG_KEY_0"] != "" {
		t.Errorf("SSH ls-remote: child env prompt %q, count %q, key 0 set %v",
			env["GIT_TERMINAL_PROMPT"], env["GIT_CONFIG_COUNT"], env["GIT_CONFIG_KEY_0"] != "")
	}
}

// git fails and writes the header to stderr. The errors of ls-remote, clone
// and fetch hold no token in %v, %+v, %s or %q, nor does an error that
// errors.Unwrap returns from them; they keep the redacted stderr text.
// errors.As finds the *exec.ExitError.
func TestRunnerErrors_RedactTheToken(t *testing.T) {
	setProviderEnv(t, "")
	useFakeGit(t, true)
	for _, tt := range []struct {
		ref, token, redacted string
	}{
		{"github.com/org/repo", fakeGH, "sent Authorization: Bearer ***"},
		{"gitlab.com/group/project", fakeGL, "sent PRIVATE-TOKEN: ***"},
	} {
		r := ResolveGitURL(tt.ref, "")
		if r.AuthHeader == "" {
			t.Fatalf("%s: no header", tt.ref)
		}
		root := t.TempDir()
		for _, op := range []struct {
			name string
			run  func() error
		}{
			{"ls-remote", func() error { _, err := ResolveRefAuth(r.URL, "", r.AuthHeader); return err }},
			{"clone", func() error { return EnsureCloneAuth(root, tt.ref, r.URL, r.AuthHeader) }},
			{"fetch", func() error { return FetchURLAuth(root, tt.ref, r.URL, "c0ffee", r.AuthHeader) }},
		} {
			err := op.run()
			if err == nil {
				t.Fatalf("%s %s: no error", tt.ref, op.name)
			}
			for _, verb := range []string{"%v", "%+v", "%s", "%q"} {
				if strings.Contains(fmt.Sprintf(verb, err), tt.token) {
					t.Errorf("%s %s: the %s text has the token", tt.ref, op.name, verb)
				}
			}
			if !strings.Contains(err.Error(), tt.redacted) {
				t.Errorf("%s %s: error %q does not have the redacted stderr %q", tt.ref, op.name, err, tt.redacted)
			}
			for e := errors.Unwrap(err); e != nil; e = errors.Unwrap(e) {
				if strings.Contains(e.Error(), tt.token) {
					t.Errorf("%s %s: a wrapped %T has the token", tt.ref, op.name, e)
				}
			}
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 128 {
				t.Errorf("%s %s: errors.As finds no *exec.ExitError with code 128", tt.ref, op.name)
			}
		}
	}
}

// With a header and no git on the PATH, errors.Is finds exec.ErrNotFound
// through the redacted error.
func TestRunnerErrors_KeepTheChain(t *testing.T) {
	setProviderEnv(t, "")
	t.Setenv("PATH", t.TempDir())
	r := ResolveGitURL("github.com/org/repo", "")
	root := t.TempDir()
	_, lsErr := ResolveRefAuth(r.URL, "", r.AuthHeader)
	for i, err := range []error{
		lsErr,
		EnsureCloneAuth(root, "github.com/org/repo", r.URL, r.AuthHeader),
		FetchURLAuth(root, "github.com/org/repo", r.URL, "c0ffee", r.AuthHeader),
	} {
		if !errors.Is(err, exec.ErrNotFound) {
			t.Errorf("command %d: errors.Is(%v, exec.ErrNotFound) = false", i, err)
		}
	}
}
