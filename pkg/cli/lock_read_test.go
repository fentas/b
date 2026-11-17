package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fentas/b/pkg/env"
	"github.com/fentas/b/pkg/lock"
)

// lockReadConfig is a b.yaml with the preset tool and one env, so that the
// commands that show envs read b.lock too.
const lockReadConfig = "binaries:\n  tool:\n    version: v1.0.0\nenvs:\n  github.com/org/infra:\n    files:\n      \"manifests/**\": {dest: k8s}\n"

// noEnvSync makes the test fail when a command syncs an env or asks a
// remote for a commit: the tests of b.lock reads stay offline.
func noEnvSync(t *testing.T) {
	t.Helper()
	saveHooks(t)
	syncEnvFunc = func(env.EnvConfig, string, string, *lock.EnvEntry) (*env.SyncResult, error) {
		t.Error("a command synced an env")
		return &env.SyncResult{Skipped: true}, nil
	}
	resolveRefFunc = func(string, string) (string, error) {
		t.Error("a command asked a remote for a commit")
		return "", os.ErrNotExist
	}
}

// TestUnreadableLock_EveryCommandFails: a b.lock that is there but cannot be
// read (it is not JSON, or it is not a file) stops every command that reads
// it, with the hint of b install, before any download and before any write.
// Before, b list and b version panicked on it, and b update took it as
// empty.
func TestUnreadableLock_EveryCommandFails(t *testing.T) {
	commands := [][]string{
		{"install"},
		{"install", "tool"},
		{"install", "--force", "tool"},
		{"update"},
		{"update", "tool"},
		{"update", "--force", "tool"},
		{"update", "--envs-only"},
		{"list"},
		{"version", "--local"},
		{"verify"},
		{"env", "status"},
		{"env", "resolve"},
		{"env", "remove", "github.com/org/infra"},
	}
	for name, spoil := range map[string]func(t *testing.T, file string){
		"not JSON": func(t *testing.T, file string) {
			mustWrite(t, file, []byte("{not json\n"))
		},
		"a directory": func(t *testing.T, file string) {
			mustMkdir(t, file)
		},
	} {
		for _, args := range commands {
			t.Run(name+"/"+strings.Join(args, " "), func(t *testing.T) {
				noEnvSync(t)
				f := newPinsFixture(t)
				mustWrite(t, f.config, []byte(lockReadConfig))
				f.present(t, "v1.0.0", false)
				lockFile := filepath.Join(f.binDir, "b.lock")
				spoil(t, lockFile)

				_, err := f.runB(t, f.presets(), args...)
				if err == nil {
					t.Fatal("no error")
				}
				for _, want := range []string{"cannot read b.lock", "Hint: repair " + lockFile + ", or remove it"} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error %q, want %q in it", err, want)
					}
				}
				if n := f.hits.Load(); n != 0 {
					t.Errorf("%d downloads, want 0", n)
				}
				if got := f.file(t); got != "tool v1.0.0\n" {
					t.Errorf(".bin/tool is %q, want it as it was", got)
				}
				if got, err := os.ReadFile(f.config); err != nil || string(got) != lockReadConfig {
					t.Errorf("b.yaml is %q (%v), want it as it was", got, err)
				}
				if name == "not JSON" {
					if got, err := os.ReadFile(lockFile); err != nil || string(got) != "{not json\n" {
						t.Errorf("b.lock is %q (%v), want it as it was", got, err)
					}
				}
			})
		}
	}
}

// TestMissingLock_IsNotAnError: a b.lock that is not there is the normal
// state before the first install. No command fails on it.
func TestMissingLock_IsNotAnError(t *testing.T) {
	for _, args := range [][]string{
		{"install", "tool"},
		{"update", "tool"},
		{"list"},
		{"version", "--local"},
		{"verify"},
		{"env", "status"},
		{"env", "resolve"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			noEnvSync(t)
			f := newPinsFixture(t)
			mustWrite(t, f.config, []byte(lockReadConfig))
			f.present(t, "v1.0.0", false)
			if _, err := f.runB(t, f.presets(), args...); err != nil {
				t.Errorf("b %s: %v", strings.Join(args, " "), err)
			}
		})
	}
}
