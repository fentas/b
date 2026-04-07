package env

import (
	"strings"
	"testing"
)

// TestMergeWithStructuralFallback_CleanTextMergePreservesBytes verifies
// the doMerge wiring: when the text 3-way merge resolves cleanly, we
// keep its byte-for-byte output and DO NOT round-trip through the
// structural merge (which would reorder keys and drop comments).
func TestMergeWithStructuralFallback_CleanTextMergePreservesBytes(t *testing.T) {
	base := []byte("a: 1\n# stays\nb: 2\n")
	local := []byte("a: 1\n# stays\nb: 2\nc: 3\n") // local adds c
	upstream := []byte("a: 1\n# stays\nb: 2\n")    // upstream unchanged
	out, hasConflict, err := mergeWithStructuralFallback(local, base, upstream, "b.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if hasConflict {
		t.Errorf("expected no conflict")
	}
	if !strings.Contains(string(out), "# stays") {
		t.Errorf("comment lost from clean text merge:\n%s", out)
	}
}

// TestMergeWithStructuralFallback_AdjacentInsertsResolveStructurally
// is the integration test for the doMerge structural fallback wiring:
// the text merge would normally produce conflict markers when local
// and upstream both add adjacent map entries, and we expect the
// structural merge to take over and produce a clean result.
func TestMergeWithStructuralFallback_AdjacentInsertsResolveStructurally(t *testing.T) {
	base := []byte("binaries:\n  kubectl: {}\n")
	local := []byte("binaries:\n  kubectl: {}\n  helm: {}\n")
	upstream := []byte("binaries:\n  kubectl: {}\n  kustomize: {}\n")
	out, hasConflict, err := mergeWithStructuralFallback(local, base, upstream, "b.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if hasConflict {
		t.Errorf("expected structural merge to resolve adjacent inserts cleanly")
	}
	s := string(out)
	for _, k := range []string{"kubectl", "helm", "kustomize"} {
		if !strings.Contains(s, k) {
			t.Errorf("missing %q in fallback output:\n%s", k, s)
		}
	}
	// The output must NOT contain conflict markers.
	if strings.Contains(s, "<<<<<<<") {
		t.Errorf("conflict markers leaked into clean fallback output:\n%s", s)
	}
}

// TestStructural_AdjacentInsertsNoConflict is the headline case: local and
// upstream both add new entries inside the same map. The text-based
// git merge-file path flags this as a conflict because the inserted lines
// land next to each other; the structural merge resolves it cleanly.
func TestStructural_AdjacentInsertsNoConflict(t *testing.T) {
	base := []byte(`binaries:
  kubectl: {}
`)
	local := []byte(`binaries:
  kubectl: {}
  helm: {}
`)
	upstream := []byte(`binaries:
  kubectl: {}
  kustomize: {}
`)
	out, conflict, err := Merge3WayStructural(local, base, upstream, "b.yaml")
	if err != nil {
		t.Fatalf("structural merge: %v", err)
	}
	if conflict {
		t.Fatal("expected no conflict for adjacent inserts")
	}
	s := string(out)
	for _, k := range []string{"kubectl", "helm", "kustomize"} {
		if !strings.Contains(s, k) {
			t.Errorf("missing %q in merged output:\n%s", k, s)
		}
	}
}

// TestStructural_LeafConflict reports a conflict when both sides change
// the same scalar to different values.
func TestStructural_LeafConflict(t *testing.T) {
	base := []byte(`a: 1` + "\n")
	local := []byte(`a: 2` + "\n")
	upstream := []byte(`a: 3` + "\n")
	_, conflict, err := Merge3WayStructural(local, base, upstream, "b.yaml")
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if !conflict {
		t.Fatal("expected leaf conflict")
	}
}

// TestStructural_OneSideUnchanged takes the side that did change.
func TestStructural_OneSideUnchanged(t *testing.T) {
	base := []byte("a: 1\nb: 1\n")
	local := []byte("a: 1\nb: 1\n")
	upstream := []byte("a: 1\nb: 2\n")
	out, conflict, err := Merge3WayStructural(local, base, upstream, "b.yaml")
	if err != nil || conflict {
		t.Fatalf("err=%v conflict=%v", err, conflict)
	}
	if !strings.Contains(string(out), "b: 2") {
		t.Errorf("expected b: 2, got:\n%s", out)
	}
}

// TestStructural_DeleteAccepted: upstream deletes a key, local left it
// unchanged → drop it.
func TestStructural_DeleteAccepted(t *testing.T) {
	base := []byte("a: 1\nb: 2\n")
	local := []byte("a: 1\nb: 2\n")
	upstream := []byte("a: 1\n")
	out, conflict, err := Merge3WayStructural(local, base, upstream, "b.yaml")
	if err != nil || conflict {
		t.Fatalf("err=%v conflict=%v", err, conflict)
	}
	if strings.Contains(string(out), "b:") {
		t.Errorf("b should be deleted, got:\n%s", out)
	}
}

// TestStructural_DeleteModifyConflict: upstream deletes, local modified.
func TestStructural_DeleteModifyConflict(t *testing.T) {
	base := []byte("a: 1\nb: 2\n")
	local := []byte("a: 1\nb: 9\n")
	upstream := []byte("a: 1\n")
	_, conflict, err := Merge3WayStructural(local, base, upstream, "b.yaml")
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if !conflict {
		t.Fatal("expected delete/modify conflict")
	}
}

// TestStructural_JSON exercises the JSON code path.
func TestStructural_JSON(t *testing.T) {
	base := []byte(`{"binaries":{"kubectl":{}}}`)
	local := []byte(`{"binaries":{"kubectl":{},"helm":{}}}`)
	upstream := []byte(`{"binaries":{"kubectl":{},"kustomize":{}}}`)
	out, conflict, err := Merge3WayStructural(local, base, upstream, "config.json")
	if err != nil || conflict {
		t.Fatalf("err=%v conflict=%v", err, conflict)
	}
	s := string(out)
	for _, k := range []string{"kubectl", "helm", "kustomize"} {
		if !strings.Contains(s, k) {
			t.Errorf("missing %q in:\n%s", k, s)
		}
	}
}

// TestStructural_UnsupportedFormat returns an error so doMerge falls back
// to the text path.
func TestStructural_UnsupportedFormat(t *testing.T) {
	_, _, err := Merge3WayStructural([]byte("a"), []byte("b"), []byte("c"), "README.md")
	if err == nil {
		t.Fatal("expected unsupported-format error")
	}
}

// TestStructural_NestedRecursion: nested map with adjacent inserts inside.
func TestStructural_NestedRecursion(t *testing.T) {
	base := []byte(`envs:
  github.com/x/y:
    files:
      a.yaml: {dest: a.yaml}
`)
	local := []byte(`envs:
  github.com/x/y:
    files:
      a.yaml: {dest: a.yaml}
      b.yaml: {dest: b.yaml}
`)
	upstream := []byte(`envs:
  github.com/x/y:
    files:
      a.yaml: {dest: a.yaml}
      c.yaml: {dest: c.yaml}
`)
	out, conflict, err := Merge3WayStructural(local, base, upstream, "b.yaml")
	if err != nil || conflict {
		t.Fatalf("err=%v conflict=%v", err, conflict)
	}
	s := string(out)
	for _, k := range []string{"a.yaml", "b.yaml", "c.yaml"} {
		if !strings.Contains(s, k) {
			t.Errorf("missing %s in:\n%s", k, s)
		}
	}
}
