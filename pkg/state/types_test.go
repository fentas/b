package state

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v2"
)

func TestEnvConfigUnmarshal(t *testing.T) {
	input := `
binaries:
  kubectl: {}
envs:
  github.com/org/infra:
    version: v2.1.0
    ignore:
      - "*.md"
      - "tests/**"
    strategy: replace
    files:
      "manifests/base/**":
      "manifests/hetzner/**": /hetzner
      "configs/ingress.yaml":
        dest: /config
        ignore:
          - "*.bak"
`
	var s State
	if err := yaml.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(s.Envs) != 1 {
		t.Fatalf("got %d envs, want 1", len(s.Envs))
	}

	e := s.Envs[0]
	if e.Key != "github.com/org/infra" {
		t.Errorf("key = %q, want %q", e.Key, "github.com/org/infra")
	}
	if e.Version != "v2.1.0" {
		t.Errorf("version = %q, want %q", e.Version, "v2.1.0")
	}
	if len(e.Ignore) != 2 {
		t.Errorf("ignore len = %d, want 2", len(e.Ignore))
	}
	if e.Strategy != "replace" {
		t.Errorf("strategy = %q, want %q", e.Strategy, "replace")
	}

	if len(e.Files) != 3 {
		t.Fatalf("files len = %d, want 3", len(e.Files))
	}

	// Bare key (null value)
	baseGlob, ok := e.Files["manifests/base/**"]
	if !ok {
		t.Fatal("missing manifests/base/** glob")
	}
	if baseGlob.Dest != "" {
		t.Errorf("base glob dest = %q, want empty", baseGlob.Dest)
	}

	// String shorthand
	hetznerGlob, ok := e.Files["manifests/hetzner/**"]
	if !ok {
		t.Fatal("missing manifests/hetzner/** glob")
	}
	if hetznerGlob.Dest != "/hetzner" {
		t.Errorf("hetzner glob dest = %q, want %q", hetznerGlob.Dest, "/hetzner")
	}

	// Object form
	ingressGlob, ok := e.Files["configs/ingress.yaml"]
	if !ok {
		t.Fatal("missing configs/ingress.yaml glob")
	}
	if ingressGlob.Dest != "/config" {
		t.Errorf("ingress glob dest = %q, want %q", ingressGlob.Dest, "/config")
	}
	if len(ingressGlob.Ignore) != 1 || ingressGlob.Ignore[0] != "*.bak" {
		t.Errorf("ingress glob ignore = %v, want [*.bak]", ingressGlob.Ignore)
	}
}

func TestEnvConfigMarshal(t *testing.T) {
	s := &State{
		Envs: EnvList{
			{
				Key:     "github.com/org/infra",
				Version: "v2.0",
			},
		},
	}

	data, err := yaml.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Verify it contains the env key
	str := string(data)
	if !contains(str, "github.com/org/infra") {
		t.Errorf("marshal output missing env key:\n%s", str)
	}
}

func TestEnvListGet(t *testing.T) {
	list := EnvList{
		{Key: "github.com/org/a"},
		{Key: "github.com/org/b"},
	}

	if e := list.Get("github.com/org/a"); e == nil {
		t.Error("expected to find org/a")
	}
	if e := list.Get("github.com/org/c"); e != nil {
		t.Error("expected nil for org/c")
	}
}

func TestLoadConfigFromPath(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := tmpDir + "/b.yaml"

	content := `binaries:
  kubectl:
    version: "v1.28.0"
  jq: {}
envs:
  github.com/org/infra:
    version: v2.0
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	s, err := LoadConfigFromPath(configPath)
	if err != nil {
		t.Fatalf("LoadConfigFromPath() error = %v", err)
	}
	if s == nil {
		t.Fatal("expected non-nil state")
	}
	if len(s.Binaries) != 2 {
		t.Errorf("got %d binaries, want 2", len(s.Binaries))
	}
	if len(s.Envs) != 1 {
		t.Errorf("got %d envs, want 1", len(s.Envs))
	}
}

func TestLoadConfigFromPath_Missing(t *testing.T) {
	_, err := LoadConfigFromPath("/nonexistent/path/b.yaml")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestLoadConfigFromPath_InvalidYAML(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := tmpDir + "/b.yaml"
	// Use a YAML tab character at line start which yaml.v2 rejects
	if err := os.WriteFile(configPath, []byte("\tbinaries: {"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadConfigFromPath(configPath)
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

func TestLoadConfigFromPath_RelativeFilePaths(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := tmpDir + "/.bin/b.yaml"
	if err := os.MkdirAll(tmpDir+"/.bin", 0755); err != nil {
		t.Fatal(err)
	}
	content := `binaries:
  kubectl:
    file: ../bin/kubectl
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	s, err := LoadConfigFromPath(configPath)
	if err != nil {
		t.Fatalf("LoadConfigFromPath() error = %v", err)
	}

	kb := s.Binaries.Get("kubectl")
	if kb == nil {
		t.Fatal("expected kubectl binary")
	}
	// File path should be resolved relative to config dir
	if kb.File == "../bin/kubectl" {
		t.Error("expected file path to be resolved, got relative path")
	}
}

func TestSaveConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := tmpDir + "/sub/b.yaml"

	s := &State{
		Binaries: BinaryList{
			{Name: "jq"},
		},
	}

	if err := SaveConfig(s, configPath); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	// Verify file exists and is valid YAML
	loaded, err := LoadConfigFromPath(configPath)
	if err != nil {
		t.Fatalf("re-loading saved config: %v", err)
	}
	if len(loaded.Binaries) != 1 {
		t.Errorf("loaded %d binaries, want 1", len(loaded.Binaries))
	}
}

func TestCreateDefaultConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := tmpDir + "/b.yaml"

	if err := CreateDefaultConfig(configPath); err != nil {
		t.Fatalf("CreateDefaultConfig() error = %v", err)
	}

	s, err := LoadConfigFromPath(configPath)
	if err != nil {
		t.Fatalf("loading default config: %v", err)
	}
	if s.Binaries.Get("b") == nil {
		t.Error("default config should include 'b' binary")
	}
}

func TestBinaryListMarshalYAML(t *testing.T) {
	list := BinaryList{
		{Name: "jq", Enforced: "jq-1.7"},
		{Name: "envsubst", Alias: "renvsubst"},
		{Name: "kubectl", File: "/usr/local/bin/kubectl"},
		{Name: "terraform"}, // bare entry
	}

	result, err := list.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML() error = %v", err)
	}

	m, ok := result.(map[string]interface{})
	if !ok {
		t.Fatal("expected map result")
	}

	// jq should have version
	jqCfg, ok := m["jq"].(map[string]string)
	if !ok {
		t.Fatal("jq config should be map[string]string")
	}
	if jqCfg["version"] != "jq-1.7" {
		t.Errorf("jq version = %q, want %q", jqCfg["version"], "jq-1.7")
	}

	// envsubst should have alias
	esCfg, ok := m["envsubst"].(map[string]string)
	if !ok {
		t.Fatal("envsubst config should be map[string]string")
	}
	if esCfg["alias"] != "renvsubst" {
		t.Errorf("envsubst alias = %q, want %q", esCfg["alias"], "renvsubst")
	}

	// kubectl should have file
	kCfg, ok := m["kubectl"].(map[string]string)
	if !ok {
		t.Fatal("kubectl config should be map[string]string")
	}
	if kCfg["file"] != "/usr/local/bin/kubectl" {
		t.Errorf("kubectl file = %q", kCfg["file"])
	}
}

func TestBinaryListGet(t *testing.T) {
	list := BinaryList{
		{Name: "jq"},
		{Name: "kubectl"},
	}

	if b := list.Get("jq"); b == nil {
		t.Error("expected to find jq")
	}
	if b := list.Get("helm"); b != nil {
		t.Error("expected nil for helm")
	}
}

func TestBinaryListUnmarshalYAML(t *testing.T) {
	input := `
jq:
  version: "jq-1.7"
kubectl: {}
github.com/derailed/k9s:
  version: "v0.32.0"
`
	var list BinaryList
	if err := yaml.Unmarshal([]byte(input), &list); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Check provider ref detection
	for _, b := range list {
		if b.Name == "github.com/derailed/k9s" && !b.IsProviderRef {
			t.Error("expected IsProviderRef=true for github.com/derailed/k9s")
		}
		if b.Name == "jq" && b.IsProviderRef {
			t.Error("expected IsProviderRef=false for jq")
		}
	}
}

func TestBinaryListUnmarshalYAML_WithAsset(t *testing.T) {
	input := `
github.com/arg-sh/argsh:
  asset: "argsh-so-*"
  version: "v1.0.0"
`
	var list BinaryList
	if err := yaml.Unmarshal([]byte(input), &list); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("got %d binaries, want 1", len(list))
	}
	if list[0].Asset != "argsh-so-*" {
		t.Errorf("asset = %q, want %q", list[0].Asset, "argsh-so-*")
	}
	if list[0].Version != "v1.0.0" {
		t.Errorf("version = %q, want %q", list[0].Version, "v1.0.0")
	}
	if !list[0].IsProviderRef {
		t.Error("expected IsProviderRef=true")
	}
}

func TestBinaryListMarshalYAML_WithAsset(t *testing.T) {
	list := BinaryList{
		{Name: "github.com/arg-sh/argsh", Asset: "argsh-so-*", Enforced: "v1.0.0"},
	}
	data, err := yaml.Marshal(&list)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(data)
	if !strings.Contains(s, "asset: argsh-so-*") {
		t.Errorf("marshal output missing asset field:\n%s", s)
	}
	if !strings.Contains(s, "version: v1.0.0") {
		t.Errorf("marshal output missing version field:\n%s", s)
	}
}

