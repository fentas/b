package path

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindConfigFile(t *testing.T) {
	// Isolate env so an ancestor PATH_BIN/PATH_BASE can't influence the walk
	t.Setenv("PATH_BIN", "")
	t.Setenv("PATH_BASE", "")

	// No config anywhere UNDER the test tempdir. FindConfigFile walks up to
	// the filesystem root so it may still find a b.yaml in an ancestor (e.g.
	// on dev machines where /tmp's parent has one). What we can deterministically
	// assert is: the returned path, if non-empty, is NOT located inside our
	// fresh tempdir — i.e., we correctly found nothing of our own creation.
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
		absDir, _ := filepath.Abs(dir)
		if strings.HasPrefix(p, absDir) {
			t.Errorf("unexpected match inside tempdir: %q (under %q)", p, absDir)
		}
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
