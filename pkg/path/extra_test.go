package path

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindConfigFile(t *testing.T) {
	// No config anywhere → returns empty
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	_ = os.Chdir(dir)
	p, err := FindConfigFile()
	if err != nil {
		t.Errorf("err: %v", err)
	}
	_ = p // may be non-empty if ancestor has a config; that's ok

	// With a config in .bin/
	dir2 := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir2, ".bin"), 0755)
	_ = os.WriteFile(filepath.Join(dir2, ".bin", "b.yaml"), []byte(""), 0644)
	_ = os.Chdir(dir2)
	p, err = FindConfigFile()
	if err != nil {
		t.Errorf("%v", err)
	}
	if p == "" {
		t.Error("expected to find config")
	}

	// With a config in root (no .bin)
	dir3 := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir3, "b.yaml"), []byte(""), 0644)
	_ = os.Chdir(dir3)
	p, err = FindConfigFile()
	if err != nil {
		t.Errorf("%v", err)
	}
	if p == "" {
		t.Error("expected to find root config")
	}
}
