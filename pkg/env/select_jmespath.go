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
//	"binaries | [?...]"         → false (pipe, filter)
//	"{b: binaries}"             → false (multi-select hash)
//	"binaries[0]"               → false (index)
//	"from_items(items(...))"    → false (function, parens)
//
// This is a lexical classifier, not a full JMESPath parser — anything that
// contains a character outside [A-Za-z0-9_.-] is treated as "complex".
// That's conservative (some valid simple paths with weird characters get
// routed to JMESPath and lose comments) but safe (no false positives).
func isSimpleDotPath(sel string) bool {
	s := strings.TrimPrefix(sel, ".")
	if s == "" {
		return false
	}
	// Reject any character that could introduce JMESPath grammar. This
	// covers brackets, parens, quotes, pipes, stars, filters, wildcards,
	// comparison operators, multi-select hashes/lists, function commas.
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z':
		case c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9':
		case c == '_' || c == '-' || c == '.':
		default:
			return false
		}
	}
	// Reject consecutive or trailing dots.
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
