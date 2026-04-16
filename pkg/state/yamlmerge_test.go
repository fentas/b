package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fentas/b/pkg/binary"
	"gopkg.in/yaml.v3"
)

func TestSaveConfig_PreservesComments(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "b.yaml")

	// Write initial file with comments
	initial := `# Project tools managed by b
binaries:
  # Core tools
  jq:
  kubectl:
    version: v1.28.0
`
	if err := os.WriteFile(configPath, []byte(initial), 0644); err != nil {
		t.Fatal(err)
	}

	// Load, modify, save
	config, err := LoadConfigFromPath(configPath)
	if err != nil {
		t.Fatal(err)
	}

	// Modify a version
	for _, b := range config.Binaries {
		if b.Name == "kubectl" {
			b.Enforced = "v1.29.0"
		}
	}

	if err := SaveConfig(config, configPath); err != nil {
		t.Fatal(err)
	}

	// Read back and check comments are preserved
	result, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(result), "# Project tools managed by b") {
		t.Errorf("top comment should be preserved, got:\n%s", result)
	}
	if !strings.Contains(string(result), "# Core tools") {
		t.Errorf("inline comment should be preserved, got:\n%s", result)
	}
}

func TestSaveConfig_NewFile(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "new.yaml")

	config := &State{
		Binaries: BinaryList{},
	}

	if err := SaveConfig(config, configPath); err != nil {
		t.Fatal(err)
	}

	result, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(result), "binaries") {
		t.Errorf("should contain binaries, got:\n%s", result)
	}
}

func TestSaveConfig_AddsBinaryPreservingComments(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "b.yaml")

	initial := `# My project
binaries:
  # Always needed
  jq:
`
	if err := os.WriteFile(configPath, []byte(initial), 0644); err != nil {
		t.Fatal(err)
	}

	config, err := LoadConfigFromPath(configPath)
	if err != nil {
		t.Fatal(err)
	}

	// Add a new binary
	config.Binaries = append(config.Binaries, &binary.LocalBinary{Name: "kubectl"})

	if err := SaveConfig(config, configPath); err != nil {
		t.Fatal(err)
	}

	result, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(result), "# My project") {
		t.Errorf("top comment should be preserved, got:\n%s", result)
	}
	if !strings.Contains(string(result), "# Always needed") {
		t.Errorf("inline comment should be preserved, got:\n%s", result)
	}
	if !strings.Contains(string(result), "kubectl") {
		t.Errorf("new binary should be added, got:\n%s", result)
	}
}

func TestMergeMappings_PreservesComments(t *testing.T) {
	original := `# header
key1:
  # sub comment
  sub: value1
key2: value2
`
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(original), &doc); err != nil {
		t.Fatal(err)
	}

	// Create a new mapping with updated key1
	update := `key1:
  sub: updated
key2: value2
`
	var newDoc yaml.Node
	if err := yaml.Unmarshal([]byte(update), &newDoc); err != nil {
		t.Fatal(err)
	}

	mergeMappings(doc.Content[0], newDoc.Content[0])

	out, err := yaml.Marshal(&doc)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(out), "# header") {
		t.Errorf("header comment should be preserved, got:\n%s", out)
	}
	if !strings.Contains(string(out), "# sub comment") {
		t.Errorf("sub comment should be preserved, got:\n%s", out)
	}
}

// TestSaveConfig_PreservesUnknownTopLevelKeys is a regression test for a
// bug where 'b install --add' wiped any top-level keys that weren't part
// of the b.yaml schema (e.g. a user-defined 'groups:' section used by
// external tooling).
func TestSaveConfig_PreservesUnknownTopLevelKeys(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "b.yaml")

	initial := `binaries:
  jq: {}

# User-defined top-level section used by external tooling.
groups:
  core:
    - jq
  optional:
    - kubectl
`
	if err := os.WriteFile(configPath, []byte(initial), 0644); err != nil {
		t.Fatal(err)
	}

	config, err := LoadConfigFromPath(configPath)
	if err != nil {
		t.Fatalf("LoadConfigFromPath: %v", err)
	}

	// Simulate 'b install --add kubectl'
	config.Binaries = append(config.Binaries, &binary.LocalBinary{Name: "kubectl"})

	if err := SaveConfig(config, configPath); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	result, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(result)

	if !strings.Contains(got, "kubectl") {
		t.Errorf("expected newly added binary 'kubectl', got:\n%s", got)
	}
	if !strings.Contains(got, "groups:") {
		t.Errorf("top-level 'groups:' was wiped by SaveConfig, got:\n%s", got)
	}
	if !strings.Contains(got, "- jq") || !strings.Contains(got, "- kubectl") {
		t.Errorf("'groups' nested content was not preserved, got:\n%s", got)
	}
}

// TestSaveConfig_PreservesUnknownBinaryFields is a regression test for the
// same bug at the binary-entry level: a user may annotate binaries with
// custom fields ('groups', 'team', 'owner', ...) that b doesn't know
// about, and SaveConfig must not drop them.
func TestSaveConfig_PreservesUnknownBinaryFields(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "b.yaml")

	initial := `binaries:
  jq:
    groups: [core, cli]
    owner: platform-team
  kubectl:
    groups: [core]
`
	if err := os.WriteFile(configPath, []byte(initial), 0644); err != nil {
		t.Fatal(err)
	}

	config, err := LoadConfigFromPath(configPath)
	if err != nil {
		t.Fatalf("LoadConfigFromPath: %v", err)
	}

	// Simulate 'b install --add yq' → add a new binary.
	config.Binaries = append(config.Binaries, &binary.LocalBinary{Name: "yq"})

	if err := SaveConfig(config, configPath); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	result, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(result)

	if !strings.Contains(got, "yq") {
		t.Errorf("expected newly added binary 'yq', got:\n%s", got)
	}
	// Custom per-binary fields must survive a round-trip through SaveConfig.
	if !strings.Contains(got, "groups:") {
		t.Errorf("per-binary 'groups' field was wiped, got:\n%s", got)
	}
	if !strings.Contains(got, "owner: platform-team") {
		t.Errorf("per-binary 'owner' field was wiped, got:\n%s", got)
	}
}
