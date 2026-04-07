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

// TestSpliceYAMLStructural_NonContiguousScopedKeys verifies that the
// structural splice handles two scoped keys separated by an out-of-scope
// key in the local file, without reordering. (Reviewer note on PR #126.)
//
// Local layout:
//
//	binaries: ...     ← in-scope
//	envs: ...         ← out-of-scope
//	extras: ...       ← in-scope
//
// After splice with select:[binaries, extras], the order must remain
// binaries → envs → extras (envs untouched in the middle), and both
// scoped keys must hold the merged values.
func TestSpliceYAMLStructural_NonContiguousScopedKeys(t *testing.T) {
	local := []byte(`binaries:
  old: {}

envs:
  github.com/keep/me:
    files:
      a.yaml:
        dest: a.yaml

extras:
  legacy: {}
`)
	merged := []byte(`binaries:
  new: {}
extras:
  shiny: {}
`)
	out, err := spliceSelectedScope(local, merged, []string{"binaries", "extras"}, "b.yaml")
	if err != nil {
		t.Fatalf("splice: %v", err)
	}
	outStr := string(out)

	// Both scoped keys hold the merged values
	if !strings.Contains(outStr, "new:") {
		t.Errorf("binaries.new missing: %s", outStr)
	}
	if !strings.Contains(outStr, "shiny:") {
		t.Errorf("extras.shiny missing: %s", outStr)
	}
	// Old in-scope content gone (replaced by merge)
	if strings.Contains(outStr, "old:") {
		t.Errorf("binaries.old should have been replaced: %s", outStr)
	}
	if strings.Contains(outStr, "legacy:") {
		t.Errorf("extras.legacy should have been replaced: %s", outStr)
	}
	// Out-of-scope envs preserved in the middle
	if !strings.Contains(outStr, "github.com/keep/me") {
		t.Errorf("envs scope dropped: %s", outStr)
	}

	// Order check: binaries must appear before envs, envs before extras.
	binaries := strings.Index(outStr, "binaries:")
	envs := strings.Index(outStr, "envs:")
	extras := strings.Index(outStr, "extras:")
	if binaries >= envs || envs >= extras {
		t.Errorf("scoped keys reordered (binaries=%d envs=%d extras=%d):\n%s",
			binaries, envs, extras, outStr)
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

// TestSpliceSelectedScope_JSONErrors — JSON splice is not implemented;
// passing through `merged` would silently drop out-of-scope JSON content
// (the exact #122 bug), so the function must error out instead. Per
// copilot review on PR #126.
func TestSpliceSelectedScope_JSONErrors(t *testing.T) {
	local := []byte(`{"binaries": {"a": 1}, "envs": {}}`)
	merged := []byte(`{"binaries": {"a": 1, "b": 2}}`)
	_, err := spliceSelectedScope(local, merged, []string{"binaries"}, "config.json")
	if err == nil {
		t.Fatal("expected error for scoped JSON merge (not yet supported)")
	}
	if !strings.Contains(err.Error(), "JSON") {
		t.Errorf("error should mention JSON, got: %v", err)
	}
}

// TestSpliceYAMLStructural_HeaderCommentsPreservedOnEmptyDoc verifies
// that splicing into a YAML file with only header comments (and no
// document content) preserves the comments instead of dropping them.
// yaml.v3 doesn't surface header-only files as Document.Content, so
// the empty-doc fast path has to fall back to byte concatenation.
func TestSpliceYAMLStructural_HeaderCommentsPreservedOnEmptyDoc(t *testing.T) {
	local := []byte(`# managed by hand — do not edit
# generated from upstream
`)
	merged := []byte("binaries:\n  kubectl: {}\n")
	out, err := spliceSelectedScope(local, merged, []string{"binaries"}, "b.yaml")
	if err != nil {
		t.Fatalf("splice: %v", err)
	}
	outStr := string(out)
	if !strings.Contains(outStr, "managed by hand") {
		t.Errorf("header comment dropped, got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "kubectl") {
		t.Errorf("merged binaries missing, got:\n%s", outStr)
	}
}

// TestSpliceYAMLText_PreservesCommentAttributedToNextKey verifies the
// boundary attribution fix from PR #126 round 5: a comment block sitting
// between an in-scope key and an out-of-scope key must be preserved when
// the in-scope key is replaced. Before the fix, topLevelKeyRanges set
// the in-scope key's endByte to the start of the next key's line, so
// the comment block ended up inside the in-scope key's range and got
// silently dropped during the splice.
func TestSpliceYAMLText_PreservesCommentAttributedToNextKey(t *testing.T) {
	local := []byte(`binaries:
  a: {}

# This comment belongs to envs:, not binaries:
# It must survive a binaries-only splice.
envs:
  github.com/keep/me: {}
`)
	merged := []byte(`binaries:
<<<<<<< local
  a: {}
=======
  a: {}
  b: {}
>>>>>>> upstream
`)
	out, err := spliceSelectedScope(local, merged, []string{"binaries"}, "b.yaml")
	if err != nil {
		t.Fatalf("splice: %v", err)
	}
	outStr := string(out)
	if !strings.Contains(outStr, "This comment belongs to envs") {
		t.Errorf("comment block above envs: was dropped during binaries splice, got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "It must survive") {
		t.Errorf("second comment line was dropped, got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "github.com/keep/me") {
		t.Errorf("envs scope itself was dropped, got:\n%s", outStr)
	}
}

// TestSpliceYAMLText_AddsScopedKeyMissingInLocal verifies the text
// splice's "additions" path: a scoped key present in `merged` but
// absent from `local` must be appended to the output (not silently
// dropped). Per copilot review on PR #126 round 3.
func TestSpliceYAMLText_AddsScopedKeyMissingInLocal(t *testing.T) {
	local := []byte(`binaries:
  a: {}
envs:
  github.com/keep/me: {}
`)
	// merged contains BOTH binaries (in local) AND extras (NOT in
	// local) — extras must end up in the output.
	merged := []byte(`binaries:
<<<<<<< local
  a: {}
=======
  a: {}
  b: {}
>>>>>>> upstream
extras:
  shiny: {}
`)
	out, err := spliceSelectedScope(local, merged, []string{"binaries", "extras"}, "b.yaml")
	if err != nil {
		t.Fatalf("splice: %v", err)
	}
	outStr := string(out)
	// envs preserved
	if !strings.Contains(outStr, "github.com/keep/me") {
		t.Errorf("envs dropped, got:\n%s", outStr)
	}
	// extras (addition) appended
	if !strings.Contains(outStr, "extras:") || !strings.Contains(outStr, "shiny:") {
		t.Errorf("extras addition missing, got:\n%s", outStr)
	}
}

// TestSpliceYAMLText_RemovesScopedKeyAbsentInMerge verifies the text
// splice's "deletions" path: a scoped key present in `local` but
// absent from `merged` must be removed from the output. Per copilot
// review on PR #126 round 3.
func TestSpliceYAMLText_RemovesScopedKeyAbsentInMerge(t *testing.T) {
	local := []byte(`binaries:
  old: {}
envs:
  github.com/keep/me: {}
extras:
  legacy: {}
`)
	// merged only has binaries (with conflict markers); the merge
	// decided extras should not exist anymore. The text splice
	// should drop the extras range from local.
	merged := []byte(`binaries:
<<<<<<< local
  old: {}
=======
  new: {}
>>>>>>> upstream
`)
	out, err := spliceSelectedScope(local, merged, []string{"binaries", "extras"}, "b.yaml")
	if err != nil {
		t.Fatalf("splice: %v", err)
	}
	outStr := string(out)
	// envs preserved
	if !strings.Contains(outStr, "github.com/keep/me") {
		t.Errorf("envs dropped, got:\n%s", outStr)
	}
	// extras removed (no `legacy` reference)
	if strings.Contains(outStr, "legacy") {
		t.Errorf("extras should have been deleted, got:\n%s", outStr)
	}
	if strings.Contains(outStr, "extras:") {
		t.Errorf("extras key should have been deleted, got:\n%s", outStr)
	}
}

// TestScanTopLevelKeyRanges_PreservesPrefix verifies that header
// comments and other content above the first top-level key are kept
// in the first key's range. Per copilot review on PR #126 round 2:
// dropping these bytes during text-splice fallback would lose user
// content even when the structural splice is unavailable.
func TestScanTopLevelKeyRanges_PreservesPrefix(t *testing.T) {
	src := []byte(`# header comment
# more
binaries:
  a: {}
envs:
  x: {}
`)
	out := scanTopLevelKeyRanges(src)
	binaries, ok := out["binaries"]
	if !ok {
		t.Fatalf("binaries key missing")
	}
	if !strings.Contains(string(binaries), "header comment") {
		t.Errorf("first key range should include preceding header comments, got:\n%s", binaries)
	}
	if !strings.Contains(string(binaries), "more") {
		t.Errorf("first key range should include all preceding lines, got:\n%s", binaries)
	}
	envs, ok := out["envs"]
	if !ok {
		t.Fatalf("envs key missing")
	}
	if !strings.HasPrefix(string(envs), "envs:") {
		t.Errorf("non-first key range should start at the key, got:\n%s", envs)
	}
}

// TestSpliceYAMLStructural_NonMappingErrors verifies the structural
// splice now errors out (rather than silently passing through `merged`)
// when the local YAML root is not a mapping. Per copilot review on
// PR #126 round 2.
func TestSpliceYAMLStructural_NonMappingErrors(t *testing.T) {
	local := []byte("- item1\n- item2\n") // sequence, not mapping
	merged := []byte("binaries:\n  a: {}\n")
	_, err := spliceYAMLStructural(local, merged, map[string]bool{"binaries": true})
	if err == nil {
		t.Error("expected error when local root is not a mapping")
	}
}

// TestSpliceSelectedScope_JSONErrorsForNewFile verifies that JSON +
// select errors out even when the destination file doesn't exist yet,
// so a first sync can't silently produce a half-written file. Per
// copilot review on PR #126 round 2.
func TestSpliceSelectedScope_JSONErrorsForNewFile(t *testing.T) {
	merged := []byte(`{"binaries": {"a": 1}}`)
	// Empty `local` simulates the not-exist case (the caller passes
	// nil bytes when ReadFile returned ErrNotExist).
	_, err := spliceSelectedScope(nil, merged, []string{"binaries"}, "config.json")
	if err == nil {
		t.Fatal("expected error for JSON select even when local file doesn't exist")
	}
	if !strings.Contains(err.Error(), "JSON") {
		t.Errorf("error should mention JSON, got: %v", err)
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
