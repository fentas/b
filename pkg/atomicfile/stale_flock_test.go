//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package atomicfile

import (
	"os"
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
