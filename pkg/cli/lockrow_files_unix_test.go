//go:build unix

package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/fentas/b/pkg/lock"
	"github.com/fentas/goodies/streams"
)

// noBlock runs fn. It fails the test when fn does not return within 5
// seconds: fn blocks on the FIFO fifo ("" for none), which noBlock then
// opens for writing, so that fn returns.
func noBlock(t *testing.T, fifo string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		if fifo != "" {
			if w, err := os.OpenFile(fifo, os.O_WRONLY, 0); err == nil {
				_ = w.Close()
			}
		}
		<-done
		t.Fatalf("blocked on the FIFO %s", fifo)
	}
}

// verifyLines runs `b verify` in the fixture and returns the status of each
// b.lock row (and each env file) by its name, and the error of verify. It
// fails the test when verify blocks on the FIFO fifo (noBlock).
func (f *pinsFixture) verifyLines(t *testing.T, fifo string) (map[string]string, error) {
	t.Helper()
	out := &bytes.Buffer{}
	var err error
	noBlock(t, fifo, func() {
		shared := NewSharedOptions(&streams.IO{In: strings.NewReader(""), Out: out, ErrOut: &bytes.Buffer{}}, f.presets())
		shared.ConfigPath = f.config
		if err = shared.LoadConfig(); err == nil {
			err = (&VerifyOptions{SharedOptions: shared}).Run()
		}
	})
	lines := map[string]string{}
	for _, line := range strings.Split(out.String(), "\n") {
		if fields := strings.Fields(line); len(fields) > 1 {
			lines[fields[0]] = strings.Join(fields[1:], " ")
		}
	}
	return lines, err
}

// TestVerify_ReadsOnlyRegularFilesInTheProject: a b.lock row names its file
// by a path, so a crafted row can name any file. b verify and the prune read
// only a regular file inside the project or the binary directory. b verify
// reports a row outside them, also through a symlink, as outside and does
// not read it (the sha256 of the outside file is the one of the row, so a
// read gives ✓). It reports a FIFO as not a regular file and does not block
// on it. A dangling symlink is a missing file for b verify and for the
// prune, which removes its row; the prune keeps the other rows.
func TestVerify_ReadsOnlyRegularFilesInTheProject(t *testing.T) {
	f := newPinsFixture(t)
	outside := filepath.Join(t.TempDir(), "secret")
	mustWrite(t, outside, []byte("secret\n"))
	hash, err := lock.SHA256File(outside)
	if err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(f.binDir, outside)
	if err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(f.binDir, "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skip(err)
	}
	if err := os.Symlink(outside, filepath.Join(f.binDir, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(f.binDir, "nothing"), filepath.Join(f.binDir, "dangling")); err != nil {
		t.Fatal(err)
	}
	good := f.binFile(t, "good", "good\n")
	f.writeLock(t,
		lock.BinEntry{Name: outside, SHA256: hash},
		lock.BinEntry{Name: filepath.ToSlash(rel), SHA256: hash},
		lock.BinEntry{Name: "link", SHA256: hash},
		lock.BinEntry{Name: "fifo", SHA256: hash},
		lock.BinEntry{Name: "dangling", SHA256: hash},
		lock.BinEntry{Name: "good", SHA256: good},
	)

	lines, err := f.verifyLines(t, fifo)
	if err == nil {
		t.Error("b verify: no error, want the rows that differ")
	}
	for name, want := range map[string]string{
		outside:               "✗ " + msgOutside,
		filepath.ToSlash(rel): "✗ " + msgOutside,
		"link":                "✗ " + msgOutside,
		"fifo":                "✗ not a regular file",
		"dangling":            "✗ missing",
		"good":                "✓",
	} {
		if got := lines[name]; got != want {
			t.Errorf("b verify, row %s: %q, want %q", name, got, want)
		}
	}

	f.install(t, false)
	if got, want := f.rowNames(t), strings.Join([]string{outside, filepath.ToSlash(rel), "link", "fifo", "good"}, " "); got != want {
		t.Errorf("b.lock rows after `b install`: %q, want %q", got, want)
	}
}

// TestVerify_BrokenPathsAreMissingFiles: a symlink loop, a path through a
// file (plain/sub) and a path outside the project where no file is, are no
// files. b verify reports them as missing, and the prune removes their rows
// (no b.yaml entry installs them). b finds out only whether a file is at a
// path outside the project; it does not read it.
func TestVerify_BrokenPathsAreMissingFiles(t *testing.T) {
	f := newPinsFixture(t)
	if err := os.Symlink("loop", filepath.Join(f.binDir, "loop")); err != nil {
		t.Fatal(err)
	}
	f.binFile(t, "plain", "plain\n")
	f.writeLock(t,
		lock.BinEntry{Name: "loop", SHA256: "a"},
		lock.BinEntry{Name: "plain/sub", SHA256: "b"},
		lock.BinEntry{Name: "../../nothing-outside/x", SHA256: "c"},
	)
	lines, err := f.verifyLines(t, "")
	if err == nil {
		t.Error("b verify: no error, want the missing files")
	}
	for _, name := range []string{"loop", "plain/sub", "../../nothing-outside/x"} {
		if got := lines[name]; got != "✗ missing" {
			t.Errorf("b verify, row %s: %q, want ✗ missing", name, got)
		}
	}
	f.install(t, false)
	if got := f.rowNames(t); got != "" {
		t.Errorf("b.lock rows after `b install`: %q, want none", got)
	}
}

// TestVerify_PathThatBCannotFollow: b cannot follow the path of a row
// through a directory without permission. It cannot tell whether the file
// is there: b verify reports the error, and the prune keeps the row.
func TestVerify_PathThatBCannotFollow(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root follows each path")
	}
	f := newPinsFixture(t)
	locked := filepath.Join(f.binDir, "locked")
	mustMkdir(t, locked)
	mustWrite(t, filepath.Join(locked, "x"), []byte("x\n"))
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	f.writeLock(t, lock.BinEntry{Name: "locked/x", SHA256: "a"})
	lines, err := f.verifyLines(t, "")
	if err == nil {
		t.Error("b verify: no error, want the error of the path")
	}
	if got := lines["locked/x"]; !strings.Contains(got, "permission denied") {
		t.Errorf("b verify, row locked/x: %q, want the error permission denied", got)
	}
	f.install(t, false)
	if got := f.rowNames(t); got != "locked/x" {
		t.Errorf("b.lock rows after `b install`: %q, want locked/x", got)
	}
}

