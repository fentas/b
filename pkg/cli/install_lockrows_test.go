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
	f.updateWith(t, func(*UpdateOptions) {}, args...)
}

// updateWith runs `b update <args>` in the fixture with the flags that set
// gives.
func (f *pinsFixture) updateWith(t *testing.T, set func(*UpdateOptions), args ...string) {
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
	set(o)
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
	// The root command loads b.yaml for each subcommand.
	if err := shared.LoadConfig(); err != nil {
		return err
	}
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
				if e := lk.FindBinary(tc.file); e != nil && e.Binary != "tool" {
					t.Errorf("b.lock row %s names the binary %q, want tool", tc.file, e.Binary)
				}
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
// under the alias. The row under the old name goes, also in a named update,
// which removes no other rows.
func TestUpdate_RowOfAnOlderBMoves(t *testing.T) {
	for _, args := range [][]string{nil, {"fakereld://a/tool"}} {
		t.Run("b update "+strings.Join(args, " "), func(t *testing.T) {
			f := newPinsFixture(t)
			f.serveRefs(t)
			mustWrite(t, f.config, []byte("binaries:\n  fakereld://a/tool:\n    alias: ta\n"))
			hash := f.binFile(t, "ta", "tool a/v2.0.0\n")
			f.writeLock(t, lock.BinEntry{Name: "tool", Version: "v2.0.0", SHA256: hash, Source: "fakereld://a/tool", Provider: "fakereld", Digest: "sha256:a"})

			f.update(t, args...)
			if n := f.hits.Load(); n != 0 {
				t.Errorf("%d downloads, want 0: the digest and the file match the row", n)
			}
			lk := f.readLock(t)
			if e := lk.FindBinary("ta"); e == nil || e.Digest != "sha256:a" || e.SHA256 != hash || e.Binary != "tool" {
				t.Errorf("b.lock row ta: %+v, want the row of .bin/ta", e)
			}
			if got := f.rowNames(t); got != "ta" {
				t.Errorf("b.lock rows: %q, want only ta", got)
			}
		})
	}
}

// TestUpdate_FirstRunAfterAnOlderB: the first command after an upgrade of b
// is `b update`. It moves the row that an older b wrote under the binary's
// name to the name of the file before it removes the rows that describe no
// file, so the later `b install` keeps the file. This holds for a provider
// ref with an alias and for an alias entry of a preset.
func TestUpdate_FirstRunAfterAnOlderB(t *testing.T) {
	for _, tc := range []struct {
		name, config, file, content string
		old                         lock.BinEntry
	}{{
		name:    "a provider ref with an alias",
		config:  "binaries:\n  fakerel://a/tool:\n    alias: ta\n    version: v1.0.0\n",
		file:    "ta",
		content: "tool a/v1.0.0\n",
		old:     lock.BinEntry{Name: "tool", Version: "v1.0.0", Source: "fakerel://a/tool", Provider: "fakerel"},
	}, {
		name:    "an alias entry of a preset",
		config:  "binaries:\n  t2:\n    alias: tool\n    version: v3.0.0\n",
		file:    "t2",
		content: "tool v3.0.0\n",
		old:     lock.BinEntry{Name: "tool", Version: "v3.0.0", Preset: true},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPinsFixture(t)
			f.serveRefs(t)
			mustWrite(t, f.config, []byte(tc.config))
			old := tc.old
			old.SHA256 = f.binFile(t, tc.file, tc.content)
			f.writeLock(t, old)

			f.update(t)
			if got := f.rowNames(t); got != tc.file {
				t.Errorf("b.lock rows after `b update`: %q, want %q", got, tc.file)
			}
			f.install(t, false)
			if n := f.hits.Load(); n != 0 {
				t.Errorf("b update, then b install: %d downloads, want 0", n)
			}
			f.checkRow(t, f.readLock(t), tc.file, tc.content, tc.old.Version)
		})
	}
}