func TestBinaryListUnmarshalYAML_NilBinary(t *testing.T) {
	input := `
terraform:
`
	var list BinaryList
	if err := yaml.Unmarshal([]byte(input), &list); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("got %d binaries, want 1", len(list))
	}
	if list[0].Name != "terraform" {
		t.Errorf("name = %q", list[0].Name)
	}
}

func TestStateMarshalYAML(t *testing.T) {
	s := &State{
		Binaries: BinaryList{
			{Name: "jq"},
		},
		Envs: EnvList{
			{Key: "github.com/org/infra", Version: "v2.0"},
		},
	}

	result, err := s.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML() error = %v", err)
	}

	m, ok := result.(map[string]interface{})
	if !ok {
		t.Fatal("expected map result")
	}
	if _, ok := m["binaries"]; !ok {
		t.Error("missing binaries key")
	}
	if _, ok := m["envs"]; !ok {
		t.Error("missing envs key")
	}
}

func TestStateMarshalYAML_NoEnvs(t *testing.T) {
	s := &State{
		Binaries: BinaryList{
			{Name: "jq"},
		},
	}

	result, err := s.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML() error = %v", err)
	}

	m := result.(map[string]interface{})
	if _, ok := m["envs"]; ok {
		t.Error("should not have envs key when empty")
	}
}

func TestStateUnmarshalYAML(t *testing.T) {
	input := `
binaries:
  jq: {}
envs:
  github.com/org/repo:
    version: v1.0
`
	var s State
	if err := yaml.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(s.Binaries) != 1 {
		t.Errorf("got %d binaries, want 1", len(s.Binaries))
	}
	if len(s.Envs) != 1 {
		t.Errorf("got %d envs, want 1", len(s.Envs))
	}
}

func TestEnvListMarshalYAML_AllForms(t *testing.T) {
	list := EnvList{
		{Key: "github.com/org/empty"},                                 // bare entry
		{Key: "github.com/org/versioned", Version: "v2.0"},            // has version
		{Key: "github.com/org/strategy", Strategy: "merge"},           // non-default strategy
		{Key: "github.com/org/default-strategy", Strategy: "replace"}, // default strategy (should be omitted)
	}

	result, err := list.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML() error = %v", err)
	}

	m := result.(map[string]interface{})

	// empty entry should have empty struct
	if _, ok := m["github.com/org/empty"].(*struct{}); !ok {
		t.Errorf("empty entry should be *struct{}, got %T", m["github.com/org/empty"])
	}

	// strategy=replace (default) should be omitted
	if cfg, ok := m["github.com/org/default-strategy"].(map[string]interface{}); ok {
		if _, hasStrategy := cfg["strategy"]; hasStrategy {
			t.Error("default strategy 'replace' should be omitted")
		}
	}
}

func TestEnvListUnmarshalYAML_NilValue(t *testing.T) {
	input := `
github.com/org/bare:
`
	var list EnvList
	if err := yaml.Unmarshal([]byte(input), &list); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("got %d entries, want 1", len(list))
	}
	if list[0].Key != "github.com/org/bare" {
		t.Errorf("key = %q", list[0].Key)
	}
}

func TestParseFilesMap_AllForms(t *testing.T) {
	raw := map[string]interface{}{
		"dir/**": nil,                                             // null
		"file/*": "output/",                                       // string
		"cfg/**": map[interface{}]interface{}{"dest": "configs/"}, // map with dest
	}

	result := parseFilesMap(raw)
	if len(result) != 3 {
		t.Fatalf("got %d entries, want 3", len(result))
	}
	if result["dir/**"].Dest != "" {
		t.Errorf("null value should have empty dest")
	}
	if result["file/*"].Dest != "output/" {
		t.Errorf("string value dest = %q", result["file/*"].Dest)
	}
	if result["cfg/**"].Dest != "configs/" {
		t.Errorf("map value dest = %q", result["cfg/**"].Dest)
	}
}

func TestParseFilesMap_Nil(t *testing.T) {
	result := parseFilesMap(nil)
	if result != nil {
		t.Errorf("expected nil for nil input, got %v", result)
	}
}

func TestParseFilesMap_WithIgnore(t *testing.T) {
	raw := map[string]interface{}{
		"src/**": map[interface{}]interface{}{
			"dest":   "out/",
			"ignore": []interface{}{"*.bak", "*.tmp"},
		},
	}

	result := parseFilesMap(raw)
	gc := result["src/**"]
	if gc.Dest != "out/" {
		t.Errorf("dest = %q", gc.Dest)
	}
	if len(gc.Ignore) != 2 {
		t.Fatalf("ignore len = %d, want 2", len(gc.Ignore))
	}
	if gc.Ignore[0] != "*.bak" || gc.Ignore[1] != "*.tmp" {
		t.Errorf("ignore = %v", gc.Ignore)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsStr(s, sub))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
