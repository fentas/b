package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/fentas/b/pkg/binary"
	"github.com/fentas/b/pkg/lock"
	"github.com/fentas/goodies/streams"
)

// The install decision and the lock write without the progress writer, so
// they also run in the race-enabled test job (the end-to-end install tests
// in install_pins_norace_test.go are !race).

// TestPlanInstall: a present file is kept when no version is asked for, or
// when b.lock says that file (by its sha256) is the version asked for;
// otherwise the binary is downloaded.
func TestPlanInstall(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "tool")
	mustWriteExec(t, file, []byte("tool v1.0.0\n"))
	hash, err := lock.SHA256File(file)
	if err != nil {
		t.Fatal(err)
	}
	locked := &lock.BinEntry{Name: "tool", Version: "v1.0.0", SHA256: hash}
	stale := &lock.BinEntry{Name: "tool", Version: "v1.0.0", SHA256: "another file"}

	for _, tc := range []struct {
		name     string
		file     string
		version  string
		prev     *lock.BinEntry
		force    bool
		download bool
		kept     string
	}{
		{"missing file", filepath.Join(dir, "missing"), "", nil, false, true, ""},
		{"--force", file, "", locked, true, true, ""},
		{"no version asked, locked", file, "", locked, false, false, "v1.0.0"},
		{"no version asked, not locked", file, "", nil, false, false, ""},
		{"no version asked, b.lock hashes another file", file, "", stale, false, false, ""},
		{"latest asked, locked", file, "latest", locked, false, false, "v1.0.0"},
		{"the locked version asked", file, "v1.0.0", locked, false, false, "v1.0.0"},
		{"another version asked", file, "v3.0.0", locked, false, true, ""},
		{"a version asked, not locked", file, "v1.0.0", nil, false, true, ""},
		{"a version asked, b.lock hashes another file", file, "v1.0.0", stale, false, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &binary.Binary{Name: "tool", Version: tc.version, File: tc.file}
			got := planInstall(b, tc.prev, tc.force)
			if got.download != tc.download || got.keptVersion != tc.kept {
				t.Errorf("planInstall = {download: %v, kept: %q}, want {download: %v, kept: %q}", got.download, got.keptVersion, tc.download, tc.kept)
			}
		})
	}
}

// TestUpdateLock_FollowsThePlan: a kept file is recorded with its kept
// version, a downloaded one with its version, and a failed download leaves
// the previous entry.
func TestUpdateLock_FollowsThePlan(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "b.yaml")
	mustWrite(t, config, []byte("binaries: {}\n"))
	files := map[string]string{}
	for _, name := range []string{"kept", "downloaded", "failed"} {
		files[name] = filepath.Join(dir, name)
		mustWriteExec(t, files[name], []byte(name+"\n"))
	}
	prev := &lock.Lock{Binaries: []lock.BinEntry{{Name: "failed", Version: "v1.0.0", SHA256: "the old file"}}}
	if err := lock.WriteLock(dir, prev, "test"); err != nil {
		t.Fatal(err)
	}

	kept := &binary.Binary{Name: "kept", Version: "", File: files["kept"]}
	downloaded := &binary.Binary{Name: "downloaded", Version: "v2.0.0", File: files["downloaded"]}
	failed := &binary.Binary{Name: "failed", Version: "v3.0.0", File: files["failed"]}
	o := &InstallOptions{
		SharedOptions: &SharedOptions{
			IO:               &streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}},
			ConfigPath:       config,
			loadedConfigPath: config,
		},
		plans: map[*binary.Binary]installPlan{
			kept:       {keptVersion: "v1.5.0"},
			downloaded: {download: true},
			failed:     {download: true},
		},
		failed: map[*binary.Binary]bool{failed: true},
	}
	if err := o.updateLock([]*binary.Binary{kept, downloaded, failed}); err != nil {
		t.Fatal(err)
	}
	lk, err := lock.ReadLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"kept": "v1.5.0", "downloaded": "v2.0.0", "failed": "v1.0.0"} {
		e := lk.FindBinary(name)
		if e == nil || e.Version != want {
			t.Errorf("%s: entry %+v, want version %s", name, e, want)
		}
	}
	if e := lk.FindBinary("failed"); e != nil && e.SHA256 != "the old file" {
		t.Errorf("failed: the entry was rewritten with sha256 %s", e.SHA256)
	}
	if _, err := os.Stat(filepath.Join(dir, "b.lock")); err != nil {
		t.Fatal(err)
	}
}

// TestLockName: an alias entry of a preset has its own b.lock row under the
// alias. The preset's own entry and a provider ref keep the row under the
// binary's name, so b.lock files written before still match them.
func TestLockName(t *testing.T) {
	for _, tc := range []struct {
		name string
		b    *binary.Binary
		want string
	}{
		{"a preset", &binary.Binary{Name: "tool"}, "tool"},
		{"an alias entry of a preset", &binary.Binary{Name: "tool", Alias: "t2"}, "t2"},
		{"a provider ref", &binary.Binary{Name: "tool", AutoDetect: true, ProviderRef: "github.com/org/tool"}, "tool"},
		{"a provider ref with an alias", &binary.Binary{Name: "tool", Alias: "t", AutoDetect: true, ProviderRef: "github.com/org/tool"}, "tool"},
	} {
		if got := lockName(tc.b); got != tc.want {
			t.Errorf("%s: lockName = %q, want %q", tc.name, got, tc.want)
		}
	}
}
