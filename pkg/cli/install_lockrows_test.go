package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/fentas/b/pkg/binary"
	"github.com/fentas/b/pkg/lock"
	"github.com/fentas/b/pkg/provider"
	"github.com/fentas/goodies/streams"
)

// fakeRelease is a release provider for tests. A ref <scheme>://<org>/<name>
// has one release asset, the raw binary <name>, which the pins fixture serves
// at /<org>/<version> (serveRefs): its content is "tool <org>/<version>\n".
// The latest version is v2.0.0.
type fakeRelease struct{ scheme string }

// fakeReleaseDigest is fakeRelease with a digest: sha256:<org>.
type fakeReleaseDigest struct{ fakeRelease }

// fakeReleaseBase is the URL of the server of the fake release providers.
var fakeReleaseBase atomic.Value // string

func init() {
	provider.Register(&fakeRelease{scheme: "fakerel"})
	provider.Register(&fakeReleaseDigest{fakeRelease{scheme: "fakereld"}})
}

func (f *fakeRelease) Name() string { return f.scheme }

func (f *fakeRelease) Match(ref string) bool { return strings.HasPrefix(ref, f.scheme+"://") }

func (f *fakeRelease) LatestVersion(string) (string, error) { return "v2.0.0", nil }

func (f *fakeRelease) org(ref string) string {
	return strings.SplitN(strings.TrimPrefix(ref, f.scheme+"://"), "/", 2)[0]
}

func (f *fakeRelease) FetchRelease(ref, version string) (*provider.Release, error) {
	base, _ := fakeReleaseBase.Load().(string)
	if base == "" {
		return nil, fmt.Errorf("%s: no release server", ref)
	}
	if version == "" {
		version = "v2.0.0"
	}
	return &provider.Release{Version: version, Assets: []provider.Asset{{
		Name: provider.BinaryName(ref),
		URL:  base + "/" + f.org(ref) + "/" + version,
	}}}, nil
}

func (f *fakeReleaseDigest) ResolveDigest(ref, _ string) (string, error) {
	return "sha256:" + f.org(ref), nil
}

// serveRefs makes the fixture's server the server of the fake release
// providers for this test.
func (f *pinsFixture) serveRefs(t *testing.T) {
	t.Helper()
	fakeReleaseBase.Store(f.srv.URL)
	t.Cleanup(func() { fakeReleaseBase.Store("") })
}

// update runs `b update <args>` in the fixture.
func (f *pinsFixture) update(t *testing.T, args ...string) {
	t.Helper()
	shared := NewSharedOptions(&streams.IO{
		In:     strings.NewReader(""),
		Out:    &syncWriter{w: &bytes.Buffer{}},
		ErrOut: &syncWriter{w: &bytes.Buffer{}},
	}, f.presets())
	shared.ConfigPath = f.config
	if err := shared.LoadConfig(); err != nil {
		t.Fatal(err)
	}
	o := &UpdateOptions{SharedOptions: shared}
	if err := o.Complete(args); err != nil {
		t.Fatal(err)
	}
	if err := o.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := o.Run(); err != nil {
		t.Fatalf("b update: %v", err)
	}
}

// verify runs `b verify` in the fixture.
func (f *pinsFixture) verify(t *testing.T) error {
	t.Helper()
	shared := NewSharedOptions(mkIO(), f.presets())
	shared.ConfigPath = f.config
	return (&VerifyOptions{SharedOptions: shared}).Run()
}

// writeLock writes b.lock with the given binary rows.
func (f *pinsFixture) writeLock(t *testing.T, rows ...lock.BinEntry) {
	t.Helper()
	if err := lock.WriteLock(f.binDir, &lock.Lock{Binaries: rows}, "test"); err != nil {
		t.Fatal(err)
	}
}

// readLock reads b.lock.
func (f *pinsFixture) readLock(t *testing.T) *lock.Lock {
	t.Helper()
	lk, err := lock.ReadLock(f.binDir)
	if err != nil {
		t.Fatal(err)
	}
	return lk
}

// binFile writes .bin/<name> with content and returns its sha256.
func (f *pinsFixture) binFile(t *testing.T, name, content string) string {
	t.Helper()
	file := filepath.Join(f.binDir, name)
	mustWriteExec(t, file, []byte(content))
	hash, err := lock.SHA256File(file)
	if err != nil {
		t.Fatal(err)
	}
	return hash
}

