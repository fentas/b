//go:build linux

package atomicfile

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
)

// fcntlLock is the lock that Linux takes for a flock on NFS: a POSIX write
// lock (F_SETLK) on the whole file. It needs a file that is open for
// writing.
func fcntlLock(f *os.File) (bool, error) {
	c, err := f.SyscallConn()
	if err != nil {
		return false, err
	}
	var lockErr error
	if err := c.Control(func(fd uintptr) {
		lockErr = syscall.FcntlFlock(fd, syscall.F_SETLK, &syscall.Flock_t{Type: syscall.F_WRLCK})
	}); err != nil {
		return false, err
	}
	if errors.Is(lockErr, syscall.EAGAIN) || errors.Is(lockErr, syscall.EACCES) {
		return false, nil
	}
	return lockErr == nil, lockErr
}

// With the lock of flock on NFS, RemoveStale still removes a stale temp
// file: it opens the file read-write. A read-only open gets EBADF from the
// lock, and every stale temp file on NFS stayed.
func TestRemoveStale_WithTheNFSLock(t *testing.T) {
	orig := lockFile
	t.Cleanup(func() { lockFile = orig })
	lockFile = fcntlLock
	dir := t.TempDir()
	path := filepath.Join(dir, "tool")
	stale := filepath.Join(dir, ".tool.5.tmp")
	if err := os.WriteFile(stale, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	// The lock is the one of NFS: a read-only file gets no lock.
	ro, err := os.Open(stale)
	if err != nil {
		t.Fatal(err)
	}
	if locked, err := fcntlLock(ro); locked || !errors.Is(err, syscall.EBADF) {
		t.Fatalf("the lock on a read-only file: %v, %v; want EBADF", locked, err)
	}
	_ = ro.Close()
	removed, err := RemoveStale(path)
	if err != nil || !slices.Equal(removed, []string{stale}) {
		t.Errorf("removed %v, error %v; want [%s]", removed, err, stale)
	}
}
