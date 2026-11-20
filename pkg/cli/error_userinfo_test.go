package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/fentas/b/pkg/env"
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
// the same text. For a key with "***", the hint names no `b env remove`
// command: that command would not remove the key. A key that shows as it
// is (no user part, or an SSH user name with no password) keeps the
// command.
func TestEnvAdd_ExistsErrorHidesThePassword(t *testing.T) {
	calls := gitSpy(t)
	for _, tt := range []struct {
		ref, shown string
		command    bool
	}{
		{"https://user:" + canaryPassword + "@github.com/org/repo", "https://***@github.com/org/repo#lbl", false},
		{"ssh://user:" + canaryPassword + "@host.example/org/repo", "ssh://***@host.example/org/repo#lbl", false},
		{"github.com/org/repo", "github.com/org/repo#lbl", true},
		{"git@github.com:org/repo", "git@github.com:org/repo#lbl", true},
	} {
		want := tt.shown + " already exists in b.yaml — remove it from b.yaml first"
		if tt.command {
			want = tt.shown + " already exists in b.yaml — remove it first with `b env remove " + tt.shown + "`"
		}
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
// text and "<refused env key #N>" in place of the key, and the failure
// summary names the same text. N is the place of the key in the sorted list
// of the env keys, so the three keys get three texts. There is no
// "resolving" line, no token is chosen, no git command runs, and b.lock does
// not get the key. This holds for b update with no argument and with the key
// as its argument, with and without B_GIT_SSH_AS_HTTPS=1. RedactUserinfo
// does not hide the first and the last key, so the key column shows the
// fixed text.
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
	// The sorted order of the keys: https://..., ssh://..., user:...
	placeholders := []string{"<refused env key #1>", "<refused env key #3>", "<refused env key #2>"}
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
				shown := placeholders // the keys in the order of the env list
				if refs != nil {
					o.specifiedEnvRefs = refs
					shown = placeholders[i : i+1]
				}
				err := o.Run()
				if err == nil || !strings.HasSuffix(err.Error(), "env(s) failed: "+strings.Join(shown, ", ")) {
					t.Errorf("%s: error %q, want the failure summary with the fixed key texts %q", name,
						strings.ReplaceAll(fmt.Sprint(err), canaryPassword, "<password>"), shown)
				}
				checkRefusalFormats(t, name, err)
				for _, placeholder := range shown {
					line := "  " + fmt.Sprintf("%-40s", placeholder) + " ✗ " + refusal + "\n"
					if got := strings.Count(errOut.String(), line); got != 1 {
						t.Errorf("%s: %d refusal lines for %s, want 1; error output %q", name, got, placeholder,
							strings.ReplaceAll(errOut.String(), canaryPassword, "<password>"))
					}
				}
				if got := strings.Count(errOut.String(), " ✗ "); got != len(shown) {
					t.Errorf("%s: %d ✗ lines, want %d", name, got, len(shown))
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

// b update shows an env key that gitcache.CheckRef lets pass with "***" in
// place of its user part, in each line that names the key and in the
// summary: the rollback error, the error of the first and of the second
// sync, the up-to-date line, the plan header and the safety refusal. Each
// key holds a password: the https:// key gets the host error from the real
// sync, and no git command runs for it. The password and the token are in
// no output, in no format verb of the error and not in b.lock.
func TestUpdateEnvs_KeyWithUserPartShownRedacted(t *testing.T) {
	saveHooks(t)
	calls := gitSpy(t)
	t.Setenv("GITHUB_TOKEN", canaryToken)
	t.Setenv(gitcache.SSHAsHTTPSEnv, "")
	isTTYFunc = func() bool { return false }
	result := func(cfg env.EnvConfig, status string) *env.SyncResult {
		return &env.SyncResult{Ref: cfg.Ref, Label: cfg.Label, Commit: "c0ffee1",
			Files: []lock.LockFile{{Path: "a.yaml", Dest: "a.yaml", Status: status}}}
	}
	for _, k := range []struct{ key, shown string }{
		{"https://user:" + canaryPassword + "@github.com/org/repo", "https://***@github.com/org/repo"},
		{"ssh://user:" + canaryPassword + "@host.example/org/repo#lbl", "ssh://***@host.example/org/repo#lbl"},
	} {
		for _, tt := range []struct {
			name    string
			setup   func(o *UpdateOptions, e *state.EnvEntry)
			sync    func(cfg env.EnvConfig) (*env.SyncResult, error) // nil: the real sync
			line    string                                           // the text after the key column
			stdout  bool                                             // the line is on stdout
			summary string                                           // the error of Run without the key; "" for no error
		}{
			{"rollback, no previous commit", func(o *UpdateOptions, _ *state.EnvEntry) { o.Rollback = true },
				func(env.EnvConfig) (*env.SyncResult, error) { return nil, errors.New("the sync ran") },
				" ✗ no previous commit to rollback to", false, "1 env(s) failed: "},
			{"first sync, the host error", nil, nil,
				" ✗ resolving https://***@github.com/org/repo@: refusing https://***@github.com/org/repo.git: the host part",
				false, "1 env(s) failed: "},
			{"first sync fails", nil,
				func(env.EnvConfig) (*env.SyncResult, error) { return nil, errors.New("upstream failure") },
				" ✗ upstream failure", false, "1 env(s) failed: "},
			{"up to date", nil,
				func(cfg env.EnvConfig) (*env.SyncResult, error) {
					return &env.SyncResult{Ref: cfg.Ref, Label: cfg.Label, Skipped: true, Message: "(up to date)"}, nil
				},
				" (up to date)", true, ""},
			{"plan header, dry run", func(o *UpdateOptions, _ *state.EnvEntry) { o.DryRun = true },
				func(cfg env.EnvConfig) (*env.SyncResult, error) { return result(cfg, "replaced"), nil },
				" (new) → c0ffee1", true, ""},
			{"safety refusal", func(_ *UpdateOptions, e *state.EnvEntry) { e.Safety = state.SafetyStrict },
				func(cfg env.EnvConfig) (*env.SyncResult, error) { return result(cfg, "conflict"), nil },
				" ✗ strict safety: plan contains 1 conflict", false, "safety refused 1 env(s): "},
			{"second sync fails", func(_ *UpdateOptions, e *state.EnvEntry) { e.Safety = state.SafetyStrict },
				func(cfg env.EnvConfig) (*env.SyncResult, error) {
					if cfg.DryRun {
						return result(cfg, "replaced"), nil
					}
					return nil, errors.New("apply failure")
				},
				" ✗ apply failure", false, "1 env(s) failed: "},
		} {
			if tt.sync == nil && !strings.HasPrefix(k.key, "https://") {
				continue // the real sync of the ssh:// key runs git
			}
			name := k.shown + " " + tt.name
			syncEnvFunc = env.SyncEnv
			if tt.sync != nil {
				sync := tt.sync
				syncEnvFunc = func(cfg env.EnvConfig, _, _ string, _ *lock.EnvEntry) (*env.SyncResult, error) {
					return sync(cfg)
				}
			}
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
			e := &state.EnvEntry{Key: k.key, Files: map[string]envmatch.GlobConfig{"a/**": {}}}
			o := &UpdateOptions{SharedOptions: &SharedOptions{
				IO:               io,
				ConfigPath:       configPath,
				loadedConfigPath: configPath,
				Config:           &state.State{Envs: state.EnvList{e}},
			}}
			if tt.setup != nil {
				tt.setup(o, e)
			}
			err := o.Run()
			switch {
			case tt.summary == "" && err != nil:
				t.Errorf("%s: error %q, want none", name, strings.ReplaceAll(err.Error(), canaryPassword, "<password>"))
			case tt.summary != "" && (err == nil || err.Error() != tt.summary+k.shown):
				t.Errorf("%s: error %q, want %q", name,
					strings.ReplaceAll(fmt.Sprint(err), canaryPassword, "<password>"), tt.summary+k.shown)
			}
			checkRefusalFormats(t, name, err)
			stream := errOut.String()
			if tt.stdout {
				stream = out.String()
			}
			if want := "  " + fmt.Sprintf("%-40s", k.shown) + tt.line; !strings.Contains(stream, want) {
				t.Errorf("%s: output %q, want the line %q", name, strings.ReplaceAll(stream, canaryPassword, "<password>"), want)
			}
			all := out.String() + errOut.String()
			if strings.Contains(all, canaryPassword) || strings.Contains(all, canaryToken) {
				t.Errorf("%s: the output shows the password or the token", name)
			}
			if b, err := os.ReadFile(filepath.Join(dir, "b.lock")); err != nil || strings.Contains(string(b), canaryPassword) {
				t.Errorf("%s: b.lock cannot be read or holds the password (read error: %v)", name, err != nil)
			}
		}
	}
	if c := calls(); c != "" {
		t.Errorf("git ran:\n%s", strings.ReplaceAll(c, canaryPassword, "<password>"))
	}
}

// b install shows an env key of b.yaml that gitcache.CheckRef lets pass
// with "***" in place of its user part, in its ✗ line, its sync line and its
// up-to-date line. The https:// key gets the host error, and no git command
// runs for it. git reads the ssh:// key from a local repo
// (url.<bare>.insteadOf). The password and the token are in no output. b.lock
// gets an entry for the ssh:// key only, with the ref as b.yaml has it.
func TestInstall_KeyWithUserPartShownRedacted(t *testing.T) {
	offlineUpstream(t)
	t.Setenv(gitcache.SSHAsHTTPSEnv, "")
	t.Setenv("GIT_CONFIG_COUNT", "2")
	t.Setenv("GIT_CONFIG_KEY_1", os.Getenv("GIT_CONFIG_KEY_0"))
	t.Setenv("GIT_CONFIG_VALUE_1", "ssh://user:"+canaryPassword+"@github.com/org/repo.git")
	t.Setenv("GITHUB_TOKEN", canaryToken)
	dir := t.TempDir()
	t.Setenv("PATH_BASE", dir)
	t.Setenv("PATH_BIN", dir)
	configPath := filepath.Join(dir, "b.yaml")
	mustWrite(t, configPath, []byte("binaries: {}\n"))
	const (
		httpsKey   = "https://user:" + canaryPassword + "@github.com/org/repo"
		httpsShown = "https://***@github.com/org/repo"
		sshKey     = "ssh://user:" + canaryPassword + "@github.com/org/repo#lbl"
		sshShown   = "ssh://***@github.com/org/repo#lbl"
	)
	files := map[string]envmatch.GlobConfig{"manifests/*.yaml": {Dest: "out"}}
	for _, run := range []struct{ name, sshLine string }{
		{"first run", " (new) → "},
		{"second run", " (up to date)"},
	} {
		io, out, errOut := bufIO()
		o := &InstallOptions{SharedOptions: &SharedOptions{
			IO:               io,
			ConfigPath:       configPath,
			loadedConfigPath: configPath,
			Config:           &state.State{Envs: state.EnvList{{Key: httpsKey, Files: files}, {Key: sshKey, Files: files}}},
		}}
		if err := o.Complete(nil); err != nil {
			t.Fatalf("%s: Complete: %s", run.name, strings.ReplaceAll(err.Error(), canaryPassword, "<password>"))
		}
		if err := o.Run(); err != nil {
			t.Fatalf("%s: Run: %s", run.name, strings.ReplaceAll(err.Error(), canaryPassword, "<password>"))
		}
		if want := "  " + fmt.Sprintf("%-40s", httpsShown) + " ✗ resolving " + httpsShown + "@: refusing https://***@github.com/org/repo.git"; !strings.Contains(errOut.String(), want) {
			t.Errorf("%s: error output %q, want the line %q", run.name, strings.ReplaceAll(errOut.String(), canaryPassword, "<password>"), want)
		}
		if want := "  " + fmt.Sprintf("%-40s", sshShown) + run.sshLine; !strings.Contains(out.String(), want) {
			t.Errorf("%s: output %q, want the line %q", run.name, strings.ReplaceAll(out.String(), canaryPassword, "<password>"), want)
		}
		all := out.String() + errOut.String()
		if strings.Contains(all, canaryPassword) || strings.Contains(all, canaryToken) {
			t.Errorf("%s: the output shows the password or the token", run.name)
		}
	}
	lk, err := lock.ReadLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(lk.Envs) != 1 || lk.Envs[0].Ref != gitcache.RefBase(sshKey) || lk.Envs[0].Label != "lbl" {
		t.Errorf("b.lock has %d env entries, want one for the ssh:// key", len(lk.Envs))
	}
}

// installDir makes the project dir of a b install test: PATH_BASE and
// PATH_BIN, a b.yaml with no entries and an empty b.lock. It returns the
// path of b.yaml.
func installDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PATH_BASE", dir)
	t.Setenv("PATH_BIN", dir)
	configPath := filepath.Join(dir, "b.yaml")
	mustWrite(t, configPath, []byte("binaries: {}\n"))
	if err := lock.WriteLock(dir, &lock.Lock{Version: 1}, "test"); err != nil {
		t.Fatal(err)
	}
	return configPath
}

// checkInstallFiles fails the test when b.yaml or b.lock in the dir of
// configPath cannot be read or holds the password or the token.
func checkInstallFiles(t *testing.T, name, configPath string) {
	t.Helper()
	for _, file := range []string{configPath, filepath.Join(filepath.Dir(configPath), "b.lock")} {
		b, err := os.ReadFile(file)
		if err != nil || strings.Contains(string(b), canaryPassword) || strings.Contains(string(b), canaryToken) {
			t.Errorf("%s: %s cannot be read or holds the password or the token (read error: %v)", name, filepath.Base(file), err != nil)
		}
	}
}

// b install refuses an env key of b.yaml with a password or more than one @
// in front of its host, or with an @ after another "://", before it splits
// the key (RefLabel, RefBase), as b update does. With no argument, each such
// key gets one ✗ line with the fixed text and "<refused env key #N>" in
// place of the key: N is the place of the key in the sorted list of the env
// keys. With the key as the argument, b install stops with the fixed text
// alone. The password and the token are in no output, in no format verb of
// the error and not in b.yaml or b.lock. No token is chosen and no git
// command runs.
func TestInstall_RefusedKeyRefusedFirst(t *testing.T) {
	calls := gitSpy(t)
	t.Setenv("GITHUB_TOKEN", canaryToken)
	const refusal = "refusing a git ref with a password or more than one @ before its path: remove the user part"
	keys := []string{
		"user:p@" + canaryPassword + "@github.com/org/repo#lbl",
		"https://a?b://user:" + canaryPassword + "@host.example/x",
		"ssh://git@" + canaryPassword + "@github.com/org/repo",
	}
	// The sorted order of the keys: https://..., ssh://..., user:...
	placeholders := []string{"<refused env key #3>", "<refused env key #1>", "<refused env key #2>"}
	envs := func() state.EnvList {
		var list state.EnvList
		for _, key := range keys {
			list = append(list, &state.EnvEntry{Key: key, Files: map[string]envmatch.GlobConfig{"a/**": {}}})
		}
		return list
	}
	for _, knob := range []string{"", "1"} {
		t.Setenv(gitcache.SSHAsHTTPSEnv, knob)
		for i, key := range keys {
			if gitcache.ResolveGitURL(key, "").AuthToken != "" {
				t.Errorf("%s=%q: a token is chosen for key %d", gitcache.SSHAsHTTPSEnv, knob, i)
			}
		}

		// No argument: each key of b.yaml.
		name := fmt.Sprintf("%s=%q no argument", gitcache.SSHAsHTTPSEnv, knob)
		configPath := installDir(t)
		io, out, errOut := bufIO()
		o := &InstallOptions{SharedOptions: &SharedOptions{
			IO:               io,
			ConfigPath:       configPath,
			loadedConfigPath: configPath,
			Config:           &state.State{Envs: envs()},
		}}
		err := o.Complete(nil)
		if err == nil {
			err = o.Run()
		}
		if err != nil {
			t.Errorf("%s: error %q, want none", name, strings.ReplaceAll(err.Error(), canaryPassword, "<password>"))
		}
		checkRefusalFormats(t, name, err)
		for _, placeholder := range placeholders {
			line := "  " + fmt.Sprintf("%-40s", placeholder) + " ✗ " + refusal + "\n"
			if got := strings.Count(errOut.String(), line); got != 1 {
				t.Errorf("%s: %d refusal lines for %s, want 1; error output %q", name, got, placeholder,
					strings.ReplaceAll(errOut.String(), canaryPassword, "<password>"))
			}
		}
		if got := strings.Count(errOut.String(), " ✗ "); got != len(keys) {
			t.Errorf("%s: %d ✗ lines, want %d", name, got, len(keys))
		}
		if all := out.String() + errOut.String(); strings.Contains(all, canaryPassword) || strings.Contains(all, canaryToken) {
			t.Errorf("%s: the output shows the password or the token", name)
		}
		checkInstallFiles(t, name, configPath)

		// The key as the argument, also with --add.
		for i, key := range keys {
			for _, args := range [][]string{{key}, {"--add", key}} {
				name := fmt.Sprintf("%s=%q key %d %v", gitcache.SSHAsHTTPSEnv, knob, i, args[:len(args)-1])
				configPath := installDir(t)
				io, out, errOut := bufIO()
				cmd := NewInstallCmd(&SharedOptions{
					IO:               io,
					ConfigPath:       configPath,
					loadedConfigPath: configPath,
					Config:           &state.State{Envs: envs()},
				})
				cmd.SetOut(out)
				cmd.SetErr(errOut)
				cmd.SetArgs(args)
				err := cmd.Execute()
				if err == nil || err.Error() != refusal {
					t.Errorf("%s: error %q, want the fixed text alone", name,
						strings.ReplaceAll(fmt.Sprint(err), canaryPassword, "<password>"))
				}
				checkRefusal(t, name, err, refusal)
				if all := out.String() + errOut.String(); strings.Contains(all, canaryPassword) || strings.Contains(all, canaryToken) {
					t.Errorf("%s: the output shows the password or the token", name)
				}
				checkInstallFiles(t, name, configPath)
			}
		}
	}
	if c := calls(); c != "" {
		t.Errorf("git ran:\n%s", strings.ReplaceAll(c, canaryPassword, "<password>"))
	}
}

// b install refuses an argument with a password or more than one @ in front
// of its host before it splits the argument, with the fixed text alone:
//   - "user:password@host/org/repo:/glob": the split at the last @ gave the
//     ref "user:password";
//   - "user.name:password@host/org/repo:/glob": the colon scan stops at the
//     first ":" after a ".", so it gave the ref "user.name" and put the
//     password in the glob;
//   - an argument with no glob, which b would read as a binary.
//
// This holds with and without a version, a dest and --add. The password and
// the token are in no output, in no format verb of the error and not in
// b.yaml or b.lock, and no git command runs.
func TestInstall_RefusedArgumentRefusedFirst(t *testing.T) {
	calls := gitSpy(t)
	t.Setenv("GITHUB_TOKEN", canaryToken)
	const refusal = "refusing a git ref with a password or more than one @ before its path: remove the user part"
	var refs []string
	for _, user := range []string{"user:" + canaryPassword + "@", "user.name:" + canaryPassword + "@", "user:p@" + canaryPassword + "@"} {
		for _, version := range []string{"", "@v1"} {
			refs = append(refs, user+"github.com/org/repo"+version)
		}
	}
	refs = append(refs,
		"git@"+canaryPassword+"@github.com:org/repo",
		"https://a?b://user:"+canaryPassword+"@host.example/x")
	for _, knob := range []string{"", "1"} {
		t.Setenv(gitcache.SSHAsHTTPSEnv, knob)
		for i, ref := range refs {
			for _, args := range [][]string{
				{ref + ":/manifests/**"},
				{ref + ":manifests/**", "out"},
				{"--add", ref + ":/manifests/**", "out"},
				{ref},
			} {
				name := fmt.Sprintf("%s=%q ref %d with %d argument(s)", gitcache.SSHAsHTTPSEnv, knob, i, len(args))
				configPath := installDir(t)
				io, out, errOut := bufIO()
				cmd := NewInstallCmd(&SharedOptions{IO: io, ConfigPath: configPath, loadedConfigPath: configPath})
				cmd.SetOut(out)
				cmd.SetErr(errOut)
				cmd.SetArgs(args)
				err := cmd.Execute()
				if err == nil || err.Error() != refusal {
					t.Errorf("%s: error %q, want the fixed text alone", name,
						strings.ReplaceAll(fmt.Sprint(err), canaryPassword, "<password>"))
				}
				checkRefusal(t, name, err, refusal)
				if all := out.String() + errOut.String(); strings.Contains(all, canaryPassword) || strings.Contains(all, canaryToken) {
					t.Errorf("%s: the output shows the password or the token", name)
				}
				checkInstallFiles(t, name, configPath)
			}
		}
	}
	if c := calls(); c != "" {
		t.Errorf("git ran:\n%s", strings.ReplaceAll(c, canaryPassword, "<password>"))
	}
}

// The ref check of b install does not change an argument that b fetched
// before: each form still parses to the same ref, version, label, glob and
// dest, with no error.
func TestParseSCPArg_LegalFormsPass(t *testing.T) {
	for _, tt := range []struct {
		arg                       string
		ref, label, version, glob string
	}{
		{"github.com/org/repo:/glob", "github.com/org/repo", "", "", "glob"},
		{"github.com/org/repo@v1:/glob", "github.com/org/repo", "", "v1", "glob"},
		{"github.com/org/repo#lbl@v1:/glob", "github.com/org/repo", "lbl", "v1", "glob"},
		{"https://github.com/org/repo@v1:/glob", "https://github.com/org/repo", "", "v1", "glob"},
		{"github.com/org/repo@v1:/a/@scope/**", "github.com/org/repo", "", "v1", "a/@scope/**"},
		{"./local/repo:/glob", "./local/repo", "", "", "glob"},
	} {
		ei, consumed, ok, err := parseSCPArg(tt.arg, []string{"out"})
		if err != nil || !ok || consumed != 1 {
			t.Errorf("parseSCPArg(%q): ok %v, consumed %d, error %v; want ok, 1, none", tt.arg, ok, consumed, err)
			continue
		}
		if ei.ref != tt.ref || ei.label != tt.label || ei.version != tt.version || ei.glob != tt.glob || ei.dest != "out" {
			t.Errorf("parseSCPArg(%q) = %+v, want ref %q, label %q, version %q, glob %q, dest out",
				tt.arg, ei, tt.ref, tt.label, tt.version, tt.glob)
		}
	}
}

// b install reads the @ of a user part in an argument as a part of the ref,
// not as the start of the version, with the rule of RefVersion. The ref keeps
// its user part and its host; a version comes after a later @.
func TestParseSCPArg_UserPartIsNotAVersion(t *testing.T) {
	for _, tt := range []struct {
		arg                       string
		ref, label, version, glob string
	}{
		{"https://user@github.com/org/repo:/glob", "https://user@github.com/org/repo", "", "", "glob"},
		{"https://user@github.com/org/repo@v1:/glob", "https://user@github.com/org/repo", "", "v1", "glob"},
		{"https://user@github.com/org/repo#lbl:/glob", "https://user@github.com/org/repo", "lbl", "", "glob"},
		{"https://user@github.com/org/repo#lbl@v1:/glob", "https://user@github.com/org/repo", "lbl", "v1", "glob"},
		{"user@github.com/org/repo:/glob", "user@github.com/org/repo", "", "", "glob"},
		{"user@github.com/org/repo@v1:/glob", "user@github.com/org/repo", "", "v1", "glob"},
		{"ssh://git@github.com/org/repo:/glob", "ssh://git@github.com/org/repo", "", "", "glob"},
		{"ssh://git@github.com/org/repo@v1:/glob", "ssh://git@github.com/org/repo", "", "v1", "glob"},
	} {
		ei, consumed, ok, err := parseSCPArg(tt.arg, nil)
		if err != nil || !ok || consumed != 0 {
			t.Errorf("parseSCPArg(%q): ok %v, consumed %d, error %v; want ok, 0, none", tt.arg, ok, consumed, err)
			continue
		}
		if ei.ref != tt.ref || ei.label != tt.label || ei.version != tt.version || ei.glob != tt.glob || ei.dest != "" {
			t.Errorf("parseSCPArg(%q) = %+v, want ref %q, label %q, version %q, glob %q, no dest",
				tt.arg, ei, tt.ref, tt.label, tt.version, tt.glob)
		}
	}
}

// b install keeps the host of an argument with a token as the user name and
// no password ("https://token@host/org/repo:/glob", also with no scheme and
// with a version). The sync refuses the https URL with a user part before
// git runs, so no git command runs and the token does not go to a host. The
// error names the right host, with "***" in place of the user part. The
// token is in no output, in no format verb of the error and not in b.yaml
// or b.lock, with and without a dest, --add and B_GIT_SSH_AS_HTTPS=1.
func TestInstall_UserNameArgumentKeepsItsHost(t *testing.T) {
	calls := gitSpy(t)
	t.Setenv("GITHUB_TOKEN", canaryToken)
	for _, knob := range []string{"", "1"} {
		t.Setenv(gitcache.SSHAsHTTPSEnv, knob)
		for _, host := range []string{"github.com", "host.example"} {
			for _, user := range []string{"https://" + canaryToken + "@", canaryToken + "@"} {
				for _, version := range []string{"", "v1"} {
					ref := user + host + "/org/repo"
					shown := gitcache.RedactUserinfo(ref)
					if shown == ref || !strings.HasSuffix(shown, "***@"+host+"/org/repo") {
						t.Fatalf("RedactUserinfo of a ref with a user part gives %q", shown)
					}
					arg := ref + "@" + version + ":/manifests/**"
					if version == "" {
						arg = ref + ":/manifests/**"
					}
					name := fmt.Sprintf("%s=%q %s %q", gitcache.SSHAsHTTPSEnv, knob, shown, version)
					ei, _, ok, err := parseSCPArg(arg, nil)
					if err != nil || !ok || ei.ref != ref || ei.version != version || ei.label != "" || ei.glob != "manifests/**" {
						t.Errorf("%s: parse: ok %v, error %v, ref %q, version %q, label %q, glob %q; want ok, the ref with its host, version %q",
							name, ok, err != nil, strings.ReplaceAll(ei.ref, canaryToken, "<token>"),
							strings.ReplaceAll(ei.version, canaryToken, "<token>"), ei.label, ei.glob, version)
					}
					want := "syncing " + shown + ": resolving " + shown + "@" + version + ": refusing https://***@" + host + "/org/repo.git: "
					for _, args := range [][]string{{arg}, {arg, "out"}, {"--add", arg, "out"}} {
						name := fmt.Sprintf("%s with %d argument(s)", name, len(args))
						configPath := installDir(t)
						io, out, errOut := bufIO()
						cmd := NewInstallCmd(&SharedOptions{IO: io, ConfigPath: configPath, loadedConfigPath: configPath})
						cmd.SetOut(out)
						cmd.SetErr(errOut)
						cmd.SetArgs(args)
						err := cmd.Execute()
						if err == nil || !strings.HasPrefix(err.Error(), want) {
							t.Errorf("%s: error %q, want it to start with %q", name,
								strings.ReplaceAll(fmt.Sprint(err), canaryToken, "<token>"), want)
						}
						checkRefusalFormats(t, name, err)
						if all := out.String() + errOut.String(); strings.Contains(all, canaryToken) {
							t.Errorf("%s: the output shows the token", name)
						}
						checkInstallFiles(t, name, configPath)
					}
				}
			}
		}
	}
	if c := calls(); c != "" {
		t.Errorf("git ran:\n%s", strings.ReplaceAll(c, canaryToken, "<token>"))
	}
}
