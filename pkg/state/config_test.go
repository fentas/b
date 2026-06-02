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

	// SaveConfig must not mutate the caller's in-memory state — the paths it
	// relativizes for disk are written to a copy, so the live config keeps the
	// absolute paths the running process depends on.
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

// TestSaveConfig_KeepsRelativeFilePaths_RelativeConfigPath covers the case
// Copilot flagged: when configPath is relative, LoadConfigFromPath joins
// `file:` into a still-relative path (e.g. ".bin/jira"), so the relativize on
// save must not gate on filepath.IsAbs — otherwise the round-trip rewrites
// "jira" to ".bin/jira".
func TestSaveConfig_KeepsRelativeFilePaths_RelativeConfigPath(t *testing.T) {
	tmpDir := t.TempDir()
	binDir := filepath.Join(tmpDir, ".bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Run with cwd == tmpDir so a relative config path resolves correctly.
	t.Chdir(tmpDir)

	relConfigPath := filepath.Join(".bin", "b.yaml") // relative, with a dir component
	initial := `binaries:
  github.com/ankitpokhrel/jira-cli:
    file: jira
  jq: {}
`
	if err := os.WriteFile(relConfigPath, []byte(initial), 0644); err != nil {
		t.Fatal(err)
	}

	config, err := LoadConfigFromPath(relConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	// Sanity: load produced a still-relative joined path (the trigger).
	if got := mustFile(config, "github.com/ankitpokhrel/jira-cli"); got != filepath.Join(".bin", "jira") {
		t.Fatalf("expected joined-but-relative %q, got %q", filepath.Join(".bin", "jira"), got)
	}

	config.Binaries = append(config.Binaries, &binary.LocalBinary{Name: "yq"})
	if err := SaveConfig(config, relConfigPath); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(relConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.Contains(got, "file: jira\n") {
		t.Errorf("expected jira-cli to keep 'file: jira', got:\n%s", got)
	}
	if strings.Contains(got, ".bin/jira") {
		t.Errorf("file: path leaked the config dir prefix, got:\n%s", got)
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
// that don't live under the config dir (and the degenerate "equals configDir"
// case) are preserved verbatim, and that the input config is never mutated.
func TestRelativizeFiles_LeavesOutsidePathsAbsolute(t *testing.T) {
	configDir := filepath.Join(string(filepath.Separator)+"home", "user", ".bin")
	outside := filepath.Join(string(filepath.Separator)+"opt", "bin", "tool")
	config := &State{
		Binaries: BinaryList{
			&binary.LocalBinary{Name: "tool", File: outside},
			&binary.LocalBinary{Name: "self", File: configDir}, // rel would be "."
		},
	}

	out := relativizeFiles(config, configDir)

	if got := mustFile(out, "tool"); got != outside {
		t.Errorf("outside path should stay absolute, got %q", got)
	}
	if got := mustFile(out, "self"); got != configDir {
		t.Errorf("path equal to configDir should stay absolute, got %q", got)
	}
	// Input must be untouched (no-mutation contract).
	if got := mustFile(config, "tool"); got != outside {
		t.Errorf("input config was mutated, got %q", got)
	}
}

func mustFile(s *State, name string) string {
	if b := s.Binaries.Get(name); b != nil {
		return b.File
	}
	return ""
}
