package env

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"gopkg.in/yaml.v3"
)

// filterContent applies select filters to file content.
// If selectors is empty, returns content unchanged.
// Supports YAML (.yaml, .yml) and JSON (.json) files.
//
// Selector syntax is **hybrid**:
//
//   - Simple dot-paths are routed to the comment-preserving Node API
//     path for YAML. The classifier accepts any selector that does not
//     contain JMESPath grammar characters (brackets, parens, braces,
//     pipe, star, ampersand, comma, quotes, comparison ops, backtick,
//     backslash, question mark, whitespace) and does not contain
//     empty path segments. This matches the legacy `filterYAML`
//     validator's behavior, so plain top-level keys with characters
//     like `/`, `+`, `@`, `#` continue to work without quoting.
//
//   - Complex expressions — filter predicates, projections, functions,
//     multi-select hashes, array indexing — are routed to the JMESPath
//     path (jmespath-community fork, which supports items/from_items).
//     JMESPath operates on the decoded data structure, so comments and
//     layout are NOT preserved for complex expressions. That trade-off is
//     only paid when the caller explicitly writes a complex query.
//
//   - Mixing simple and complex selectors on the same file is supported:
//     simple ones extract via Node API (comments kept), complex ones run
//     via JMESPath (comments lost for their output), and the results are
//     merged top-level into the final document. Any key present in both
//     results has the JMESPath version take precedence (authoritative).
//
// See docs/env-sync.mdx for examples and the comment-preservation matrix.
func filterContent(content []byte, selectors []string, filePath string) ([]byte, error) {
	if len(selectors) == 0 {
		return content, nil
	}

	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".yaml", ".yml":
		return filterYAMLHybrid(content, selectors)
	case ".json":
		return filterJSONHybrid(content, selectors)
	default:
		return nil, fmt.Errorf("select is only supported for YAML/JSON files, got %s", ext)
	}
}

// filterYAMLHybrid dispatches to the Node-API path or JMESPath based on
// the shape of each selector.
func filterYAMLHybrid(content []byte, selectors []string) ([]byte, error) {
	simple, complex := splitSelectorsByComplexity(selectors)

	// No complex expressions → fast path (Node API, comments preserved).
	if len(complex) == 0 {
		return filterYAML(content, simple)
	}

	// No simple paths → JMESPath only.
	if len(simple) == 0 {
		return filterYAMLJMESPath(content, complex)
	}

	// Mixed: run both paths, merge top-level keys. JMESPath wins on collisions.
	simpleOut, err := filterYAML(content, simple)
	if err != nil {
		return nil, err
	}
	jmesOut, err := filterYAMLJMESPath(content, complex)
	if err != nil {
		return nil, err
	}
	return mergeYAMLTopLevel(simpleOut, jmesOut)
}

// filterJSONHybrid dispatches JSON to either the legacy gjson path or
// JMESPath. Since JSON has no comments, the only benefit of the gjson
// path is its slightly different semantics — but the hybrid classifier
// keeps behavior uniform with YAML so users don't have to learn two
// dialects.
func filterJSONHybrid(content []byte, selectors []string) ([]byte, error) {
	simple, complex := splitSelectorsByComplexity(selectors)

	if len(complex) == 0 {
		return filterJSON(content, simple)
	}
	if len(simple) == 0 {
		return filterJSONJMESPath(content, complex)
	}

	// Mixed: run both and merge.
	simpleOut, err := filterJSON(content, simple)
	if err != nil {
		return nil, err
	}
	jmesOut, err := filterJSONJMESPath(content, complex)
	if err != nil {
		return nil, err
	}
	return mergeJSONTopLevel(simpleOut, jmesOut)
}

