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
// semantics. To preserve backward compatibility for those legacy
// plain keys, the classifier must NOT route them to JMESPath,
// where such selectors may fail to parse as expressions.
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
	// A leading `@` is the JMESPath current-node operator. `@` or
	// `@.foo` would otherwise pass the character blocklist below
	// (since `@` is harmless mid-key) but those expressions need
	// to evaluate against the JMESPath engine, not the legacy
	// dot-path validator. Reject the leading case explicitly.
	if s[0] == '@' {
		return false
	}
	// Blocklist any character that introduces JMESPath grammar.
	// Anything else passes through — including `/`, `+`, `#`, and
	// `@` mid-key — so plain top-level keys containing those
	// characters keep using the comment-preserving Node API path.
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
//     wrapped under a key chosen by `wrapKeyFor`. The key is selected
//     by a small fallback chain: a leading identifier followed by
//     JMESPath grammar (e.g. `binaries[?...]` → `binaries`), the
//     trailing identifier of a simple dot-path (`database.host` →
//     `host`), the whole expression when it's a bare identifier
//     (`binaries` → `binaries`), or the literal string "result" as a
//     last resort. See wrapKeyFor for the exact rules.
//
//   - Nil results are ignored (JMESPath spec: missing paths return null).
//
// Limitation: a quoted-identifier expression like `'"weird[name]"'`
// returns the *value* of that key, not a {key: value} pair, so the
// original key name is lost in the merged output. Object values get
// merged at the top level; scalar/array values get wrapped under
// "result". Users who need to preserve the original key name must
// wrap it explicitly in a multi-select hash like
// `'{"weird[name]": "weird[name]"}'`. The docs callout in
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

// wrapKeyFor picks a top-level key under which to place a non-map
// JMESPath result. The fallback chain is:
//
//  1. The expression STARTS with an identifier followed by JMESPath
//     grammar like `[`, `|`, ` `, or `.*` (e.g. `binaries[?...]`,
//     `binaries | [0]`, `binaries[*].name`): use the leading
//     identifier. The leading identifier is almost always the
//     conceptual "thing" the user is filtering. This step also
//     wins over the trailing-identifier step for projection
//     expressions like `binaries[*].name`, where `binaries` is a
//     better wrap key than `name`.
//
//     Function calls (`items(binaries)`) DO NOT count as a leading
//     identifier — the leading text is the function name, not a
//     conceptual top-level key. Such expressions fall through to
//     step 4.
//
//  2. The expression is a simple dot-path that ends in `.identifier`
//     (e.g. `database.host`): use the trailing identifier. The
//     intuition is "this expression drills DOWN to a leaf field;
//     the leaf is what the user wanted".
//
//  3. The whole expression is a simple identifier (e.g. `binaries`):
//     use it.
//
//  4. Otherwise → "result".
//
// Step 1 was added in response to copilot review: previously a filter
// expression like `binaries[?contains(groups, 'core')]` returned
// a flat array and got wrapped under "result", which is surprising for
// users who'd expect "binaries". The leading-identifier extraction
// handles those cases without changing behavior for users who
// explicitly wrap with a multi-select hash.
func wrapKeyFor(sel string) string {
	s := strings.TrimPrefix(sel, ".")
	if s == "" {
		return "result"
	}

	// (1) Leading identifier followed by JMESPath grammar (NOT a
	// function call). Walk forward from index 0 collecting an
	// identifier prefix, then look at the next character to decide
	// whether this looks like an expression we should wrap under
	// the leading identifier.
	leadingEnd := 0
	for leadingEnd < len(s) {
		c := s[leadingEnd]
		isIdent := (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') ||
			(c >= '0' && c <= '9') || c == '_' || c == '-'
		if !isIdent {
			break
		}
		leadingEnd++
	}
	if leadingEnd > 0 && leadingEnd < len(s) {
		next := s[leadingEnd]
		// `[` `|` ` ` are JMESPath operators that take the leading
		// identifier as their input. `(` is a function call —
		// the leading identifier is a function name, not a key.
		// `.` ends in step 2 / 3 unless followed by `*` (projection).
		isOperator := next == '[' || next == '|' || next == ' ' || next == '\t'
		// `database.*` projection — the leading identifier IS the
		// thing being projected.
		if !isOperator && next == '.' && leadingEnd+1 < len(s) && s[leadingEnd+1] == '*' {
			isOperator = true
		}
		// `binaries[*].name` — the leading identifier is `binaries`,
		// followed by `[`, which is the projection bracket.
		if isOperator {
			leading := s[:leadingEnd]
			// Reject digit-leading (would be confusing as a key)
			// and the case where the whole expression is a simple
			// identifier (caught by step 3 below for nicer reading).
			if leading[0] < '0' || leading[0] > '9' {
				return leading
			}
		}
	}

	// (2) Trailing identifier after the last dot, only if the
	// expression is a simple dot-path (no JMESPath grammar in the
	// middle). isSimpleDotPath(s) == true implies the whole thing
	// is dot-paths only, so the last segment is a meaningful key.
	if isSimpleDotPath(s) {
		if i := strings.LastIndex(s, "."); i >= 0 {
			return s[i+1:]
		}
		// (3) Whole expression is a simple identifier.
		return s
	}

	// (4) Fallback for expressions we can't classify.
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
