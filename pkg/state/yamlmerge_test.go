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