// checkRow checks that the b.lock row name describes .bin/<name>: the file
// has content, and the row has version and the sha256 of the file.
func (f *pinsFixture) checkRow(t *testing.T, lk *lock.Lock, name, content, version string) {
	t.Helper()
	file := filepath.Join(f.binDir, name)
	data, err := os.ReadFile(file)
	if err != nil {
		t.Errorf(".bin/%s: %v", name, err)
		return
	}
	if string(data) != content {
		t.Errorf(".bin/%s is %q, want %q", name, data, content)
	}
	hash, err := lock.SHA256File(file)
	if err != nil {
		t.Fatal(err)
	}
	if e := lk.FindBinary(name); e == nil || e.Version != version || e.SHA256 != hash {
		t.Errorf("b.lock row %q: %+v, want version %s and the sha256 of .bin/%s", name, e, version, name)
	}
}

// TestInstall_TwoRefsOfOneNameHaveTheirOwnRows: two provider refs with the
// same binary name, each under its own alias and at its own version. Each
// has its own b.lock row, under its alias, so a second and a third `b
// install` download nothing. With one row under the binary name, each run
// downloads one of them again.
func TestInstall_TwoRefsOfOneNameHaveTheirOwnRows(t *testing.T) {
	f := newPinsFixture(t)
	f.serveRefs(t)
	mustWrite(t, f.config, []byte("binaries:\n  fakerel://a/tool:\n    alias: ta\n    version: v1.0.0\n  fakerel://b/tool:\n    alias: tb\n    version: v3.0.0\n"))
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
	lk := f.readLock(t)
	f.checkRow(t, lk, "ta", "tool a/v1.0.0\n", "v1.0.0")
	f.checkRow(t, lk, "tb", "tool b/v3.0.0\n", "v3.0.0")
	if e := lk.FindBinary("tool"); e != nil {
		t.Errorf("b.lock row tool: %+v, want none: no file .bin/tool", e)
	}
	if err := f.verify(t); err != nil {
		t.Errorf("b verify: %v", err)
	}
}

// TestInstall_RowOfAnOlderBMoves: b.lock from an older b has the row of an
// aliased binary under the binary's name. The install finds it, keeps the
// file (its sha256 is the one in the row), and writes the row under the
// alias. The row under the old name goes, so b verify passes. This holds
// for a provider ref with an alias (rows of b up to this change) and for an
// alias entry of a preset (rows of b before alias rows), with and without a
// binary argument.
func TestInstall_RowOfAnOlderBMoves(t *testing.T) {
	for _, tc := range []struct {
		name, config, file, content, version string
		old                                  lock.BinEntry
		args                                 []string
	}{{
		name:    "a provider ref with an alias",
		config:  "binaries:\n  fakerel://a/tool:\n    alias: ta\n    version: v1.0.0\n",
		file:    "ta",
		content: "tool a/v1.0.0\n",
		version: "v1.0.0",
		old:     lock.BinEntry{Name: "tool", Version: "v1.0.0", Source: "fakerel://a/tool", Provider: "fakerel"},
		args:    []string{"fakerel://a/tool"},
	}, {
		name:    "an alias entry of a preset",
		config:  "binaries:\n  t2:\n    alias: tool\n    version: v3.0.0\n",
		file:    "t2",
		content: "tool v3.0.0\n",
		version: "v3.0.0",
		old:     lock.BinEntry{Name: "tool", Version: "v3.0.0", Preset: true},
		args:    []string{"t2"},
	}} {
		for _, args := range [][]string{nil, tc.args} {
			t.Run(fmt.Sprintf("%s, b install %s", tc.name, strings.Join(args, " ")), func(t *testing.T) {
				f := newPinsFixture(t)
				f.serveRefs(t)
				mustWrite(t, f.config, []byte(tc.config))
				old := tc.old
				old.SHA256 = f.binFile(t, tc.file, tc.content)
				f.writeLock(t, old)

				f.install(t, false, args...)
				if n := f.hits.Load(); n != 0 {
					t.Errorf("%d downloads, want 0: the file is the one of the row", n)
				}
				lk := f.readLock(t)
				f.checkRow(t, lk, tc.file, tc.content, tc.version)
				if e := lk.FindBinary("tool"); e != nil {
					t.Errorf("b.lock row under the old name: %+v, want none", e)
				}
				if err := f.verify(t); err != nil {
					t.Errorf("b verify: %v", err)
				}
			})
		}
	}
}

