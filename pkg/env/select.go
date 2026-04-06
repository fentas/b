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
// Each selector is a gjson path (e.g. "binaries", "database.host").
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
func filterYAML(content []byte, selectors []string) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(content, &doc); err != nil {
		return nil, fmt.Errorf("parsing YAML for select: %w", err)
	}

	// doc is a Document node; its first child is the root mapping
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, fmt.Errorf("unexpected YAML structure")
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("YAML root is not a mapping")
	}

	// Build a new mapping with only the selected keys
	result := &yaml.Node{
		Kind: yaml.MappingNode,
		Tag:  "!!map",
	}

	for _, sel := range selectors {
		key := strings.TrimPrefix(sel, ".")
		if key == "" {
			continue
		}

		// For simple top-level keys, extract directly from the AST
		// For nested keys (a.b.c), fall back to marshal→gjson→unmarshal
		if !strings.Contains(key, ".") {
			if keyNode, valNode := findMappingKey(root, key); keyNode != nil {
				result.Content = append(result.Content, keyNode, valNode)
			}
		} else {
			// Nested path: convert to JSON, use gjson, convert back
			if err := extractNestedYAML(root, key, result); err != nil {
				// Silently skip missing nested keys
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

// findMappingKey finds a key-value pair in a YAML mapping node.
func findMappingKey(mapping *yaml.Node, key string) (*yaml.Node, *yaml.Node) {
	for i := 0; i < len(mapping.Content)-1; i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i], mapping.Content[i+1]
		}
	}
	return nil, nil
}

// extractNestedYAML extracts a nested key from YAML using gjson for path resolution.
func extractNestedYAML(root *yaml.Node, path string, result *yaml.Node) error {
	// Marshal the root to JSON for gjson lookup
	var data interface{}
	if err := root.Decode(&data); err != nil {
		return err
	}
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		return err
	}

	// Use gjson to extract the value
	val := gjson.GetBytes(jsonBytes, path)
	if !val.Exists() {
		return fmt.Errorf("path %q not found", path)
	}

	// Build the nested structure as YAML nodes
	parts := strings.Split(path, ".")
	var valueNode yaml.Node
	if err := yaml.Unmarshal([]byte(val.Raw), &valueNode); err != nil {
		// Fall back to string value
		valueNode = yaml.Node{Kind: yaml.ScalarNode, Value: val.String()}
	} else if valueNode.Kind == yaml.DocumentNode && len(valueNode.Content) > 0 {
		valueNode = *valueNode.Content[0]
	}

	// Create nested key structure
	current := result
	for i, part := range parts {
		keyNode := &yaml.Node{Kind: yaml.ScalarNode, Value: part}
		if i == len(parts)-1 {
			current.Content = append(current.Content, keyNode, &valueNode)
		} else {
			// Find or create nested mapping
			_, existing := findMappingKey(current, part)
			if existing != nil && existing.Kind == yaml.MappingNode {
				current = existing
			} else {
				nested := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
				current.Content = append(current.Content, keyNode, nested)
				current = nested
			}
		}
	}
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
