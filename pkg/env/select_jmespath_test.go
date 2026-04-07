package env

import (
	"strings"
	"testing"
)

// --- classifier ---

func TestIsSimpleDotPath(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		// simple
		{"binaries", true},
		{".binaries", true},
		{"binaries.kubectl", true},
		{"database.host", true},
		{"a-b", true},
		{"a_b", true},
		{"a.b.c.d", true},
		// complex
		{"", false},
		{".", false},
		{"..", false},
		{"..a", false}, // multiple leading dots — per copilot review on PR #127
		{"...a.b", false},
		{"a..b", false},
		{"a.", false},
		{"binaries[0]", false},
		{"binaries | [?groups]", false},
		{"{a: b}", false},
		{"from_items(items(binaries))", false},
		{"binaries.*", false},
		{"'quoted.key'", false},
	}
	for _, c := range cases {
		got := isSimpleDotPath(c.in)
		if got != c.want {
			t.Errorf("isSimpleDotPath(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestSplitSelectorsByComplexity(t *testing.T) {
	simple, complex := splitSelectorsByComplexity([]string{
		"binaries",
		"{filtered: binaries.kubectl}",
		"database.host",
		"binaries | [?contains([1].groups, 'core')]",
	})
	if len(simple) != 2 || simple[0] != "binaries" || simple[1] != "database.host" {
		t.Errorf("simple = %v", simple)
	}
	if len(complex) != 2 {
		t.Errorf("complex = %v", complex)
	}
}

// --- JMESPath YAML path ---

func TestFilterYAMLJMESPath_MultiSelectHash(t *testing.T) {
	content := []byte(`binaries:
  kubectl: {}
  kustomize: {}
profiles:
  core: {}
envs:
  github.com/x/y: {}
`)
	// JMESPath multi-select hash: wrap binaries under a custom key.
	out, err := filterYAMLJMESPath(content, []string{"{bins: binaries}"})
	if err != nil {
		t.Fatalf("filterYAMLJMESPath: %v", err)
	}
	outStr := string(out)
	if !strings.Contains(outStr, "bins:") {
		t.Errorf("expected wrapper key 'bins', got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "kubectl") {
		t.Errorf("missing kubectl, got:\n%s", outStr)
	}
	// profiles/envs must be dropped
	if strings.Contains(outStr, "profiles:") {
		t.Errorf("profiles not filtered, got:\n%s", outStr)
	}
	if strings.Contains(outStr, "envs:") {
		t.Errorf("envs not filtered, got:\n%s", outStr)
	}
}

// TestFilterYAMLJMESPath_BinaryGroupsFilter exercises the flagship use
// case from issue #124: filter binaries by a `groups` field, preserving
// the original keys via from_items/items.
func TestFilterYAMLJMESPath_BinaryGroupsFilter(t *testing.T) {
	content := []byte(`binaries:
  kubectl:
    groups: [core, cli]
  kustomize:
    groups: [core, build]
  tilt:
    groups: [local-dev]
  hcloud:
    groups: [hetzner]
profiles:
  core: {}
`)
	// Pick only binaries that have 'core' in their groups. In
	// jmespath-community v1.1.1, items() returns [key, value] tuples, so
	// [1].groups accesses the value's groups field.
	out, err := filterYAMLJMESPath(content, []string{
		"{binaries: from_items(items(binaries)[?contains([1].groups, 'core')])}",
	})
	if err != nil {
		t.Fatalf("filterYAMLJMESPath: %v", err)
	}
	outStr := string(out)
	// Both core binaries present with original keys.
	if !strings.Contains(outStr, "kubectl") {
		t.Errorf("kubectl dropped, got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "kustomize") {
		t.Errorf("kustomize dropped, got:\n%s", outStr)
	}
	// Non-core binaries filtered out.
	if strings.Contains(outStr, "tilt") {
		t.Errorf("tilt should be filtered out, got:\n%s", outStr)
	}
	if strings.Contains(outStr, "hcloud") {
		t.Errorf("hcloud should be filtered out, got:\n%s", outStr)
	}
	// profiles dropped too (not in the expression result).
	if strings.Contains(outStr, "profiles:") {
		t.Errorf("profiles should be dropped, got:\n%s", outStr)
	}
}

// TestFilterYAMLJMESPath_OrAndFilter — multi-group OR selection
func TestFilterYAMLJMESPath_OrAndFilter(t *testing.T) {
	content := []byte(`binaries:
  kubectl:
    groups: [core]
  helm:
    groups: [infra]
  tilt:
    groups: [local-dev]
`)
	out, err := filterYAMLJMESPath(content, []string{
		"{binaries: from_items(items(binaries)[?contains([1].groups, 'core') || contains([1].groups, 'infra')])}",
	})
	if err != nil {
		t.Fatalf("%v", err)
	}
	outStr := string(out)
	if !strings.Contains(outStr, "kubectl") {
		t.Error("kubectl missing")
	}
	if !strings.Contains(outStr, "helm") {
		t.Error("helm missing")
	}
	if strings.Contains(outStr, "tilt") {
		t.Error("tilt should be filtered")
	}
}

// --- routing via filterContent ---

func TestFilterContent_Hybrid_SimpleOnly(t *testing.T) {
	content := []byte(`binaries:
  kubectl: {}
profiles:
  core: {}
`)
	// Simple dot-path → Node API path → comments preserved (we don't
	// assert comments here, just that it works backward-compatibly).
	out, err := filterContent(content, []string{"binaries"}, "b.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "kubectl") {
		t.Errorf("got %s", out)
	}
}

func TestFilterContent_Hybrid_ComplexOnly(t *testing.T) {
	content := []byte(`binaries:
  a:
    groups: [core]
  b:
    groups: [other]
`)
	out, err := filterContent(content,
		[]string{"{binaries: from_items(items(binaries)[?contains([1].groups, 'core')])}"},
		"b.yaml")
	if err != nil {
		t.Fatal(err)
	}
	outStr := string(out)
	if !strings.Contains(outStr, "a:") {
		t.Errorf("a missing, got:\n%s", outStr)
	}
	if strings.Contains(outStr, "b:") {
		t.Errorf("b should be filtered, got:\n%s", outStr)
	}
}

// TestFilterContent_Hybrid_Mixed_PreservesSimpleComments verifies that
// when a mixed-list select runs, the simple-side keys keep their
// comments. Per copilot review on PR #127: the previous map-roundtrip
// implementation of mergeYAMLTopLevel silently dropped comments here,
// contradicting the docs claim.
func TestFilterContent_Hybrid_Mixed_PreservesSimpleComments(t *testing.T) {
	content := []byte(`# top-of-file comment
binaries:
  a:
    groups: [core]
  b:
    groups: [other]

# this comment is attached to envs
envs:
  github.com/example/repo: # inline comment
    files:
      a.yaml:
        dest: a.yaml
`)
	out, err := filterContent(content,
		[]string{
			"envs", // simple → Node API path
			"{binaries: from_items(items(binaries)[?contains([1].groups, 'core')])}", // complex → JMESPath
		},
		"b.yaml")
	if err != nil {
		t.Fatal(err)
	}
	outStr := string(out)

	// JMESPath side worked.
	if !strings.Contains(outStr, "binaries:") || !strings.Contains(outStr, "a:") {
		t.Errorf("binaries scope missing, got:\n%s", outStr)
	}
	if strings.Contains(outStr, "b:") {
		t.Errorf("b should be filtered out by JMESPath, got:\n%s", outStr)
	}

	// Simple-side comments survived. Either the head comment on `envs:`
	// or the inline comment on the github.com line should be present —
	// any of these proves the merge didn't strip them.
	hasComment := strings.Contains(outStr, "# this comment is attached to envs") ||
		strings.Contains(outStr, "# inline comment")
	if !hasComment {
		t.Errorf("simple-side comments stripped during mixed merge, got:\n%s", outStr)
	}
}

func TestFilterContent_Hybrid_Mixed(t *testing.T) {
	content := []byte(`binaries:
  a: {}
envs:
  github.com/x/y: {}
profiles:
  core: {}
`)
	// Simple "envs" + complex "{binaries: from_items(...)}".
	out, err := filterContent(content,
		[]string{"envs", "{binaries: from_items(items(binaries)[?true])}"},
		"b.yaml")
	if err != nil {
		t.Fatal(err)
	}
	outStr := string(out)
	// Both sides merged: envs from simple path, binaries from JMESPath.
	if !strings.Contains(outStr, "envs:") {
		t.Errorf("envs dropped, got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "binaries:") {
		t.Errorf("binaries dropped, got:\n%s", outStr)
	}
	// profiles not selected by either side.
	if strings.Contains(outStr, "profiles:") {
		t.Errorf("profiles should be dropped, got:\n%s", outStr)
	}
}

func TestFilterContent_JMESPath_JSON(t *testing.T) {
	content := []byte(`{"binaries": {"a": {"g": ["x"]}, "b": {"g": ["y"]}}, "envs": {}}`)
	out, err := filterContent(content,
		[]string{"{binaries: from_items(items(binaries)[?contains([1].g, 'x')])}"},
		"config.json")
	if err != nil {
		t.Fatal(err)
	}
	outStr := string(out)
	if !strings.Contains(outStr, `"a"`) {
		t.Errorf("a missing, got:\n%s", outStr)
	}
	if strings.Contains(outStr, `"b"`) {
		t.Errorf("b should be filtered, got:\n%s", outStr)
	}
}

func TestFilterContent_JMESPath_UnknownKey(t *testing.T) {
	// JMESPath returns null for missing paths; runJMESPathSelectors must
	// skip nil results and not crash.
	content := []byte("binaries: {}\n")
	out, err := filterContent(content, []string{"{missing: nonexistent}"}, "b.yaml")
	if err != nil {
		t.Fatal(err)
	}
	// Empty result → "{}\n" or similar; just check we didn't error.
	_ = out
}

// TestFilterContent_InvalidJMESPath ensures a malformed expression returns
// an error instead of panicking.
func TestFilterContent_InvalidJMESPath(t *testing.T) {
	content := []byte("binaries: {}\n")
	_, err := filterContent(content, []string{"[?"}, "b.yaml")
	if err == nil {
		t.Error("expected error for malformed JMESPath")
	}
}

// TestWrapKeyFor covers the fallback-key logic for non-map results.
func TestWrapKeyFor(t *testing.T) {
	cases := []struct {
		sel, want string
	}{
		{"binaries", "binaries"},
		{".binaries", "binaries"},
		{"database.host", "host"},
		{"items(binaries)", "result"},
		{"", "result"},
	}
	for _, c := range cases {
		if got := wrapKeyFor(c.sel); got != c.want {
			t.Errorf("wrapKeyFor(%q) = %q, want %q", c.sel, got, c.want)
		}
	}
}
