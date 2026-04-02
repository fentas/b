package state

import (
	"strings"
	"testing"

	"github.com/fentas/b/pkg/envmatch"
)

func TestResolveProfileIncludes_NoIncludes(t *testing.T) {
	p := &EnvEntry{
		Key:   "base",
		Files: map[string]envmatch.GlobConfig{"a/**": {Dest: "a/"}},
	}
	resolved, err := ResolveProfileIncludes(p, EnvList{p})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved != p {
		t.Error("should return same pointer when no includes")
	}
}

func TestResolveProfileIncludes_Simple(t *testing.T) {
	base := &EnvEntry{
		Key:   "base",
		Files: map[string]envmatch.GlobConfig{"manifests/base/**": {Dest: "base/"}},
	}
	staging := &EnvEntry{
		Key:      "staging",
		Includes: []string{"base"},
		Files:    map[string]envmatch.GlobConfig{"manifests/staging/**": {Dest: "staging/"}},
	}
	profiles := EnvList{base, staging}

	resolved, err := ResolveProfileIncludes(staging, profiles)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := resolved.Files["manifests/base/**"]; !ok {
		t.Error("missing base files")
	}
	if _, ok := resolved.Files["manifests/staging/**"]; !ok {
		t.Error("missing staging files")
	}
	if len(resolved.Includes) != 0 {
		t.Error("includes should be nil after resolution")
	}
}

func TestResolveProfileIncludes_Transitive(t *testing.T) {
	core := &EnvEntry{
		Key:      "core",
		Files:    map[string]envmatch.GlobConfig{"core/**": {Dest: "core/"}},
		Strategy: "merge",
	}
	base := &EnvEntry{
		Key:      "base",
		Includes: []string{"core"},
		Files:    map[string]envmatch.GlobConfig{"base/**": {Dest: "base/"}},
	}
	full := &EnvEntry{
		Key:      "full",
		Includes: []string{"base"},
		Files:    map[string]envmatch.GlobConfig{"full/**": {Dest: "full/"}},
	}
	profiles := EnvList{core, base, full}

	resolved, err := ResolveProfileIncludes(full, profiles)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(resolved.Files) != 3 {
		t.Errorf("expected 3 file entries, got %d", len(resolved.Files))
	}
	if resolved.Strategy != "merge" {
		t.Errorf("strategy = %q, want 'merge' (from core)", resolved.Strategy)
	}
}

func TestResolveProfileIncludes_CircularDetected(t *testing.T) {
	a := &EnvEntry{Key: "a", Includes: []string{"b"}}
	b := &EnvEntry{Key: "b", Includes: []string{"a"}}
	profiles := EnvList{a, b}

	_, err := ResolveProfileIncludes(a, profiles)
	if err == nil {
		t.Fatal("expected circular include error")
	}
	if !strings.Contains(err.Error(), "circular") {
		t.Errorf("expected 'circular' in error, got: %v", err)
	}
}

func TestResolveProfileIncludes_SelfInclude(t *testing.T) {
	a := &EnvEntry{Key: "a", Includes: []string{"a"}}
	profiles := EnvList{a}

	_, err := ResolveProfileIncludes(a, profiles)
	if err == nil {
		t.Fatal("expected circular include error")
	}
}

func TestResolveProfileIncludes_MissingProfile(t *testing.T) {
	a := &EnvEntry{Key: "a", Includes: []string{"nonexistent"}}
	profiles := EnvList{a}

	_, err := ResolveProfileIncludes(a, profiles)
	if err == nil {
		t.Fatal("expected missing profile error")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error, got: %v", err)
	}
}

func TestResolveProfileIncludes_OverrideOrder(t *testing.T) {
	first := &EnvEntry{
		Key:   "first",
		Files: map[string]envmatch.GlobConfig{"shared/**": {Dest: "first/"}},
	}
	second := &EnvEntry{
		Key:   "second",
		Files: map[string]envmatch.GlobConfig{"shared/**": {Dest: "second/"}},
	}
	combo := &EnvEntry{
		Key:      "combo",
		Includes: []string{"first", "second"},
	}
	profiles := EnvList{first, second, combo}

	resolved, err := ResolveProfileIncludes(combo, profiles)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gc := resolved.Files["shared/**"]
	if gc.Dest != "second/" {
		t.Errorf("dest = %q, want 'second/' (later include wins)", gc.Dest)
	}
}

func TestResolveProfileIncludes_TopLevelOverrides(t *testing.T) {
	base := &EnvEntry{
		Key:      "base",
		Strategy: "replace",
		Files:    map[string]envmatch.GlobConfig{"a/**": {}},
	}
	top := &EnvEntry{
		Key:      "top",
		Includes: []string{"base"},
		Strategy: "merge",
	}
	profiles := EnvList{base, top}

	resolved, err := ResolveProfileIncludes(top, profiles)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resolved.Strategy != "merge" {
		t.Errorf("strategy = %q, want 'merge' (top-level overrides)", resolved.Strategy)
	}
}

func TestResolveProfileIncludes_IgnoreMerge(t *testing.T) {
	base := &EnvEntry{
		Key:    "base",
		Ignore: []string{"*.md", "LICENSE"},
		Files:  map[string]envmatch.GlobConfig{"a/**": {}},
	}
	top := &EnvEntry{
		Key:      "top",
		Includes: []string{"base"},
		Ignore:   []string{"*.test", "*.md"}, // *.md is duplicate
	}
	profiles := EnvList{base, top}

	resolved, err := ResolveProfileIncludes(top, profiles)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have *.md, LICENSE, *.test (deduplicated)
	if len(resolved.Ignore) != 3 {
		t.Errorf("expected 3 ignore entries, got %d: %v", len(resolved.Ignore), resolved.Ignore)
	}
}

func TestResolveProfileIncludes_DescriptionNotInherited(t *testing.T) {
	base := &EnvEntry{
		Key:         "base",
		Description: "Base description",
		Files:       map[string]envmatch.GlobConfig{"a/**": {}},
	}
	top := &EnvEntry{
		Key:         "top",
		Description: "Top description",
		Includes:    []string{"base"},
	}
	profiles := EnvList{base, top}

	resolved, err := ResolveProfileIncludes(top, profiles)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resolved.Description != "Top description" {
		t.Errorf("description = %q, want 'Top description'", resolved.Description)
	}
}
