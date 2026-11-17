package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fentas/goodies/streams"

	"github.com/fentas/b/pkg/binary"
	"github.com/fentas/b/pkg/env"
	"github.com/fentas/b/pkg/lock"
	"github.com/fentas/b/pkg/state"
)

// jqFailed is the error of updateBinaries when the download of jq failed.
func jqFailed() error {
	jq := &binary.Binary{Name: "jq"}
	return failedDownloads([]*binary.Binary{jq}, map[*binary.Binary]error{jq: errors.New("HTTP 404")}, "")
}

// envUpdate is `b update` of a b.yaml with the binary jq and one env. The
// binary update gives binErr (updateBinariesF), and the env sync gives
// syncErr. It counts the env syncs.
func envUpdate(t *testing.T, binErr, syncErr error) (*UpdateOptions, *int) {
	t.Helper()
	saveHooks(t)
	syncs := 0
	syncEnvFunc = func(env.EnvConfig, string, string, *lock.EnvEntry) (*env.SyncResult, error) {
		syncs++
		if syncErr != nil {
			return nil, syncErr
		}
		return &env.SyncResult{Skipped: true, Message: "ok"}, nil
	}
	dir := t.TempDir()
	t.Setenv("PATH_BIN", dir)
	t.Setenv("PATH_BASE", dir)
	config := filepath.Join(dir, "b.yaml")
	mustWrite(t, config, []byte("binaries: {}\n"))
	if err := lock.WriteLock(dir, &lock.Lock{Version: 1}, "test"); err != nil {
		t.Fatal(err)
	}
	shared := NewSharedOptions(&streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}}, []*binary.Binary{{Name: "jq", Version: "1.7"}})
	shared.ConfigPath = config
	shared.loadedConfigPath = config
	shared.Config = &state.State{
		Binaries: state.BinaryList{&binary.LocalBinary{Name: "jq"}},
		Envs:     state.EnvList{{Key: "github.com/org/repo"}},
	}
	o := &UpdateOptions{SharedOptions: shared, updateBinariesF: func([]*binary.Binary) error { return binErr }}
	return o, &syncs
}

// specified makes o the update of `b update jq github.com/org/repo`.
func specified(o *UpdateOptions) *UpdateOptions {
	o.specifiedBinaries = []*binary.Binary{{Name: "jq", Version: "1.7"}}
	o.specifiedEnvRefs = []string{"github.com/org/repo"}
	return o
}

// A failed download does not stop the env update: b update syncs the env,
// then returns the error of the download.
func TestUpdate_FailedDownloadStillUpdatesEnvs(t *testing.T) {
	for name, run := range map[string]func(*UpdateOptions) error{
		"all":       (*UpdateOptions).runAll,
		"specified": func(o *UpdateOptions) error { return specified(o).runSpecified() },
	} {
		t.Run(name, func(t *testing.T) {
			o, syncs := envUpdate(t, jqFailed(), nil)
			err := run(o)
			if err == nil || err.Error() != jqFailed().Error() {
				t.Errorf("error %v, want the download error %q", err, jqFailed())
			}
			if *syncs != 1 {
				t.Errorf("%d env syncs, want 1", *syncs)
			}
		})
	}
}

// When a download and an env fail, b update returns both errors.
func TestUpdate_FailedDownloadAndEnvReturnBoth(t *testing.T) {
	for name, run := range map[string]func(*UpdateOptions) error{
		"all":       (*UpdateOptions).runAll,
		"specified": func(o *UpdateOptions) error { return specified(o).runSpecified() },
	} {
		t.Run(name, func(t *testing.T) {
			o, _ := envUpdate(t, jqFailed(), errors.New("git clone failed"))
			err := run(o)
			if err == nil || !strings.Contains(err.Error(), jqFailed().Error()) || !strings.Contains(err.Error(), "1 env(s) failed") {
				t.Errorf("error %v, want the download error and the env error", err)
			}
		})
	}
}

// Another error of the binary update stops b update at once, as before: no
// env update.
func TestUpdate_OtherBinaryErrorStops(t *testing.T) {
	for name, run := range map[string]func(*UpdateOptions) error{
		"all":       (*UpdateOptions).runAll,
		"specified": func(o *UpdateOptions) error { return specified(o).runSpecified() },
	} {
		t.Run(name, func(t *testing.T) {
			o, syncs := envUpdate(t, errors.New("cannot read b.lock"), nil)
			if err := run(o); err == nil || err.Error() != "cannot read b.lock" {
				t.Errorf("error %v, want the error of the binary update", err)
			}
			if *syncs != 0 {
				t.Errorf("%d env syncs, want none", *syncs)
			}
		})
	}
}

// When b.lock cannot be read after the downloads (the digest refresh of b
// update), the error names the failed download and the b.lock error. The
// command stops on it: the prune does not run, so the b.lock error comes
// once. Before, the b.lock error replaced the error of the download.
func TestUpdate_LockRefreshErrorKeepsTheDownloadError(t *testing.T) {
	f := newPinsFixture(t)
	mustWrite(t, f.config, []byte("binaries:\n  t2:\n    alias: tool\n    version: v3.0.0\n"))
	f.writeLock(t, lock.BinEntry{Name: "t2", Version: "v3.0.0", SHA256: "the sha256 of .bin/t2", Preset: true})
	f.missing["v3.0.0"] = true
	lockPath := filepath.Join(f.binDir, "b.lock")
	// The download breaks b.lock: the reads before it work, the digest
	// refresh after it fails.
	var breakErr error
	f.mu.Lock()
	f.onRequest = func() {
		err := os.WriteFile(lockPath, []byte("{not a lock"), 0644)
		f.mu.Lock()
		breakErr = err
		f.mu.Unlock()
	}
	f.mu.Unlock()
	err := f.cmdErr(t, "update", func(*UpdateOptions) {})
	f.mu.Lock()
	if breakErr != nil {
		t.Fatal(breakErr)
	}
	f.mu.Unlock()
	if f.hits.Load() == 0 {
		t.Fatal("no download, want one that fails")
	}
	if err == nil {
		t.Fatal("b update: no error, want the download error and the b.lock error")
	}
	msg := err.Error()
	if want := "download failed:\n  t2 (tool): tool v3.0.0 not found (latest: v2.0.0)\ncannot read b.lock: "; !strings.HasPrefix(msg, want) {
		t.Errorf("b update: error %q, want the error of the download of t2, then the b.lock error", msg)
	}
	if n := strings.Count(msg, "cannot read b.lock: "); n != 1 || !isLockReadError(err) {
		t.Errorf("b update: the b.lock error comes %d times in %q, want once", n, msg)
	}
}
