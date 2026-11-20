package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/fentas/b/pkg/envmatch"
	"github.com/fentas/b/pkg/gitcache"
	"github.com/fentas/b/pkg/lock"
	"github.com/fentas/b/pkg/state"
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

// gitSpy isolates HOME and makes a fake git the only program on the PATH.
// The fake git writes its arguments to a file and fails. The returned
// function gives the arguments of each call, one call on each line.
func gitSpy(t *testing.T) func() string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake git is a shell script")
	}
	home := t.TempDir()
	t.Setenv("HOME", home) // gitcache.DefaultCacheRoot is under HOME
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
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

// checkRefusal fails the test when err is nil, is not or does not end in
// the fixed text refusal, or shows the password or the token in a format
// verb.
func checkRefusal(t *testing.T, name string, err error, refusal string) {
	t.Helper()
	if err == nil {
		t.Errorf("%s: no error", name)
		return
	}
	for _, verb := range []string{"%s", "%v", "%+v", "%q", "%#v"} {
		text := fmt.Sprintf(verb, err)
		if strings.Contains(text, canaryPassword) || strings.Contains(text, canaryToken) {
			t.Errorf("%s: the %s text shows the password or the token", name, verb)
		}
	}
	if err.Error() != refusal && !strings.HasSuffix(err.Error(), ": "+refusal) {
		t.Errorf("%s: error %q, want the fixed text %q", name, strings.ReplaceAll(err.Error(), canaryPassword, "<password>"), refusal)
	}
}

// b env add, b env match and b env profiles refuse a ref whose user part b
// cannot show safely with a fixed text. No part of the password and no
// token shows, no token is chosen and no git command runs, with and without
// B_GIT_SSH_AS_HTTPS=1.
func TestEnvCommands_RefusedRefShowsNoPassword(t *testing.T) {
	calls := gitSpy(t)
	t.Setenv("GITHUB_TOKEN", canaryToken)
	io, _, _ := bufIO()
	const (
		atText     = "refusing a git ref with a password or more than one @ before its path: remove the user part"
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
	}
	for _, knob := range []string{"", "1"} {
		t.Setenv(gitcache.SSHAsHTTPSEnv, knob)
		for _, tt := range tests {
			if gitcache.ResolveGitURL(tt.ref, "").AuthToken != "" {
				t.Errorf("%s=%q: a token is chosen", gitcache.SSHAsHTTPSEnv, knob)
			}
			name := gitcache.SSHAsHTTPSEnv + "=" + knob
			checkRefusal(t, name+" env add", (&EnvAddOptions{SharedOptions: NewSharedOptions(io, nil)}).Run(tt.ref+"#base"), tt.refusal)
			checkRefusal(t, name+" env match", (&EnvMatchOptions{SharedOptions: NewSharedOptions(io, nil)}).Run([]string{tt.ref, "manifests/*.yaml"}), tt.refusal)
			checkRefusal(t, name+" env profiles", (&EnvProfilesOptions{SharedOptions: NewSharedOptions(io, nil)}).Run(tt.ref), tt.refusal)
		}
	}
	if c := calls(); c != "" {
		t.Errorf("git ran:\n%s", strings.ReplaceAll(c, canaryPassword, "<password>"))
	}
}

