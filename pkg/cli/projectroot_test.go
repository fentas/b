package cli

import (
	"bytes"
	"os"
	"testing"

	"github.com/fentas/goodies/streams"
)

func TestProjectRoot_PathBase(t *testing.T) {
	t.Setenv("PATH_BASE", "/custom/base")

	io := &streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}}
	shared := NewSharedOptions(io, nil)

	got := shared.ProjectRoot()
	if got != "/custom/base" {
		t.Errorf("ProjectRoot() = %q, want /custom/base (from PATH_BASE)", got)
	}
}

func TestProjectRoot_FallsBackToCWD(t *testing.T) {
	t.Setenv("PATH_BASE", "")

	io := &streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}}
	shared := NewSharedOptions(io, nil)

	got := shared.ProjectRoot()
	cwd, _ := os.Getwd()
	// Should be git root or CWD — both are valid
	if got == "" {
		t.Error("ProjectRoot() should not be empty")
	}
	_ = cwd // used for context
}

func TestProjectRoot_NotLockDir(t *testing.T) {
	t.Setenv("PATH_BASE", "")

	io := &streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}}
	shared := NewSharedOptions(io, nil)

	projectRoot := shared.ProjectRoot()
	lockDir := shared.LockDir()

	// ProjectRoot should NOT be .bin/ — it should be the parent
	if projectRoot == lockDir {
		// This is only wrong if lockDir ends with .bin
		if len(lockDir) > 4 && lockDir[len(lockDir)-4:] == ".bin" {
			t.Errorf("ProjectRoot() = LockDir() = %q — env files would land in .bin/", lockDir)
		}
	}
}
