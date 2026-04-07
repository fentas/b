package path

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindConfigFile(t *testing.T) {
	// Isolate env so an ancestor PATH_BIN/PATH_BASE can't influence the walk
	t.Setenv("PATH_BIN", "")
	t.Setenv("PATH_BASE", "")

	// No config anywhere → returns empty (use a deeply nested temp dir so no
	// ancestor in /tmp accidentally has a b.yaml)
	dir := t.TempDir()
	deep := filepath.Join(dir, "a", "b", "c")
	if err := os.MkdirAll(deep, 0755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(deep)
	p, err := FindConfigFile()
	if err != nil {
		t.Errorf("err: %v", err)
	}
	if p != "" {
		t.Errorf("expected empty for no-config case, got %q", p)
	}

	// With a config in .bin/
	dir2 := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir2, ".bin"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir2, ".bin", "b.yaml"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir2)
	p, err = FindConfigFile()
	if err != nil {
		t.Errorf("%v", err)
	}
	if p == "" {
		t.Error("expected to find config")
	}

	// With a config in root (no .bin)
	dir3 := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir3, "b.yaml"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir3)
	p, err = FindConfigFile()
	if err != nil {
		t.Errorf("%v", err)
	}
	if p == "" {
		t.Error("expected to find root config")
	}
}
