package state

import (
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
