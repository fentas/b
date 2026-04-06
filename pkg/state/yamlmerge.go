package state

import (
	"os"

	"gopkg.in/yaml.v3"
)

// SaveConfigPreserving saves the configuration while preserving comments
// and formatting from the existing file. If the file doesn't exist,
// falls back to a clean marshal.
func SaveConfigPreserving(config *State, configPath string) error {
	existing, err := os.ReadFile(configPath)
	if err != nil {
		// File doesn't exist — do a clean write
		return SaveConfig(config, configPath)
	}

	// Parse existing file as Node tree
	var doc yaml.Node
	if err := yaml.Unmarshal(existing, &doc); err != nil {
		// Can't parse existing — overwrite
		return SaveConfig(config, configPath)
	}

	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return SaveConfig(config, configPath)
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return SaveConfig(config, configPath)
	}

	// Marshal the new config to get the updated data
	newData, err := yaml.Marshal(config)
	if err != nil {
		return err
	}

	var newDoc yaml.Node
	if err := yaml.Unmarshal(newData, &newDoc); err != nil {
		return err
	}
	if newDoc.Kind != yaml.DocumentNode || len(newDoc.Content) == 0 {
		return SaveConfig(config, configPath)
	}
	newRoot := newDoc.Content[0]

	// Merge new values into existing tree, preserving comments on unchanged keys
	mergeMappings(root, newRoot)

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, out, 0644)
}

// mergeMappings merges src mapping into dst mapping.
// Existing keys in dst are updated with values from src.
// New keys in src are appended to dst.
// Comments on existing keys in dst are preserved.
func mergeMappings(dst, src *yaml.Node) {
	if dst.Kind != yaml.MappingNode || src.Kind != yaml.MappingNode {
		return
	}

	for i := 0; i < len(src.Content)-1; i += 2 {
		srcKey := src.Content[i]
		srcVal := src.Content[i+1]

		// Find matching key in dst
		found := false
		for j := 0; j < len(dst.Content)-1; j += 2 {
			dstKey := dst.Content[j]
			dstVal := dst.Content[j+1]

			if dstKey.Value == srcKey.Value {
				found = true
				// If both are mappings, recurse
				if dstVal.Kind == yaml.MappingNode && srcVal.Kind == yaml.MappingNode {
					mergeMappings(dstVal, srcVal)
				} else {
					// Replace value but preserve the key's comments
					keyComment := dstKey.HeadComment
					keyLineComment := dstKey.LineComment
					*dstVal = *srcVal
					dstKey.HeadComment = keyComment
					dstKey.LineComment = keyLineComment
				}
				break
			}
		}

		if !found {
			// Append new key-value pair
			dst.Content = append(dst.Content, srcKey, srcVal)
		}
	}

	// Remove keys from dst that are not in src
	newContent := make([]*yaml.Node, 0, len(dst.Content))
	for j := 0; j < len(dst.Content)-1; j += 2 {
		dstKey := dst.Content[j]
		dstVal := dst.Content[j+1]

		found := false
		for i := 0; i < len(src.Content)-1; i += 2 {
			if src.Content[i].Value == dstKey.Value {
				found = true
				break
			}
		}
		if found {
			newContent = append(newContent, dstKey, dstVal)
		}
	}
	dst.Content = newContent
}
