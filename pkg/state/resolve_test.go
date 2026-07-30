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

func TestResolveProfileIncludes_SharedDependency(t *testing.T) {
	// Diamond: A includes B and C, both include D. Should NOT be a cycle.
	d := &EnvEntry{
		Key:   "d",
		Files: map[string]envmatch.GlobConfig{"d/**": {Dest: "d/"}},
	}
	b := &EnvEntry{
		Key:      "b",
		Includes: []string{"d"},
		Files:    map[string]envmatch.GlobConfig{"b/**": {Dest: "b/"}},
	}
	c := &EnvEntry{
		Key:      "c",
		Includes: []string{"d"},
		Files:    map[string]envmatch.GlobConfig{"c/**": {Dest: "c/"}},
	}
	a := &EnvEntry{
		Key:      "a",
		Includes: []string{"b", "c"},
	}
	profiles := EnvList{a, b, c, d}

	resolved, err := ResolveProfileIncludes(a, profiles)
	if err != nil {
		t.Fatalf("shared dependency should not be circular: %v", err)
	}

	// Should have files from d, b, c (d included once)
	if _, ok := resolved.Files["d/**"]; !ok {
		t.Error("missing d files")
	}
	if _, ok := resolved.Files["b/**"]; !ok {
		t.Error("missing b files")
	}
	if _, ok := resolved.Files["c/**"]; !ok {
		t.Error("missing c files")
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

func TestResolveProfileIncludes_SameGlobSelectsUnion(t *testing.T) {
	// The lok8s shape: every profile in the chain selects a DIFFERENT binaries
	// group out of the SAME .bin/b.yaml. Replacing the glob config kept only
	// the outermost profile's selector, so `kubeone` installed the kubeone
	// group and none of core's (argsh, sops, kubectl, jq, yq).
	core := &EnvEntry{
		Key:   "core",
		Files: map[string]envmatch.GlobConfig{".bin/b.yaml": {Select: []string{"{binaries: core}"}}},
	}
	kustomize := &EnvEntry{
		Key:   "kustomize",
		Files: map[string]envmatch.GlobConfig{".bin/b.yaml": {Select: []string{"{binaries: kustomize}"}}},
	}
	local := &EnvEntry{
		Key:      "local",
		Includes: []string{"core", "kustomize"},
		Files:    map[string]envmatch.GlobConfig{".bin/b.yaml": {Select: []string{"{binaries: local}"}}},
	}
	kubeone := &EnvEntry{
		Key:      "kubeone",
		Includes: []string{"local"},
		Files:    map[string]envmatch.GlobConfig{".bin/b.yaml": {Select: []string{"{binaries: kubeone}"}}},
	}

	resolved, err := ResolveProfileIncludes(kubeone, EnvList{core, kustomize, local, kubeone})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	got := resolved.Files[".bin/b.yaml"].Select
	want := []string{
		"{binaries: core}",
		"{binaries: kustomize}",
		"{binaries: local}",
		"{binaries: kubeone}",
	}
	if len(got) != len(want) {
		t.Fatalf("selectors for .bin/b.yaml = %v, want all four groups %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("selector %d = %q, want %q (base profiles must come first)", i, got[i], w)
		}
	}
}

func TestResolveProfileIncludes_SameGlobDestAndIgnoreMerge(t *testing.T) {
	base := &EnvEntry{
		Key: "base",
		Files: map[string]envmatch.GlobConfig{
			"x/**": {Dest: "base/", Ignore: []string{"*.tmp"}},
		},
	}
	child := &EnvEntry{
		Key:      "child",
		Includes: []string{"base"},
		Files: map[string]envmatch.GlobConfig{
			"x/**": {Dest: "child/", Ignore: []string{"*.bak", "*.tmp"}},
		},
	}

	resolved, err := ResolveProfileIncludes(child, EnvList{base, child})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	gc := resolved.Files["x/**"]
	// One file can only land in one place, so Dest stays last-wins.
	if gc.Dest != "child/" {
		t.Errorf("Dest = %q, want child/ (override wins)", gc.Dest)
	}
	if len(gc.Ignore) != 2 || gc.Ignore[0] != "*.tmp" || gc.Ignore[1] != "*.bak" {
		t.Errorf("Ignore = %v, want [*.tmp *.bak] unioned without duplicates", gc.Ignore)
	}
}
