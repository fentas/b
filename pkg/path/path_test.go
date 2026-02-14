package path

import (
	"os"
	"path/filepath"
	"testing"
)

// TestGetBinaryPath_FallbackCWD verifies that GetBinaryPath falls back to
// CWD/.bin when no env vars are set and no git root is found (issue #86).
func TestGetBinaryPath_FallbackCWD(t *testing.T) {
	// Clear env vars that would take priority
	for _, key := range []string{"PATH_BIN", "PATH_BASE"} {
		old := os.Getenv(key)
		os.Unsetenv(key)
		defer os.Setenv(key, old)
	}

	// Work in a temp dir that has no .git
	tmp := t.TempDir()
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}

	got := GetBinaryPath()
	want := filepath.Join(tmp, ".bin")
	if got != want {
		t.Errorf("GetBinaryPath() = %q, want %q (CWD fallback)", got, want)
	}
}

// TestGetBinaryPath_EnvOverride verifies that PATH_BIN takes priority.
func TestGetBinaryPath_EnvOverride(t *testing.T) {
	old := os.Getenv("PATH_BIN")
	defer os.Setenv("PATH_BIN", old)

	os.Setenv("PATH_BIN", "/custom/bin")
	got := GetBinaryPath()
	if got != "/custom/bin" {
		t.Errorf("GetBinaryPath() = %q, want %q", got, "/custom/bin")
	}
}

// TestGetBinaryPath_GitRoot verifies that git root is used when available.
func TestGetBinaryPath_GitRoot(t *testing.T) {
	for _, key := range []string{"PATH_BIN", "PATH_BASE"} {
		old := os.Getenv(key)
		os.Unsetenv(key)
		defer os.Setenv(key, old)
	}

	// Create a temp dir with a .git directory
	tmp := t.TempDir()
	if err := os.Mkdir(filepath.Join(tmp, ".git"), 0755); err != nil {
		t.Fatal(err)
	}

	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}

	got := GetBinaryPath()
	want := tmp + "/.bin"
	if got != want {
		t.Errorf("GetBinaryPath() = %q, want %q (git root)", got, want)
	}
}
