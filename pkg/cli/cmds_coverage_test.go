package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fentas/goodies/streams"

	"github.com/fentas/b/pkg/binary"
)

func mkIO() *streams.IO {
	return &streams.IO{In: strings.NewReader(""), Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}}
}

// mustWrite writes content to path, calling t.Fatalf on error.
func mustWrite(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}

// mustWriteExec is like mustWrite but with executable mode.
func mustWriteExec(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.WriteFile(path, content, 0755); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}

// mustMkdir creates a directory tree, calling t.Fatalf on error.
func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatalf("MkdirAll %s: %v", path, err)
	}
}

func mkBinaries() []*binary.Binary {
	return []*binary.Binary{
		{Name: "jq", Context: context.Background()},
		{Name: "kubectl", Context: context.Background()},
	}
}

func TestNewRootCmd_AllSubcommands(t *testing.T) {
	// Isolate from host config
	dir := t.TempDir()
	t.Setenv("PATH_BIN", filepath.Join(dir, ".bin"))
	t.Chdir(dir)

	root := NewRootCmd(mkBinaries(), mkIO(), "dev", "")
	if root == nil {
		t.Fatal("nil root")
	}
	// Check expected subcommands exist
	want := []string{"install", "update", "list", "search", "init", "version", "request", "verify", "cache", "env"}
	for _, w := range want {
		found := false
		for _, c := range root.Commands() {
			if c.Name() == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing subcommand: %s", w)
		}
	}

	// Execute help to cover the custom usage template
	root.SetArgs([]string{"--help"})
	_ = root.Execute()
}

func TestNewCmdBinary_Nil(t *testing.T) {
	c := NewCmdBinary(nil)
	if c == nil {
		t.Fatal("nil cmd")
	}
}

func TestNewCmdBinary_WithOptions(t *testing.T) {
	opts := &CmdBinaryOptions{
		IO:       mkIO(),
		Binaries: mkBinaries(),
	}
	c := NewCmdBinary(opts)
	if c == nil {
		t.Fatal("nil")
	}
	// Trigger AddFlags path via help
	c.SetArgs([]string{"--help"})
	_ = c.Execute()
}

func TestInit_Run_CreatesFiles(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH_BIN", filepath.Join(dir, ".bin"))
	t.Chdir(dir)

	shared := NewSharedOptions(mkIO(), mkBinaries())
	o := &InitOptions{SharedOptions: shared}
	if err := o.Complete(nil); err != nil {
		t.Error(err)
	}
	if err := o.Validate(); err != nil {
		t.Error(err)
	}
	if err := o.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Idempotent skip paths: run again without force should error on config,
	// but createGitignore/createEnvrc should detect existing and skip.
	if err := o.Run(); err == nil {
		t.Error("expected already-exists error")
	}
	o.Force = true
	// With --force and existing files, Run should succeed and skip gitignore/envrc
	if err := o.Run(); err != nil {
		t.Errorf("force run: %v", err)
	}

	// Verify files exist
	if _, err := os.Stat(filepath.Join(dir, ".bin", "b.yaml")); err != nil {
		t.Error(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".envrc")); err != nil {
		t.Error(err)
	}

	// isDirenvInstalled
	_ = o.isDirenvInstalled()
}

func TestNewInitCmd_Help(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH_BIN", filepath.Join(dir, ".bin"))
	t.Chdir(dir)
	shared := NewSharedOptions(mkIO(), nil)
	c := NewInitCmd(shared)
	c.SetArgs([]string{"--help"})
	_ = c.Execute()
}

func TestNewCacheCmd_Subcommands(t *testing.T) {
	shared := NewSharedOptions(mkIO(), nil)
	c := NewCacheCmd(shared)
	if c == nil {
		t.Fatal("nil")
	}
	// Exercise clean and path help
	c.SetArgs([]string{"path"})
	_ = c.Execute()
	c.SetArgs([]string{"clean", "--help"})
	_ = c.Execute()
}

func TestFirstLine(t *testing.T) {
	if got := firstLine("a\nb\n"); got != "a" {
		t.Errorf("got %q", got)
	}
	if got := firstLine("single"); got != "single" {
		t.Errorf("got %q", got)
	}
	if got := firstLine("with\r\ncr"); got != "with" {
		t.Errorf("got %q", got)
	}
}

func TestSharedOptions_ProjectRoot_PATHBASE(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH_BASE", dir)
	so := NewSharedOptions(mkIO(), nil)
	if got := so.ProjectRoot(); got != dir {
		t.Errorf("ProjectRoot = %q", got)
	}
}

func TestSharedOptions_ValidateBinaryPath(t *testing.T) {
	t.Setenv("PATH_BIN", t.TempDir())
	so := NewSharedOptions(mkIO(), nil)
	if err := so.ValidateBinaryPath(); err != nil {
		t.Errorf("%v", err)
	}
}

func TestSharedOptions_LockDir(t *testing.T) {
	dir := t.TempDir()
	so := NewSharedOptions(mkIO(), nil)
	so.ConfigPath = filepath.Join(dir, "b.yaml")
	if got := so.LockDir(); got != dir {
		t.Errorf("LockDir = %q", got)
	}
}

func TestSharedOptions_ApplyQuietMode(t *testing.T) {
	so := NewSharedOptions(mkIO(), nil)
	so.Quiet = true
	so.ApplyQuietMode()
}
