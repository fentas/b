package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fentas/b/pkg/envmatch"
	"github.com/fentas/b/pkg/gitcache"
	"github.com/fentas/b/pkg/state"
	"github.com/fentas/goodies/streams"
)

// installWithEnvs runs `b install <args>` in the fixture f, with envs as the
// envs of its b.yaml. It returns the output, the error output and the error
// of Run.
func installWithEnvs(t *testing.T, f *pinsFixture, envs state.EnvList, args ...string) (string, string, error) {
	t.Helper()
	// The binaries install in parallel, and each goroutine can write to
	// IO.ErrOut. A bytes.Buffer does not take concurrent writes.
	var out, errOut bytes.Buffer
	shared := NewSharedOptions(&streams.IO{
		In:     strings.NewReader(""),
		Out:    &syncWriter{w: &out},
		ErrOut: &syncWriter{w: &errOut},
	}, f.presets())
	shared.ConfigPath = f.config
	if err := shared.LoadConfig(); err != nil {
		t.Fatal(err)
	}
	shared.Config.Envs = envs
	o := &InstallOptions{SharedOptions: shared}
	if err := o.Complete(args); err != nil {
		t.Fatalf("Complete: %s", hideCanaries(err.Error()))
	}
	err := o.Run()
	return out.String(), errOut.String(), err
}

// withTool gives the b.yaml of f the entry tool at v1.0.0.
func withTool(t *testing.T, f *pinsFixture) {
	t.Helper()
	mustWrite(t, f.config, []byte("binaries:\n  tool:\n    version: v1.0.0\n"))
}

// checkTool fails the test when .bin/tool of f is not the v1.0.0 file, or
// when its b.lock entry does not describe that file.
func checkTool(t *testing.T, f *pinsFixture) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.binDir, "tool"))
	if err != nil || string(data) != "tool v1.0.0\n" {
		t.Errorf(".bin/tool is %q (read error: %v), want the v1.0.0 file", data, err)
		return
	}
	if e, describes := f.entry(t); e.Version != "v1.0.0" || !describes {
		t.Errorf("b.lock says %q and describes .bin/tool = %v, want v1.0.0 and true", e.Version, describes)
	}
}