// For a ref with a user part, b env match, b env profiles and b env add
// show "***@" in place of the user part in the resolve error (env.go,
// "resolving"). b refuses the URL, so no remote git command runs. The user
// part here has no password: CheckRef refuses a password
// (TestEnvCommands_PasswordRefRefusedFirst).
func TestEnvCommands_ResolveErrorHidesTheUserPart(t *testing.T) {
	offlineUpstream(t)
	ref := fakePassword + "@github.com/org/repo"
	io, _, _ := bufIO()
	for _, tt := range []struct {
		name string
		run  func() error
	}{
		{"env match", func() error {
			return (&EnvMatchOptions{SharedOptions: NewSharedOptions(io, nil)}).Run([]string{ref, "manifests/*.yaml"})
		}},
		{"env profiles", func() error {
			return (&EnvProfilesOptions{SharedOptions: NewSharedOptions(io, nil)}).Run(ref)
		}},
		{"env add", func() error {
			_, err := (&EnvAddOptions{SharedOptions: NewSharedOptions(io, nil)}).fetchUpstream(gitcache.RefBase(ref), "", ref)
			return err
		}},
	} {
		err := tt.run()
		if err == nil {
			t.Fatalf("%s: no error for a ref with a password", tt.name)
		}
		msg := err.Error()
		if strings.Contains(msg, fakePassword) {
			t.Errorf("%s: the error shows the password", tt.name)
		}
		if !strings.HasPrefix(msg, "resolving ***@github.com/org/repo: refusing https://***@github.com/org/repo.git") {
			t.Errorf("%s: error %q, want the resolve prefix and the refused URL with ***@", tt.name, msg)
		}
	}
}

// b env add with no #label, and b env add -i with no terminal, name the ref
// in their error with "***" in place of the user part. No git command runs.
func TestEnvAdd_RefErrorsHideTheUserPart(t *testing.T) {
	saveHooks(t)
	isTTYFunc = func() bool { return false }
	calls := gitSpy(t)
	t.Setenv("GITHUB_TOKEN", canaryToken)
	io, _, _ := bufIO()
	const ref = "https://user:" + canaryPassword + "@github.com/org/repo"
	for _, tt := range []struct {
		interactive bool
		want        string
	}{
		{false, "profile name required — use https://***@github.com/org/repo#<name>"},
		{true, "interactive mode requires a terminal — use https://***@github.com/org/repo#<name> syntax instead"},
	} {
		err := (&EnvAddOptions{SharedOptions: NewSharedOptions(io, nil), Interactive: tt.interactive}).Run(ref)
		if err == nil {
			t.Fatalf("interactive %v: no error", tt.interactive)
		}
		for _, verb := range []string{"%s", "%v", "%+v", "%q", "%#v"} {
			text := fmt.Sprintf(verb, err)
			if strings.Contains(text, canaryPassword) || strings.Contains(text, canaryToken) {
				t.Errorf("interactive %v: the %s text shows the password or the token", tt.interactive, verb)
			}
		}
		if !strings.HasPrefix(err.Error(), tt.want) {
			t.Errorf("interactive %v: error %q, want the prefix %q", tt.interactive,
				strings.ReplaceAll(err.Error(), canaryPassword, "<password>"), tt.want)
		}
	}
	if c := calls(); c != "" {
		t.Errorf("git ran:\n%s", strings.ReplaceAll(c, canaryPassword, "<password>"))
	}
}

