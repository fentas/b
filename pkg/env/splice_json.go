package env

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// spliceJSON is the JSON sibling of spliceYAML. It replaces the values
// of in-scope top-level keys in `local` with the values from `merged`,
// preserving all out-of-scope keys.
//
// JSON has no comments and no canonical formatting beyond indentation,
// so the splice round-trips both sides through encoding/json and emits
// a fresh document. The local file's key ordering is preserved for
// out-of-scope keys; new in-scope keys land at the end.
//
// Conflict markers are not supported on the JSON path: a JSON document
// containing `<<<<<<<` is not parseable, and there is no equivalent of
// the YAML text-splice fallback that can scan a half-broken document
// at top-level granularity. When the merge produces conflict markers,
// the splice errors out with a clear message asking the user to
// resolve manually. This is a deliberate scope limit, not a data-loss
// risk: the caller never writes the partial result.
func spliceJSON(local, merged []byte, selectors []string) ([]byte, error) {
	// Reject complex JMESPath selectors. topLevelKeysFromSelectors
	// is a literal-string operation, so an expression like
	// `from_items(items(binaries))` would be treated as a key
	// literally named "from_items(items(binaries))" and the splice
	// would silently look for it (and skip the file). The JSON
	// splice only supports simple dot-paths today; the caller
	// should drop the select or move the data to YAML.
	for _, s := range selectors {
		if !isSimpleDotPath(s) {
			return nil, fmt.Errorf("JSON splice: complex JMESPath selector %q is not supported (only simple dot-paths)", s)
		}
	}
	scope := topLevelKeysFromSelectors(selectors)
	if len(scope) == 0 {
		// Selectors that reduce to no top-level keys (empty,
		// "."), are degenerate. Returning `merged` here would
		// silently drop the consumer's local file in a merge
		// flow where merged is `{}` from a JMESPath that hit
		// nothing. Refuse explicitly so the caller fixes the
		// selector instead of losing data.
		if len(selectors) > 0 {
			return nil, fmt.Errorf("JSON splice: selectors %v reduced to no top-level keys (use a real key like \"binaries\" or drop the select)", selectors)
		}
		return merged, nil
	}

	// JSON cannot host git conflict markers without becoming
	// unparseable, and we can't reliably scan a half-broken JSON
	// document to splice per-key. Check both sides — a previous run
	// can leave markers in the local file too — and surface this
	// clearly so the caller (and the user) can resolve manually.
	// The YAML splice has a structured fallback for this case;
	// JSON does not yet.
	if containsConflictMarkers(local) {
		return nil, fmt.Errorf("JSON splice: local file contains unresolved conflict markers; resolve them manually before re-running")
	}
	if containsConflictMarkers(merged) {
		return nil, fmt.Errorf("JSON splice: merge produced conflict markers; resolve them manually before re-running")
	}
	return spliceJSONStructural(local, merged, scope)
}

// spliceJSONStructural parses both sides, overwrites in-scope top-level
// keys in `local` from `merged`, and re-emits. JSON's lack of ordering
// guarantees in the spec means encoding/json's map iteration order is
// not preserved; we use json.RawMessage with an ordered key list to
// keep the local document's ordering stable for unchanged keys, and
// place new in-scope keys at the end.
func spliceJSONStructural(local, merged []byte, scope map[string]bool) ([]byte, error) {
	localOrdered, err := decodeOrderedJSONObject(local)
	if err != nil {
		return nil, fmt.Errorf("parse local JSON: %w", err)
	}
	mergedOrdered, err := decodeOrderedJSONObject(merged)
	if err != nil {
		return nil, fmt.Errorf("parse merged JSON: %w", err)
	}

	// Build a quick lookup of merged values for in-scope keys.
	mergedByKey := make(map[string]json.RawMessage, len(mergedOrdered.Values))
	for i, k := range mergedOrdered.Keys {
		mergedByKey[k] = mergedOrdered.Values[i]
	}

	out := orderedJSONObject{}
	seen := make(map[string]bool, len(localOrdered.Keys))

	// Walk local in order: replace in-scope keys (or drop them if the
	// merged side dropped them); pass through out-of-scope keys.
	for i, k := range localOrdered.Keys {
		if scope[k] {
			if mv, ok := mergedByKey[k]; ok {
				out.Keys = append(out.Keys, k)
				out.Values = append(out.Values, mv)
			}
			// else: in-scope key vanished from merged → drop
		} else {
			out.Keys = append(out.Keys, k)
			out.Values = append(out.Values, localOrdered.Values[i])
		}
		seen[k] = true
	}
	// Append in-scope keys that exist in merged but not in local.
	for _, k := range mergedOrdered.Keys {
		if !scope[k] || seen[k] {
			continue
		}
		out.Keys = append(out.Keys, k)
		out.Values = append(out.Values, mergedByKey[k])
	}

	return encodeOrderedJSONObject(out, "  ")
}