// TestInstall_OldRowOfAPresentFileStays: a row under the name of a binary
// stays while the file of that name is there. (1) `b install tool@v1.0.0`,
// then `b install --alias foo tool@v3.0.0`: the row tool stays, so a later
// `b install tool@v1.0.0` keeps .bin/tool. (2) b.lock of an older b has one
// row tool for .bin/tool and for .bin/t2 (an alias entry of tool), which
// have the same content: `b install t2` writes the row t2 from it, and the
// row tool stays.
func TestInstall_OldRowOfAPresentFileStays(t *testing.T) {
	t.Run("b install --alias foo tool", func(t *testing.T) {
		f := newPinsFixture(t)
		f.install(t, false, "tool@v1.0.0")
		if err := f.runWith(t, func(o *InstallOptions) { o.Alias = "foo" }, "tool@v3.0.0"); err != nil {
			t.Fatalf("b install --alias foo tool@v3.0.0: %v", err)
		}
		f.hits.Store(0)
		f.install(t, false, "tool@v1.0.0")
		if n := f.hits.Load(); n != 0 {
			t.Errorf("the second b install tool@v1.0.0: %d downloads, want 0", n)
		}
		lk := f.readLock(t)
		f.checkRow(t, lk, "tool", "tool v1.0.0\n", "v1.0.0")
		f.checkRow(t, lk, "foo", "tool v3.0.0\n", "v3.0.0")
	})
	t.Run("one old row of two files", func(t *testing.T) {
		f := newPinsFixture(t)
		mustWrite(t, f.config, []byte("binaries:\n  t2:\n    alias: tool\n    version: v3.0.0\n"))
		hash := f.binFile(t, "tool", "tool v3.0.0\n")
		f.binFile(t, "t2", "tool v3.0.0\n")
		f.writeLock(t, lock.BinEntry{Name: "tool", Version: "v3.0.0", SHA256: hash, Preset: true})

		f.install(t, false, "t2")
		if n := f.hits.Load(); n != 0 {
			t.Errorf("%d downloads, want 0: the row describes .bin/t2", n)
		}
		lk := f.readLock(t)
		f.checkRow(t, lk, "tool", "tool v3.0.0\n", "v3.0.0")
		f.checkRow(t, lk, "t2", "tool v3.0.0\n", "v3.0.0")
		if err := f.verify(t); err != nil {
			t.Errorf("b verify: %v", err)
		}
	})
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

// staleRowsFixture is a project with b.lock rows that no b.yaml entry
// installs. b.yaml has an alias entry t2 of the preset tool, and an alias
// entry t3 with its file outside the binary directory (b.yaml `file:`).
// b.lock has the row of t2; the row of t3 under its alias, as an older b
// wrote it; the row tool of an older b (before alias rows) without a file
// .bin/tool; the row gone of an entry that b.yaml does not have any more,
// without a file; and the row adhoc of a present file that b.yaml does not
// install (`b install` of a binary without --add).
func staleRowsFixture(t *testing.T) *pinsFixture {
	t.Helper()
	f := newPinsFixture(t)
	mustWrite(t, f.config, []byte("binaries:\n  t2:\n    alias: tool\n    version: v3.0.0\n  t3:\n    alias: tool\n    version: v1.0.0\n    file: ../tools/t3\n"))
	t2 := f.binFile(t, "t2", "tool v3.0.0\n")
	adhoc := f.binFile(t, "adhoc", "other v2.0.0\n")
	t3file := filepath.Join(f.dir, "tools", "t3")
	mustMkdir(t, filepath.Dir(t3file))
	mustWriteExec(t, t3file, []byte("tool v1.0.0\n"))
	t3, err := lock.SHA256File(t3file)
	if err != nil {
		t.Fatal(err)
	}
	f.writeLock(t,
		lock.BinEntry{Name: "t2", Version: "v3.0.0", SHA256: t2, Preset: true},
		lock.BinEntry{Name: "t3", Version: "v1.0.0", SHA256: t3, Preset: true},
		lock.BinEntry{Name: "tool", Version: "v3.0.0", SHA256: t2, Preset: true},
		lock.BinEntry{Name: "gone", Version: "v1.0.0", SHA256: "the sha256 of a removed file", Preset: true},
		lock.BinEntry{Name: "adhoc", Version: "v2.0.0", SHA256: adhoc, Preset: true},
	)
	return f
}

// rowNames are the names of the binary rows in b.lock, in order.
func (f *pinsFixture) rowNames(t *testing.T) string {
	t.Helper()
	var names []string
	for _, e := range f.readLock(t).Binaries {
		names = append(names, e.Name)
	}
	return strings.Join(names, " ")
}

// TestInstall_PrunesRowsThatDescribeNoFile: `b install` without arguments
// removes the b.lock rows that no b.yaml entry installs and whose file is
// not there: b verify reports such a row as missing. The rows of the
// entries stay: the row of t3, whose file is outside the binary directory,
// moves to the path of its file. The row of a present file without an
// entry stays too. A named install removes nothing.
func TestInstall_PrunesRowsThatDescribeNoFile(t *testing.T) {
	f := staleRowsFixture(t)
	f.install(t, false, "t2")
	if got, want := f.rowNames(t), "t2 t3 tool gone adhoc"; got != want {
		t.Errorf("b.lock rows after `b install t2`: %q, want %q", got, want)
	}

	f.install(t, false)
	if n := f.hits.Load(); n != 0 {
		t.Errorf("%d downloads, want 0", n)
	}
	if got, want := f.rowNames(t), "t2 adhoc ../tools/t3"; got != want {
		t.Errorf("b.lock rows after `b install`: %q, want %q", got, want)
	}
	if err := f.verify(t); err != nil {
		t.Errorf("b verify: %v", err)
	}
}

// TestUpdate_PrunesRowsThatDescribeNoFile: `b update` without arguments
// removes the same rows as `b install`. `b update --dry-run` removes nothing.
func TestUpdate_PrunesRowsThatDescribeNoFile(t *testing.T) {
	f := staleRowsFixture(t)
	f.updateWith(t, func(o *UpdateOptions) { o.DryRun = true })
	if got, want := f.rowNames(t), "t2 t3 tool gone adhoc"; got != want {
		t.Errorf("b.lock rows after `b update --dry-run`: %q, want %q", got, want)
	}
	f.update(t)
	if got, want := f.rowNames(t), "t2 adhoc ../tools/t3"; got != want {
		t.Errorf("b.lock rows after `b update`: %q, want %q", got, want)
	}
	if n := f.hits.Load(); n != 0 {
		t.Errorf("%d downloads, want 0", n)
	}
	if err := f.verify(t); err != nil {
		t.Errorf("b verify: %v", err)
	}
}

// TestInstall_SharedLockKeepsTheRowOfAFileElsewhere: two configuration
// files in .bin use the same b.lock. b.ci.yaml puts t3 outside the binary
// directory (`file: ../tools/t3`); b.yaml does not have t3. `b install`
// with b.yaml keeps the row of t3: its file is there. So the next install
// with b.ci.yaml downloads nothing, and b verify passes.
func TestInstall_SharedLockKeepsTheRowOfAFileElsewhere(t *testing.T) {
	f := newPinsFixture(t)
	main := f.config
	ci := filepath.Join(f.binDir, "b.ci.yaml")
	mustWrite(t, ci, []byte("binaries:\n  t3:\n    alias: tool\n    version: v1.0.0\n    file: ../tools/t3\n"))
	mustWrite(t, main, []byte("binaries:\n  tool:\n    version: v1.0.0\n"))
	f.config = ci
	f.install(t, false)
	f.config = main
	f.install(t, false)
	if got, want := f.rowNames(t), "../tools/t3 tool"; got != want {
		t.Errorf("b.lock rows: %q, want %q", got, want)
	}
	f.config = ci
	f.hits.Store(0)
	f.install(t, false)
	if n := f.hits.Load(); n != 0 {
		t.Errorf("the second install with b.ci.yaml: %d downloads, want 0", n)
	}
	if err := f.verify(t); err != nil {
		t.Errorf("b verify: %v", err)
	}
}

// TestInstall_TwoRefsWithTheirOwnFiles: two provider refs with the same
// binary name, each with its own `file:`, install two files. b.yaml is
// accepted, and each file has its own b.lock row under its path, so a
// second `b install` downloads nothing.
func TestInstall_TwoRefsWithTheirOwnFiles(t *testing.T) {
	f := newPinsFixture(t)
	f.serveRefs(t)
	mustWrite(t, f.config, []byte("binaries:\n  fakerel://a/tool:\n    version: v1.0.0\n    file: ../a/tool\n  fakerel://b/tool:\n    version: v3.0.0\n    file: ../b/tool\n"))
	f.install(t, false)
	if n := f.hits.Load(); n != 2 {
		t.Fatalf("the first install: %d downloads, want 2", n)
	}
	f.hits.Store(0)
	f.install(t, false)
	if n := f.hits.Load(); n != 0 {
		t.Errorf("the second install: %d downloads, want 0", n)
	}
	lk := f.readLock(t)
	for name, version := range map[string]string{"../a/tool": "v1.0.0", "../b/tool": "v3.0.0"} {
		if e := lk.FindBinary(name); e == nil || e.Version != version || e.Binary != "tool" {
			t.Errorf("b.lock row %q: %+v, want version %s of the binary tool", name, e, version)
		}
	}
	if err := f.verify(t); err != nil {
		t.Errorf("b verify: %v", err)
	}
}

// cmdErr runs `b install <args>` (install) or `b update <args>` (update) in
// the fixture, with the flags that set gives to update, and returns the
// error of Complete, else the error of Run. A refusal in Complete stops the
// command before any download.
func (f *pinsFixture) cmdErr(t *testing.T, cmd string, set func(*UpdateOptions), args ...string) error {
	t.Helper()
	if cmd == "install" {
		return f.installErr(t, func(*InstallOptions) {}, args...)
	}
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
	set(o)
	if err := o.Complete(args); err != nil {
		return err
	}
	if err := o.Validate(); err != nil {
		return err
	}
	return o.Run()
}

// TestInstall_RefusesTwoEntriesOfOneFile: a b.yaml in which two entries
// install the same file is refused by `b install` and `b update`, with or
// without a binary argument, before any download: each command runs, and
// without the refusal it downloads. The error names the entries and the
// file, and tells how to fix b.yaml. `b update --envs-only`, `b update
// --group <name>` and `b update --plan-json` (also with a binary argument,
// which it ignores) do not touch binaries, so they do not refuse.
func TestInstall_RefusesTwoEntriesOfOneFile(t *testing.T) {
	for _, tc := range []struct {
		name, config, want string
	}{{
		name:   "two provider refs with the same binary name",
		config: "binaries:\n  fakerel://a/tool:\n    version: v1.0.0\n  fakerel://b/tool: {}\n",
		want:   "b.yaml: the entries fakerel://a/tool and fakerel://b/tool install the same file tool",
	}, {
		name:   "a preset entry and a provider ref with its name as alias",
		config: "binaries:\n  tool: {}\n  fakerel://x/y:\n    alias: tool\n",
		want:   "b.yaml: the entries fakerel://x/y and tool install the same file tool",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPinsFixture(t)
			f.serveRefs(t)
			mustWrite(t, f.config, []byte(tc.config))
			for _, run := range []struct {
				cmd  string
				args []string
			}{{"install", nil}, {"install", []string{"other"}}, {"update", nil}, {"update", []string{"other"}}} {
				err := f.cmdErr(t, run.cmd, func(*UpdateOptions) {}, run.args...)
				if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "alias: <name>") {
					t.Errorf("b %s %v: error %v, want %q and a hint", run.cmd, run.args, err, tc.want)
				}
				if n := f.hits.Swap(0); n != 0 {
					t.Errorf("b %s %v: %d downloads, want 0", run.cmd, run.args, n)
				}
			}
			if err := f.cmdErr(t, "update", func(o *UpdateOptions) { o.EnvsOnly = true }); err != nil {
				t.Errorf("b update --envs-only: %v, want no error", err)
			}
			if err := f.cmdErr(t, "update", func(o *UpdateOptions) { o.Group = "g" }); err != nil {
				t.Errorf("b update --group g: %v, want no error", err)
			}
			for _, args := range [][]string{nil, {"other"}} {
				if err := f.cmdErr(t, "update", func(o *UpdateOptions) { o.PlanJSON = true }, args...); err != nil {
					t.Errorf("b update --plan-json %v: %v, want no error", args, err)
				}
			}
			if n := f.hits.Load(); n != 0 {
				t.Errorf("b update --envs-only, --group, --plan-json: %d downloads, want 0", n)
			}
		})
	}
}

