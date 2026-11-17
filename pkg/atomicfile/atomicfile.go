// Package atomicfile replaces a file in one step. The new content goes into a
// temp file in the directory of the file, and a rename puts it in place. A
// reader sees the old file or the new one, never a part of the new one. When
// a write fails, the temp file is removed and the old file stays as it was.
//
// The rename makes a new file. Thus:
//   - The directory of the file must be writable. When the path is a symlink,
//     this is the directory of the file that the link points to.
//   - The new file belongs to the user who runs the program, with the mode
//     that Commit gives. The owner of the old file does not carry over.
//   - A hard link to the old file keeps the old content.
//
// A write that is stopped before Commit or Abort (Ctrl-C, a kill, a crash)
// leaves its temp file beside the file. RemoveStale removes such temp files
// of a file.
package atomicfile

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// errDone is the error of a Commit after Commit or Abort.
var errDone = errors.New("atomicfile: the file is already committed or aborted")

// File is the new content of a file, in a temp file beside it. Commit puts it
// in place of the file, and Abort removes it. Until Commit, the file stays as
// it was.
type File struct {
	tmp  *os.File
	path string
	done bool
}

// Create starts the new content of the file at path. When path is a symlink,
// the link stays and the new content replaces the file that it points to.
// The temp file is in the directory of that file, so the rename stays in one
// filesystem. The directory must exist. When path is a symlink that does not
// resolve (its target is missing, or the links make a loop), Create returns
// an error and writes nothing, because the rename would replace the link.
//
// While the temp file is open, Create holds a lock on it where the platform
// has one (lockTemp), so that RemoveStale of another process leaves it.
// When a RemoveStale took the new temp file before the lock, Create makes
// another one (createTries in all).
func Create(path string) (*File, error) {
	path, err := resolve(path)
	if err != nil {
		return nil, err
	}
	pattern := "." + filepath.Base(path) + ".*.tmp"
	for range createTries {
		tmp, err := createTemp(filepath.Dir(path), pattern)
		if err != nil {
			return nil, err
		}
		if lockTemp(tmp) {
			return &File{tmp: tmp, path: path}, nil
		}
		// A RemoveStale of another process holds this temp file, or
		// removed it: it is not this write's file.
		_ = tmp.Close()
	}
	return nil, fmt.Errorf("cannot make a temp file for %s: a cleanup of another process took each one", path)
}

// createTries is how many temp files Create makes before it fails.
const createTries = 5

// createTemp is os.CreateTemp; a test replaces it.
var createTemp = os.CreateTemp

// resolve is the file that a replace of path writes: the target of a
// symlink at path, else path. A symlink that does not resolve is an error.
func resolve(path string) (string, error) {
	target, err := filepath.EvalSymlinks(path)
	if err == nil {
		return target, nil
	}
	if fi, lerr := os.Lstat(path); lerr == nil && fi.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("cannot replace %s: it is a symlink that does not resolve (a missing target or a loop): %w", path, err)
	}
	return path, nil
}

// isTemp reports whether name is the name of a temp file that Create makes
// for a file named base: "." + base + "." + a number + ".tmp" (os.CreateTemp
// puts a random decimal number in place of the * of the pattern). The temp
// files of other files do not match, also when their names start alike: the
// temp file of "tool.1" is ".tool.1.<number>.tmp", which is not a temp file
// of "tool".
func isTemp(name, base string) bool {
	rest, ok := strings.CutPrefix(name, "."+base+".")
	if !ok {
		return false
	}
	number, ok := strings.CutSuffix(rest, ".tmp")
	if !ok || number == "" {
		return false
	}
	for _, c := range number {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// RemoveStale removes the temp files of the file at path that a stopped
// write left (Ctrl-C, a kill, a crash), and returns their paths. It looks in
// the directory where Create puts them (the directory of the target of a
// symlink at path). It removes a file only when:
//   - its name is a temp file name of this file (isTemp);
//   - it is a regular file;
//   - no process uses it: no process holds the lock of Create on it
//     (removeIfStale). Where b cannot find this out, the file stays.
//
// A missing directory, or a symlink at path that does not resolve, is no
// error: Create cannot have made a temp file there.
func RemoveStale(path string) ([]string, error) {
	target, err := resolve(path)
	if err != nil {
		return nil, nil
	}
	dir, base := filepath.Dir(target), filepath.Base(target)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var removed []string
	var errs []error
	for _, e := range entries {
		if !isTemp(e.Name(), base) {
			continue
		}
		file := filepath.Join(dir, e.Name())
		ok, err := removeIfStale(file)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if ok {
			removed = append(removed, file)
		}
	}
	return removed, errors.Join(errs...)
}

// Write writes p to the temp file.
func (f *File) Write(p []byte) (int, error) {
	return f.tmp.Write(p)
}

// Commit syncs and closes the temp file, sets its mode, and renames it to the
// path of the file. When a step fails, Commit removes the temp file, and the
// file stays as it was. On Windows, the rename fails while a program has the
// file open (for example, a binary that runs); the file then stays too.
func (f *File) Commit(mode os.FileMode) error {
	if f.done {
		return errDone
	}
	f.done = true
	name := f.tmp.Name()
	err := f.tmp.Sync()
	if closeErr := f.tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Chmod(name, mode)
	}
	if err == nil {
		err = os.Rename(name, f.path)
	}
	if err != nil {
		_ = os.Remove(name)
	}
	return err
}

// Abort closes and removes the temp file. The file stays as it was. After
// Commit or Abort, Abort does nothing, so a deferred Abort is safe.
func (f *File) Abort() {
	if f.done {
		return
	}
	f.done = true
	_ = f.tmp.Close()
	_ = os.Remove(f.tmp.Name())
}

// Write replaces the file at path with the content that fill writes, and
// gives it the mode mode (Create, Commit). When fill or a step fails, Write
// removes the temp file, and the file stays as it was.
func Write(path string, mode os.FileMode, fill func(w io.Writer) error) error {
	f, err := Create(path)
	if err != nil {
		return err
	}
	defer f.Abort()
	if err := fill(f); err != nil {
		return err
	}
	return f.Commit(mode)
}
