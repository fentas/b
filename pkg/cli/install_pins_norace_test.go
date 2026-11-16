//go:build !race

package cli

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/fentas/b/pkg/binary"
	"github.com/fentas/b/pkg/lock"
)

// pinsFixture is a project with a .bin directory, a b.yaml, and a release
// server for the preset "tool": GET /<version> answers "tool <version>\n",
// or 404 when the version is in missing. The latest version is v2.0.0.
type pinsFixture struct {
	dir, binDir, config string
	srv                 *httptest.Server
	hits                atomic.Int32
	missing             map[string]bool
}

func newPinsFixture(t *testing.T) *pinsFixture {
	t.Helper()
	f := &pinsFixture{dir: t.TempDir(), missing: map[string]bool{}}
	f.binDir = filepath.Join(f.dir, ".bin")
	mustMkdir(t, f.binDir)
	f.config = filepath.Join(f.binDir, "b.yaml")
	mustWrite(t, f.config, []byte("binaries: {}\n"))
	t.Setenv("PATH_BIN", f.binDir)
	t.Setenv("PATH_BASE", f.dir)
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits.Add(1)
		version := strings.TrimPrefix(r.URL.Path, "/")
		if f.missing[version] {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, "tool "+version+"\n")
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *pinsFixture) presets() []*binary.Binary {
	return []*binary.Binary{{
		Name:     "tool",
		Context:  context.Background(),
		URLF:     func(b *binary.Binary) (string, error) { return f.srv.URL + "/" + b.Version, nil },
		VersionF: func(*binary.Binary) (string, error) { return "v2.0.0", nil },
	}}
}

// present writes .bin/tool as the given version, and records it in b.lock
// when locked is true.
func (f *pinsFixture) present(t *testing.T, version string, locked bool) {
	t.Helper()
	file := filepath.Join(f.binDir, "tool")
	mustWriteExec(t, file, []byte("tool "+version+"\n"))
	if !locked {
		return
	}
	hash, err := lock.SHA256File(file)
	if err != nil {
		t.Fatal(err)
	}
	lk := &lock.Lock{Binaries: []lock.BinEntry{{Name: "tool", Version: version, SHA256: hash, Preset: true}}}
	if err := lock.WriteLock(f.binDir, lk, "test"); err != nil {
		t.Fatal(err)
	}
}

// install runs `b install <args>` in the fixture.
func (f *pinsFixture) install(t *testing.T, force bool, args ...string) {
	t.Helper()
	shared := NewSharedOptions(mkIO(), f.presets())
	shared.ConfigPath = f.config
	shared.Force = force
	if err := shared.LoadConfig(); err != nil {
		t.Fatal(err)
	}
	o := &InstallOptions{SharedOptions: shared}
	if err := o.Complete(args); err != nil {
		t.Fatal(err)
	}
	if err := o.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// file is the content of .bin/tool. entry is its b.lock entry, and whether
// the entry describes the file on disk: its sha256 is the file's, and its
// version is the version the file is.
func (f *pinsFixture) file(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.binDir, "tool"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func (f *pinsFixture) entry(t *testing.T) (*lock.BinEntry, bool) {
	t.Helper()
	lk, err := lock.ReadLock(f.binDir)
	if err != nil {
		t.Fatal(err)
	}
	e := lk.FindBinary("tool")
	if e == nil {
		t.Fatal("no b.lock entry for tool")
	}
	hash, err := lock.SHA256File(filepath.Join(f.binDir, "tool"))
	if err != nil {
		t.Fatal(err)
	}
	fileVersion := strings.TrimSpace(strings.TrimPrefix(f.file(t), "tool "))
	return e, e.SHA256 == hash && e.Version == fileVersion
}

// TestInstall_AnotherVersionReplacesThePresentBinary: `b install tool@v3`
// over a present v1 replaces the binary. It must never keep the v1 bytes and
// record their sha256 under v3.
func TestInstall_AnotherVersionReplacesThePresentBinary(t *testing.T) {
	for _, locked := range []bool{true, false} {
		f := newPinsFixture(t)
		f.present(t, "v1.0.0", locked)
		f.install(t, false, "tool@v3.0.0")
		if got := f.file(t); got != "tool v3.0.0\n" {
			t.Errorf("locked=%v: .bin/tool is %q, want the v3.0.0 binary", locked, got)
		}
		if e, describes := f.entry(t); e.Version != "v3.0.0" || !describes {
			t.Errorf("locked=%v: b.lock says %s and describes the file on disk = %v, want v3.0.0 and true", locked, e.Version, describes)
		}
		if n := f.hits.Load(); n != 1 {
			t.Errorf("locked=%v: %d downloads, want 1", locked, n)
		}
	}
}

// TestInstall_RequestedVersionAlreadyPresentIsKept: when b.lock says the
// present file is the version asked for, the install keeps it.
func TestInstall_RequestedVersionAlreadyPresentIsKept(t *testing.T) {
	f := newPinsFixture(t)
	f.present(t, "v3.0.0", true)
	f.install(t, false, "tool@v3.0.0")
	if n := f.hits.Load(); n != 0 {
		t.Errorf("%d downloads, want 0", n)
	}
	if e, describes := f.entry(t); e.Version != "v3.0.0" || !describes {
		t.Errorf("b.lock says %s and describes the file on disk = %v", e.Version, describes)
	}
}

// TestInstall_FailedDownloadKeepsTheLockEntry: a download that fails leaves
// the old file and its b.lock entry as they were.
func TestInstall_FailedDownloadKeepsTheLockEntry(t *testing.T) {
	f := newPinsFixture(t)
	f.present(t, "v1.0.0", true)
	f.missing["v3.0.0"] = true
	f.install(t, true, "tool@v3.0.0")
	if got := f.file(t); got != "tool v1.0.0\n" {
		t.Errorf(".bin/tool is %q, want the old v1.0.0 file", got)
	}
	if e, describes := f.entry(t); e.Version != "v1.0.0" || !describes {
		t.Errorf("b.lock says %s and describes the file on disk = %v, want the old entry v1.0.0", e.Version, describes)
	}
}
