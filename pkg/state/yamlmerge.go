package state

import (
	"os"

	"gopkg.in/yaml.v3"
)

// SaveConfigPreserving saves the configuration while preserving comments
// and formatting from the existing file. Falls back to clean marshal
// on any read/parse error (without re-entering SaveConfig).
func SaveConfigPreserving(config *State, configPath string) error {
	existing, err := os.ReadFile(configPath)
	if err != nil {
		return saveConfigClean(config, configPath)
	}

	// Parse existing file as Node tree
	var doc yaml.Node
	if err := yaml.Unmarshal(existing, &doc); err != nil {
		return saveConfigClean(config, configPath)
	}

	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return saveConfigClean(config, configPath)
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return saveConfigClean(config, configPath)
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
		return saveConfigClean(config, configPath)
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

// mergeMappings synchronizes dst to match src for mapping nodes.
// Existing keys in dst are updated with values from src, new keys are appended.
// Keys in dst not present in src are removed. Nested mappings are merged recursively.
// Comments on retained existing keys are preserved.
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
					// Replace value but preserve comments on both key and value
					keyHead := dstKey.HeadComment
					keyLine := dstKey.LineComment
					keyFoot := dstKey.FootComment
					valHead := dstVal.HeadComment
					valLine := dstVal.LineComment
					valFoot := dstVal.FootComment
					*dstVal = *srcVal
					dstKey.HeadComment = keyHead
					dstKey.LineComment = keyLine
					dstKey.FootComment = keyFoot
					if valHead != "" {
						dstVal.HeadComment = valHead
					}
					if valLine != "" {
						dstVal.LineComment = valLine
					}
					if valFoot != "" {
						dstVal.FootComment = valFoot
					}
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
