package cli

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/fentas/b/pkg/binary"
	"github.com/fentas/b/pkg/lock"
	"github.com/fentas/b/pkg/state"
	"github.com/fentas/goodies/streams"
)

// pinsFixture is a project with a .bin directory, a b.yaml, and a release
// server for the presets "tool" and "other": GET /<version> answers
// "tool <version>\n" and GET /other/<version> answers "other <version>\n",
// or 404 when the version is in missing, or an empty body when the version
// is in empty. The latest version is v2.0.0. paths are the paths of the
// requests to the server.
type pinsFixture struct {
	dir, binDir, config string
	srv                 *httptest.Server
	hits                atomic.Int32
	missing, empty      map[string]bool
	mu                  sync.Mutex
	paths               []string
}

func newPinsFixture(t *testing.T) *pinsFixture {
	t.Helper()
	f := &pinsFixture{dir: t.TempDir(), missing: map[string]bool{}, empty: map[string]bool{}}
	f.binDir = filepath.Join(f.dir, ".bin")
	mustMkdir(t, f.binDir)
	f.config = filepath.Join(f.binDir, "b.yaml")
	mustWrite(t, f.config, []byte("binaries: {}\n"))
	t.Setenv("PATH_BIN", f.binDir)
	t.Setenv("PATH_BASE", f.dir)
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits.Add(1)
		f.mu.Lock()
		f.paths = append(f.paths, r.URL.Path)
		f.mu.Unlock()
		name, version := "tool", strings.TrimPrefix(r.URL.Path, "/")
		if v, ok := strings.CutPrefix(version, "other/"); ok {
			name, version = "other", v
		}
		if f.missing[version] {
			http.NotFound(w, r)
			return
		}
		if f.empty[version] {
			return
		}
		_, _ = io.WriteString(w, name+" "+version+"\n")
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// presets are the presets tool and other, which the server of the fixture
// serves. Each has a GitHub repository: the source of its b.lock row.
func (f *pinsFixture) presets() []*binary.Binary {
	latest := func(*binary.Binary) (string, error) { return "v2.0.0", nil }
	return []*binary.Binary{{
		Name:       "tool",
		GitHubRepo: "test/tool",
		Context:    context.Background(),
		URLF:       func(b *binary.Binary) (string, error) { return f.srv.URL + "/" + b.Version, nil },
		VersionF:   latest,
	}, {
		Name:       "other",
		GitHubRepo: "test/other",
		Context:    context.Background(),
		URLF:       func(b *binary.Binary) (string, error) { return f.srv.URL + "/other/" + b.Version, nil },
		VersionF:   latest,
	}}
}

// present writes .bin/tool as the given version, and records it in b.lock
// when locked is true, in the row that b install writes for it.
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
	lk := &lock.Lock{Binaries: []lock.BinEntry{{Name: "tool", Version: version, SHA256: hash, Source: "github.com/test/tool", Preset: true}}}
	if err := lock.WriteLock(f.binDir, lk, "test"); err != nil {
		t.Fatal(err)
	}
}

// install runs `b install <args>` in the fixture.
func (f *pinsFixture) install(t *testing.T, force bool, args ...string) {
	t.Helper()
	if err := f.run(t, force, args...); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// run is install, and returns the error of Run.
func (f *pinsFixture) run(t *testing.T, force bool, args ...string) error {
	t.Helper()
	return f.runWith(t, func(o *InstallOptions) { o.Force = force }, args...)
}

// runWith runs `b install <args>` in the fixture with the flags that set
// gives (--add, --on-post, ...), and returns the error of Run.
func (f *pinsFixture) runWith(t *testing.T, set func(*InstallOptions), args ...string) error {
	t.Helper()
	// Run installs the binaries in parallel, and each goroutine writes to
	// IO.ErrOut (an onPost hook's output, a hook warning). The CLI's
	// os.Stderr takes concurrent writes; a bytes.Buffer does not.
	shared := NewSharedOptions(&streams.IO{
		In:     strings.NewReader(""),
		Out:    &syncWriter{w: &bytes.Buffer{}},
		ErrOut: &syncWriter{w: &bytes.Buffer{}},
	}, f.presets())
	shared.ConfigPath = f.config
	if err := shared.LoadConfig(); err != nil {
		t.Fatal(err)
	}
	o := &InstallOptions{SharedOptions: shared}
	set(o)
	if err := o.Complete(args); err != nil {
		t.Fatal(err)
	}
	return o.Run()
}

// requests are the paths of the requests to the release server.
func (f *pinsFixture) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.paths...)
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

