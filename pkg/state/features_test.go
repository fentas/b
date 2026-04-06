package state

import (
	"testing"

	"github.com/fentas/b/pkg/envmatch"
	"gopkg.in/yaml.v3"
)

// --- Feature 4: EnvList.Remove ---

func TestEnvList_Remove_Found(t *testing.T) {
	list := EnvList{
		{Key: "github.com/org/a"},
		{Key: "github.com/org/b"},
		{Key: "github.com/org/c"},
	}

	if !list.Remove("github.com/org/b") {
		t.Error("Remove should return true when found")
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(list))
	}
	if list.Get("github.com/org/b") != nil {
		t.Error("removed entry should not be findable")
	}
}

func TestEnvList_Remove_NotFound(t *testing.T) {
	list := EnvList{
		{Key: "github.com/org/a"},
	}

	if list.Remove("github.com/org/missing") {
		t.Error("Remove should return false when not found")
	}
	if len(list) != 1 {
		t.Error("list should be unchanged")
	}
}

func TestEnvList_Remove_Empty(t *testing.T) {
	list := EnvList{}
	if list.Remove("any") {
		t.Error("Remove on empty list should return false")
	}
}

// --- Feature 10: Group field ---

func TestEnvEntry_GroupField_Unmarshal(t *testing.T) {
	yamlData := `
github.com/org/infra:
  version: v2.0
  group: dev
github.com/org/shared:
  group: prod
`
	var list EnvList
	if err := yaml.Unmarshal([]byte(yamlData), &list); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	for _, e := range list {
		switch e.Key {
		case "github.com/org/infra":
			if e.Group != "dev" {
				t.Errorf("infra group = %q, want %q", e.Group, "dev")
			}
			if e.Version != "v2.0" {
				t.Errorf("infra version = %q, want %q", e.Version, "v2.0")
			}
		case "github.com/org/shared":
			if e.Group != "prod" {
				t.Errorf("shared group = %q, want %q", e.Group, "prod")
			}
		default:
			t.Errorf("unexpected key: %s", e.Key)
		}
	}
}

func TestEnvEntry_GroupField_Marshal(t *testing.T) {
	list := EnvList{
		{Key: "github.com/org/infra", Version: "v2.0", Group: "dev"},
	}

	data, err := yaml.Marshal(&list)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	// Unmarshal back and verify
	var list2 EnvList
	if err := yaml.Unmarshal(data, &list2); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if len(list2) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(list2))
	}
	if list2[0].Group != "dev" {
		t.Errorf("group = %q, want %q", list2[0].Group, "dev")
	}
}

// --- Feature 7: Hook fields ---

func TestEnvEntry_HookFields_Unmarshal(t *testing.T) {
	yamlData := `
github.com/org/infra:
  version: v2.0
  onPreSync: "echo pre"
  onPostSync: "echo post"
`
	var list EnvList
	if err := yaml.Unmarshal([]byte(yamlData), &list); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if len(list) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(list))
	}
	e := list[0]
	if e.OnPreSync != "echo pre" {
		t.Errorf("OnPreSync = %q, want %q", e.OnPreSync, "echo pre")
	}
	if e.OnPostSync != "echo post" {
		t.Errorf("OnPostSync = %q, want %q", e.OnPostSync, "echo post")
	}
}

func TestEnvEntry_HookFields_Marshal(t *testing.T) {
	list := EnvList{
		{Key: "github.com/org/infra", OnPreSync: "validate.sh", OnPostSync: "reload.sh"},
	}

	data, err := yaml.Marshal(&list)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var list2 EnvList
	if err := yaml.Unmarshal(data, &list2); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if len(list2) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(list2))
	}
	if list2[0].OnPreSync != "validate.sh" {
		t.Errorf("OnPreSync = %q, want %q", list2[0].OnPreSync, "validate.sh")
	}
	if list2[0].OnPostSync != "reload.sh" {
		t.Errorf("OnPostSync = %q, want %q", list2[0].OnPostSync, "reload.sh")
	}
}

// --- Description field ---

func TestEnvEntry_Description_Unmarshal(t *testing.T) {
	yamlData := `
github.com/org/infra#base:
  description: "Base Kubernetes manifests"
  version: v2.0
github.com/org/infra#monitoring:
  description: "Prometheus + Grafana stack"
`
	var list EnvList
	if err := yaml.Unmarshal([]byte(yamlData), &list); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if len(list) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(list))
	}

	foundBase := false
	foundMonitoring := false

	for _, e := range list {
		switch e.Key {
		case "github.com/org/infra#base":
			foundBase = true
			if e.Description != "Base Kubernetes manifests" {
				t.Errorf("base description = %q", e.Description)
			}
		case "github.com/org/infra#monitoring":
			foundMonitoring = true
			if e.Description != "Prometheus + Grafana stack" {
				t.Errorf("monitoring description = %q", e.Description)
			}
		}
	}

	if !foundBase {
		t.Error("expected entry github.com/org/infra#base to be present")
	}
	if !foundMonitoring {
		t.Error("expected entry github.com/org/infra#monitoring to be present")
	}
}

func TestEnvEntry_Description_Roundtrip(t *testing.T) {
	list := EnvList{
		{Key: "github.com/org/infra#base", Description: "Base manifests", Version: "v2.0"},
	}

	data, err := yaml.Marshal(&list)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var list2 EnvList
	if err := yaml.Unmarshal(data, &list2); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if len(list2) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(list2))
	}
	if list2[0].Description != "Base manifests" {
		t.Errorf("description = %q, want %q", list2[0].Description, "Base manifests")
	}
}

