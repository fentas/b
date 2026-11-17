//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package atomicfile

import (
	"errors"
	"io/fs"
	"os"
	"syscall"
)

// lockTemp takes an exclusive flock on the new temp file f of Create, and
// reports whether f is still the file of this write. It is not when a
// RemoveStale of another process found f before the lock: that RemoveStale
// holds the lock, or it removed the name.
//
// The process holds the lock until it closes f (Commit, Abort, or its end,
// also a kill), so RemoveStale of another process leaves f while it is in
// use. A filesystem without flock gives an error: f then has no lock, and
// RemoveStale cannot take one either, so it leaves f too.
func lockTemp(f *os.File) bool {
	locked, err := flock(f)
	if err != nil {
		return true
	}
	if !locked {
		return false
	}
	now, err := os.Lstat(f.Name())
	if errors.Is(err, fs.ErrNotExist) {
		return false
	}
	if err != nil {
		return true
	}
	opened, err := f.Stat()
	return err != nil || os.SameFile(opened, now)
}

// flock takes an exclusive flock on f without waiting. It reports false
// when another open file holds one, also in this process: a flock belongs
// to an open file, not to the process.
func flock(f *os.File) (bool, error) {
	c, err := f.SyscallConn()
	if err != nil {
		return false, err
	}
	var lockErr error
	if err := c.Control(func(fd uintptr) {
		lockErr = syscall.Flock(int(fd), syscall.LOCK_EX|syscall.LOCK_NB)
	}); err != nil {
		return false, err
	}
	if errors.Is(lockErr, syscall.EWOULDBLOCK) {
		return false, nil
	}
	return lockErr == nil, lockErr
}

// removeIfStale removes the temp file at file when it is a regular file and
// no process holds the flock of Create on it. It reports whether it removed
// the file. A file that a process uses, a file of another type, a file that
// is gone, and a file that b cannot lock (no flock on that filesystem) stay
// and give no error.
func removeIfStale(file string) (bool, error) {
	fi, err := os.Lstat(file)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if !fi.Mode().IsRegular() {
		return false, nil
	}
	// O_NOFOLLOW: a symlink put in its place is not opened. O_NONBLOCK: a
	// FIFO put in its place does not block the open.
	f, err := os.OpenFile(file, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ELOOP) {
			return false, nil
		}
		return false, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return false, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(fi, opened) {
		return false, nil
	}
	if locked, err := flock(f); err != nil || !locked {
		return false, nil
	}
	// The lock is on the file that was opened. Remove the name only while
	// it still names that file.
	if now, err := os.Lstat(file); err != nil || !os.SameFile(now, opened) {
		return false, nil
	}
	if err := os.Remove(file); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
