package binary

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestBinary_FileAsWrittenNotInYAML: FileAsWritten is kept in memory only,
// as in LocalBinary. JSON and YAML do not carry it. YAML does not set it:
// a `fileaswritten:` key (the key that yaml.v3 gives the field without a
// tag) leaves it empty. yaml.v3 cannot marshal a whole Binary (it has func
// fields), so the test checks the tag that yaml.v3 reads to marshal it.
func TestBinary_FileAsWrittenNotInYAML(t *testing.T) {
	for _, typ := range []reflect.Type{reflect.TypeOf(Binary{}), reflect.TypeOf(LocalBinary{})} {
		field, ok := typ.FieldByName("FileAsWritten")
		if !ok {
			t.Fatalf("%s has no field FileAsWritten", typ)
		}
		for _, key := range []string{"json", "yaml"} {
			if tag, ok := field.Tag.Lookup(key); !ok || tag != "-" {
				t.Errorf("%s.FileAsWritten: %s tag %q (set %v), want \"-\"", typ, key, tag, ok)
			}
		}
	}

	var b Binary
	if err := yaml.Unmarshal([]byte("name: tool\nfileaswritten: ./tools/x\nFileAsWritten: ./tools/y\n"), &b); err != nil {
		t.Fatal(err)
	}
	if b.Name != "tool" || b.FileAsWritten != "" {
		t.Errorf("from YAML: name %q, FileAsWritten %q, want tool and \"\"", b.Name, b.FileAsWritten)
	}

	data, err := json.Marshal(&Binary{Name: "tool", File: "/p/tools/x", FileAsWritten: "./tools/x"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "tools/x") {
		t.Errorf("JSON of a Binary: %s, want no file", data)
	}
}
