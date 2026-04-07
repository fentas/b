package env

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestApplyPinsYAML_PinnedTopLevelKeySurvives the pinned key in local
// must overwrite the value the merge produced.
func TestApplyPinsYAML_PinnedTopLevelKeySurvives(t *testing.T) {
	local := []byte(`binaries:
  kubectl:
    version: v1.30.0
    b.pin: true
  kustomize:
    version: v5.0.0
`)
	pending := []byte(`binaries:
  kubectl:
    version: v1.31.0
  kustomize:
    version: v5.5.0
`)
	out, err := applyPinsYAML(local, pending, "b.yaml")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "v1.30.0") {
		t.Errorf("pinned kubectl version lost: %s", s)
	}
	if strings.Contains(s, "v1.31.0") {
		t.Errorf("pinned kubectl wasn't restored: %s", s)
	}
	// kustomize is not pinned → upstream value wins.
	if !strings.Contains(s, "v5.5.0") {
		t.Errorf("kustomize update lost: %s", s)
	}
	// pin annotation must remain so the next sync still honors it.
	if !strings.Contains(s, "b.pin") {
		t.Errorf("pin annotation stripped: %s", s)
	}
}

// TestApplyPinsYAML_NoPinsIsNoOp returns pending unchanged when local
// has no annotations. The function must short-circuit cheaply, not
// re-emit YAML and risk reformatting.
func TestApplyPinsYAML_NoPinsIsNoOp(t *testing.T) {
	local := []byte("binaries:\n  kubectl: {}\n")
	pending := []byte("binaries:\n  kubectl:\n    version: v2\n")
	out, err := applyPinsYAML(local, pending, "b.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(pending) {
		t.Errorf("expected pass-through, got:\n%s", out)
	}
}

// TestApplyPinsYAML_NonYAMLPasses non-YAML files don't get pin handling.
func TestApplyPinsYAML_NonYAMLPasses(t *testing.T) {
	out, err := applyPinsYAML([]byte("local"), []byte("pending"), "config.json")
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "pending" {
		t.Errorf("expected pass-through for JSON, got: %s", out)
	}
}

// TestApplyPinsYAML_PinAddsBackDeletedKey: pinned key was deleted
// upstream → consumer's annotation says "keep it anyway", so the
// pinned entry is reinserted in the pending document.
func TestApplyPinsYAML_PinAddsBackDeletedKey(t *testing.T) {
	local := []byte(`binaries:
  helm:
    version: v3.14.0
    b.pin: true
  kubectl:
    version: v1.30.0
`)
	pending := []byte(`binaries:
  kubectl:
    version: v1.31.0
`)
	out, err := applyPinsYAML(local, pending, "b.yaml")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "helm") {
		t.Errorf("deleted-but-pinned helm should be re-added: %s", s)
	}
	if !strings.Contains(s, "v3.14.0") {
		t.Errorf("helm version lost: %s", s)
	}
}

// TestApplyPinsYAML_NestedPinAtArbitraryDepth: a pin deep in the tree
// is honored just like a top-level one.
func TestApplyPinsYAML_NestedPinAtArbitraryDepth(t *testing.T) {
	local := []byte(`envs:
  github.com/x/y:
    files:
      a.yaml:
        dest: a.yaml
        b.pin: true
        custom: keep-me
`)
	pending := []byte(`envs:
  github.com/x/y:
    files:
      a.yaml:
        dest: changed.yaml
`)
	out, err := applyPinsYAML(local, pending, "b.yaml")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if strings.Contains(s, "changed.yaml") {
		t.Errorf("upstream value should not have replaced pinned key: %s", s)
	}
	if !strings.Contains(s, "keep-me") {
		t.Errorf("pinned subtree fields lost: %s", s)
	}
}

// TestApplyPinsYAML_PinFalseIsNotApin documents that only true-ish
// values (true/yes/on) trigger the pin. `false` is treated as "no
// pin set" so the upstream value can flow through.
func TestApplyPinsYAML_PinFalseIsNotApin(t *testing.T) {
	local := []byte(`binaries:
  kubectl:
    version: v1.30.0
    b.pin: false
`)
	pending := []byte(`binaries:
  kubectl:
    version: v1.31.0
`)
	out, err := applyPinsYAML(local, pending, "b.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "v1.31.0") {
		t.Errorf("upstream value should have flowed through with b.pin: false, got:\n%s", out)
	}
}

// TestCollectPinnedPaths exercises the path discovery directly so
// regressions show up before the splice does.
func TestCollectPinnedPaths(t *testing.T) {
	local := []byte(`a:
  b:
    b.pin: true
    x: 1
  c:
    x: 2
d:
  b.pin: true
`)
	var doc yaml.Node
	if err := yaml.Unmarshal(local, &doc); err != nil {
		t.Fatal(err)
	}
	pins := collectPinnedPaths(&doc, nil)
	if len(pins) != 2 {
		t.Fatalf("want 2 pins, got %d: %+v", len(pins), pins)
	}
	wantPaths := map[string]bool{
		"a.b": true,
		"d":   true,
	}
	for _, p := range pins {
		key := strings.Join(p.path, ".")
		if !wantPaths[key] {
			t.Errorf("unexpected pin path: %s", key)
		}
	}
}
