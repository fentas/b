package env

import (
	"strings"
	"testing"
)

// --- filterContent dispatch ---

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

func TestFilterContent_UnsupportedExtension(t *testing.T) {
	_, err := filterContent([]byte("content"), []string{".key"}, "file.txt")
	if err == nil {
		t.Error("expected error for unsupported extension")
	}
}

// --- YAML filtering ---

func TestFilterYAML_SingleKey(t *testing.T) {
	content := []byte("# top comment\nbinaries:\n  jq: {}\n  kubectl: {}\nprofiles:\n  base:\n    description: test\n")
	out, err := filterContent(content, []string{".binaries"}, "b.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "jq") {
		t.Errorf("should contain jq, got:\n%s", out)
	}
	if strings.Contains(string(out), "profiles") {
		t.Errorf("should NOT contain profiles, got:\n%s", out)
	}
}

func TestFilterYAML_MultipleKeys(t *testing.T) {
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

func TestFilterYAML_NestedKey(t *testing.T) {
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

func TestFilterYAML_MissingKey(t *testing.T) {
	content := []byte("a: 1\n")
	out, err := filterContent(content, []string{".nonexistent"}, "test.yaml")
	if err != nil {
		t.Fatal(err)
	}
	s := strings.TrimSpace(string(out))
	if s != "{}" {
		t.Errorf("missing key should produce empty map, got: %q", s)
	}
}

func TestFilterYAML_PreservesComments(t *testing.T) {
	content := []byte("# file header\nbinaries:\n  # inline comment\n  jq: {}\nprofiles:\n  base: {}\n")
	out, err := filterContent(content, []string{".binaries"}, "b.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "inline comment") {
		t.Errorf("should preserve inline comment, got:\n%s", out)
	}
}

func TestFilterYAML_PreservesKeyOrder(t *testing.T) {
	content := []byte("z_last: 1\na_first: 2\nm_middle: 3\n")
	out, err := filterContent(content, []string{".z_last", ".a_first"}, "config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	zIdx := strings.Index(string(out), "z_last")
	aIdx := strings.Index(string(out), "a_first")
	if zIdx < 0 || aIdx < 0 {
		t.Errorf("both keys should be present, got:\n%s", out)
	}
	if zIdx > aIdx {
		t.Errorf("key order should be preserved (z_last before a_first), got:\n%s", out)
	}
}

func TestFilterYAML_InvalidYAML(t *testing.T) {
	_, err := filterContent([]byte("{{invalid"), []string{".key"}, "test.yaml")
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

func TestFilterYAML_YmlExtension(t *testing.T) {
	content := []byte("key: value\n")
	out, err := filterContent(content, []string{".key"}, "test.yml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "key") {
		t.Errorf("should contain key, got:\n%s", out)
	}
}

// --- JSON filtering with gjson/sjson ---

func TestFilterJSON_SingleKey(t *testing.T) {
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

func TestFilterJSON_NestedKey(t *testing.T) {
	content := []byte(`{"database":{"host":"localhost","port":5432},"redis":{"host":"redis"}}`)
	out, err := filterContent(content, []string{".database.host"}, "config.json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "localhost") {
		t.Error("should contain database.host")
	}
	if strings.Contains(string(out), "redis") {
		t.Error("should NOT contain redis")
	}
}

func TestFilterJSON_MultipleKeys(t *testing.T) {
	content := []byte(`{"a":1,"b":2,"c":3}`)
	out, err := filterContent(content, []string{".a", ".c"}, "data.json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"a"`) || !strings.Contains(string(out), `"c"`) {
		t.Errorf("should contain a and c, got:\n%s", out)
	}
	if strings.Contains(string(out), `"b"`) {
		t.Error("should NOT contain b")
	}
}

func TestFilterJSON_MissingKey(t *testing.T) {
	content := []byte(`{"a":1}`)
	out, err := filterContent(content, []string{".nonexistent"}, "test.json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(out)) != "{}" {
		t.Errorf("missing key should produce empty object, got: %q", string(out))
	}
}

func TestFilterJSON_InvalidJSON(t *testing.T) {
	_, err := filterContent([]byte("{invalid"), []string{".key"}, "test.json")
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestFilterJSON_GjsonAdvancedPath(t *testing.T) {
	content := []byte(`{"items":[{"name":"a","val":1},{"name":"b","val":2}]}`)
	out, err := filterContent(content, []string{".items"}, "data.json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"name"`) {
		t.Error("should contain items array")
	}
}

// --- Real-world scenario: b.yaml binaries selection ---

func TestFilterYAML_BYaml_BinariesOnly(t *testing.T) {
	content := []byte(`binaries:
  jq:
  kubectl:
    version: v1.28.0
profiles:
  base:
    description: "Base config"
    files:
      manifests/**:
        dest: base/
envs:
  github.com/org/infra:
    version: v2.0
`)
	out, err := filterContent(content, []string{".binaries"}, ".bin/b.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "jq") {
		t.Error("should contain jq")
	}
	if !strings.Contains(string(out), "kubectl") {
		t.Error("should contain kubectl")
	}
	if strings.Contains(string(out), "profiles") {
		t.Error("should NOT contain profiles")
	}
	if strings.Contains(string(out), "envs") {
		t.Error("should NOT contain envs")
	}
}
