package env

import (
	"strings"
	"testing"
)

func TestFilterContent_EmptySelectors(t *testing.T) {
	content := []byte("key: value\n")
	out, err := filterContent(content, nil, "test.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(content) {
		t.Errorf("empty selectors should return content unchanged")
	}
}

func TestFilterContent_YAML_SingleKey(t *testing.T) {
	content := []byte("binaries:\n  jq: {}\n  kubectl: {}\nprofiles:\n  base:\n    description: test\n")
	out, err := filterContent(content, []string{".binaries"}, "b.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "jq") {
		t.Errorf("should contain jq, got: %s", out)
	}
	if strings.Contains(string(out), "profiles") {
		t.Errorf("should NOT contain profiles, got: %s", out)
	}
}

func TestFilterContent_YAML_MultipleKeys(t *testing.T) {
	content := []byte("a: 1\nb: 2\nc: 3\n")
	out, err := filterContent(content, []string{".a", ".c"}, "config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "a:") {
		t.Error("should contain a")
	}
	if strings.Contains(string(out), "b:") {
		t.Error("should NOT contain b")
	}
	if !strings.Contains(string(out), "c:") {
		t.Error("should contain c")
	}
}

func TestFilterContent_YAML_NestedKey(t *testing.T) {
	content := []byte("database:\n  host: localhost\n  port: 5432\nredis:\n  host: localhost\n")
	out, err := filterContent(content, []string{".database.host"}, "config.yml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "host") {
		t.Error("should contain host")
	}
	if strings.Contains(string(out), "redis") {
		t.Error("should NOT contain redis")
	}
}

func TestFilterContent_YAML_MissingKey(t *testing.T) {
	content := []byte("a: 1\n")
	out, err := filterContent(content, []string{".nonexistent"}, "test.yaml")
	if err != nil {
		t.Fatal(err)
	}
	// Should produce empty YAML (just {})
	s := strings.TrimSpace(string(out))
	if s != "{}" {
		t.Errorf("missing key should produce empty map, got: %q", s)
	}
}

func TestFilterContent_JSON_SingleKey(t *testing.T) {
	content := []byte(`{"database":{"host":"localhost"},"cache":{"ttl":60}}`)
	out, err := filterContent(content, []string{".database"}, "config.json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "localhost") {
		t.Error("should contain database.host")
	}
	if strings.Contains(string(out), "cache") {
		t.Error("should NOT contain cache")
	}
}

func TestFilterContent_UnsupportedExtension(t *testing.T) {
	_, err := filterContent([]byte("content"), []string{".key"}, "file.txt")
	if err == nil {
		t.Error("expected error for unsupported extension")
	}
}

func TestFilterContent_InvalidYAML(t *testing.T) {
	_, err := filterContent([]byte("{{invalid"), []string{".key"}, "test.yaml")
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

func TestFilterContent_InvalidJSON(t *testing.T) {
	_, err := filterContent([]byte("{invalid"), []string{".key"}, "test.json")
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

// --- ProjectRoot tests ---

func TestLookupNested(t *testing.T) {
	data := map[string]interface{}{
		"a": map[string]interface{}{
			"b": map[string]interface{}{
				"c": "deep",
			},
		},
	}
	val := lookupNested(data, []string{"a", "b", "c"})
	if val != "deep" {
		t.Errorf("expected 'deep', got %v", val)
	}
}

func TestLookupNested_Missing(t *testing.T) {
	data := map[string]interface{}{"a": 1}
	val := lookupNested(data, []string{"x"})
	if val != nil {
		t.Errorf("expected nil for missing key, got %v", val)
	}
}

func TestSetNested(t *testing.T) {
	data := make(map[string]interface{})
	setNested(data, []string{"a", "b"}, "value")
	sub, ok := data["a"].(map[string]interface{})
	if !ok {
		t.Fatal("expected nested map")
	}
	if sub["b"] != "value" {
		t.Errorf("expected 'value', got %v", sub["b"])
	}
}
