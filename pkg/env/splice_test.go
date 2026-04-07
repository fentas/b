package env

import (
	"strings"
	"testing"
)

func TestTopLevelKeysFromSelectors(t *testing.T) {
	cases := []struct {
		name      string
		selectors []string
		want      []string
	}{
		{"empty", nil, nil},
		{"single top-level", []string{"binaries"}, []string{"binaries"}},
		{"leading dot", []string{".binaries"}, []string{"binaries"}},
		{"nested path", []string{"database.host"}, []string{"database"}},
		{"multiple", []string{"binaries", "profiles"}, []string{"binaries", "profiles"}},
		{"dedup", []string{"binaries", ".binaries"}, []string{"binaries"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := topLevelKeysFromSelectors(c.selectors)
			if len(got) != len(c.want) {
				t.Errorf("len = %d, want %d (%v)", len(got), len(c.want), got)
			}
			for _, k := range c.want {
				if !got[k] {
					t.Errorf("missing key %q in %v", k, got)
				}
			}
		})
	}
}

func TestContainsConflictMarkers(t *testing.T) {
	yes := []byte("a\n<<<<<<< local\nb\n=======\nc\n>>>>>>> upstream\nd\n")
	if !containsConflictMarkers(yes) {
		t.Error("expected true for marked content")
	}
	no := []byte("binaries:\n  kubectl: {}\n")
	if containsConflictMarkers(no) {
		t.Error("expected false for clean YAML")
	}
	partial := []byte("# ======= section separator =======\n")
	if containsConflictMarkers(partial) {
		t.Error("partial match should not count")
	}
}

// TestSpliceYAMLStructural_ReplacesScopedKey verifies the fast path: a
// scoped top-level key is replaced, out-of-scope keys are untouched.
func TestSpliceYAMLStructural_ReplacesScopedKey(t *testing.T) {
	local := []byte(`binaries:
  kubectl: {}
  argsh: {}

envs:
  github.com/example/repo:
    files:
      README.md:
        dest: docs/README.md
`)
	merged := []byte(`binaries:
  kubectl: {}
  kustomize: {}
  tilt: {}
`)
	out, err := spliceSelectedScope(local, merged, []string{"binaries"}, "b.yaml")
	if err != nil {
		t.Fatalf("spliceSelectedScope: %v", err)
	}
	outStr := string(out)
	// binaries is now the merged version
	if !strings.Contains(outStr, "kustomize") {
		t.Errorf("merged key kustomize missing: %s", outStr)
	}
	if !strings.Contains(outStr, "tilt") {
		t.Errorf("merged key tilt missing: %s", outStr)
	}
	// envs survived
	if !strings.Contains(outStr, "github.com/example/repo") {
		t.Errorf("envs scope dropped: %s", outStr)
	}
	if !strings.Contains(outStr, "docs/README.md") {
		t.Errorf("envs dest dropped: %s", outStr)
	}
}

// TestSpliceYAMLStructural_RemovesScopedKeyAbsentInMerge verifies that if
// the merge decided a scoped key should no longer exist, the splice
// removes it from local too.
func TestSpliceYAMLStructural_RemovesScopedKeyAbsentInMerge(t *testing.T) {
	local := []byte(`binaries:
  helm: {}
profiles:
  something: {}
`)
	// merged doesn't include binaries at all — merge resolved to "no
	// scoped content remains".
	merged := []byte(`{}
`)
	out, err := spliceSelectedScope(local, merged, []string{"binaries"}, "b.yaml")
	if err != nil {
		t.Fatalf("splice: %v", err)
	}
	outStr := string(out)
	if strings.Contains(outStr, "helm") {
		t.Errorf("binaries.helm should have been removed by splice, got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "profiles") {
		t.Errorf("out-of-scope profiles was dropped, got:\n%s", outStr)
	}
}

// TestSpliceYAMLText_PreservesOutOfScopeOnConflict verifies the
// conflict-path splice: when merged contains conflict markers, out-of-scope
// content is still preserved byte-for-byte.
func TestSpliceYAMLText_PreservesOutOfScopeOnConflict(t *testing.T) {
	local := []byte(`binaries:
  kubectl: {}
  argsh: {}

envs:
  github.com/keep/me:
    files:
      a.yaml:
        dest: a.yaml
`)
	// merged content contains conflict markers — can't be parsed as YAML
	merged := []byte(`binaries:
  kubectl: {}
<<<<<<< local
  argsh: {}
=======
  tilt: {}
>>>>>>> upstream
`)
	out, err := spliceSelectedScope(local, merged, []string{"binaries"}, "b.yaml")
	if err != nil {
		t.Fatalf("splice: %v", err)
	}
	outStr := string(out)

	// Out-of-scope content preserved byte-for-byte
	if !strings.Contains(outStr, "github.com/keep/me") {
		t.Errorf("out-of-scope envs dropped during conflict splice, got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "a.yaml") {
		t.Errorf("out-of-scope env dest dropped, got:\n%s", outStr)
	}

	// Conflict markers present — user will need to resolve
	if !containsConflictMarkers(out) {
		t.Errorf("conflict markers should have been passed through, got:\n%s", outStr)
	}
}

// TestSpliceYAMLText_AppendsWhenLocalHasNoScopedKey verifies the edge case
// where the local file has no top-level key in the selector scope: the
// merged content should be appended.
func TestSpliceYAMLText_AppendsWhenLocalHasNoScopedKey(t *testing.T) {
	local := []byte(`envs:
  foo: bar
`)
	merged := []byte(`<<<<<<< local
binaries: {}
=======
binaries:
  new: {}
>>>>>>> upstream
`)
	out, err := spliceSelectedScope(local, merged, []string{"binaries"}, "b.yaml")
	if err != nil {
		t.Fatalf("splice: %v", err)
	}
	outStr := string(out)
	// envs kept
	if !strings.Contains(outStr, "envs") || !strings.Contains(outStr, "foo: bar") {
		t.Errorf("envs dropped, got:\n%s", outStr)
	}
	// merged content appended
	if !strings.Contains(outStr, "<<<<<<< local") {
		t.Errorf("merged content missing, got:\n%s", outStr)
	}
}

// TestSpliceSelectedScope_JSONPassthrough — JSON splice is not yet
// implemented, so the function should return merged unchanged for .json.
func TestSpliceSelectedScope_JSONPassthrough(t *testing.T) {
	local := []byte(`{"binaries": {"a": 1}, "envs": {}}`)
	merged := []byte(`{"binaries": {"a": 1, "b": 2}}`)
	out, err := spliceSelectedScope(local, merged, []string{"binaries"}, "config.json")
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(merged) {
		t.Errorf("JSON splice should pass merged through, got %q", out)
	}
}

// TestSpliceSelectedScope_NoSelectors — no selectors means "merge was
// whole-file", so splice is a pass-through.
func TestSpliceSelectedScope_NoSelectors(t *testing.T) {
	local := []byte("old\n")
	merged := []byte("new\n")
	out, err := spliceSelectedScope(local, merged, nil, "foo.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "new\n" {
		t.Errorf("no-selectors splice should equal merged, got %q", out)
	}
}