// TestRegularFileSHA256: b reads only a regular file, and a FIFO does not
// block it. regularFileSHA256 takes a path without symlinks and refuses a
// symlink before it opens it; fileSHA256 follows the symlinks first.
// describesFile (the check of the file of a binary against its row) does
// not block on a FIFO either.
func TestRegularFileSHA256(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	mustWrite(t, file, []byte("file\n"))
	want, err := lock.SHA256File(file)
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skip(err)
	}
	if got, err := regularFileSHA256(file); err != nil || got != want {
		t.Errorf("regularFileSHA256(file) = %q, %v; want %q", got, err, want)
	}
	if got, err := fileSHA256(link); err != nil || got != want {
		t.Errorf("fileSHA256(symlink) = %q, %v; want %q", got, err, want)
	}
	for name, p := range map[string]string{"a symlink": link, "a directory": dir, "a FIFO": fifo} {
		noBlock(t, fifo, func() {
			if _, err := regularFileSHA256(p); !errors.Is(err, errNotRegular) {
				t.Errorf("regularFileSHA256(%s): %v, want %v", name, err, errNotRegular)
			}
		})
	}
	noBlock(t, fifo, func() {
		if describesFile(&lock.BinEntry{SHA256: want}, fifo) {
			t.Error("describesFile(FIFO) = true, want false")
		}
	})
}

// TestVerify_EnvFileFIFODoesNotBlock: b verify reads an env file only when
// it is a regular file, and a FIFO does not block it.
func TestVerify_EnvFileFIFODoesNotBlock(t *testing.T) {
	f := newPinsFixture(t)
	fifo := filepath.Join(f.dir, "fifo.yaml")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skip(err)
	}
	lk := &lock.Lock{Envs: []lock.EnvEntry{{Ref: "github.com/org/infra", Files: []lock.LockFile{{Path: "fifo.yaml", Dest: "fifo.yaml", SHA256: "a"}}}}}
	if err := lock.WriteLock(f.binDir, lk, "test"); err != nil {
		t.Fatal(err)
	}
	lines, err := f.verifyLines(t, fifo)
	if err == nil {
		t.Error("b verify: no error, want the FIFO")
	}
	if got := lines["fifo.yaml"]; got != "✗ not a regular file" {
		t.Errorf("b verify, env file fifo.yaml: %q, want ✗ not a regular file", got)
	}
}
