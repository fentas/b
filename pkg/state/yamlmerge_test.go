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

// TestSaveConfig_PreservesUnknownEnvFields is the envs:/profiles: counterpart
// to TestSaveConfig_PreservesUnknownBinaryFields — a user may annotate an env
// or profile entry with custom fields (e.g. 'owner:', 'labels:') that b
// doesn't know about, and SaveConfig must not drop them.
func TestSaveConfig_PreservesUnknownEnvFields(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "b.yaml")

	initial := `binaries:
  jq: {}
envs:
  github.com/org/infra:
    version: v2.0
    owner: platform-team
    labels: [prod]
    files:
      "manifests/**":
        dest: manifests/
profiles:
  base:
    description: baseline
    owner: sre
    files:
      "base/**":
`
	if err := os.WriteFile(configPath, []byte(initial), 0644); err != nil {
		t.Fatal(err)
	}

	config, err := LoadConfigFromPath(configPath)
	if err != nil {
		t.Fatalf("LoadConfigFromPath: %v", err)
	}

	// Simulate adding a new binary (touches the same SaveConfig path).
	config.Binaries = append(config.Binaries, &binary.LocalBinary{Name: "yq"})

	if err := SaveConfig(config, configPath); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	result, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(result)

	var saved struct {
		Envs     map[string]map[string]interface{} `yaml:"envs"`
		Profiles map[string]map[string]interface{} `yaml:"profiles"`
	}
	if err := yaml.Unmarshal(result, &saved); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, got)
	}

	env, ok := saved.Envs["github.com/org/infra"]
	if !ok {
		t.Fatalf("env 'github.com/org/infra' missing, got:\n%s", got)
	}
	if owner, _ := env["owner"].(string); owner != "platform-team" {
		t.Errorf("env.owner wiped, got %q\nfull:\n%s", owner, got)
	}
	if _, ok := env["labels"]; !ok {
		t.Errorf("env.labels wiped, got:\n%s", got)
	}

	profile, ok := saved.Profiles["base"]
	if !ok {
		t.Fatalf("profile 'base' missing, got:\n%s", got)
	}
	if owner, _ := profile["owner"].(string); owner != "sre" {
		t.Errorf("profile.owner wiped, got %q\nfull:\n%s", owner, got)
	}
}

// TestSaveConfig_PreservesUnknownFilesGlobFields ensures custom user fields
// inside an 'envs.<name>.files.<glob>:' mapping (e.g. 'owner:') survive a
// SaveConfig, even when the marshaler would otherwise emit the glob value as
// a scalar shorthand and thereby collapse the mapping.
func TestSaveConfig_PreservesUnknownFilesGlobFields(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "b.yaml")

	initial := `binaries:
  jq: {}
envs:
  github.com/org/infra:
    files:
      "manifests/**":
        dest: out/
        owner: platform-team
`
	if err := os.WriteFile(configPath, []byte(initial), 0644); err != nil {
		t.Fatal(err)
	}

	config, err := LoadConfigFromPath(configPath)
	if err != nil {
		t.Fatalf("LoadConfigFromPath: %v", err)
	}
	// The marshaler would shrink this to the scalar shorthand "out/".
	config.Binaries = append(config.Binaries, &binary.LocalBinary{Name: "yq"})

	if err := SaveConfig(config, configPath); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	result, _ := os.ReadFile(configPath)
	got := string(result)

	var saved struct {
		Envs map[string]struct {
			Files map[string]map[string]interface{} `yaml:"files"`
		} `yaml:"envs"`
	}
	if err := yaml.Unmarshal(result, &saved); err != nil {
		// If the scalar shorthand kicked in, files will not fit this shape.
		// Fall back to inspecting the raw YAML in the error so we can see it.
		t.Fatalf("unmarshal saved yaml: %v\n%s", err, got)
	}
	glob, ok := saved.Envs["github.com/org/infra"].Files["manifests/**"]
	if !ok {
		t.Fatalf("expected files['manifests/**'] to remain a mapping, got:\n%s", got)
	}
	if dest, _ := glob["dest"].(string); dest != "out/" {
		t.Errorf("dest changed: %q\n%s", dest, got)
	}
	if owner, _ := glob["owner"].(string); owner != "platform-team" {
		t.Errorf("user-owned 'owner' field was wiped, got %q\nfull:\n%s", owner, got)
	}
}

