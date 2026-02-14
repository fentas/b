package path

import (
	"os"
	"path/filepath"
	"testing"
)

// realDir resolves symlinks so tests pass on macOS where
// /var is a symlink to /private/var.
func realDir(t *testing.T, dir string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

// clearPathEnvs uses t.Setenv to clear PATH_BIN and PATH_BASE,
// automatically restoring (or unsetting) them when the test finishes.
func clearPathEnvs(t *testing.T) {
	t.Helper()
	t.Setenv("PATH_BIN", "")
	t.Setenv("PATH_BASE", "")
	os.Unsetenv("PATH_BIN")
	os.Unsetenv("PATH_BASE")
}

// TestGetBinaryPath_FallbackCWD verifies that GetBinaryPath falls back to
// CWD/.bin when no env vars are set and no git root is found (issue #86).
func TestGetBinaryPath_FallbackCWD(t *testing.T) {
	clearPathEnvs(t)

	// Work in a temp dir that has no .git
	tmp := realDir(t, t.TempDir())
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
	t.Setenv("PATH_BIN", "/custom/bin")
	got := GetBinaryPath()
	if got != "/custom/bin" {
		t.Errorf("GetBinaryPath() = %q, want %q", got, "/custom/bin")
	}
}

// TestGetBinaryPath_GitRoot verifies that git root is used when available.
func TestGetBinaryPath_GitRoot(t *testing.T) {
	clearPathEnvs(t)

	// Create a temp dir with a .git directory
	tmp := realDir(t, t.TempDir())
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
	want := filepath.Join(tmp, ".bin")
	if got != want {
		t.Errorf("GetBinaryPath() = %q, want %q (git root)", got, want)
	}
}

// TestGetBinaryPath_PathBaseFallback verifies PATH_BASE is used when PATH_BIN is unset.
func TestGetBinaryPath_PathBaseFallback(t *testing.T) {
	clearPathEnvs(t)
	t.Setenv("PATH_BASE", "/project/base")

	got := GetBinaryPath()
	if got != "/project/base" {
		t.Errorf("GetBinaryPath() = %q, want %q (PATH_BASE)", got, "/project/base")
	}
}

// TestGetBinaryPath_PathBinOverridesAll verifies PATH_BIN wins over PATH_BASE and git root.
func TestGetBinaryPath_PathBinOverridesAll(t *testing.T) {
	// Set both — PATH_BIN should win
	t.Setenv("PATH_BIN", "/priority/bin")
	t.Setenv("PATH_BASE", "/base/bin")

	// Even inside a git repo
	tmp := realDir(t, t.TempDir())
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
	if got != "/priority/bin" {
		t.Errorf("GetBinaryPath() = %q, want %q (PATH_BIN should override all)", got, "/priority/bin")
	}
}

// TestGetDefaultConfigPath_FallbackCWD verifies config path uses CWD fallback.
func TestGetDefaultConfigPath_FallbackCWD(t *testing.T) {
	clearPathEnvs(t)

	tmp := realDir(t, t.TempDir())
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}

	got := GetDefaultConfigPath()
	want := filepath.Join(tmp, ".bin", "b.yaml")
	if got != want {
		t.Errorf("GetDefaultConfigPath() = %q, want %q", got, want)
	}
}
