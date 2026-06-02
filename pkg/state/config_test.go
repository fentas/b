package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fentas/b/pkg/binary"
)

// TestSaveConfig_KeepsRelativeFilePaths verifies that a load→modify→save
// round-trip (as performed by `b install --add`) does not rewrite the user's
// relative `file:` values to absolute paths. LoadConfigFromPath absolutizes
// them in memory; SaveConfig must reverse that on the way out.
func TestSaveConfig_KeepsRelativeFilePaths(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "b.yaml")

	initial := `binaries:
  github.com/ankitpokhrel/jira-cli:
    file: jira
  khelm:
    file: .kustomize/khelm.mgoltzsche.github.com/v2/chartrenderer/ChartRenderer
  jq: {}
`
	if err := os.WriteFile(configPath, []byte(initial), 0644); err != nil {
		t.Fatal(err)
	}

	// Load (this absolutizes the relative file: paths in memory) ...
	config, err := LoadConfigFromPath(configPath)
	if err != nil {
		t.Fatal(err)
	}

	// ... sanity-check that load did absolutize as expected.
	if khelm := config.Binaries.Get("khelm"); khelm == nil || !filepath.IsAbs(khelm.File) {
		t.Fatalf("expected khelm.File to be absolute after load, got %q", mustFile(config, "khelm"))
	}

	// Simulate `--add`: append a new binary and save back to the same file.
	config.Binaries = append(config.Binaries, &binary.LocalBinary{Name: "yq"})
	if err := SaveConfig(config, configPath); err != nil {
		t.Fatal(err)
	}

	// The on-disk file must still carry the original relative paths.
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)

	if !strings.Contains(got, "file: jira\n") {
		t.Errorf("expected jira-cli to keep relative 'file: jira', got:\n%s", got)
	}
	if !strings.Contains(got, "file: .kustomize/khelm.mgoltzsche.github.com/v2/chartrenderer/ChartRenderer") {
		t.Errorf("expected khelm to keep its relative file path, got:\n%s", got)
	}
	if strings.Contains(got, tmpDir) {
		t.Errorf("config should not contain absolute paths (%s), got:\n%s", tmpDir, got)
	}

	// In-memory state must be restored to absolute so the running process is
	// unaffected by the save.
	if khelm := config.Binaries.Get("khelm"); khelm == nil || !filepath.IsAbs(khelm.File) {
		t.Errorf("expected khelm.File to remain absolute in memory after save, got %q", mustFile(config, "khelm"))
	}

	// Reloading must yield the same absolute path as before (proves the
	// relative value still resolves against the config dir).
	reloaded, err := LoadConfigFromPath(configPath)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(tmpDir, "jira")
	if got := mustFile(reloaded, "github.com/ankitpokhrel/jira-cli"); got != want {
		t.Errorf("reloaded jira file = %q, want %q", got, want)
	}
}

// TestSaveConfig_KeepsRelativeFilePaths_NewFile covers the clean-save path
// (no pre-existing file), where the binaries are marshaled from scratch.
func TestSaveConfig_KeepsRelativeFilePaths_NewFile(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "b.yaml")

	// Mimic in-memory state as produced after a load: an absolute File path
	// pointing under the config dir.
	config := &State{
		Binaries: BinaryList{
			&binary.LocalBinary{Name: "khelm", File: filepath.Join(tmpDir, "tools", "khelm")},
		},
	}

	if err := SaveConfig(config, configPath); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.Contains(got, "file: tools/khelm") {
		t.Errorf("expected relative 'file: tools/khelm', got:\n%s", got)
	}
	if strings.Contains(got, tmpDir) {
		t.Errorf("config should not contain absolute paths (%s), got:\n%s", tmpDir, got)
	}
}

// TestRelativizeFiles_LeavesOutsidePathsAbsolute ensures absolute File paths
// that don't live under the config dir are preserved verbatim.
func TestRelativizeFiles_LeavesOutsidePathsAbsolute(t *testing.T) {
	outside := filepath.Join(string(filepath.Separator)+"opt", "bin", "tool")
	config := &State{
		Binaries: BinaryList{
			&binary.LocalBinary{Name: "tool", File: outside},
		},
	}

	restore := relativizeFiles(config, filepath.Join(string(filepath.Separator)+"home", "user", ".bin"))
	if got := mustFile(config, "tool"); got != outside {
		t.Errorf("outside path should stay absolute, got %q", got)
	}
	restore()
	if got := mustFile(config, "tool"); got != outside {
		t.Errorf("restore changed an untouched path, got %q", got)
	}
}

func mustFile(s *State, name string) string {
	if b := s.Binaries.Get(name); b != nil {
		return b.File
	}
	return ""
}
