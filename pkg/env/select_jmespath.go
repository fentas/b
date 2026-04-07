package env

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jmespath-community/go-jmespath"
	"gopkg.in/yaml.v3"
)

// isSimpleDotPath reports whether a selector is a plain dot-path that can
// be handled by the comment-preserving Node API path in select.go. Complex
// expressions (filters, projections, functions, multi-select hashes, array
// indexing) contain JMESPath operator characters and are routed to the
// JMESPath path instead.
//
// Examples:
//
//	"binaries"                  → true
//	".binaries"                 → true
//	"database.host"             → true
//	"binaries.kubectl"          → true
//	"foo/bar"                   → true  (legacy plain key with /)
//	"my-key"                    → true
//	"binaries | [?...]"         → false (pipe, filter)
//	"{b: binaries}"             → false (multi-select hash)
//	"binaries[0]"               → false (index/bracket)
//	"from_items(items(...))"    → false (function, parens)
//
// Classification policy: this is a *blocklist* of JMESPath grammar
// characters, not an allowlist of identifier characters. The legacy
// YAML dot-path validator (in filterYAML) only rejects `[]` and `\`
// and empty segments — it accepts plain keys with characters like
// `/`, `+`, `@`, `#`, etc. JSON selection (in filterJSON) does not
// use that same validator and instead follows gjson/sjson path
// semantics. To stay backward compatible (per copilot review on
// PR #127 rounds 2 and 5), the classifier must NOT route those
// legacy plain keys to JMESPath, where they'd hit a parse error.
//
// The blocklisted characters are exactly the ones that introduce
// JMESPath grammar: brackets `[]`, parens `()`, braces `{}`, pipe `|`,
// star `*`, ampersand `&`, comma `,`, single/double quote `'"`,
// comparison `<>=!`, backtick (literal), and backslash. Empty/double
// dots are also rejected to keep the classification consistent with
// the YAML validator's segment check.
func isSimpleDotPath(sel string) bool {
	// Reject multiple leading dots (e.g. "..a"). The existing simple
	// dot-path validator (filterYAML) treats ".a..b" → segments
	// ["", "a", "", "b"] and errors out. Without this guard the
	// classifier would accept "..a" as simple, then the validator
	// would reject it at runtime — the classification and validation
	// would disagree.
	if strings.HasPrefix(sel, "..") {
		return false
	}
	s := strings.TrimPrefix(sel, ".")
	if s == "" {
		return false
	}
	// Blocklist any character that introduces JMESPath grammar.
	// Anything else passes through — including `/`, `+`, `@`, `#`,
	// etc. — so plain top-level keys containing those characters keep
	// using the comment-preserving Node API path.
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '[', ']', '(', ')', '{', '}',
			'|', '*', '&', ',',
			'\'', '"', '`',
			'<', '>', '=', '!',
			'\\', '?',
			' ', '\t', '\n', '\r':
			return false
		}
	}
	// Reject consecutive or trailing dots so the segment validator
	// downstream doesn't reject what we classified as simple.
	if strings.HasSuffix(s, ".") || strings.Contains(s, "..") {
		return false
	}
	return true
}

// splitSelectorsByComplexity partitions selectors into (simple, complex).
// Simple ones can go through the comment-preserving Node API, complex ones
// must go through JMESPath.
func splitSelectorsByComplexity(selectors []string) (simple, complex []string) {
	for _, s := range selectors {
		if isSimpleDotPath(s) {
			simple = append(simple, s)
		} else {
			complex = append(complex, s)
		}
	}
	return
}

