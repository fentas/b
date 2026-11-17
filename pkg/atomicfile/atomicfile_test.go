package atomicfile

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// writeString is a fill that writes s.
func writeString(s string) func(io.Writer) error {
	return func(w io.Writer) error {
		_, err := io.WriteString(w, s)
		return err
	}
}

// read is the content of the file at path.
func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// noTemp fails the test when a temp file of Create is in dir.
func noTemp(t *testing.T, dir string) {
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

// TestWrite_ReplacesTheFileInOneStep: Write puts a new file in place of the
// old one. A hard link to the old file still has the old content, so Write
// did not write into the old file. The new file has the mode.
func TestWrite_ReplacesTheFileInOneStep(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tool")
	if err := os.WriteFile(path, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Link(path, link); err != nil {
		t.Skipf("no hard links here: %v", err)
	}
	if err := Write(path, 0755, writeString("new")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := read(t, path); got != "new" {
		t.Errorf("file is %q, want %q", got, "new")
	}
	if got := read(t, link); got != "old" {
		t.Errorf("the hard link to the old file is %q, want %q: Write wrote into the old file", got, "old")
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0755 {
			t.Errorf("mode %v, want 0755", fi.Mode().Perm())
		}
	}
	noTemp(t, dir)
}

// TestWrite_NewFile: Write creates a file that is not there.
func TestWrite_NewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tool")
	if err := Write(path, 0755, writeString("new")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := read(t, path); got != "new" {
		t.Errorf("file is %q, want %q", got, "new")
	}
	noTemp(t, dir)
}

// TestWrite_FailedFillKeepsTheFile: when fill fails after it wrote a part,
// the old file stays as it was and no temp file stays.
func TestWrite_FailedFillKeepsTheFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tool")
	if err := os.WriteFile(path, []byte("old"), 0755); err != nil {
		t.Fatal(err)
	}
	cut := errors.New("the connection closed")
	err := Write(path, 0755, func(w io.Writer) error {
		if _, err := io.WriteString(w, "a part of the new"); err != nil {
			return err
		}
		return cut
	})
	if !errors.Is(err, cut) {
		t.Fatalf("Write: %v, want the error of fill", err)
	}
	if got := read(t, path); got != "old" {
		t.Errorf("file is %q, want the old file", got)
	}
	noTemp(t, dir)
}

// TestWrite_FollowsASymlink: when the path is a symlink, the link stays, and
// the file that it points to gets the new content. The temp file is beside
// that file.
func TestWrite_FollowsASymlink(t *testing.T) {
	dir, targetDir := t.TempDir(), t.TempDir()
	target := filepath.Join(targetDir, "tool")
	if err := os.WriteFile(target, []byte("old"), 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "tool")
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	if err := Write(path, 0755, writeString("new")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("%s is not a symlink any more", path)
	}
	if got := read(t, target); got != "new" {
		t.Errorf("target is %q, want %q", got, "new")
	}
	noTemp(t, dir)
	noTemp(t, targetDir)
}

// checkLink fails the test when path is not a symlink to want.
func checkLink(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.Readlink(path)
	if err != nil {
		t.Fatalf("%s is not a symlink any more: %v", path, err)
	}
	if got != want {
		t.Errorf("%s points to %q, want %q", path, got, want)
	}
}

// TestWrite_RefusesASymlinkThatDoesNotResolve: when the path is a symlink
// whose target is missing, or links that make a loop, Write returns an error
// and writes nothing. The links stay as they were, no target is made, and no
// temp file stays.
func TestWrite_RefusesASymlinkThatDoesNotResolve(t *testing.T) {
	t.Run("a missing target", func(t *testing.T) {
		dir, targetDir := t.TempDir(), t.TempDir()
		target := filepath.Join(targetDir, "tool")
		path := filepath.Join(dir, "tool")
		if err := os.Symlink(target, path); err != nil {
			t.Skipf("no symlinks here: %v", err)
		}
		err := Write(path, 0755, writeString("new"))
		if err == nil || !strings.Contains(err.Error(), "symlink that does not resolve") {
			t.Fatalf("Write: %v, want the error about the symlink", err)
		}
		checkLink(t, path, target)
		if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("target %s: %v, want no file", target, err)
		}
		noTemp(t, dir)
		noTemp(t, targetDir)
	})
	t.Run("a loop", func(t *testing.T) {
		dir := t.TempDir()
		a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
		if err := os.Symlink(b, a); err != nil {
			t.Skipf("no symlinks here: %v", err)
		}
		if err := os.Symlink(a, b); err != nil {
			t.Fatal(err)
		}
		err := Write(a, 0755, writeString("new"))
		if err == nil || !strings.Contains(err.Error(), "symlink that does not resolve") {
			t.Fatalf("Write: %v, want the error about the symlink", err)
		}
		checkLink(t, a, b)
		checkLink(t, b, a)
		noTemp(t, dir)
	})
}

// TestCommit_FailedRenameKeepsTheFile: when the rename fails (here: a
// directory is at the path), Commit returns the error, removes the temp file
// and leaves the path as it was.
func TestCommit_FailedRenameKeepsTheFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tool")
	if err := os.MkdirAll(filepath.Join(path, "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, 0755, writeString("new")); err == nil {
		t.Fatal("Write over a directory: no error")
	}
	if fi, err := os.Stat(path); err != nil || !fi.IsDir() {
		t.Errorf("%s: %v, want the directory as it was", path, err)
	}
	noTemp(t, dir)
}

// TestAbort: Abort removes the temp file and leaves the file as it was. After
// Commit, Abort does nothing, and a second Commit is an error.
func TestAbort(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tool")
	if err := os.WriteFile(path, []byte("old"), 0755); err != nil {
		t.Fatal(err)
	}

	f, err := Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(f, "new"); err != nil {
		t.Fatal(err)
	}
	f.Abort()
	f.Abort()
	if got := read(t, path); got != "old" {
		t.Errorf("after Abort, file is %q, want the old file", got)
	}
	noTemp(t, dir)
	if err := f.Commit(0755); err == nil {
		t.Error("Commit after Abort: no error")
	}

	f, err = Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(f, "new"); err != nil {
		t.Fatal(err)
	}
	if err := f.Commit(0755); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	f.Abort()
	if got := read(t, path); got != "new" {
		t.Errorf("after Commit and Abort, file is %q, want %q", got, "new")
	}
	if err := f.Commit(0755); err == nil {
		t.Error("a second Commit: no error")
	}
	noTemp(t, dir)
}