// mergeYAMLTopLevel merges b's top-level keys into a's. Any key present
// in b overrides a; other keys from a survive.
//
// This function operates on yaml.v3 Node trees so that comments and
// layout attached to a's keys (which came from the comment-preserving
// Node API path in filterYAML) survive the merge. The previous
// implementation round-tripped through map[string]interface{} and
// silently dropped all that information — see copilot review on PR #127.
func mergeYAMLTopLevel(a, b []byte) ([]byte, error) {
	aDoc, aRoot, err := parseYAMLMappingDoc(a, "simple")
	if err != nil {
		return nil, fmt.Errorf("merging YAML results: %w", err)
	}
	_, bRoot, err := parseYAMLMappingDoc(b, "jmespath")
	if err != nil {
		return nil, fmt.Errorf("merging YAML results: %w", err)
	}

	// Walk b's top-level pairs. For each, replace the matching pair in a
	// (if any), or append. Comments / styles attached to a's untouched
	// nodes are preserved because we never touch those nodes.
	for i := 0; i+1 < len(bRoot.Content); i += 2 {
		bKey := bRoot.Content[i]
		bVal := bRoot.Content[i+1]
		if idx := findYAMLTopLevelKey(aRoot, bKey.Value); idx >= 0 {
			aRoot.Content[idx] = bKey
			aRoot.Content[idx+1] = bVal
			continue
		}
		aRoot.Content = append(aRoot.Content, bKey, bVal)
	}

	out, err := yaml.Marshal(aDoc)
	if err != nil {
		return nil, fmt.Errorf("encoding merged YAML: %w", err)
	}
	return out, nil
}

// parseYAMLMappingDoc parses YAML bytes into a Document node and returns
// (doc, root mapping, err). Empty input is normalized to an empty
// mapping document so callers don't have to special-case it. The source
// label is included in error messages for diagnostics.
func parseYAMLMappingDoc(data []byte, source string) (*yaml.Node, *yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, nil, fmt.Errorf("parsing %s YAML: %w", source, err)
	}
	if doc.Kind == 0 {
		// Empty input — synthesize an empty document with an empty
		// mapping so the caller can append into it.
		doc = yaml.Node{
			Kind: yaml.DocumentNode,
			Content: []*yaml.Node{{
				Kind: yaml.MappingNode,
				Tag:  "!!map",
			}},
		}
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 || doc.Content[0] == nil {
		return nil, nil, fmt.Errorf("parsing %s YAML: expected a document with a mapping root", source)
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, nil, fmt.Errorf("parsing %s YAML: expected a top-level mapping, got kind %d", source, root.Kind)
	}
	return &doc, root, nil
}

// findYAMLTopLevelKey returns the index of the key node in a mapping
// node's Content slice (so the matching value is at index+1), or -1 if
// the key isn't present. Walks pairs in source order.
func findYAMLTopLevelKey(mapping *yaml.Node, key string) int {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i] != nil && mapping.Content[i].Value == key {
			return i
		}
	}
	return -1
}

// mergeJSONTopLevel is the JSON sibling of mergeYAMLTopLevel.
func mergeJSONTopLevel(a, b []byte) ([]byte, error) {
	var aData, bData map[string]interface{}
	if err := json.Unmarshal(a, &aData); err != nil {
		return nil, fmt.Errorf("merging JSON results: parsing simple: %w", err)
	}
	if err := json.Unmarshal(b, &bData); err != nil {
		return nil, fmt.Errorf("merging JSON results: parsing jmespath: %w", err)
	}
	if aData == nil {
		aData = make(map[string]interface{})
	}
	for k, v := range bData {
		aData[k] = v
	}
	out, err := json.MarshalIndent(aData, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encoding merged JSON: %w", err)
	}
	return append(out, '\n'), nil
}

