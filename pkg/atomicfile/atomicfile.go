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
package atomicfile

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
func Create(path string) (*File, error) {
	target, err := filepath.EvalSymlinks(path)
	if err == nil {
		path = target
	} else if fi, lerr := os.Lstat(path); lerr == nil && fi.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("cannot replace %s: it is a symlink that does not resolve (a missing target or a loop): %w", path, err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return nil, err
	}
	return &File{tmp: tmp, path: path}, nil
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