// installErr runs `b install <args>` in the fixture with the flags that set
// gives, and returns the error of Complete, else the error of Run.
func (f *pinsFixture) installErr(t *testing.T, set func(*InstallOptions), args ...string) error {
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
	o := &InstallOptions{SharedOptions: shared}
	set(o)
	if err := o.Complete(args); err != nil {
		return err
	}
	return o.Run()
}

// TestInstall_AddRefusesASecondEntryOfOneFile: b.yaml has the provider ref
// fakerel://a/tool. `b install --add fakerel://b/tool@v3.0.0` adds a second
// entry of the file tool, a b.yaml that the next run refuses. It is refused
// before any download, with the message of the duplicate check, and b.yaml
// does not change. With `--alias tb`, the new entry has its own file, and
// the --add works.
func TestInstall_AddRefusesASecondEntryOfOneFile(t *testing.T) {
	f := newPinsFixture(t)
	f.serveRefs(t)
	config := "binaries:\n  fakerel://a/tool:\n    version: v1.0.0\n"
	mustWrite(t, f.config, []byte(config))

	err := f.installErr(t, func(o *InstallOptions) { o.Add = true }, "fakerel://b/tool@v3.0.0")
	want := "b.yaml: the entries fakerel://a/tool and fakerel://b/tool install the same file tool"
	if err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "alias: <name>") {
		t.Errorf("b install --add fakerel://b/tool@v3.0.0: error %v, want %q and a hint", err, want)
	}
	if n := f.hits.Load(); n != 0 {
		t.Errorf("%d downloads, want 0", n)
	}
	if data, err := os.ReadFile(f.config); err != nil || string(data) != config {
		t.Errorf("b.yaml: %q (%v), want it unchanged", data, err)
	}

	if err := f.installErr(t, func(o *InstallOptions) { o.Add = true; o.Alias = "tb" }, "fakerel://b/tool@v3.0.0"); err != nil {
		t.Fatalf("b install --add --alias tb fakerel://b/tool@v3.0.0: %v", err)
	}
	f.install(t, false)
	lk := f.readLock(t)
	f.checkRow(t, lk, "tool", "tool a/v1.0.0\n", "v1.0.0")
	f.checkRow(t, lk, "tb", "tool b/v3.0.0\n", "v3.0.0")
}

