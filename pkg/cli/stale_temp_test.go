package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/fentas/b/pkg/atomicfile"
	"github.com/fentas/b/pkg/binary"
)

// flockHere reports whether atomicfile can tell a temp file in use from a
// stale one on this platform (its flock).
func flockHere() bool {
	switch runtime.GOOS {
	case "linux", "darwin", "freebsd", "netbsd", "openbsd", "dragonfly":
		return true
	}
	return false
}

// notStale are files in .bin that are not temp files of tool: b removes
// none of them.
var notStale = []string{".tool.tmp", ".tool.abc.tmp", ".tool.1.2.tmp", ".toolx.123.tmp", "tool.123.tmp", ".other.123.tmp", ".b.lock.123.tmp"}

// staleFixture is a pins fixture with tool v1.0.0 (in b.yaml and b.lock),
// the stale temp file .bin/.tool.123.tmp that a stopped write left, and the
// files of notStale.
func staleFixture(t *testing.T) *pinsFixture {
	t.Helper()
	if !flockHere() {
		t.Skip("no flock here: b leaves every temp file")
	}
	f := newPinsFixture(t)
	mustWrite(t, f.config, []byte("binaries:\n  tool: {}\n"))
	f.present(t, "v1.0.0", true)
	for _, name := range append([]string{".tool.123.tmp"}, notStale...) {
		mustWrite(t, filepath.Join(f.binDir, name), []byte("part"))
	}
	return f
}

// exists reports whether .bin/<name> is there.
func (f *pinsFixture) exists(name string) bool {
	_, err := os.Lstat(filepath.Join(f.binDir, name))
	return err == nil
}

// leftNotStale fails the test when a file of notStale is gone.
func (f *pinsFixture) leftNotStale(t *testing.T, cmd string) {
	t.Helper()
	for _, name := range notStale {
		if !f.exists(name) {
			t.Errorf("after %s, .bin/%s is gone: it is not a temp file of tool", cmd, name)
		}
	}
}

// TestInstallAndUpdate_RemoveStaleTempFiles: b install and b update remove
// the temp file that a stopped write of the binary left, and name it. This
// also happens when the install keeps the file (no download). Files of
// other names stay.
func TestInstallAndUpdate_RemoveStaleTempFiles(t *testing.T) {
	for _, args := range [][]string{{"install", "tool"}, {"update", "tool"}} {
		cmd := "b " + strings.Join(args, " ")
		t.Run(cmd, func(t *testing.T) {
			f := staleFixture(t)
			stderr, err := f.runB(t, f.presets(), args...)
			if err != nil {
				t.Fatalf("%s: %v", cmd, err)
			}
			if f.exists(".tool.123.tmp") {
				t.Errorf("after %s, the stale temp file .bin/.tool.123.tmp is still there", cmd)
			}
			if !strings.Contains(stderr, ".tool.123.tmp: a stopped install or update of tool left this temp file") {
				t.Errorf("%s does not name the temp file it removed; stderr:\n%s", cmd, stderr)
			}
			f.leftNotStale(t, cmd)
		})
	}
}

// TestInstall_KeepsATempFileInUse: a temp file that a write in progress
// holds (another b, here atomicfile.Create of the test) stays. The stale one
// goes in the same run.
func TestInstall_KeepsATempFileInUse(t *testing.T) {
	f := staleFixture(t)
	w, err := atomicfile.Create(filepath.Join(f.binDir, "tool"))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Abort()
	before := tempFiles(t, f.binDir)
	if _, err := f.runB(t, f.presets(), "install", "tool"); err != nil {
		t.Fatalf("b install tool: %v", err)
	}
	after := tempFiles(t, f.binDir)
	if f.exists(".tool.123.tmp") {
		t.Error("the stale temp file .bin/.tool.123.tmp is still there")
	}
	// The temp file in use is the one temp file of tool that is not
	// .tool.123.tmp; it must stay.
	var inUse string
	for _, name := range before {
		if name != ".tool.123.tmp" && strings.HasPrefix(name, ".tool.") && !slices.Contains(notStale, name) {
			inUse = name
		}
	}
	if inUse == "" {
		t.Fatalf("no temp file of the write in progress among %v", before)
	}
	if !slices.Contains(after, inUse) {
		t.Errorf("b install removed the temp file in use %s", inUse)
	}
	if _, err := w.Write([]byte("new")); err != nil {
		t.Fatal(err)
	}
	if err := w.Commit(0755); err != nil {
		t.Errorf("the write in progress cannot commit: %v", err)
	}
}

// TestUpdateDryRun_KeepsStaleTempFiles: b update --dry-run removes no file.
func TestUpdateDryRun_KeepsStaleTempFiles(t *testing.T) {
	f := staleFixture(t)
	if _, err := f.runB(t, f.presets(), "update", "--dry-run", "tool"); err != nil {
		t.Fatalf("b update --dry-run tool: %v", err)
	}
	if !f.exists(".tool.123.tmp") {
		t.Error("b update --dry-run removed .bin/.tool.123.tmp")
	}
}

// TestVerifyAndPrune_IgnoreTempFiles: b verify and the b.lock prune read the
// b.lock rows, not the binary directory. A stale temp file beside a binary
// does not make b verify fail, gets no b.lock row, and stays: neither of
// them removes a file.
func TestVerifyAndPrune_IgnoreTempFiles(t *testing.T) {
	f := staleFixture(t)
	if err := f.verify(t); err != nil {
		t.Errorf("b verify with a stale temp file: %v", err)
	}
	shared := NewSharedOptions(mkIO(), f.presets())
	shared.ConfigPath = f.config
	if err := shared.LoadConfig(); err != nil {
		t.Fatal(err)
	}
	removed, err := shared.pruneLock()
	if err != nil || len(removed) > 0 {
		t.Errorf("pruneLock: removed %v, error %v; want nothing", removed, err)
	}
	if got := f.rowNames(t); got != "tool" {
		t.Errorf("b.lock rows: %s, want tool", got)
	}
	if !f.exists(".tool.123.tmp") {
		t.Error("b verify or the prune removed .bin/.tool.123.tmp")
	}
	f.leftNotStale(t, "b verify and the prune")
}

// TestRemoveStaleTemps_KeepsFileEmpty: the cleanup finds the file of a
// binary without a File (the binary directory and its name), and does not
// set File: b update reads an empty File as "not known yet".
func TestRemoveStaleTemps_KeepsFileEmpty(t *testing.T) {
	f := staleFixture(t)
	b := &binary.Binary{Name: "tool"}
	NewSharedOptions(mkIO(), nil).removeStaleTemps([]*binary.Binary{b})
	if b.File != "" {
		t.Errorf("File is %q, want it empty", b.File)
	}
	if f.exists(".tool.123.tmp") {
		t.Error("the stale temp file .bin/.tool.123.tmp is still there")
	}
	f.leftNotStale(t, "removeStaleTemps")
}