// TestInstall_OldRowOfAnotherFileStays: b.yaml has the preset's own entry
// and an alias entry. b.lock from an older b has only the row under the
// preset's name, of the preset's own file. For the alias entry, that row
// describes another file (its sha256 is not the one of .bin/t2): the pinned
// alias entry is downloaded once, and the row of .bin/tool stays as it is.
func TestInstall_OldRowOfAnotherFileStays(t *testing.T) {
	f := newPinsFixture(t)
	mustWrite(t, f.config, []byte("binaries:\n  tool:\n    version: v1.0.0\n  t2:\n    alias: tool\n    version: v3.0.0\n"))
	hash := f.binFile(t, "tool", "tool v1.0.0\n")
	f.binFile(t, "t2", "tool v3.0.0\n")
	f.writeLock(t, lock.BinEntry{Name: "tool", Version: "v1.0.0", SHA256: hash, Preset: true})

	f.install(t, false, "t2")
	if n := f.hits.Load(); n != 1 {
		t.Errorf("%d downloads, want 1: no row describes .bin/t2", n)
	}
	lk := f.readLock(t)
	f.checkRow(t, lk, "tool", "tool v1.0.0\n", "v1.0.0")
	f.checkRow(t, lk, "t2", "tool v3.0.0\n", "v3.0.0")
}

// TestUpdate_TwoRefsOfOneNameHaveTheirOwnDigests: `b update` keeps the
// digest of each of two provider refs with the same binary name under its
// own alias. Each digest matches its row, so b update downloads nothing.
// With one digest per binary name, one of them is downloaded at each run.
func TestUpdate_TwoRefsOfOneNameHaveTheirOwnDigests(t *testing.T) {
	f := newPinsFixture(t)
	f.serveRefs(t)
	mustWrite(t, f.config, []byte("binaries:\n  fakereld://a/tool:\n    alias: ta\n  fakereld://b/tool:\n    alias: tb\n"))
	f.install(t, false)
	if n := f.hits.Load(); n != 2 {
		t.Fatalf("the install: %d downloads, want 2", n)
	}
	lk := f.readLock(t)
	for name, digest := range map[string]string{"ta": "sha256:a", "tb": "sha256:b"} {
		if e := lk.FindBinary(name); e == nil || e.Digest != digest {
			t.Errorf("b.lock row %q: %+v, want the digest %s", name, e, digest)
		}
	}
	for run := 1; run <= 2; run++ {
		f.hits.Store(0)
		f.update(t)
		if n := f.hits.Load(); n != 0 {
			t.Errorf("update %d: %d downloads, want 0", run, n)
		}
	}
}

// TestUpdate_RowOfAnOlderBMoves: b.lock from an older b has the row of a
// provider ref with an alias under the binary's name. b update finds it: the
// digest and the file match, so it downloads nothing, and it writes the row
// under the alias.
func TestUpdate_RowOfAnOlderBMoves(t *testing.T) {
	f := newPinsFixture(t)
	f.serveRefs(t)
	mustWrite(t, f.config, []byte("binaries:\n  fakereld://a/tool:\n    alias: ta\n"))
	hash := f.binFile(t, "ta", "tool a/v2.0.0\n")
	f.writeLock(t, lock.BinEntry{Name: "tool", Version: "v2.0.0", SHA256: hash, Source: "fakereld://a/tool", Provider: "fakereld", Digest: "sha256:a"})

	f.update(t)
	if n := f.hits.Load(); n != 0 {
		t.Errorf("%d downloads, want 0: the digest and the file match the row", n)
	}
	lk := f.readLock(t)
	if e := lk.FindBinary("ta"); e == nil || e.Digest != "sha256:a" || e.SHA256 != hash {
		t.Errorf("b.lock row ta: %+v, want the row of .bin/ta", e)
	}
	if e := lk.FindBinary("tool"); e != nil {
		t.Errorf("b.lock row under the old name: %+v, want none", e)
	}
}

// TestDigestMatchesLock_OldRowNeedsTheFile: a row under the binary's name
// counts for an aliased binary only when its sha256 is the one of the file.
func TestDigestMatchesLock_OldRowNeedsTheFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "ta")
	mustWriteExec(t, file, []byte("ta\n"))
	hash, err := lock.SHA256File(file)
	if err != nil {
		t.Fatal(err)
	}
	b := &binary.Binary{Name: "tool", Alias: "ta", File: file, AutoDetect: true, ProviderRef: "fakedigest://a"}
	for _, tc := range []struct {
		name string
		sha  string
		want bool
	}{
		{"the row of the file", hash, true},
		{"the row of another file", "another file", false},
	} {
		lk := &lock.Lock{Binaries: []lock.BinEntry{{Name: "tool", SHA256: tc.sha, Source: "fakedigest://a", Digest: "sha256:aaa"}}}
		if got := digestMatchesLock(b, lk, "sha256:aaa"); got != tc.want {
			t.Errorf("%s: digestMatchesLock = %v, want %v", tc.name, got, tc.want)
		}
	}
}