// b env add, b env match and b env profiles refuse a ref with a password or
// more than one @ in front of the host, or with an @ after another "://",
// before anything else: the error is
// the fixed text alone. This holds for b env add with no #label and for
// b env add -i with no terminal, whose errors name the ref, with and
// without a path and a version. No token is chosen and no git command runs.
func TestEnvCommands_PasswordRefRefusedFirst(t *testing.T) {
	saveHooks(t)
	isTTYFunc = func() bool { return false }
	calls := gitSpy(t)
	t.Setenv("GITHUB_TOKEN", canaryToken)
	io, out, errOut := bufIO()
	const refusal = "refusing a git ref with a password or more than one @ before its path: remove the user part"
	var refs []string
	for _, user := range []string{"user:p@" + canaryPassword + "@", "user:" + canaryPassword + "@"} {
		for _, path := range []string{"", "/org/repo"} {
			for _, version := range []string{"", "@v1"} {
				refs = append(refs, user+"github.com"+path+version)
			}
		}
	}
	refs = append(refs,
		"git@"+canaryPassword+"@github.com:org/repo",
		"ssh://git@"+canaryPassword+"@github.com/org/repo",
		"https://a?b://user:"+canaryPassword+"@github.com/org/repo")
	for _, knob := range []string{"", "1"} {
		t.Setenv(gitcache.SSHAsHTTPSEnv, knob)
		for i, ref := range refs {
			if gitcache.ResolveGitURL(ref, "").AuthToken != "" {
				t.Errorf("%s=%q ref %d: a token is chosen", gitcache.SSHAsHTTPSEnv, knob, i)
			}
			for _, tt := range []struct {
				name string
				run  func() error
			}{
				{"env add, no label", func() error {
					return (&EnvAddOptions{SharedOptions: NewSharedOptions(io, nil)}).Run(ref)
				}},
				{"env add -i, no terminal", func() error {
					return (&EnvAddOptions{SharedOptions: NewSharedOptions(io, nil), Interactive: true}).Run(ref)
				}},
				{"env add #base", func() error {
					return (&EnvAddOptions{SharedOptions: NewSharedOptions(io, nil)}).Run(ref + "#base")
				}},
				{"env match", func() error {
					return (&EnvMatchOptions{SharedOptions: NewSharedOptions(io, nil)}).Run([]string{ref, "manifests/*.yaml"})
				}},
				{"env profiles", func() error {
					return (&EnvProfilesOptions{SharedOptions: NewSharedOptions(io, nil)}).Run(ref)
				}},
			} {
				name := fmt.Sprintf("%s=%q ref %d %s", gitcache.SSHAsHTTPSEnv, knob, i, tt.name)
				err := tt.run()
				if err == nil || err.Error() != refusal {
					t.Errorf("%s: error %q, want the fixed text alone", name,
						strings.ReplaceAll(fmt.Sprint(err), canaryPassword, "<password>"))
				}
				checkRefusal(t, name, err, refusal)
			}
		}
	}
	if strings.Contains(out.String()+errOut.String(), canaryPassword) {
		t.Error("the output shows the password")
	}
	if c := calls(); c != "" {
		t.Errorf("git ran:\n%s", strings.ReplaceAll(c, canaryPassword, "<password>"))
	}
}

// The "already exists" error of b env add names the key of b.yaml with
// "***" in place of the user part: the password is in the ref and in the
// key that b.yaml holds. Run returns it before any git command, and
// addProfile (the add of each profile that b env add -i selects) returns
// the same text.
func TestEnvAdd_ExistsErrorHidesThePassword(t *testing.T) {
	calls := gitSpy(t)
	for _, tt := range []struct{ ref, shown string }{
		{"https://user:" + canaryPassword + "@github.com/org/repo", "https://***@github.com/org/repo#lbl"},
		{"ssh://user:" + canaryPassword + "@host.example/org/repo", "ssh://***@host.example/org/repo#lbl"},
	} {
		want := tt.shown + " already exists in b.yaml — remove it first with `b env remove " + tt.shown + "`"
		io, out, errOut := bufIO()
		shared := NewSharedOptions(io, nil)
		shared.Config = &state.State{Envs: state.EnvList{{Key: tt.ref + "#lbl"}}}
		o := &EnvAddOptions{SharedOptions: shared}
		source := &state.EnvEntry{Key: "lbl", Files: map[string]envmatch.GlobConfig{"a/**": {}}}
		for name, err := range map[string]error{
			"Run":        o.Run(tt.ref + "#lbl"),
			"addProfile": o.addProfile(tt.ref, "lbl", "", source, &state.State{Profiles: state.EnvList{source}}),
		} {
			if err == nil || err.Error() != want {
				t.Errorf("%s %s: error %q, want %q", tt.shown, name,
					strings.ReplaceAll(fmt.Sprint(err), canaryPassword, "<password>"), want)
			}
			checkRefusal(t, tt.shown+" "+name, err, want)
		}
		if strings.Contains(out.String()+errOut.String(), canaryPassword) {
			t.Errorf("%s: the output shows the password", tt.shown)
		}
	}
	if c := calls(); c != "" {
		t.Errorf("git ran:\n%s", strings.ReplaceAll(c, canaryPassword, "<password>"))
	}
}