// TestInstall_AddOfThePinnedVersionTakesThePinsSpelling: `b install --add`
// with the version of the enforced pin in another spelling (with or without
// a leading "v") installs the version of the pin, under the pin's spelling.
// An installed binary of that version is kept: no download, and b.lock stays
// byte for byte. A missing binary is downloaded under the pin's spelling. The
// b.yaml entry keeps the pin's spelling, and gets no second one.
func TestInstall_AddOfThePinnedVersionTakesThePinsSpelling(t *testing.T) {
	for _, tc := range []struct{ pin, arg string }{
		{"v3.0.0", "3.0.0"},
		{"3.0.0", "v3.0.0"},
	} {
		bYAML := "binaries:\n  tool:\n    version: " + tc.pin + "\n    enforced: " + tc.pin + "\n"
		add := func(o *InstallOptions) { o.Add = true }
		checkConfig := func(t *testing.T, f *pinsFixture) {
			t.Helper()
			cfg, err := state.LoadConfigFromPath(f.config)
			if err != nil {
				t.Fatal(err)
			}
			if len(cfg.Binaries) != 1 || cfg.Binaries[0].Name != "tool" {
				t.Fatalf("b.yaml entries %+v, want the one entry tool", cfg.Binaries)
			}
			if e := cfg.Binaries[0]; e.Version != tc.pin || e.Enforced != tc.pin {
				t.Errorf("entry tool: version %q, enforced %q; want %s, %s", e.Version, e.Enforced, tc.pin, tc.pin)
			}
		}

		t.Run(tc.arg+" over an installed "+tc.pin, func(t *testing.T) {
			f := newPinsFixture(t)
			mustWrite(t, f.config, []byte(bYAML))
			f.present(t, tc.pin, true)
			lockFile := filepath.Join(f.binDir, "b.lock")
			before, err := os.ReadFile(lockFile)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.runWith(t, add, "tool@"+tc.arg); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if got := f.requests(); len(got) != 0 {
				t.Errorf("downloads %v, want none: %s is installed", got, tc.pin)
			}
			if got := f.file(t); got != "tool "+tc.pin+"\n" {
				t.Errorf(".bin/tool is %q, want the %s file, untouched", got, tc.pin)
			}
			if after, _ := os.ReadFile(lockFile); string(after) != string(before) {
				t.Errorf("b.lock changed on an install that changed nothing:\n%s\nwas:\n%s", after, before)
			}
			checkConfig(t, f)
		})

		t.Run(tc.arg+" over a missing "+tc.pin, func(t *testing.T) {
			f := newPinsFixture(t)
			mustWrite(t, f.config, []byte(bYAML))
			if err := f.runWith(t, add, "tool@"+tc.arg); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if got := f.requests(); len(got) != 1 || got[0] != "/"+tc.pin {
				t.Errorf("downloads %v, want one of /%s", got, tc.pin)
			}
			if e, describes := f.entry(t); e.Version != tc.pin || !describes {
				t.Errorf("b.lock says %q and describes the file on disk = %v, want %s and true", e.Version, describes, tc.pin)
			}
			checkConfig(t, f)
		})
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

// TestInstall_KeptBinaryKeepsItsLockVersion: an install that keeps the
// present file records the version b.lock gives that file, not "". A bare
// `b install tool` over a locked v1.0.0 downloads nothing and leaves b.lock
// byte for byte. A file that b.lock does not describe is kept with an empty
// version (b cannot tell which version it is), never with a guessed one.
func TestInstall_KeptBinaryKeepsItsLockVersion(t *testing.T) {
	f := newPinsFixture(t)
	f.present(t, "v1.0.0", true)
	before, err := os.ReadFile(filepath.Join(f.binDir, "b.lock"))
	if err != nil {
		t.Fatal(err)
	}
	f.install(t, false, "tool")
	if n := f.hits.Load(); n != 0 {
		t.Errorf("%d downloads, want 0", n)
	}
	if e, describes := f.entry(t); e.Version != "v1.0.0" || !describes {
		t.Errorf("b.lock says %q and describes the file on disk = %v, want v1.0.0 and true", e.Version, describes)
	}
	if after, _ := os.ReadFile(filepath.Join(f.binDir, "b.lock")); string(after) != string(before) {
		t.Errorf("b.lock changed on an install that changed nothing:\n%s\nwas:\n%s", after, before)
	}

	g := newPinsFixture(t)
	g.present(t, "v1.0.0", false)
	g.install(t, false, "tool")
	lk, err := lock.ReadLock(g.binDir)
	if err != nil {
		t.Fatal(err)
	}
	if e := lk.FindBinary("tool"); e == nil || e.Version != "" {
		t.Errorf("a kept file that b.lock does not describe: entry %+v, want version \"\"", e)
	}
}

// TestInstall_ConfigVersionIsTheRequestedVersion: with a version pinned in
// b.yaml, `b install tool` asks for that version: a present file of it is
// kept, a present file of another version is replaced.
func TestInstall_ConfigVersionIsTheRequestedVersion(t *testing.T) {
	for _, tc := range []struct{ pinned, present, want string }{
		{"v1.0.0", "v1.0.0", "tool v1.0.0\n"},
		{"v3.0.0", "v1.0.0", "tool v3.0.0\n"},
	} {
		f := newPinsFixture(t)
		mustWrite(t, f.config, []byte("binaries:\n  tool:\n    version: "+tc.pinned+"\n"))
		f.present(t, tc.present, true)
		f.install(t, false, "tool")
		if got := f.file(t); got != tc.want {
			t.Errorf("pinned %s over %s: .bin/tool is %q, want %q", tc.pinned, tc.present, got, tc.want)
		}
		if e, describes := f.entry(t); e.Version != tc.pinned || !describes {
			t.Errorf("pinned %s over %s: b.lock says %q and describes the file on disk = %v", tc.pinned, tc.present, e.Version, describes)
		}
	}
}

// TestInstall_TwoEntriesOfOnePresetKeepTheirFiles: b.yaml has the preset's
// own entry and an alias entry of the same preset, at other versions. Each
// entry has its own b.lock row, so a second and a third `b install` keep
// both files and download nothing. b verify finds each row's file.
func TestInstall_TwoEntriesOfOnePresetKeepTheirFiles(t *testing.T) {
	f := newPinsFixture(t)
	mustWrite(t, f.config, []byte("binaries:\n  tool:\n    version: v1.0.0\n  t2:\n    alias: tool\n    version: v3.0.0\n"))
	f.install(t, false)
	if n := f.hits.Load(); n != 2 {
		t.Fatalf("the first install: %d downloads, want 2", n)
	}
	for run := 2; run <= 3; run++ {
		f.hits.Store(0)
		f.install(t, false)
		if n := f.hits.Load(); n != 0 {
			t.Errorf("install %d: %d downloads, want 0", run, n)
		}
	}
	lk, err := lock.ReadLock(f.binDir)
	if err != nil {
		t.Fatal(err)
	}
	for name, version := range map[string]string{"tool": "v1.0.0", "t2": "v3.0.0"} {
		file := filepath.Join(f.binDir, name)
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if got := string(data); got != "tool "+version+"\n" {
			t.Errorf(".bin/%s is %q, want the %s binary", name, got, version)
		}
		hash, err := lock.SHA256File(file)
		if err != nil {
			t.Fatal(err)
		}
		e := lk.FindBinary(name)
		if e == nil || e.Version != version || e.SHA256 != hash {
			t.Errorf("b.lock row %q: %+v, want version %s and the sha256 of .bin/%s", name, e, version, name)
		}
	}

	shared := NewSharedOptions(mkIO(), f.presets())
	shared.ConfigPath = f.config
	if err := (&VerifyOptions{SharedOptions: shared}).Run(); err != nil {
		t.Errorf("b verify: %v", err)
	}
}

// TestInstall_UnreadableLockIsAnError: a b.lock that is there but cannot be
// read (it is not JSON, or it is not a file) stops the install before any
// download, with an error that names b.lock. Taken as an empty lock, it
// would make each run download every pinned binary again.
func TestInstall_UnreadableLockIsAnError(t *testing.T) {
	for name, spoil := range map[string]func(t *testing.T, file string){
		"not JSON": func(t *testing.T, file string) {
			mustWrite(t, file, []byte("{not json\n"))
		},
		"a directory": func(t *testing.T, file string) {
			if err := os.Remove(file); err != nil {
				t.Fatal(err)
			}
			mustMkdir(t, file)
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newPinsFixture(t)
			mustWrite(t, f.config, []byte("binaries:\n  tool:\n    version: v1.0.0\n"))
			f.present(t, "v1.0.0", true)
			spoil(t, filepath.Join(f.binDir, "b.lock"))
			for _, args := range [][]string{{"tool"}, nil} {
				err := f.run(t, false, args...)
				if err == nil || !strings.Contains(err.Error(), "b.lock") {
					t.Errorf("b install %v: error %v, want an error about b.lock", args, err)
				}
			}
			if n := f.hits.Load(); n != 0 {
				t.Errorf("%d downloads, want 0", n)
			}
			if got := f.file(t); got != "tool v1.0.0\n" {
				t.Errorf(".bin/tool is %q, want the v1.0.0 file, untouched", got)
			}
		})
	}
}

// TestInstall_NoArgumentsInstallsTheConfigEntries: `b install` without an
// argument installs the binaries of the b.yaml entries, each at its own file
// and version, and no other preset. b.lock then describes each file, and a
// second run downloads nothing.
func TestInstall_NoArgumentsInstallsTheConfigEntries(t *testing.T) {
	f := newPinsFixture(t)
	mustWrite(t, f.config, []byte("binaries:\n  tool:\n    version: v1.0.0\n  o2:\n    alias: other\n"))
	f.install(t, false)
	if n := f.hits.Load(); n != 2 {
		t.Errorf("the first install: %d downloads, want 2", n)
	}
	lk, err := lock.ReadLock(f.binDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ file, content, version string }{
		{"tool", "tool v1.0.0\n", "v1.0.0"},
		{"o2", "other v2.0.0\n", "v2.0.0"},
	} {
		path := filepath.Join(f.binDir, tc.file)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf(".bin/%s: %v", tc.file, err)
			continue
		}
		if string(data) != tc.content {
			t.Errorf(".bin/%s is %q, want %q", tc.file, data, tc.content)
		}
		hash, err := lock.SHA256File(path)
		if err != nil {
			t.Fatal(err)
		}
		if e := lk.FindBinary(tc.file); e == nil || e.Version != tc.version || e.SHA256 != hash {
			t.Errorf("b.lock row %q: %+v, want version %s and the sha256 of .bin/%s", tc.file, e, tc.version, tc.file)
		}
	}
	if _, err := os.Stat(filepath.Join(f.binDir, "other")); !os.IsNotExist(err) {
		t.Errorf(".bin/other: %v, want no file: the preset other has no entry of its own", err)
	}

	f.hits.Store(0)
	f.install(t, false)
	if n := f.hits.Load(); n != 0 {
		t.Errorf("the second install: %d downloads, want 0", n)
	}
}

