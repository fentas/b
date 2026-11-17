//go:build unix

package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/fentas/b/pkg/lock"
	"github.com/fentas/goodies/streams"
)

// verifyLines runs `b verify` in the fixture and returns the status of each
// b.lock row by its name, and the error of verify. It fails the test when
// verify does not return within 5 seconds (it blocks on a FIFO).
func (f *pinsFixture) verifyLines(t *testing.T, fifo string) (map[string]string, error) {
	t.Helper()
	type result struct {
		out string
		err error
	}
	done := make(chan result, 1)
	go func() {
		out := &bytes.Buffer{}
		shared := NewSharedOptions(&streams.IO{In: strings.NewReader(""), Out: out, ErrOut: &bytes.Buffer{}}, f.presets())
		shared.ConfigPath = f.config
		err := (&VerifyOptions{SharedOptions: shared}).Run()
		done <- result{out.String(), err}
	}()
	var r result
	select {
	case r = <-done:
	case <-time.After(5 * time.Second):
		// Open the FIFO for writing, so that verify returns.
		if w, err := os.OpenFile(fifo, os.O_WRONLY, 0); err == nil {
			_ = w.Close()
		}
		<-done
		t.Fatalf("b verify blocked on the FIFO %s", fifo)
	}
	lines := map[string]string{}
	for _, line := range strings.Split(r.out, "\n") {
		if fields := strings.Fields(line); len(fields) > 1 {
			lines[fields[0]] = strings.Join(fields[1:], " ")
		}
	}
	return lines, r.err
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
		outside:               "✗ outside the project and the binary directory (not read)",
		filepath.ToSlash(rel): "✗ outside the project and the binary directory (not read)",
		"link":                "✗ outside the project and the binary directory (not read)",
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