// The profile list headers of b env profiles and b env add -i name the ref
// with "***" in place of the user part, and so does the "already exists"
// error of a selected profile. The output of b env add -i shows no part of
// the password. An ssh:// ref keeps its password up to git; here git reads
// that URL from a local repo (url.<bare>.insteadOf).
func TestEnvCommands_ProfileListsHideThePassword(t *testing.T) {
	saveHooks(t)
	offlineUpstream(t)
	t.Setenv(gitcache.SSHAsHTTPSEnv, "")
	t.Setenv("GIT_CONFIG_COUNT", "2")
	t.Setenv("GIT_CONFIG_KEY_1", os.Getenv("GIT_CONFIG_KEY_0"))
	t.Setenv("GIT_CONFIG_VALUE_1", "ssh://user:"+canaryPassword+"@github.com/org/repo.git")
	isTTYFunc = func() bool { return true }
	const (
		ref   = "ssh://user:" + canaryPassword + "@github.com/org/repo"
		shown = "ssh://***@github.com/org/repo"
	)

	// b env profiles: the header. Its "Install a profile with" hint names
	// the ref as the user wrote it, so this test reads the header line only.
	io, out, _ := bufIO()
	err := (&EnvProfilesOptions{SharedOptions: NewSharedOptions(io, nil)}).Run(ref)
	if err != nil {
		t.Fatalf("env profiles: %s", strings.ReplaceAll(err.Error(), canaryPassword, "<password>"))
	}
	header := ""
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(line, "Available profiles from ") {
			header = line
			break
		}
	}
	if !strings.HasPrefix(header, "Available profiles from "+shown+" @ ") || strings.Contains(header, canaryPassword) {
		t.Errorf("env profiles: header %q, want the ref with ***", strings.ReplaceAll(header, canaryPassword, "<password>"))
	}

	io, out, errOut := bufIO()
	shared := NewSharedOptions(io, nil)
	shared.Config = &state.State{Envs: state.EnvList{{Key: ref + "#base"}}}
	o := &EnvAddOptions{SharedOptions: shared, Interactive: true, stdinReader: strings.NewReader("1\n")}
	err = o.Run(ref)
	if err == nil || err.Error() != "no profiles were added" {
		t.Errorf("env add -i: error %q, want no profiles added", strings.ReplaceAll(fmt.Sprint(err), canaryPassword, "<password>"))
	}
	if !strings.Contains(out.String(), "Available profiles from "+shown+":") {
		t.Errorf("env add -i: output %q, want the header with ***", strings.ReplaceAll(out.String(), canaryPassword, "<password>"))
	}
	if !strings.Contains(errOut.String(), shown+"#base already exists in b.yaml") {
		t.Errorf("env add -i: error output %q, want the exists error with ***", strings.ReplaceAll(errOut.String(), canaryPassword, "<password>"))
	}
	if strings.Contains(out.String()+errOut.String(), canaryPassword) {
		t.Error("env add -i: the output shows the password")
	}
}

