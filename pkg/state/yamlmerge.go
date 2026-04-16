package state

import (
	"os"

	"gopkg.in/yaml.v3"
)

// managedKey reports whether b owns the given key at the given path in the
// b.yaml schema. Only managed keys that disappear from the new (marshaled)
// state are removed from the existing file — any other dst-only key is
// assumed to be user-owned (e.g. a custom 'groups:' section or per-binary
// 'owner:' annotation) and is preserved verbatim.
//
// The empty path represents the file root.
func managedKey(path []string, key string) bool {
	switch len(path) {
	case 0:
		// File root — b owns these top-level sections.
		return key == "binaries" || key == "envs" || key == "profiles"
	case 1:
		// One level in; the previous level decides the schema:
		//   binaries.<name>   — always managed (map entries are b's list)
		//   envs.<name>       — always managed
		//   profiles.<name>   — always managed
		switch path[0] {
		case "binaries", "envs", "profiles":
			return true
		}
		return false
	case 2:
		// Two levels in — individual entry fields.
		switch path[0] {
		case "binaries":
			switch key {
			case "version", "alias", "file", "asset", "latest", "enforced":
				return true
			}
			return false
		case "envs", "profiles":
			// Matches state.EnvEntry serialization.
			switch key {
			case "description", "includes", "version", "ignore",
				"strategy", "safety", "group", "onPreSync",
				"onPostSync", "files":
				return true
			}
			return false
		}
		return false
	}
	// Deeper (e.g. files.<glob>, files.<glob>.dest) — assume managed so
	// deletions of managed fields still propagate.
	return true
}

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

	// Merge new values into existing tree, preserving comments on unchanged
	// keys and any user-owned keys (i.e. keys b doesn't manage at this
	// path — e.g. a top-level 'groups:' or per-binary 'owner:' annotation).
	mergeMappingsAt(root, newRoot, nil)

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, out, 0644)
}

// mergeMappings synchronizes dst to match src for mapping nodes.
// Existing keys in dst are updated with values from src, new keys are appended.
// Keys in dst not present in src are removed unconditionally (schema-agnostic).
// Comments on retained existing keys are preserved.
//
// Prefer mergeMappingsAt for merges that must preserve user-owned keys; this
// plain variant is kept for tests and callers that want the old semantics.
func mergeMappings(dst, src *yaml.Node) {
	mergeMappingsAt(dst, src, func(_ []string, _ string) bool { return true })
}

// mergeMappingsAt is the schema-aware variant. path is the key-path from the
// file root to dst; it's extended on recursion. managed decides whether a
// dst-only key should be removed — callers pass managedKey (or nil, which
// maps to the schema-aware default) so unknown user fields survive the save.
func mergeMappingsAt(dst, src *yaml.Node, managed func(path []string, key string) bool) {
	if dst.Kind != yaml.MappingNode || src.Kind != yaml.MappingNode {
		return
	}
	if managed == nil {
		managed = managedKey
	}
	mergeMappingsPath(dst, src, nil, managed)
}

func mergeMappingsPath(dst, src *yaml.Node, path []string, managed func([]string, string) bool) {
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
					mergeMappingsPath(dstVal, srcVal, append(path, dstKey.Value), managed)
				} else if !nodesEqual(dstVal, srcVal) {
					// Only replace if value actually changed — preserves formatting/style
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

	// Remove dst-only keys, but only if b manages the key at this path.
	// Unknown user-owned keys (e.g. 'groups:', 'owner: ...') are preserved.
	newContent := make([]*yaml.Node, 0, len(dst.Content))
	for j := 0; j < len(dst.Content)-1; j += 2 {
		dstKey := dst.Content[j]
		dstVal := dst.Content[j+1]

		foundInSrc := false
		for i := 0; i < len(src.Content)-1; i += 2 {
			if src.Content[i].Value == dstKey.Value {
				foundInSrc = true
				break
			}
		}
		if foundInSrc || !managed(path, dstKey.Value) {
			newContent = append(newContent, dstKey, dstVal)
		}
	}
	dst.Content = newContent
}

// nodesEqual compares two YAML nodes for semantic equality.
func nodesEqual(a, b *yaml.Node) bool {
	if a.Kind != b.Kind || a.Tag != b.Tag || a.Value != b.Value {
		return false
	}
	if len(a.Content) != len(b.Content) {
		return false
	}
	for i := range a.Content {
		if !nodesEqual(a.Content[i], b.Content[i]) {
			return false
		}
	}
	return true
}
