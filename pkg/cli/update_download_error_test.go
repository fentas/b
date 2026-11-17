package cli

import (
	"bytes"
	"errors"
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
