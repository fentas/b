package lock

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// writeOldLock writes a b.lock with one entry and the mode 0600 to dir and
// gives its content.
func writeOldLock(t *testing.T, dir string) string {
	t.Helper()
	old := &Lock{Binaries: []BinEntry{{Name: "old", Version: "v1.0.0", SHA256: "aaa"}}}
	if err := WriteLock(dir, old, "v1.0.0"); err != nil {
		t.Fatalf("WriteLock: %v", err)
	}
	path := filepath.Join(dir, lockFileName)
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// noLockTemp fails the test when a temp file of the write is in dir.
func noLockTemp(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("temp file %s is still in %s", e.Name(), dir)
		}
	}
}

// newLock is a lock with other entries than the one of writeOldLock.
func newLock() *Lock {
	return &Lock{Binaries: []BinEntry{{Name: "new", Version: "v2.0.0", SHA256: "bbb"}}}
}

// TestWriteLock_ReplacesTheFileInOneStep: WriteLock puts a new b.lock in
// place of the old one. A hard link to the old b.lock still has the old
// content, so WriteLock did not write into the old file. The new b.lock
// keeps the mode of the old one.
func TestWriteLock_ReplacesTheFileInOneStep(t *testing.T) {
	dir := t.TempDir()
	old := writeOldLock(t, dir)
	path := filepath.Join(dir, lockFileName)
	link := filepath.Join(dir, "link")
	if err := os.Link(path, link); err != nil {
		t.Skipf("no hard links here: %v", err)
	}

	if err := WriteLock(dir, newLock(), "v2.0.0"); err != nil {
		t.Fatalf("WriteLock: %v", err)
	}
	got, err := ReadLock(dir)
	if err != nil {
		t.Fatalf("ReadLock: %v", err)
	}
	if len(got.Binaries) != 1 || got.Binaries[0].Name != "new" {
		t.Errorf("b.lock has %+v, want the entry %q", got.Binaries, "new")
	}
	data, err := os.ReadFile(link)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != old {
		t.Errorf("the hard link to the old b.lock is\n%s\nwant the old content: WriteLock wrote into the old file", data)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0600 {
			t.Errorf("mode %v, want 0600, the mode of the old b.lock", fi.Mode().Perm())
		}
	}
	noLockTemp(t, dir)
}

// TestWriteLock_FailedWriteKeepsTheOldLock: when the new b.lock cannot be
// written (here: the directory is read-only, so the temp file cannot be
// made), WriteLock returns the error and the old b.lock stays as it was.
func TestWriteLock_FailedWriteKeepsTheOldLock(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a read-only directory does not stop a new file on windows")
	}
	dir := t.TempDir()
	old := writeOldLock(t, dir)
	if err := os.Chmod(dir, 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0755) })
	if f, err := os.CreateTemp(dir, "probe"); err == nil {
		f.Close()
		t.Skip("the read-only directory is still writable (root?)")
	}

	if err := WriteLock(dir, newLock(), "v2.0.0"); err == nil {
		t.Fatal("WriteLock in a read-only directory: no error")
	}
	data, err := os.ReadFile(filepath.Join(dir, lockFileName))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != old {
		t.Errorf("b.lock is\n%s\nwant the old b.lock", data)
	}
	noLockTemp(t, dir)
}