// TestCheckConfigFiles: each entry installs its own file, also two refs of
// one repository (one under an alias), an alias equal to the binary name,
// and two refs of one binary name with their own `file:`. All entries of one
// file are named, sorted. The file is the path relative to the binary
// directory.
func TestCheckConfigFiles(t *testing.T) {
	project := t.TempDir()
	dir := filepath.Join(project, ".bin")
	mustMkdir(t, dir)
	t.Setenv("PATH_BIN", dir)
	t.Setenv("PATH_BASE", project)
	ok := "binaries:\n  tool: {}\n  t2:\n    alias: tool\n  github.com/org/repo: {}\n  github.com/org/repo/cli:\n    alias: cli\n  github.com/org/other:\n    alias: other2\n"
	three := "binaries:\n  github.com/c/tool: {}\n  github.com/a/tool: {}\n  github.com/b/x:\n    alias: tool\n"
	files := "binaries:\n  github.com/a/tool:\n    file: ../a/tool\n  github.com/b/tool:\n    file: ../b/tool\n"
	oneFile := "binaries:\n  github.com/a/tool:\n    file: ../x/tool\n  github.com/b/tool:\n    file: ../x/tool\n  tool: {}\n"
	for _, tc := range []struct{ name, config, want string }{
		{"one file per entry", ok, ""},
		{"three entries of one file", three, "b.yaml: the entries github.com/a/tool, github.com/b/x and github.com/c/tool install the same file tool"},
		{"two refs with their own files", files, ""},
		{"two refs with one file", oneFile, "b.yaml: the entries github.com/a/tool and github.com/b/tool install the same file ../x/tool"},
	} {
		config := filepath.Join(dir, "b.yaml")
		mustWrite(t, config, []byte(tc.config))
		shared := NewSharedOptions(mkIO(), nil)
		shared.ConfigPath = config
		if err := shared.LoadConfig(); err != nil {
			t.Fatal(err)
		}
		err := checkConfigFiles(shared.Config)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: %v, want no error", tc.name, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s: %v, want %q", tc.name, err, tc.want)
		}
	}
}

