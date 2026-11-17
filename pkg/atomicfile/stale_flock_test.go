//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package atomicfile

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// newTemp is a new temp file in a test directory, as os.CreateTemp of
// Create makes it, without a lock.
func newTemp(t *testing.T) *os.File {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), ".tool.*.tmp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

// lockTemp locks a new temp file: another open file of it gets no lock.
func TestLockTemp_LocksANewTempFile(t *testing.T) {
	f := newTemp(t)
	if !lockTemp(f) {
		t.Fatal("lockTemp: false for a new temp file")
	}
	g, err := os.Open(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	if locked, err := flock(g); err != nil || locked {
		t.Errorf("flock of another open file: %v, %v; want no lock", locked, err)
	}
}

// A cleanup of another process holds the lock of the new temp file (it
// found the file before Create locked it): the file is not the write's.
func TestLockTemp_ATempFileThatACleanupHolds(t *testing.T) {
	f := newTemp(t)
	g, err := os.Open(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	if locked, err := flock(g); err != nil || !locked {
		t.Fatalf("flock: %v, %v", locked, err)
	}
	if lockTemp(f) {
		t.Error("lockTemp: true for a temp file whose lock a cleanup holds")
	}
}

// A cleanup of another process removed the new temp file before Create
// locked it: the file is not the write's.
func TestLockTemp_ATempFileThatACleanupRemoved(t *testing.T) {
	f := newTemp(t)
	if err := os.Remove(f.Name()); err != nil {
		t.Fatal(err)
	}
	if lockTemp(f) {
		t.Error("lockTemp: true for a temp file that a cleanup removed")
	}
}

// Commit holds the lock of the temp file until the file is in place: a
// RemoveStale of another process in the moment before the rename leaves
// the temp file, and the write commits with its mode. When Commit closed
// the temp file before the rename, that RemoveStale removed it, and the
// rename failed.
func TestCommit_HoldsTheLockUntilTheRename(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tool")
	if err := os.WriteFile(path, []byte("old"), 0755); err != nil {
		t.Fatal(err)
	}
	var removed []string
	var removeErr error
	runs := 0
	orig := beforeRename
	t.Cleanup(func() { beforeRename = orig })
	beforeRename = func() {
		runs++
		removed, removeErr = RemoveStale(path)
	}
	if err := Write(path, 0750, writeString("new")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if runs != 1 {
		t.Fatalf("the hook before the rename ran %d times, want 1", runs)
	}
	if removeErr != nil || len(removed) > 0 {
		t.Errorf("a RemoveStale before the rename: removed %v, error %v; want nothing", removed, removeErr)
	}
	if got := read(t, path); got != "new" {
		t.Errorf("the file is %q, want %q", got, "new")
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0750 {
		t.Errorf("the mode is %v, want %v", got, os.FileMode(0750))
	}
	noTemp(t, dir)
}

// A temp file that a symlink replaces between the Lstat and the open of
// RemoveStale stays, with no error: the open with O_NOFOLLOW fails with the
// symlink error of this system (symlinkErrs). The target of the link stays
// as it was.
func TestRemoveStale_ASymlinkPutInPlaceStaysWithoutAnError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tool")
	stale := filepath.Join(dir, ".tool.7.tmp")
	target := filepath.Join(dir, "target")
	for _, p := range []string{stale, target} {
		if err := os.WriteFile(p, []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	runs := 0
	orig := beforeOpen
	t.Cleanup(func() { beforeOpen = orig })
	beforeOpen = func() {
		runs++
		if err := os.Remove(stale); err != nil {
			t.Error(err)
		}
		if err := os.Symlink(target, stale); err != nil {
			t.Error(err)
		}
	}
	removed, err := RemoveStale(path)
	if runs != 1 {
		t.Fatalf("the hook before the open ran %d times, want 1", runs)
	}
	if err != nil || len(removed) > 0 {
		t.Errorf("removed %v, error %v; want nothing and no error", removed, err)
	}
	if fi, err := os.Lstat(stale); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the symlink %s: %v, %v; want it to stay", stale, fi, err)
	}
	if got := read(t, target); got != "x" {
		t.Errorf("the target is %q, want %q", got, "x")
	}
}

// A stale temp file that b cannot open for writing (its mode has no write
// bit) is opened read-only, which is enough for flock: it goes.
func TestRemoveStale_ATempFileWithoutWriteBitGoes(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root opens any file for writing")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "tool")
	stale := filepath.Join(dir, ".tool.8.tmp")
	if err := os.WriteFile(stale, []byte("x"), 0400); err != nil {
		t.Fatal(err)
	}
	removed, err := RemoveStale(path)
	if err != nil || !slices.Equal(removed, []string{stale}) {
		t.Errorf("removed %v, error %v; want [%s]", removed, err, stale)
	}
}
