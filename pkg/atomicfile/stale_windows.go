//go:build windows

package atomicfile

import (
	"errors"
	"io/fs"
	"os"
)

// lockTemp takes no lock on Windows, and reports true: Go opens a file
// without FILE_SHARE_DELETE, so Windows refuses to remove a temp file that a
// process has open (removeIfStale), also right after its creation.
func lockTemp(*os.File) bool { return true }

// removeIfStale removes the temp file at file when it is a regular file.
// Windows refuses to remove a file that a process has open, so a temp file
// in use stays; any error of the remove leaves the file and gives no error.
// It reports whether it removed the file.
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
	return os.Remove(file) == nil, nil
}