// --- Profiles top-level key ---

func TestState_ProfilesSection_Roundtrip(t *testing.T) {
	yamlData := `
binaries: {}
profiles:
  base:
    description: "Base manifests"
    files:
      manifests/base/**:
        dest: base/
  monitoring:
    description: "Monitoring stack"
    strategy: merge
`
	var s State
	if err := yaml.Unmarshal([]byte(yamlData), &s); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if len(s.Profiles) != 2 {
		t.Fatalf("expected 2 profiles, got %d", len(s.Profiles))
	}

	found := map[string]bool{}
	for _, p := range s.Profiles {
		found[p.Key] = true
		if p.Key == "base" && p.Description != "Base manifests" {
			t.Errorf("base description = %q", p.Description)
		}
		if p.Key == "monitoring" && p.Strategy != "merge" {
			t.Errorf("monitoring strategy = %q", p.Strategy)
		}
	}
	if !found["base"] || !found["monitoring"] {
		t.Errorf("missing profiles: %v", found)
	}

	// Marshal and re-parse
	data, err := yaml.Marshal(&s)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	var s2 State
	if err := yaml.Unmarshal(data, &s2); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if len(s2.Profiles) != 2 {
		t.Fatalf("roundtrip: expected 2 profiles, got %d", len(s2.Profiles))
	}
}

func TestState_ProfilesAndEnvsSeparate(t *testing.T) {
	yamlData := `
binaries: {}
profiles:
  base:
    description: "Base config"
envs:
  github.com/org/infra:
    version: v2.0
`
	var s State
	if err := yaml.Unmarshal([]byte(yamlData), &s); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if len(s.Profiles) != 1 {
		t.Fatalf("expected 1 profile, got %d", len(s.Profiles))
	}
	if len(s.Envs) != 1 {
		t.Fatalf("expected 1 env, got %d", len(s.Envs))
	}
	if s.Profiles[0].Key != "base" {
		t.Errorf("profile key = %q, want %q", s.Profiles[0].Key, "base")
	}
	if s.Envs[0].Key != "github.com/org/infra" {
		t.Errorf("env key = %q", s.Envs[0].Key)
	}
}

// --- Feature 10+7: All new fields together ---

func TestEnvEntry_AllNewFields_Roundtrip(t *testing.T) {
	yamlData := `
github.com/org/infra:
  version: v2.0
  strategy: merge
  group: staging
  onPreSync: "echo starting"
  onPostSync: "echo done"
`
	var list EnvList
	if err := yaml.Unmarshal([]byte(yamlData), &list); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	e := list[0]
	if e.Group != "staging" {
		t.Errorf("Group = %q, want %q", e.Group, "staging")
	}
	if e.Strategy != "merge" {
		t.Errorf("Strategy = %q, want %q", e.Strategy, "merge")
	}
	if e.OnPreSync != "echo starting" {
		t.Errorf("OnPreSync = %q", e.OnPreSync)
	}
	if e.OnPostSync != "echo done" {
		t.Errorf("OnPostSync = %q", e.OnPostSync)
	}

	// Marshal and re-parse
	data, err := yaml.Marshal(&list)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	var list2 EnvList
	if err := yaml.Unmarshal(data, &list2); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	e2 := list2[0]
	if e2.Group != "staging" {
		t.Errorf("roundtrip Group = %q", e2.Group)
	}
	if e2.OnPreSync != "echo starting" {
		t.Errorf("roundtrip OnPreSync = %q", e2.OnPreSync)
	}
}

// --- Select field parsing ---

func TestParseFilesMap_WithSelect(t *testing.T) {
	yamlData := `
github.com/org/repo:
  files:
    .bin/b.yaml:
      select:
        - .binaries
        - .envs
    manifests/**:
`
	var list EnvList
	if err := yaml.Unmarshal([]byte(yamlData), &list); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if len(list) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(list))
	}

	files := list[0].Files
	gc, ok := files[".bin/b.yaml"]
	if !ok {
		t.Fatal("expected .bin/b.yaml in files")
	}
	if len(gc.Select) != 2 {
		t.Fatalf("expected 2 selectors, got %d", len(gc.Select))
	}
	if gc.Select[0] != ".binaries" || gc.Select[1] != ".envs" {
		t.Errorf("Select = %v", gc.Select)
	}

	// manifests/** should have no select
	gc2, ok := files["manifests/**"]
	if !ok {
		t.Fatal("expected manifests/** in files")
	}
	if len(gc2.Select) != 0 {
		t.Errorf("manifests should have no select, got %v", gc2.Select)
	}
}

func TestMarshalYAML_WithSelect(t *testing.T) {
	list := EnvList{
		{
			Key: "github.com/org/repo",
			Files: map[string]envmatch.GlobConfig{
				".bin/b.yaml":  {Select: []string{".binaries"}},
				"manifests/**": {},
			},
		},
	}

	data, err := yaml.Marshal(&list)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	// Unmarshal back and verify
	var list2 EnvList
	if err := yaml.Unmarshal(data, &list2); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	files := list2[0].Files
	gc := files[".bin/b.yaml"]
	if len(gc.Select) != 1 || gc.Select[0] != ".binaries" {
		t.Errorf("roundtrip Select = %v", gc.Select)
	}

	// manifests/** should be bare key (nil → no select)
	gc2 := files["manifests/**"]
	if len(gc2.Select) != 0 {
		t.Errorf("bare key should have no select after roundtrip, got %v", gc2.Select)
	}
}