// TestInstall_FailedDownloadLeavesNoPartialFile: a download that fails after
// it has written the file (here: an empty body, which the download rejects
// after it has truncated the file) leaves no file and no b.lock entry. The
// next install of the unpinned binary then downloads it again; it must not
// keep the incomplete file and record it in b.lock as installed.
func TestInstall_FailedDownloadLeavesNoPartialFile(t *testing.T) {
	f := newPinsFixture(t)
	f.present(t, "v1.0.0", true)
	f.empty["v2.0.0"] = true
	f.install(t, true, "tool")
	if _, err := os.Stat(filepath.Join(f.binDir, "tool")); !os.IsNotExist(err) {
		t.Errorf(".bin/tool after the failed download: %v, want no file", err)
	}
	lk, err := lock.ReadLock(f.binDir)
	if err != nil {
		t.Fatal(err)
	}
	if e := lk.FindBinary("tool"); e != nil {
		t.Errorf("b.lock entry after the failed download: %+v, want none", e)
	}

	f.empty = map[string]bool{}
	f.hits.Store(0)
	f.install(t, false, "tool")
	if n := f.hits.Load(); n != 1 {
		t.Errorf("the next install: %d downloads, want 1", n)
	}
	if e, describes := f.entry(t); e.Version != "v2.0.0" || !describes {
		t.Errorf("b.lock says %q and describes the file on disk = %v, want v2.0.0 and true", e.Version, describes)
	}
}

// TestInstall_FailedDownloadDropsTheEntryOfAMissingFile: when a download of
// a missing binary fails, b.lock keeps no entry for it: the entry would
// describe a file that is not there.
func TestInstall_FailedDownloadDropsTheEntryOfAMissingFile(t *testing.T) {
	f := newPinsFixture(t)
	f.present(t, "v1.0.0", true)
	if err := os.Remove(filepath.Join(f.binDir, "tool")); err != nil {
		t.Fatal(err)
	}
	f.missing["v3.0.0"] = true
	f.install(t, false, "tool@v3.0.0")
	lk, err := lock.ReadLock(f.binDir)
	if err != nil {
		t.Fatal(err)
	}
	if e := lk.FindBinary("tool"); e != nil {
		t.Errorf("b.lock entry of the missing file: %+v, want none", e)
	}
}
