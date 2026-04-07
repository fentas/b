package testutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fentas/b/pkg/state"
)

func TestContainsAny(t *testing.T) {
	if !ContainsAny("hello world", "foo", "world") {
		t.Error("expected match")
	}
	if ContainsAny("hello", "x", "y") {
		t.Error("expected no match")
	}
}

func TestTempDirAndFile(t *testing.T) {
	dir := TempDir(t)
	if _, err := os.Stat(dir); err != nil {
		t.Fatal(err)
	}
	path := TempFile(t, "nested/sub/foo.txt", "hello")
	AssertFileExists(t, path)
	AssertFileContent(t, path, "hello")
	AssertFileContains(t, path, "ell")
	AssertFileNotExists(t, filepath.Join(dir, "missing"))
}

func TestMockBinaryAndConfig(t *testing.T) {
	b := MockBinary("jq", "v1")
	if b.Name != "jq" || b.Version != "v1" {
		t.Errorf("got %+v", b)
	}
	cfg := MockConfig("jq", "kubectl")
	if len(*cfg) != 2 {
		t.Errorf("len = %d", len(*cfg))
	}
	cfg2 := MockConfigWithVersions(map[string]string{"jq": "v1"})
	if len(*cfg2) != 1 {
		t.Errorf("len = %d", len(*cfg2))
	}
}

func TestMockIO(t *testing.T) {
	io := MockIO()
	if io.Out == nil || io.In == nil {
		t.Error("MockIO nil")
	}
	io2, out, err := MockIOWithBuffers()
	_, _ = io2.Out.Write([]byte("hi"))
	_, _ = io2.ErrOut.Write([]byte("e"))
	if out.String() != "hi" || err.String() != "e" {
		t.Errorf("out=%q err=%q", out.String(), err.String())
	}
	out.Reset()
	if out.String() != "" {
		t.Error("reset failed")
	}
}

func TestChangeDirAndCreateProject(t *testing.T) {
	dir := TempDir(t)
	ChangeDir(t, dir)
	cwd, _ := os.Getwd()
	// Resolve symlinks for macOS /private/tmp quirks
	resDir, _ := filepath.EvalSymlinks(dir)
	resCwd, _ := filepath.EvalSymlinks(cwd)
	if resCwd != resDir {
		t.Errorf("cwd=%q want %q", resCwd, resDir)
	}
	p := CreateTestProject(t, &state.State{})
	if _, err := os.Stat(filepath.Join(p, ".bin", "b.yaml")); err != nil {
		t.Errorf("config missing: %v", err)
	}
	p2 := CreateTestProject(t, nil)
	if p2 == "" {
		t.Error("empty project dir")
	}
}