// orderedJSONObject is a top-level JSON object with stable key ordering.
// Values are kept as json.RawMessage so we don't have to recurse — the
// splice operates strictly at top-level granularity.
type orderedJSONObject struct {
	Keys   []string
	Values []json.RawMessage
}

func decodeOrderedJSONObject(data []byte) (orderedJSONObject, error) {
	var out orderedJSONObject
	if len(bytes.TrimSpace(data)) == 0 {
		return out, nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return out, err
	}
	delim, ok := tok.(json.Delim)
	if !ok || delim != '{' {
		return out, fmt.Errorf("expected top-level JSON object, got %v", tok)
	}
	for dec.More() {
		ktok, err := dec.Token()
		if err != nil {
			return out, err
		}
		key, ok := ktok.(string)
		if !ok {
			return out, fmt.Errorf("expected string key, got %v", ktok)
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return out, err
		}
		out.Keys = append(out.Keys, key)
		out.Values = append(out.Values, raw)
	}
	// Consume the closing '}' so the splice rejects truncated input
	// and ensure no trailing garbage follows. Without these checks, a
	// malformed local file could be silently rewritten.
	closing, err := dec.Token()
	if err != nil {
		return out, fmt.Errorf("missing closing '}': %w", err)
	}
	if d, ok := closing.(json.Delim); !ok || d != '}' {
		return out, fmt.Errorf("expected '}' at end of object, got %v", closing)
	}
	// dec.More() is scoped to the current array/object and would
	// miss things like a stray `}` or `]` after the document. Read
	// one more token and require io.EOF — the json tokenizer skips
	// whitespace itself, so any non-whitespace bytes after the
	// closing brace produce a non-EOF token here and get rejected.
	if extra, err := dec.Token(); err != io.EOF {
		if err != nil {
			return out, fmt.Errorf("unexpected trailing content after JSON object: %w", err)
		}
		return out, fmt.Errorf("unexpected trailing content after JSON object: %v", extra)
	}
	return out, nil
}

func encodeOrderedJSONObject(o orderedJSONObject, indent string) ([]byte, error) {
	// Match encoding/json's behavior for empty objects so a splice
	// that drops every top-level key (e.g. all in-scope keys
	// vanished upstream) emits "{}\n" instead of the noisy
	// "{\n}\n".
	if len(o.Keys) == 0 {
		return []byte("{}\n"), nil
	}
	var buf bytes.Buffer
	buf.WriteString("{\n")
	for i, k := range o.Keys {
		buf.WriteString(indent)
		kb, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		buf.Write(kb)
		buf.WriteString(": ")
		// Re-indent the value so nested objects align with the parent.
		var valBuf bytes.Buffer
		if err := json.Indent(&valBuf, o.Values[i], indent, indent); err != nil {
			return nil, err
		}
		buf.Write(valBuf.Bytes())
		if i < len(o.Keys)-1 {
			buf.WriteString(",")
		}
		buf.WriteString("\n")
	}
	buf.WriteString("}\n")
	return buf.Bytes(), nil
}
