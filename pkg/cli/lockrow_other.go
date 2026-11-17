//go:build !unix

package cli

import (
	"errors"
	"io/fs"
)

// openFlags open the file of a b.lock row for its sha256. The system has
// no FIFOs that block an open, and regularFileSHA256 refuses a symlink
// before it opens the file.
const openFlags = 0

// brokenPath reports whether err says that no file is at a path.
func brokenPath(err error) bool {
	return errors.Is(err, fs.ErrNotExist)
}