// TestInstall_AliasForceDoesNotTakeOverThePresetsRow: `b install --alias
// other --force tool` installs tool at .bin/other, the file of the preset
// other. Its b.lock row says that the file is tool. A later `b install
// other` then downloads other: it must not keep tool's binary as other, and
// b.lock must not record tool's binary as other.
func TestInstall_AliasForceDoesNotTakeOverThePresetsRow(t *testing.T) {
	f := newPinsFixture(t)
	if err := f.runWith(t, func(o *InstallOptions) { o.Alias = "other"; o.Force = true }, "tool"); err != nil {
		t.Fatalf("b install --alias other --force tool: %v", err)
	}
	lk := f.readLock(t)
	f.checkRow(t, lk, "other", "tool v2.0.0\n", "v2.0.0")
	if e := lk.FindBinary("other"); e == nil || e.Binary != "tool" {
		t.Errorf("b.lock row other: %+v, want the binary tool", e)
	}

	f.hits.Store(0)
	f.install(t, false, "other")
	if n := f.hits.Load(); n != 1 {
		t.Errorf("b install other: %d downloads, want 1", n)
	}
	lk = f.readLock(t)
	f.checkRow(t, lk, "other", "other v2.0.0\n", "v2.0.0")
	if e := lk.FindBinary("other"); e == nil || e.Binary != "" {
		t.Errorf("b.lock row other: %+v, want no binary field: the file is other", e)
	}
	if err := f.verify(t); err != nil {
		t.Errorf("b verify: %v", err)
	}
}

// TestUpdate_PruneKeepsTheRowOfAnEntry: the prune keeps the row of a file
// that a b.yaml entry installs, also while the file is not there: here the
// download of `b update` fails. The row keeps the pin of the entry for the
// next install. The prune of the same `b update` removes the row gone,
// which no entry installs and whose file is not there. Then `b update`
// returns the error of the download.
func TestUpdate_PruneKeepsTheRowOfAnEntry(t *testing.T) {
	f := newPinsFixture(t)
	mustWrite(t, f.config, []byte("binaries:\n  t2:\n    alias: tool\n    version: v3.0.0\n"))
	f.writeLock(t,
		lock.BinEntry{Name: "gone", Version: "v1.0.0", SHA256: "the sha256 of .bin/gone", Preset: true},
		lock.BinEntry{Name: "t2", Version: "v3.0.0", SHA256: "the sha256 of .bin/t2", Preset: true},
	)
	f.missing["v3.0.0"] = true
	want := "download failed:\n  t2 (tool): tool v3.0.0 not found (latest: v2.0.0)"
	if err := f.cmdErr(t, "update", func(*UpdateOptions) {}); err == nil || err.Error() != want {
		t.Errorf("b update: error %v, want %q", err, want)
	}
	if f.hits.Load() == 0 {
		t.Error("no download, want one that fails")
	}
	if _, err := os.Lstat(filepath.Join(f.binDir, "t2")); !os.IsNotExist(err) {
		t.Fatalf(".bin/t2 after the failed download: %v, want no file", err)
	}
	if got := f.rowNames(t); got != "t2" {
		t.Errorf("b.lock rows after a failed `b update`: %q, want t2", got)
	}
}

// TestInstall_PresetDoesNotTakeTheRowOfARef: b.yaml has t2, an alias entry
// of the preset tool. b.lock has the row tool of a provider ref, from an
// older b, whose sha256 is the one of .bin/t2. It is not the row of t2: `b
// install t2` does not move it, so it downloads t2 (no row gives its
// version) and leaves the row tool as it is.
func TestInstall_PresetDoesNotTakeTheRowOfARef(t *testing.T) {
	f := newPinsFixture(t)
	mustWrite(t, f.config, []byte("binaries:\n  t2:\n    alias: tool\n    version: v3.0.0\n"))
	hash := f.binFile(t, "t2", "tool v3.0.0\n")
	ref := lock.BinEntry{Name: "tool", Version: "v3.0.0", SHA256: hash, Source: "fakerel://a/tool", Provider: "fakerel"}
	f.writeLock(t, ref)

	f.install(t, false, "t2")
	if n := f.hits.Load(); n != 1 {
		t.Errorf("%d downloads, want 1", n)
	}
	lk := f.readLock(t)
	if e := lk.FindBinary("tool"); e == nil || *e != ref {
		t.Errorf("b.lock row tool: %+v, want it unchanged: %+v", e, ref)
	}
	f.checkRow(t, lk, "t2", "tool v3.0.0\n", "v3.0.0")
}