// b install exits non-zero when an env of b.yaml fails (here the fake git
// fails) or when the ref check refuses its key. Each such env gets one ✗
// line, and the other envs and the binaries still sync. The error is the
// failure summary of b update: the number of these envs, and each one as
// its ✗ line shows it, with "***" in place of a user part, or
// "<refused env key #N>" for a refused key. This holds with no argument and
// with the env key as the argument, with and without a binary in the same
// run. A failed download and a failed env give one error that names both.
// The password and the token are in no output, in no format verb of the
// error and not in b.yaml or b.lock.
func TestInstall_FailedEnvExitsNonZero(t *testing.T) {
	calls := gitSpy(t)
	t.Setenv("GITHUB_TOKEN", canaryToken)
	t.Setenv(gitcache.SSHAsHTTPSEnv, "")
	const (
		refusal    = "refusing a git ref with a password or more than one @ before its path: remove the user part"
		plainKey   = "github.com/org/infra"
		userKey    = "ssh://user:" + canaryPassword + "@host.example/org/repo#lbl"
		userShown  = "ssh://***@host.example/org/repo#lbl"
		refusedKey = "user:p@" + canaryPassword + "@github.com/org/repo"
	)
	envs := func(keys ...string) state.EnvList {
		var list state.EnvList
		for _, key := range keys {
			list = append(list, &state.EnvEntry{Key: key, Files: map[string]envmatch.GlobConfig{"a/**": {}}})
		}
		return list
	}
	for _, tt := range []struct {
		name    string
		keys    []string // the env keys of b.yaml
		args    []string // the env arguments; nil: no argument
		failed  []string // the envs of the summary, as their ✗ lines show them
		refused []string // the refused envs of failed
	}{
		{"a failed env", []string{userKey}, nil, []string{userShown}, nil},
		{"a refused key", []string{refusedKey}, nil, []string{"<refused env key #1>"}, []string{"<refused env key #1>"}},
		// The sorted order of the keys: github.com/..., ssh://..., user:...
		{"two failed envs and a refused key", []string{plainKey, refusedKey, userKey}, nil,
			[]string{plainKey, "<refused env key #3>", userShown}, []string{"<refused env key #3>"}},
		{"the env key as the argument", []string{plainKey, userKey}, []string{plainKey}, []string{plainKey}, nil},
	} {
		for _, withBinary := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s, binary %v", tt.name, withBinary), func(t *testing.T) {
				f := newPinsFixture(t)
				args := tt.args
				if withBinary {
					withTool(t, f)
					if args != nil {
						args = append([]string{"tool"}, args...)
					}
				}
				before := calls()
				out, errOut, err := installWithEnvs(t, f, envs(tt.keys...), args...)
				// The fake git runs for each env that is not refused.
				if ran, want := calls() != before, len(tt.failed) > len(tt.refused); ran != want {
					t.Errorf("git ran = %v, want %v", ran, want)
				}
				want := fmt.Sprintf("%d env(s) failed: %s", len(tt.failed), strings.Join(tt.failed, ", "))
				if err == nil || err.Error() != want {
					t.Errorf("error %q, want %q", hideCanaries(fmt.Sprint(err)), want)
				}
				checkRefusalFormats(t, "Run", err)
				for _, shown := range tt.failed {
					if got := strings.Count(errOut, "  "+fmt.Sprintf("%-40s", shown)+" ✗ "); got != 1 {
						t.Errorf("%d ✗ lines for %s, want 1; error output %q", got, shown, hideCanaries(errOut))
					}
				}
				for _, shown := range tt.refused {
					if line := "  " + fmt.Sprintf("%-40s", shown) + " ✗ " + refusal + "\n"; !strings.Contains(errOut, line) {
						t.Errorf("error output %q, want the line %q", hideCanaries(errOut), line)
					}
				}
				if got := strings.Count(errOut, " ✗ "); got != len(tt.failed) {
					t.Errorf("%d ✗ lines, want %d", got, len(tt.failed))
				}
				if all := out + errOut; strings.Contains(all, canaryPassword) || strings.Contains(all, canaryToken) {
					t.Error("the output shows the password or the token")
				}
				checkInstallFiles(t, "Run", f.config)
				if withBinary {
					checkTool(t, f)
				}
			})
		}
	}

	t.Run("a failed download and a failed env", func(t *testing.T) {
		f := newPinsFixture(t)
		withTool(t, f)
		f.missing["v1.0.0"] = true
		out, errOut, err := installWithEnvs(t, f, envs(userKey))
		if err == nil || !strings.HasPrefix(err.Error(), "download failed:\n  tool: ") ||
			!strings.HasSuffix(err.Error(), "\n1 env(s) failed: "+userShown) {
			t.Errorf("error %q, want the download error, then the env summary", hideCanaries(fmt.Sprint(err)))
		}
		checkRefusalFormats(t, "Run", err)
		if all := out + errOut; strings.Contains(all, canaryPassword) || strings.Contains(all, canaryToken) {
			t.Error("the output shows the password or the token")
		}
	})
}

// b install exits 0 when each env of b.yaml syncs, at the first run and when
// the envs are up to date. This holds with no argument and with the env key
// as the argument, with and without a binary in the same run. The envs come
// from local repos.
func TestInstall_EnvsPassExitZero(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home) // gitcache.DefaultCacheRoot is under HOME
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	bareA, bareB := setupLocalRepo(t), setupLocalRepo(t)
	envs := func() state.EnvList {
		return state.EnvList{
			{Key: bareA, Files: map[string]envmatch.GlobConfig{"manifests/*.yaml": {Dest: "out-a"}}},
			{Key: bareB, Files: map[string]envmatch.GlobConfig{"manifests/*.yaml": {Dest: "out-b"}}},
		}
	}
	for _, tt := range []struct {
		name   string
		args   []string // the env arguments; nil: no argument
		synced []string // the env keys that sync
	}{
		{"no argument", nil, []string{bareA, bareB}},
		{"the env key as the argument", []string{bareA}, []string{bareA}},
	} {
		for _, withBinary := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s, binary %v", tt.name, withBinary), func(t *testing.T) {
				f := newPinsFixture(t)
				args := tt.args
				if withBinary {
					withTool(t, f)
					if args != nil {
						args = append([]string{"tool"}, args...)
					}
				}
				for _, run := range []struct{ name, line string }{
					{"first run", " (new) → "},
					{"second run", " (up to date)"},
				} {
					out, errOut, err := installWithEnvs(t, f, envs(), args...)
					if err != nil {
						t.Errorf("%s: error %q, want none", run.name, err)
					}
					if strings.Contains(errOut, " ✗ ") {
						t.Errorf("%s: error output %q, want no ✗ line", run.name, errOut)
					}
					for _, key := range tt.synced {
						if want := "  " + fmt.Sprintf("%-40s", key) + run.line; !strings.Contains(out, want) {
							t.Errorf("%s: output %q, want the line %q", run.name, out, want)
						}
					}
				}
				if withBinary {
					checkTool(t, f)
				}
			})
		}
	}
}