// runJMESPathSelectors evaluates a list of JMESPath expressions against a
// decoded data value and merges their results into a single map.
//
// Merge semantics:
//
//   - If an expression returns a map[string]interface{}, its entries are
//     merged into the result (later expressions override earlier ones for
//     the same key).
//
//   - If an expression returns something else (scalar, array), it is
//     wrapped under the last dotted segment of the expression text, or
//     under "result" if the expression doesn't end in an identifier.
//
//   - Nil results are ignored (JMESPath spec: missing paths return null).
//
// Limitation (per copilot review on PR #127): a quoted-identifier
// expression like `'"weird[name]"'` returns the *value* of that key,
// not a {key: value} pair, so the original key name is lost in the
// merged output. Object values get merged at the top level; scalar/
// array values get wrapped under "result". Users who need to preserve
// the original key name must wrap it explicitly in a multi-select hash
// like `'{"weird[name]": "weird[name]"}'`. The docs callout in
// docs/env-sync.mdx explains this trade-off to users.
//
// The merged map is then passed to marshal() to produce the final output
// bytes.
func runJMESPathSelectors(
	data interface{},
	selectors []string,
	marshal func(interface{}) ([]byte, error),
) ([]byte, error) {
	merged := make(map[string]interface{})
	for _, sel := range selectors {
		val, err := jmespath.Search(sel, data)
		if err != nil {
			return nil, fmt.Errorf("jmespath %q: %w", sel, err)
		}
		if val == nil {
			continue
		}
		if m, ok := val.(map[string]interface{}); ok {
			for k, v := range m {
				merged[k] = v
			}
			continue
		}
		// Non-map result: wrap under a sensible key.
		merged[wrapKeyFor(sel)] = val
	}
	return marshal(merged)
}

// wrapKeyFor picks a top-level key under which to place a non-map JMESPath
// result. For simple dot-paths it's the last segment; for complex
// expressions it falls back to "result".
func wrapKeyFor(sel string) string {
	s := strings.TrimPrefix(sel, ".")
	if s == "" {
		return "result"
	}
	// If the expression ends in `.identifier`, use that identifier.
	if i := strings.LastIndex(s, "."); i >= 0 && isSimpleDotPath(s[i+1:]) {
		return s[i+1:]
	}
	// If the whole expression is a simple identifier, use it.
	if isSimpleDotPath(s) {
		return s
	}
	return "result"
}

// marshalYAMLValue encodes a Go value as a YAML document (trailing newline
// included). Used when building JMESPath output for YAML files.
func marshalYAMLValue(v interface{}) ([]byte, error) {
	out, err := yaml.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshaling JMESPath result as YAML: %w", err)
	}
	return out, nil
}

// marshalJSONValue encodes a Go value as pretty-printed JSON (trailing
// newline included).
func marshalJSONValue(v interface{}) ([]byte, error) {
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshaling JMESPath result as JSON: %w", err)
	}
	return append(out, '\n'), nil
}

// filterYAMLJMESPath is the JMESPath path for YAML files. It decodes the
// YAML to a plain Go value (losing comments and layout), runs the
// selectors, and re-emits as YAML. Comments and layout are NOT preserved —
// that's the trade-off callers opt into by writing a complex expression.
func filterYAMLJMESPath(content []byte, selectors []string) ([]byte, error) {
	var data interface{}
	if err := yaml.Unmarshal(content, &data); err != nil {
		return nil, fmt.Errorf("parsing YAML for JMESPath select: %w", err)
	}
	// yaml.v3 decodes mappings into map[string]interface{} already when
	// the target is interface{}, but nested levels may be
	// map[interface{}]interface{} in older versions. Force to the JMESPath
	// shape.
	data = coerceYAMLToJMESPath(data)
	return runJMESPathSelectors(data, selectors, marshalYAMLValue)
}

// coerceYAMLToJMESPath walks a decoded YAML value tree and converts any
// map[interface{}]interface{} nodes to map[string]interface{}, which is
// what JMESPath expects. yaml.v3 uses map[string]interface{} for string
// keys, so this is usually a no-op — but we defend against older decoders
// and against exotic non-string keys (which we stringify).
func coerceYAMLToJMESPath(v interface{}) interface{} {
	switch x := v.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(x))
		for k, vv := range x {
			out[k] = coerceYAMLToJMESPath(vv)
		}
		return out
	case map[interface{}]interface{}:
		out := make(map[string]interface{}, len(x))
		for k, vv := range x {
			out[fmt.Sprintf("%v", k)] = coerceYAMLToJMESPath(vv)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(x))
		for i, vv := range x {
			out[i] = coerceYAMLToJMESPath(vv)
		}
		return out
	default:
		return v
	}
}

// filterJSONJMESPath is the JMESPath path for JSON files.
func filterJSONJMESPath(content []byte, selectors []string) ([]byte, error) {
	var data interface{}
	if err := json.Unmarshal(content, &data); err != nil {
		return nil, fmt.Errorf("parsing JSON for JMESPath select: %w", err)
	}
	return runJMESPathSelectors(data, selectors, marshalJSONValue)
}