// filterYAML extracts selected keys from YAML content using yaml.v3 Node API.
// For whole top-level keys, extracts directly from the AST — preserving comments,
// key ordering, and block/flow style. Nested dot-path selectors use gjson with a
// cached JSON conversion and may not preserve YAML comments or exact formatting.
func filterYAML(content []byte, selectors []string) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(content, &doc); err != nil {
		return nil, fmt.Errorf("parsing YAML for select: %w", err)
	}

	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, fmt.Errorf("unexpected YAML structure")
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("YAML root is not a mapping")
	}

	// Deduplicate selectors: if ".a" and ".a.b" are both present,
	// only use ".a" (the parent covers the child).
	selectors = deduplicateSelectors(selectors)

	// Cache JSON conversion for nested key lookups
	var jsonCache []byte
	needsJSON := false
	for _, sel := range selectors {
		key := strings.TrimPrefix(sel, ".")
		if strings.Contains(key, ".") {
			needsJSON = true
			break
		}
	}
	if needsJSON {
		var data interface{}
		if err := root.Decode(&data); err != nil {
			return nil, fmt.Errorf("decoding YAML for nested select: %w", err)
		}
		var jsonErr error
		jsonCache, jsonErr = json.Marshal(data)
		if jsonErr != nil {
			return nil, fmt.Errorf("encoding YAML as JSON for nested select: %w", jsonErr)
		}
	}

	// Build a new mapping with only the selected keys
	result := &yaml.Node{
		Kind: yaml.MappingNode,
		Tag:  "!!map",
	}

	// Build set of wanted keys and categorize selectors
	topLevelKeys := make(map[string]bool)    // simple top-level selections
	nestedByTop := make(map[string][]string) // top-level key → nested selectors

	for _, sel := range selectors {
		key := strings.TrimPrefix(sel, ".")
		if key == "" {
			continue
		}
		if strings.ContainsAny(key, "[]\\") {
			return nil, fmt.Errorf("YAML select only supports simple dot-paths, got %q", sel)
		}
		// Validate no empty segments (e.g. "a..b")
		parts := strings.Split(key, ".")
		for _, part := range parts {
			if part == "" {
				return nil, fmt.Errorf("YAML select only supports simple dot-paths, got %q", sel)
			}
		}
		if len(parts) == 1 {
			topLevelKeys[key] = true
		} else {
			topKey := parts[0]
			nestedByTop[topKey] = append(nestedByTop[topKey], key)
		}
	}

	added := make(map[string]bool)

	// Iterate root in source order — preserves key ordering
	for i := 0; i < len(root.Content)-1; i += 2 {
		keyNode := root.Content[i]
		valNode := root.Content[i+1]
		k := keyNode.Value

		if topLevelKeys[k] {
			result.Content = append(result.Content, keyNode, valNode)
			added[k] = true
		}
	}

	// Process nested selectors in source key order for deterministic output
	for i := 0; i < len(root.Content)-1; i += 2 {
		topKey := root.Content[i].Value
		paths, ok := nestedByTop[topKey]
		if !ok {
			continue
		}
		if added[topKey] {
			continue // whole key already selected
		}
		for _, path := range paths {
			if jsonCache == nil {
				continue
			}
			val := gjson.GetBytes(jsonCache, path)
			if !val.Exists() {
				continue
			}
			parts := strings.Split(path, ".")
			if err := addNestedToResult(result, parts, val.Raw, added); err != nil {
				return nil, fmt.Errorf("adding nested YAML selection %q: %w", path, err)
			}
		}
	}

	if len(result.Content) == 0 {
		return []byte("{}\n"), nil
	}

	newDoc := &yaml.Node{
		Kind:    yaml.DocumentNode,
		Content: []*yaml.Node{result},
	}

	out, err := yaml.Marshal(newDoc)
	if err != nil {
		return nil, fmt.Errorf("marshaling selected YAML: %w", err)
	}
	return out, nil
}

// deduplicateSelectors normalizes, removes exact duplicates, and removes selectors
// covered by a parent. e.g. if ".a" and ".a.b" are both present, only ".a" is kept.
func deduplicateSelectors(selectors []string) []string {
	// Normalize: ensure all selectors have leading dot stripped consistently
	normalized := make([]string, len(selectors))
	for i, sel := range selectors {
		normalized[i] = "." + strings.TrimPrefix(sel, ".")
	}

	seen := make(map[string]bool)
	var result []string
	for _, sel := range normalized {
		if seen[sel] {
			continue
		}
		seen[sel] = true

		covered := false
		for _, other := range normalized {
			if other == sel {
				continue
			}
			otherKey := strings.TrimPrefix(other, ".")
			selKey := strings.TrimPrefix(sel, ".")
			if strings.HasPrefix(selKey, otherKey+".") {
				covered = true
				break
			}
		}
		if !covered {
			result = append(result, sel)
		}
	}
	return result
}

