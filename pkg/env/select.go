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
// Selectors use dot-path notation for keys (e.g. "binaries", "database.host").
func filterContent(content []byte, selectors []string, filePath string) ([]byte, error) {
	if len(selectors) == 0 {
		return content, nil
	}

	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".yaml", ".yml":
		return filterYAML(content, selectors)
	case ".json":
		return filterJSON(content, selectors)
	default:
		return nil, fmt.Errorf("select is only supported for YAML/JSON files, got %s", ext)
	}
}

// filterYAML extracts selected keys from YAML content using yaml.v3 Node API.
// Preserves comments, key ordering, and block/flow style of selected sections.
// For top-level keys, extracts directly from AST. For nested keys, uses gjson
// with a cached JSON conversion.
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
		if err := root.Decode(&data); err == nil {
			jsonCache, _ = json.Marshal(data)
		}
	}

	// Build a new mapping with only the selected keys
	result := &yaml.Node{
		Kind: yaml.MappingNode,
		Tag:  "!!map",
	}

	// Track which top-level keys are already in result to avoid duplicates
	added := make(map[string]bool)

	for _, sel := range selectors {
		key := strings.TrimPrefix(sel, ".")
		if key == "" {
			continue
		}

		topKey := key
		if i := strings.Index(topKey, "."); i >= 0 {
			topKey = topKey[:i]
		}

		if added[topKey] {
			continue // already added this top-level key
		}

		// Simple top-level key — extract directly from AST (preserves comments)
		if !strings.Contains(key, ".") {
			if keyNode, valNode := findMappingKey(root, key); keyNode != nil {
				result.Content = append(result.Content, keyNode, valNode)
				added[key] = true
			}
			continue
		}

		// Nested key — use gjson with cached JSON, then build YAML nodes
		if jsonCache != nil {
			val := gjson.GetBytes(jsonCache, key)
			if !val.Exists() {
				continue
			}

			// Build nested YAML structure
			parts := strings.Split(key, ".")
			if err := addNestedToResult(result, parts, val.Raw, added); err != nil {
				continue
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

// deduplicateSelectors removes selectors that are children of other selectors.
// e.g. if ".a" and ".a.b" are both present, only ".a" is kept.
func deduplicateSelectors(selectors []string) []string {
	var result []string
	for _, sel := range selectors {
		covered := false
		for _, other := range selectors {
			if other == sel {
				continue
			}
			// Check if sel is a child of other
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
func addNestedToResult(result *yaml.Node, parts []string, jsonRaw string, added map[string]bool) error {
	if len(parts) == 0 {
		return nil
	}

	topKey := parts[0]
	if added[topKey] {
		return nil // already have this top-level key
	}

	// For nested paths, create the full nested structure
	var valueNode yaml.Node
	if err := yaml.Unmarshal([]byte(jsonRaw), &valueNode); err != nil {
		return err
	}
	if valueNode.Kind == yaml.DocumentNode && len(valueNode.Content) > 0 {
		valueNode = *valueNode.Content[0]
	}

	// Build from deepest to shallowest
	current := &valueNode
	for i := len(parts) - 1; i >= 1; i-- {
		wrapper := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		keyNode := &yaml.Node{Kind: yaml.ScalarNode, Value: parts[i]}
		wrapper.Content = append(wrapper.Content, keyNode, current)
		current = wrapper
	}

	result.Content = append(result.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: topKey},
		current,
	)
	added[topKey] = true
	return nil
}

// filterJSON extracts selected keys from JSON content using gjson/sjson.
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