// b update refuses an env key of b.yaml with a password or more than one @
// in front of its host, or with an @ after another "://", before it splits
// the key (RefLabel, RefBase). Each such key gets one ✗ line with the fixed
// text and "<refused env key>" in place of the key, and the failure summary
// names the same text. There is no "resolving" line, no token is chosen, no
// git command runs, and b.lock does not get the key. This holds for b update
// with no argument and with the key as its argument, with and without
// B_GIT_SSH_AS_HTTPS=1. RedactUserinfo does not hide the first and the last
// key, so the key column shows the fixed text.
func TestUpdateEnvs_RefusedKeyRefusedFirst(t *testing.T) {
	saveHooks(t)
	calls := gitSpy(t)
	t.Setenv("GITHUB_TOKEN", canaryToken)
	const refusal = "refusing a git ref with a password or more than one @ before its path: remove the user part"
	keys := []string{
		"https://a?b://user:" + canaryPassword + "@host.example/x",
		"user:p@" + canaryPassword + "@github.com/org/repo#lbl",
		"ssh://git@" + canaryPassword + "@github.com/org/repo",
	}
	for i, key := range keys {
		if gitcache.ResolveGitURL(key, "").AuthToken != "" {
			t.Errorf("a token is chosen for key %d", i)
		}
	}
	for _, knob := range []string{"", "1"} {
		t.Setenv(gitcache.SSHAsHTTPSEnv, knob)
		for _, run := range []struct {
			name string
			refs [][]string // nil: b update with no argument
		}{
			{"no argument", [][]string{nil}},
			{"the key as the argument", [][]string{{keys[0]}, {keys[1]}, {keys[2]}}},
		} {
			for i, refs := range run.refs {
				dir := t.TempDir()
				t.Setenv("PATH_BASE", dir)
				configPath := filepath.Join(dir, "b.yaml")
				if err := os.WriteFile(configPath, []byte("binaries: {}\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := lock.WriteLock(dir, &lock.Lock{Version: 1}, "test"); err != nil {
					t.Fatal(err)
				}
				io, out, errOut := bufIO()
				envs := state.EnvList{}
				for _, key := range keys {
					envs = append(envs, &state.EnvEntry{Key: key, Files: map[string]envmatch.GlobConfig{"a/**": {}}})
				}
				o := &UpdateOptions{SharedOptions: &SharedOptions{
					IO:               io,
					ConfigPath:       configPath,
					loadedConfigPath: configPath,
					Config:           &state.State{Envs: envs},
				}}
				name := fmt.Sprintf("%s=%q %s %d", gitcache.SSHAsHTTPSEnv, knob, run.name, i)
				want := len(keys)
				if refs != nil {
					o.specifiedEnvRefs = refs
					want = len(refs)
				}
				err := o.Run()
				if err == nil || !strings.HasSuffix(err.Error(), "env(s) failed: "+strings.TrimSuffix(strings.Repeat(refusedEnvKey+", ", want), ", ")) {
					t.Errorf("%s: error %q, want the failure summary with the fixed key text", name,
						strings.ReplaceAll(fmt.Sprint(err), canaryPassword, "<password>"))
				}
				checkRefusalFormats(t, name, err)
				line := "  " + fmt.Sprintf("%-40s", refusedEnvKey) + " ✗ " + refusal + "\n"
				if got := strings.Count(errOut.String(), line); got != want {
					t.Errorf("%s: %d refusal lines, want %d; error output %q", name, got, want,
						strings.ReplaceAll(errOut.String(), canaryPassword, "<password>"))
				}
				all := out.String() + errOut.String()
				if strings.Contains(all, canaryPassword) || strings.Contains(all, canaryToken) {
					t.Errorf("%s: the output shows the password or the token", name)
				}
				if strings.Contains(all, "resolving") {
					t.Errorf("%s: the output has a resolving line", name)
				}
				if b, err := os.ReadFile(filepath.Join(dir, "b.lock")); err != nil || strings.Contains(string(b), canaryPassword) {
					t.Errorf("%s: b.lock cannot be read or holds the password (read error: %v)", name, err != nil)
				}
			}
		}
	}
	if c := calls(); c != "" {
		t.Errorf("git ran:\n%s", strings.ReplaceAll(c, canaryPassword, "<password>"))
	}
}

// checkRefusalFormats fails the test when err shows the password or the
// token in a format verb.
func checkRefusalFormats(t *testing.T, name string, err error) {
	t.Helper()
	for _, verb := range []string{"%s", "%v", "%+v", "%q", "%#v"} {
		if text := fmt.Sprintf(verb, err); strings.Contains(text, canaryPassword) || strings.Contains(text, canaryToken) {
			t.Errorf("%s: the %s text shows the password or the token", name, verb)
		}
	}
}