// findMappingKey finds a key-value pair in a YAML mapping node.
func findMappingKey(mapping *yaml.Node, key string) (*yaml.Node, *yaml.Node) {
	for i := 0; i < len(mapping.Content)-1; i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i], mapping.Content[i+1]
		}
	}
	return nil, nil
}

// addNestedToResult adds a nested path value to the result mapping.
// If the top-level key already exists, merges the nested value into it.
func addNestedToResult(result *yaml.Node, parts []string, jsonRaw string, added map[string]bool) error {
	if len(parts) == 0 {
		return nil
	}

	topKey := parts[0]

	// Parse the value
	var valueNode yaml.Node
	if err := yaml.Unmarshal([]byte(jsonRaw), &valueNode); err != nil {
		return err
	}
	if valueNode.Kind == yaml.DocumentNode && len(valueNode.Content) > 0 {
		valueNode = *valueNode.Content[0]
	}

	// Build nested structure from deepest to shallowest
	current := &valueNode
	for i := len(parts) - 1; i >= 1; i-- {
		wrapper := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: parts[i]}
		wrapper.Content = append(wrapper.Content, keyNode, current)
		current = wrapper
	}

	// If top-level key already exists in result, merge recursively
	if added[topKey] {
		if _, existing := findMappingKey(result, topKey); existing != nil {
			if existing.Kind == yaml.MappingNode && current.Kind == yaml.MappingNode {
				mergeYAMLMappings(existing, current)
				return nil
			}
		}
		return nil // can't merge non-mappings
	}

	result.Content = append(result.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: topKey},
		current,
	)
	added[topKey] = true
	return nil
}

// mergeYAMLMappings merges src into dst by key, recursing into nested mappings.
func mergeYAMLMappings(dst, src *yaml.Node) {
	if dst.Kind != yaml.MappingNode || src.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i < len(src.Content)-1; i += 2 {
		srcKey := src.Content[i]
		srcVal := src.Content[i+1]

		found := false
		for j := 0; j < len(dst.Content)-1; j += 2 {
			if dst.Content[j].Value == srcKey.Value {
				found = true
				if dst.Content[j+1].Kind == yaml.MappingNode && srcVal.Kind == yaml.MappingNode {
					mergeYAMLMappings(dst.Content[j+1], srcVal)
				}
				break
			}
		}
		if !found {
			dst.Content = append(dst.Content, srcKey, srcVal)
		}
	}
}

// filterJSON extracts selected keys from JSON content using gjson/sjson.
// Only supports simple dot-paths and array indices that sjson can set back.
func filterJSON(content []byte, selectors []string) ([]byte, error) {
	if !gjson.ValidBytes(content) {
		return nil, fmt.Errorf("parsing JSON for select: invalid JSON")
	}

	result := []byte("{}")
	for _, sel := range selectors {
		key := strings.TrimPrefix(sel, ".")
		if key == "" {
			continue
		}

		val := gjson.GetBytes(content, key)
		if !val.Exists() {
			continue
		}

		var err error
		result, err = sjson.SetRawBytes(result, key, []byte(val.Raw))
		if err != nil {
			return nil, fmt.Errorf("setting JSON key %q: %w", key, err)
		}
	}

	// Pretty-print
	var pretty interface{}
	if err := json.Unmarshal(result, &pretty); err != nil {
		return result, nil
	}
	out, err := json.MarshalIndent(pretty, "", "  ")
	if err != nil {
		return result, nil
	}
	return append(out, '\n'), nil
}
