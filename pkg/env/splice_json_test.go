package env

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestSpliceJSON_PreservesOutOfScope is the headline #122 case for JSON:
// scope is `binaries`, but the local file also has `envs` and
// `profiles`. Splicing must keep the latter intact.
func TestSpliceJSON_PreservesOutOfScope(t *testing.T) {
	local := []byte(`{
  "binaries": {"kubectl": {}},
  "envs": {"github.com/x/y": {}},
  "profiles": {"core": {}}
}`)
	merged := []byte(`{"binaries":{"kubectl":{},"helm":{}}}`)
	out, err := spliceJSON(local, merged, []string{"binaries"})
	if err != nil {
		t.Fatalf("spliceJSON: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("output not valid JSON: %v\n%s", err, out)
	}
	bins, ok := got["binaries"].(map[string]any)
	if !ok {
		t.Fatalf("binaries missing or wrong type: %#v", got["binaries"])
	}
	if _, ok := bins["helm"]; !ok {
		t.Errorf("helm not spliced in: %#v", bins)
	}
	if _, ok := got["envs"]; !ok {
		t.Errorf("envs dropped: %#v", got)
	}
	if _, ok := got["profiles"]; !ok {
		t.Errorf("profiles dropped: %#v", got)
	}
}

// TestSpliceJSON_OrderingPreservedForOutOfScope keeps the local file's
// key order for keys we did not touch. New in-scope keys land at the end.
func TestSpliceJSON_OrderingPreservedForOutOfScope(t *testing.T) {
	local := []byte(`{"a":1,"binaries":{"old":true},"z":2}`)
	merged := []byte(`{"binaries":{"new":true}}`)
	out, err := spliceJSON(local, merged, []string{"binaries"})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	posA := strings.Index(s, `"a"`)
	posB := strings.Index(s, `"binaries"`)
	posZ := strings.Index(s, `"z"`)
	if posA >= posB || posB >= posZ {
		t.Errorf("unexpected key order: %s", s)
	}
	if !strings.Contains(s, `"new"`) {
		t.Errorf("merged value not applied: %s", s)
	}
	if strings.Contains(s, `"old"`) {
		t.Errorf("local value not replaced: %s", s)
	}
}

// TestSpliceJSON_DropVanishedScopedKey: when an in-scope key disappears
// from merged, it should be removed from local too.
func TestSpliceJSON_DropVanishedScopedKey(t *testing.T) {
	local := []byte(`{"binaries":{"x":1},"envs":{}}`)
	merged := []byte(`{}`)
	out, err := spliceJSON(local, merged, []string{"binaries"})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if _, ok := got["binaries"]; ok {
		t.Errorf("binaries should be removed: %#v", got)
	}
	if _, ok := got["envs"]; !ok {
		t.Errorf("envs dropped: %#v", got)
	}
}

// TestSpliceJSON_AddNewScopedKey: scoped key absent in local, present
// in merged → appended at the end.
func TestSpliceJSON_AddNewScopedKey(t *testing.T) {
	local := []byte(`{"envs":{}}`)
	merged := []byte(`{"binaries":{"k":{}}}`)
	out, err := spliceJSON(local, merged, []string{"binaries"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"binaries"`) {
		t.Errorf("binaries not added: %s", out)
	}
	if !strings.Contains(string(out), `"envs"`) {
		t.Errorf("envs lost: %s", out)
	}
}

// TestSpliceJSON_ConflictMarkersErrorOut: merged with conflict markers
// is not splicable into JSON; the function must error so the caller
// surfaces the situation rather than writing a half-broken file.
func TestSpliceJSON_ConflictMarkersErrorOut(t *testing.T) {
	local := []byte(`{"binaries":{"a":1}}`)
	merged := []byte("<<<<<<< local\n{\"binaries\":{\"a\":1,\"b\":2}}\n=======\n{\"binaries\":{\"a\":1,\"c\":3}}\n>>>>>>> upstream\n")
	_, err := spliceJSON(local, merged, []string{"binaries"})
	if err == nil {
		t.Fatal("expected error for conflict markers in JSON merge")
	}
	if !strings.Contains(err.Error(), "conflict") {
		t.Errorf("error should mention conflicts: %v", err)
	}
}

// TestSpliceJSON_RejectsMalformedLocal: a truncated local file (no
// closing brace) or trailing garbage after the object must produce
// an error rather than silently succeeding and rewriting the file.
func TestSpliceJSON_RejectsMalformedLocal(t *testing.T) {
	merged := []byte(`{"binaries":{"a":1}}`)
	cases := []struct {
		name  string
		local string
	}{
		{"no closing brace", `{"binaries":{"a":1}`},
		{"trailing garbage", `{"binaries":{"a":1}}garbage`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := spliceJSON([]byte(c.local), merged, []string{"binaries"})
			if err == nil {
				t.Errorf("expected error for malformed local: %s", c.local)
			}
		})
	}
}

// TestSpliceJSON_NestedSelectorIsTopLevelOnly mirrors the YAML rule:
// `database.host` reduces to top-level key `database`.
func TestSpliceJSON_NestedSelectorIsTopLevelOnly(t *testing.T) {
	local := []byte(`{"database":{"host":"old","port":5432},"other":1}`)
	merged := []byte(`{"database":{"host":"new","port":5432}}`)
	out, err := spliceJSON(local, merged, []string{"database.host"})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	db := got["database"].(map[string]any)
	if db["host"] != "new" {
		t.Errorf("host not updated: %#v", db)
	}
	if got["other"] == nil {
		t.Errorf("other dropped: %#v", got)
	}
}
