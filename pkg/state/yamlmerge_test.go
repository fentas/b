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

	// Parse the saved file so assertions are structural — "kubectl"
	// appears both in the 'groups:' list and under 'binaries:', so a
	// flat Contains check would pass even if the fix regressed.
	var saved struct {
		Binaries map[string]interface{} `yaml:"binaries"`
		Groups   map[string]interface{} `yaml:"groups"`
	}
	if err := yaml.Unmarshal(result, &saved); err != nil {
		t.Fatalf("unmarshal saved yaml: %v\n%s", err, got)
	}
	if _, ok := saved.Binaries["kubectl"]; !ok {
		t.Errorf("expected newly added binary 'kubectl' under binaries:, got:\n%s", got)
	}
	if len(saved.Groups) == 0 {
		t.Errorf("top-level 'groups:' was wiped by SaveConfig, got:\n%s", got)
	}
	if _, ok := saved.Groups["core"]; !ok {
		t.Errorf("'groups.core' nested content was not preserved, got:\n%s", got)
	}
	if _, ok := saved.Groups["optional"]; !ok {
		t.Errorf("'groups.optional' nested content was not preserved, got:\n%s", got)
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

	// Parse the saved file structurally so we assert the fields live in
	// the right place — a flat Contains would pass for a stray top-level
	// 'groups:' block even if the per-binary field was wiped.
	var saved struct {
		Binaries map[string]map[string]interface{} `yaml:"binaries"`
	}
	if err := yaml.Unmarshal(result, &saved); err != nil {
		t.Fatalf("unmarshal saved yaml: %v\n%s", err, got)
	}
	if _, ok := saved.Binaries["yq"]; !ok {
		t.Errorf("expected newly added binary 'yq' under binaries:, got:\n%s", got)
	}
	jq, ok := saved.Binaries["jq"]
	if !ok {
		t.Fatalf("jq entry missing from saved file, got:\n%s", got)
	}
	if _, ok := jq["groups"]; !ok {
		t.Errorf("per-binary 'jq.groups' was wiped, got:\n%s", got)
	}
	if owner, _ := jq["owner"].(string); owner != "platform-team" {
		t.Errorf("per-binary 'jq.owner' was wiped or changed, got %q\nfull:\n%s", owner, got)
	}
	kubectl, ok := saved.Binaries["kubectl"]
	if !ok {
		t.Fatalf("kubectl entry missing from saved file, got:\n%s", got)
	}
	if _, ok := kubectl["groups"]; !ok {
		t.Errorf("per-binary 'kubectl.groups' was wiped, got:\n%s", got)
	}
}