// TestManagedKey_MatchesMarshalOutput enforces that the managedKey predicate
// stays in sync with what BinaryList / EnvList MarshalYAML actually emit.
// If a new field is added to the marshaler it must also appear in managedKey,
// otherwise the merge won't remove it when it disappears from state. Unknown
// custom fields must NOT be reported as managed (or they'd be wiped on save).
func TestManagedKey_MatchesMarshalOutput(t *testing.T) {
	// Drive the marshalers with representative values and harvest the
	// emitted key sets.
	binSample := &BinaryList{
		&binary.LocalBinary{
			Name:     "tool",
			File:     "/tmp/tool",
			Enforced: "v1.0",
			Alias:    "alias",
			Asset:    "tool-*.tar.gz",
		},
	}
	binMarshal, err := binSample.MarshalYAML()
	if err != nil {
		t.Fatalf("BinaryList.MarshalYAML: %v", err)
	}
	// binMarshal is map[string]interface{}: {"tool": {"version":..., "alias":..., ...}}
	// Emitted keys must be managed at path [binaries, <name>].
	binRoot, ok := binMarshal.(map[string]interface{})
	if !ok {
		t.Fatalf("BinaryList.MarshalYAML returned %T, want map[string]interface{}", binMarshal)
	}
	binEntryAny, ok := binRoot["tool"]
	if !ok {
		t.Fatalf("BinaryList.MarshalYAML did not emit 'tool' entry: %v", binRoot)
	}
	binEntry, ok := binEntryAny.(map[string]string)
	if !ok {
		t.Fatalf("BinaryList.MarshalYAML 'tool' entry is %T, want map[string]string", binEntryAny)
	}
	if len(binEntry) == 0 {
		t.Fatal("BinaryList.MarshalYAML emitted an empty 'tool' entry — drift guard would silently pass")
	}
	for key := range binEntry {
		if !managedKey([]string{"binaries", "tool"}, key) {
			t.Errorf("managedKey([binaries tool], %q) = false — marshaler emits this key but predicate doesn't manage it", key)
		}
	}

	envSample := &EnvList{
		&EnvEntry{
			Key:         "github.com/org/infra",
			Description: "desc",
			Includes:    []string{"base"},
			Version:     "v1",
			Ignore:      []string{"*.md"},
			Strategy:    "merge",
			Safety:      "strict",
			Group:       "core",
			OnPreSync:   "echo pre",
			OnPostSync:  "echo post",
		},
	}
	envMarshal, err := envSample.MarshalYAML()
	if err != nil {
		t.Fatalf("EnvList.MarshalYAML: %v", err)
	}
	envRoot, ok := envMarshal.(map[string]interface{})
	if !ok {
		t.Fatalf("EnvList.MarshalYAML returned %T, want map[string]interface{}", envMarshal)
	}
	envEntryAny, ok := envRoot["github.com/org/infra"]
	if !ok {
		t.Fatalf("EnvList.MarshalYAML did not emit 'github.com/org/infra' entry: %v", envRoot)
	}
	envEntry, ok := envEntryAny.(map[string]interface{})
	if !ok {
		t.Fatalf("EnvList.MarshalYAML entry is %T, want map[string]interface{}", envEntryAny)
	}
	if len(envEntry) == 0 {
		t.Fatal("EnvList.MarshalYAML emitted an empty entry — drift guard would silently pass")
	}
	for key := range envEntry {
		if !managedKey([]string{"envs", "github.com/org/infra"}, key) {
			t.Errorf("managedKey([envs <name>], %q) = false — marshaler emits this key", key)
		}
		if !managedKey([]string{"profiles", "base"}, key) {
			t.Errorf("managedKey([profiles <name>], %q) = false — marshaler emits this key", key)
		}
	}

	// Sanity check: well-known user-custom fields on an entry must NOT be
	// managed (otherwise they'd be wiped on save).
	for _, custom := range []string{"owner", "groups", "labels", "team", "notes"} {
		if managedKey([]string{"binaries", "jq"}, custom) {
			t.Errorf("managedKey([binaries jq], %q) = true — user custom field must be preserved", custom)
		}
		if managedKey([]string{"envs", "github.com/org/infra"}, custom) {
			t.Errorf("managedKey([envs <name>], %q) = true — user custom field must be preserved", custom)
		}
	}
	if managedKey(nil, "groups") {
		t.Error("managedKey([], \"groups\") = true — top-level user section must be preserved")
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
