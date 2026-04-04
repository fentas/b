package env

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v2"
)

// filterContent applies select filters to file content.
// If selectors is empty, returns content unchanged.
// Supports YAML and JSON files (detected by extension).
// Each selector is a dot-path like ".binaries" or ".database.host".
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

// filterYAML extracts selected keys from YAML content.
func filterYAML(content []byte, selectors []string) ([]byte, error) {
	var data map[string]interface{}
	if err := yaml.Unmarshal(content, &data); err != nil {
		return nil, fmt.Errorf("parsing YAML for select: %w", err)
	}

	result := make(map[string]interface{})
	for _, sel := range selectors {
		key := strings.TrimPrefix(sel, ".")
		if key == "" {
			continue
		}

		// Support nested keys: .a.b.c
		parts := strings.Split(key, ".")
		val := lookupNested(data, parts)
		if val != nil {
			setNested(result, parts, val)
		}
	}

	out, err := yaml.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("marshaling selected YAML: %w", err)
	}
	return out, nil
}

// filterJSON extracts selected keys from JSON content.
func filterJSON(content []byte, selectors []string) ([]byte, error) {
	var data map[string]interface{}
	if err := json.Unmarshal(content, &data); err != nil {
		return nil, fmt.Errorf("parsing JSON for select: %w", err)
	}

	result := make(map[string]interface{})
	for _, sel := range selectors {
		key := strings.TrimPrefix(sel, ".")
		if key == "" {
			continue
		}

		parts := strings.Split(key, ".")
		val := lookupNested(data, parts)
		if val != nil {
			setNested(result, parts, val)
		}
	}

	out, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshaling selected JSON: %w", err)
	}
	out = append(out, '\n')
	return out, nil
}

// lookupNested traverses a nested map by key parts.
func lookupNested(data map[string]interface{}, parts []string) interface{} {
	if len(parts) == 0 {
		return nil
	}
	val, ok := data[parts[0]]
	if !ok {
		return nil
	}
	if len(parts) == 1 {
		return val
	}
	// Recurse into nested map
	switch nested := val.(type) {
	case map[string]interface{}:
		return lookupNested(nested, parts[1:])
	case map[interface{}]interface{}:
		// yaml.v2 returns map[interface{}]interface{} for nested maps
		converted := make(map[string]interface{})
		for k, v := range nested {
			converted[fmt.Sprintf("%v", k)] = v
		}
		return lookupNested(converted, parts[1:])
	}
	return nil
}

// setNested sets a value in a nested map by key parts.
func setNested(data map[string]interface{}, parts []string, val interface{}) {
	if len(parts) == 0 {
		return
	}
	if len(parts) == 1 {
		data[parts[0]] = val
		return
	}
	sub, ok := data[parts[0]].(map[string]interface{})
	if !ok {
		sub = make(map[string]interface{})
		data[parts[0]] = sub
	}
	setNested(sub, parts[1:], val)
}