// TestInstall_AbsoluteFileHasOneRowInEachCheckout: a b.yaml entry with an
// absolute `file:` outside the project has a row under that path, as b.yaml
// writes it. The name does not depend on the directory of the checkout: a
// second checkout at another depth with the same b.yaml and b.lock finds
// the row, downloads nothing and adds no row. b verify reads the file,
// because b.yaml installs it. When the entry and the file are gone, the
// prune removes the row.
func TestInstall_AbsoluteFileHasOneRowInEachCheckout(t *testing.T) {
	f := newPinsFixture(t)
	outDir := t.TempDir()
	file := filepath.Join(outDir, "custom-tool")
	cfg := []byte("binaries:\n  tool:\n    version: v2.0.0\n    file: " + file + "\n")
	mustWrite(t, f.config, cfg)
	f.install(t, false)
	want := filepath.ToSlash(file) // as b.yaml writes it
	if got := f.rowNames(t); got != want {
		t.Fatalf("b.lock rows: %q, want %q", got, want)
	}
	if err := f.verify(t); err != nil {
		t.Errorf("b verify: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(f.binDir, "b.lock"))
	if err != nil {
		t.Fatal(err)
	}

	deep := filepath.Join(t.TempDir(), "x", "y", "proj")
	f.dir, f.binDir = deep, filepath.Join(deep, ".bin")
	f.config = filepath.Join(f.binDir, "b.yaml")
	mustMkdir(t, f.binDir)
	mustWrite(t, f.config, cfg)
	mustWrite(t, filepath.Join(f.binDir, "b.lock"), data)
	t.Setenv("PATH_BIN", f.binDir)
	t.Setenv("PATH_BASE", f.dir)
	f.hits.Store(0)
	f.install(t, false)
	if n := f.hits.Load(); n != 0 {
		t.Errorf("the install in the second checkout: %d downloads, want 0", n)
	}
	if got := f.rowNames(t); got != want {
		t.Errorf("b.lock rows in the second checkout: %q, want %q", got, want)
	}
	if err := f.verify(t); err != nil {
		t.Errorf("b verify in the second checkout: %v", err)
	}

	mustWrite(t, f.config, []byte("binaries: {}\n"))
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	f.install(t, false)
	if got := f.rowNames(t); got != "" {
		t.Errorf("b.lock rows without the entry and the file: %q, want none", got)
	}
}

// TestInstall_RelativeFileOutsideTheProjectHasOneRowInEachCheckout: a
// b.yaml entry with a relative `file:` that leaves the project
// (../../tools/x, a directory beside the project) has a row under that
// path, not under an absolute path. A second checkout at another depth,
// with the same layout, the same b.yaml and b.lock and the file in its own
// tools directory, finds the row: one row, no download, and b verify
// passes in both checkouts. No row holds the path of a checkout.
func TestInstall_RelativeFileOutsideTheProjectHasOneRowInEachCheckout(t *testing.T) {
	f := newPinsFixture(t)
	cfg := []byte("binaries:\n  tool:\n    version: v2.0.0\n    file: ../../tools/x\n")
	checkout := func(dir string) {
		f.dir, f.binDir = dir, filepath.Join(dir, ".bin")
		f.config = filepath.Join(f.binDir, "b.yaml")
		mustMkdir(t, f.binDir)
		mustWrite(t, f.config, cfg)
		t.Setenv("PATH_BIN", f.binDir)
		t.Setenv("PATH_BASE", f.dir)
	}
	noCheckoutPath := func(where string, roots ...string) {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(f.binDir, "b.lock"))
		if err != nil {
			t.Fatal(err)
		}
		for _, root := range roots {
			if strings.Contains(string(data), filepath.ToSlash(root)) {
				t.Errorf("%s: b.lock holds the path %s:\n%s", where, root, data)
			}
		}
		for _, e := range f.readLock(t).Binaries {
			if filepath.IsAbs(filepath.FromSlash(e.Name)) {
				t.Errorf("%s: b.lock row %q is an absolute path", where, e.Name)
			}
		}
	}

	base := t.TempDir()
	one := filepath.Join(base, "a", "proj")
	checkout(one)
	f.install(t, false)
	if data, err := os.ReadFile(filepath.Join(base, "a", "tools", "x")); err != nil || string(data) != "tool v2.0.0\n" {
		t.Fatalf("../../tools/x of the first checkout: %q, %v; want tool v2.0.0", data, err)
	}
	if got := f.rowNames(t); got != "../../tools/x" {
		t.Errorf("b.lock rows: %q, want ../../tools/x", got)
	}
	if err := f.verify(t); err != nil {
		t.Errorf("b verify: %v", err)
	}
	noCheckoutPath("the first checkout", base)
	data, err := os.ReadFile(filepath.Join(f.binDir, "b.lock"))
	if err != nil {
		t.Fatal(err)
	}

	two := filepath.Join(base, "x", "y", "b", "proj")
	checkout(two)
	mustWrite(t, filepath.Join(f.binDir, "b.lock"), data)
	mustMkdir(t, filepath.Join(base, "x", "y", "b", "tools"))
	mustWriteExec(t, filepath.Join(base, "x", "y", "b", "tools", "x"), []byte("tool v2.0.0\n"))
	f.hits.Store(0)
	f.install(t, false)
	if n := f.hits.Load(); n != 0 {
		t.Errorf("the install in the second checkout: %d downloads, want 0", n)
	}
	if got := f.rowNames(t); got != "../../tools/x" {
		t.Errorf("b.lock rows in the second checkout: %q, want ../../tools/x", got)
	}
	if err := f.verify(t); err != nil {
		t.Errorf("b verify in the second checkout: %v", err)
	}
	noCheckoutPath("the second checkout", base)
}

// TestInstall_RelativeFileBesideBYaml: b.yaml is in the project directory,
// not in the binary directory. The row of its entry with `file: tools/x`
// is ./tools/x, relative to the directory of b.yaml (where b.lock is), and
// b verify finds the file there.
func TestInstall_RelativeFileBesideBYaml(t *testing.T) {
	f := newPinsFixture(t)
	f.config = filepath.Join(f.dir, "b.yaml")
	mustWrite(t, f.config, []byte("binaries:\n  tool:\n    version: v2.0.0\n    file: tools/x\n"))
	f.install(t, false)
	lk, err := lock.ReadLock(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(lk.Binaries) != 1 || lk.Binaries[0].Name != "./tools/x" {
		t.Errorf("b.lock rows: %+v, want the one row ./tools/x", lk.Binaries)
	}
	if err := f.verify(t); err != nil {
		t.Errorf("b verify: %v", err)
	}
}

// TestVerify_ReadsFilesInTheDirectoryOfBYaml: b.yaml is outside the
// project (b --config). b verify reads the file of a row in the directory
// of b.yaml, also when no b.yaml entry installs it.
func TestVerify_ReadsFilesInTheDirectoryOfBYaml(t *testing.T) {
	f := newPinsFixture(t)
	conf := t.TempDir()
	f.config = filepath.Join(conf, "b.yaml")
	mustWrite(t, f.config, []byte("binaries: {}\n"))
	file := filepath.Join(conf, "tools", "x")
	mustMkdir(t, filepath.Dir(file))
	mustWriteExec(t, file, []byte("x\n"))
	hash, err := lock.SHA256File(file)
	if err != nil {
		t.Fatal(err)
	}
	realConf, err := filepath.EvalSymlinks(conf)
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.ToSlash(filepath.Join(realConf, "tools", "x"))
	if err := lock.WriteLock(conf, &lock.Lock{Binaries: []lock.BinEntry{{Name: name, SHA256: hash}}}, "test"); err != nil {
		t.Fatal(err)
	}
	if err := f.verify(t); err != nil {
		t.Errorf("b verify: %v", err)
	}
}

// TestInstall_OldRowOfAnotherPresetIsDownloaded: b.yaml changed the entry
// x to `alias: other, file: ../tools/x`. An older b wrote the row x for the
// file, with the source of the preset tool. b moves the row to the name of
// the file, but the row still says that the file is tool (otherBinary): so
// `b install` downloads other, and the row then has the source of other.
func TestInstall_OldRowOfAnotherPresetIsDownloaded(t *testing.T) {
	f := newPinsFixture(t)
	mustMkdir(t, filepath.Join(f.dir, "tools"))
	mustWrite(t, f.config, []byte("binaries:\n  x:\n    alias: other\n    file: ../tools/x\n"))
	file := filepath.Join(f.dir, "tools", "x")
	mustWriteExec(t, file, []byte("tool v2.0.0\n"))
	hash, err := lock.SHA256File(file)
	if err != nil {
		t.Fatal(err)
	}
	f.writeLock(t, lock.BinEntry{Name: "x", Version: "v2.0.0", SHA256: hash, Source: "github.com/test/tool", Preset: true})
	f.install(t, false)
	if n := f.hits.Load(); n != 1 {
		t.Errorf("%d downloads, want 1", n)
	}
	if data, _ := os.ReadFile(file); string(data) != "other v2.0.0\n" {
		t.Errorf("tools/x is %q, want other v2.0.0", data)
	}
	if got := f.rowNames(t); got != "../tools/x" {
		t.Errorf("b.lock rows: %q, want ../tools/x", got)
	}
	if e := f.readLock(t).FindBinary("../tools/x"); e == nil || e.Source != "github.com/test/other" || e.Binary != "other" {
		t.Errorf("b.lock row ../tools/x: %+v, want the row of other", e)
	}
}

// TestInstall_RowWrittenAgainByAnOlderB: `b install --alias other --force
// tool` puts tool at .bin/other; its row names the binary tool. An older b
// writes b.lock again without the binary field. The row of .bin/other then
// has the source of tool, not the one of other: a later `b install other`
// downloads other, and does not keep tool's binary as other.
func TestInstall_RowWrittenAgainByAnOlderB(t *testing.T) {
	f := newPinsFixture(t)
	if err := f.runWith(t, func(o *InstallOptions) { o.Alias = "other"; o.Force = true }, "tool"); err != nil {
		t.Fatalf("b install --alias other --force tool: %v", err)
	}
	rows := f.readLock(t).Binaries
	for i := range rows {
		rows[i].Binary = ""
	}
	f.writeLock(t, rows...)

	f.hits.Store(0)
	f.install(t, false, "other")
	if n := f.hits.Load(); n != 1 {
		t.Errorf("b install other: %d downloads, want 1", n)
	}
	lk := f.readLock(t)
	f.checkRow(t, lk, "other", "other v2.0.0\n", "v2.0.0")
	if e := lk.FindBinary("other"); e == nil || e.Source != "github.com/test/other" {
		t.Errorf("b.lock row other: %+v, want the source of other", e)
	}
}

// TestOtherBinary: a b.lock row says that its file is another binary when
// its binary field names another binary, when it is the row of a provider
// ref and b is a preset, or the row of a preset or of another ref and b is
// a provider ref. A row without a binary field (from an older b) of the same
// kind is not another binary, unless it has the source of another preset.
func TestOtherBinary(t *testing.T) {
	preset := &binary.Binary{Name: "tool"}
	repo := &binary.Binary{Name: "tool", GitHubRepo: "org/tool"}
	alias := &binary.Binary{Name: "tool", Alias: "t2"}
	ref := &binary.Binary{Name: "tool", AutoDetect: true, ProviderRef: "fakerel://a/tool"}
	refAlias := &binary.Binary{Name: "tool", Alias: "ta", AutoDetect: true, ProviderRef: "fakerel://a/tool"}
	for _, tc := range []struct {
		name string
		row  lock.BinEntry
		b    *binary.Binary
		want bool
	}{
		{"a preset row of an older b", lock.BinEntry{Name: "tool", Preset: true}, preset, false},
		{"an alias row of an older b", lock.BinEntry{Name: "t2", Preset: true}, alias, false},
		{"an alias row", lock.BinEntry{Name: "t2", Binary: "tool", Preset: true}, alias, false},
		{"a row of another preset", lock.BinEntry{Name: "tool", Binary: "other", Preset: true}, preset, true},
		{"a row of a provider ref, b a preset", lock.BinEntry{Name: "tool", Source: "fakerel://a/tool", Provider: "fakerel"}, preset, true},
		{"a row of the ref", lock.BinEntry{Name: "tool", Source: "fakerel://a/tool", Provider: "fakerel"}, ref, false},
		{"an alias row of the ref", lock.BinEntry{Name: "ta", Binary: "tool", Source: "fakerel://a/tool", Provider: "fakerel"}, refAlias, false},
		{"a row of another ref", lock.BinEntry{Name: "tool", Source: "fakerel://b/tool", Provider: "fakerel"}, ref, true},
		{"a preset row, b a provider ref", lock.BinEntry{Name: "tool", Preset: true}, ref, true},
		{"a preset row of an older b with b's source", lock.BinEntry{Name: "tool", Preset: true, Source: "github.com/org/tool"}, repo, false},
		{"a preset row of an older b with another source", lock.BinEntry{Name: "tool", Preset: true, Source: "github.com/org/other"}, repo, true},
		{"a preset row of an older b without a source", lock.BinEntry{Name: "tool", Preset: true}, repo, false},
		{"a preset row with another source, b without one", lock.BinEntry{Name: "tool", Preset: true, Source: "github.com/org/other"}, preset, false},
		{"a row of b with another source", lock.BinEntry{Name: "tool", Binary: "tool", Preset: true, Source: "github.com/org/other"}, repo, false},
	} {
		if got := otherBinary(&tc.row, tc.b); got != tc.want {
			t.Errorf("%s: otherBinary = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestInstall_SymlinkedProjectHasOneRow: the name of the row of a file
// outside the binary directory does not depend on the way to the project.
// A b.yaml reached through a symlink, or a binary directory reached through
// a symlink, gives the row the same name as the real paths: ../tools/t3.
// So a run through the real paths finds the row, downloads nothing and adds
// no second row.
func TestInstall_SymlinkedProjectHasOneRow(t *testing.T) {
	for _, viaLink := range []string{"b.yaml", "the binary directory"} {
		t.Run(viaLink+" through a symlink", func(t *testing.T) {
			f := newPinsFixture(t)
			link := filepath.Join(t.TempDir(), "link")
			if err := os.Symlink(f.dir, link); err != nil {
				t.Skip(err)
			}
			mustMkdir(t, filepath.Join(f.dir, "tools"))
			mustWrite(t, f.config, []byte("binaries:\n  t3:\n    alias: tool\n    version: v1.0.0\n    file: ../tools/t3\n"))
			config := f.config
			if viaLink == "b.yaml" {
				f.config = filepath.Join(link, ".bin", "b.yaml")
			} else {
				t.Setenv("PATH_BIN", filepath.Join(link, ".bin"))
			}
			f.install(t, false)
			if got := f.rowNames(t); got != "../tools/t3" {
				t.Errorf("b.lock rows through the symlink: %q, want ../tools/t3", got)
			}

			f.config = config
			t.Setenv("PATH_BIN", f.binDir)
			f.hits.Store(0)
			f.install(t, false)
			if n := f.hits.Load(); n != 0 {
				t.Errorf("the install through the real paths: %d downloads, want 0", n)
			}
			if got := f.rowNames(t); got != "../tools/t3" {
				t.Errorf("b.lock rows: %q, want ../tools/t3", got)
			}
			if err := f.verify(t); err != nil {
				t.Errorf("b verify: %v", err)
			}
		})
	}
}
