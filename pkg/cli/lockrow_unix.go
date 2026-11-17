//go:build unix

package cli

import (
	"errors"
	"io/fs"
	"syscall"
)

// openFlags open the file of a b.lock row for its sha256: a FIFO does not
// block the open, and the open does not follow a symlink (the path has no
// symlinks, see regularFileSHA256).
const openFlags = syscall.O_NONBLOCK | syscall.O_NOFOLLOW

// brokenPath reports whether err says that no file is at a path: the path
// does not exist, a part of it is a file, not a directory (plain/sub), or
// its symlinks make a loop.
func brokenPath(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) || errors.Is(err, syscall.ELOOP)
}
