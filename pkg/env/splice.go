package env

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// spliceSelectedScope takes the consumer's full local file, the merged
// result of the selected scope (from doMerge), and the list of selectors,
// and returns a new file where only the selected top-level keys in `local`
// are replaced with the merged values. All other top-level keys, comments,
// and layout in `local` are preserved verbatim.
//
// This is the complement of filterContent: filterContent extracts a scope;
// spliceSelectedScope puts a (merged) scope back.
//
// Two paths:
//
//   - Structural splice (fast path): when `merged` parses as valid YAML,
//     rewrite the local Node tree in place (replace scoped key values,
//     append new scoped keys, remove vanished scoped keys). Out-of-scope
//     comments and layout are preserved because their Nodes are untouched.
//     Note: yaml.Marshal re-emits the whole document, so the output won't
//     be byte-identical to the input even for unchanged keys — this is a
//     limitation of yaml.v3. See TODO below.
//
//   - Text splice (conflict path): when `merged` contains `git merge-file`
//     conflict markers, it doesn't parse as YAML. In that case we find the
//     byte range of each scoped top-level key in `local` using yaml.v3
//     Node Line/Column metadata, and replace that range with the
//     conflicted text from `merged`. The rest of `local` is preserved
//     byte-for-byte, so consumer-owned content and comments survive even
//     in the conflict case — which is the #122 data-loss fix the user
//     actually cares about.
//
// Only top-level selectors are supported. Nested selectors (`database.host`)
// are flattened to their top-level key (`database`) and the whole top-level
// scope is spliced; the merge will have done the right thing inside.
func spliceSelectedScope(local, merged []byte, selectors []string, filePath string) ([]byte, error) {
	if len(selectors) == 0 {
		return merged, nil
	}

	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".yaml", ".yml":
		return spliceYAML(local, merged, selectors)
	case ".json":
		// JSON splice isn't implemented yet. The core #122 use case is
		// YAML (.bin/b.yaml) so this keeps the old behavior for JSON until
		// there is a real use case that needs it.
		return merged, nil
	default:
		return merged, nil
	}
}

// topLevelKeysFromSelectors returns the set of top-level YAML keys that are
// within the selector scope. A selector like "binaries" or ".binaries" maps
// to {binaries}; a nested selector like "database.host" or ".database.host"
// maps to {database}.
func topLevelKeysFromSelectors(selectors []string) map[string]bool {
	keys := make(map[string]bool, len(selectors))
	for _, sel := range selectors {
		key := strings.TrimPrefix(sel, ".")
		if key == "" {
			continue
		}
		if i := strings.Index(key, "."); i >= 0 {
			key = key[:i]
		}
		keys[key] = true
	}
	return keys
}

// containsConflictMarkers checks if a byte slice contains git merge-file
// conflict markers (`<<<<<<<`, `=======`, `>>>>>>>`). We detect the full
// set; a partial match could be legitimate content (e.g. a markdown
// separator `=======`).
func containsConflictMarkers(b []byte) bool {
	return bytes.Contains(b, []byte("<<<<<<< ")) &&
		bytes.Contains(b, []byte("=======")) &&
		bytes.Contains(b, []byte(">>>>>>> "))
}

// spliceYAML dispatches between structural and text splice based on
// whether `merged` is parseable YAML.
func spliceYAML(local, merged []byte, selectors []string) ([]byte, error) {
	scope := topLevelKeysFromSelectors(selectors)

	// Fast path: merged is valid YAML. Do a structural splice that
	// preserves the most layout information (comments on non-replaced
	// keys).
	if !containsConflictMarkers(merged) {
		out, err := spliceYAMLStructural(local, merged, scope)
		if err == nil {
			return out, nil
		}
		// If structural splicing fails for any reason, fall through to
		// text splicing as a defensive fallback.
	}

	// Conflict path: find the byte ranges of the in-scope top-level keys
	// in `local` and replace them with the merged text. This preserves
	// out-of-scope content (envs:, profiles:, comments) byte-for-byte
	// even when the merged output contains conflict markers.
	return spliceYAMLText(local, merged, scope)
}

// spliceYAMLStructural is the fast path: parse both sides, rewrite the
// local tree in place, re-emit.
func spliceYAMLStructural(local, merged []byte, scope map[string]bool) ([]byte, error) {
	var localDoc yaml.Node
	if err := yaml.Unmarshal(local, &localDoc); err != nil {
		return nil, fmt.Errorf("parsing local YAML for splice: %w", err)
	}

	// Empty local file: emit the merged content verbatim.
	if localDoc.Kind == 0 || len(localDoc.Content) == 0 {
		return merged, nil
	}
	if localDoc.Kind != yaml.DocumentNode {
		return nil, fmt.Errorf("unexpected local YAML structure for splice")
	}
	localRoot := localDoc.Content[0]
	if localRoot.Kind != yaml.MappingNode {
		return merged, nil
	}

	var mergedDoc yaml.Node
	if err := yaml.Unmarshal(merged, &mergedDoc); err != nil {
		return nil, fmt.Errorf("parsing merged YAML for splice: %w", err)
	}
	if mergedDoc.Kind != yaml.DocumentNode || len(mergedDoc.Content) == 0 {
		return merged, nil
	}
	mergedRoot := mergedDoc.Content[0]
	if mergedRoot.Kind != yaml.MappingNode {
		return merged, nil
	}

	// Build a quick lookup of merged's key→valueNode.
	mergedByKey := make(map[string]*yaml.Node, len(mergedRoot.Content)/2)
	mergedOrder := make([]string, 0, len(mergedRoot.Content)/2)
	for i := 0; i < len(mergedRoot.Content)-1; i += 2 {
		k := mergedRoot.Content[i].Value
		mergedByKey[k] = mergedRoot.Content[i+1]
		mergedOrder = append(mergedOrder, k)
	}

	// Walk the local tree and replace in-place for keys that are (a) in
	// the selector scope and (b) present in the merged result. Removal
	// of a scoped key absent from merged is explicit: the 3-way merge
	// decided it should not exist.
	consumed := make(map[string]bool, len(mergedByKey))
	for i := 0; i < len(localRoot.Content)-1; i += 2 {
		keyNode := localRoot.Content[i]
		k := keyNode.Value
		if !scope[k] {
			continue
		}
		if mergedVal, ok := mergedByKey[k]; ok {
			localRoot.Content[i+1] = mergedVal
			consumed[k] = true
			continue
		}
		// Scoped key removed by merge → drop from local too.
		localRoot.Content = append(localRoot.Content[:i], localRoot.Content[i+2:]...)
		i -= 2
	}

	// Append merged keys that weren't already in local (additions).
	for _, k := range mergedOrder {
		if consumed[k] || !scope[k] {
			continue
		}
		localRoot.Content = append(localRoot.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k},
			mergedByKey[k],
		)
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&localDoc); err != nil {
		return nil, fmt.Errorf("encoding spliced YAML: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("closing YAML encoder: %w", err)
	}
	return buf.Bytes(), nil
}

// keyByteRange records the byte offsets of a top-level key-value pair in
// the source text, including the key line itself and its value subtree.
type keyByteRange struct {
	key       string
	startByte int // byte offset of the start of the key line
	endByte   int // byte offset just after the last byte of the value
}

// topLevelKeyRanges walks a YAML document's top-level mapping and returns
// the byte range of each key's full "key: value" block in the source text.
// Ranges are returned in source order. Uses yaml.v3 Node Line metadata
// combined with a line→byte-offset table built from the raw source.
func topLevelKeyRanges(source []byte, root *yaml.Node) []keyByteRange {
	lineOffsets := computeLineOffsets(source)
	total := len(root.Content)
	var ranges []keyByteRange
	for i := 0; i < total-1; i += 2 {
		keyNode := root.Content[i]
		startByte := lineStart(lineOffsets, keyNode.Line)

		// End of this entry = start of the next top-level key, or EOF.
		var endByte int
		if i+2 < total-1 {
			nextKey := root.Content[i+2]
			endByte = lineStart(lineOffsets, nextKey.Line)
		} else {
			endByte = len(source)
		}
		ranges = append(ranges, keyByteRange{
			key:       keyNode.Value,
			startByte: startByte,
			endByte:   endByte,
		})
	}
	return ranges
}

// computeLineOffsets returns a slice where index i holds the byte offset
// of the start of line (i+1). Index 0 is always 0.
func computeLineOffsets(source []byte) []int {
	offsets := []int{0}
	for i, b := range source {
		if b == '\n' {
			offsets = append(offsets, i+1)
		}
	}
	return offsets
}

// lineStart returns the byte offset at which the 1-based line begins. If
// the line is out of range, returns len(source).
func lineStart(offsets []int, line int) int {
	if line <= 0 {
		return 0
	}
	if line-1 >= len(offsets) {
		return offsets[len(offsets)-1]
	}
	return offsets[line-1]
}

// spliceYAMLText is the conflict-preserving fallback: parse only `local`
// structurally (the merged bytes are unparseable because of conflict
// markers), compute the byte ranges of in-scope keys, and splice.
//
// Strategy:
//
//  1. Parse local → get top-level key byte ranges.
//  2. Identify the contiguous "scoped region" in local: the bytes from the
//     first scoped key to the end of the last scoped key. If scoped keys
//     are not contiguous (some out-of-scope key sits between them), the
//     strategy still works: we replace each scoped key's range
//     individually.
//  3. Replace each scoped key's range with the *entire* merged text the
//     first time, and with empty bytes thereafter. This is coarser than
//     per-key splicing but safe: since the merge output contains all
//     scoped keys, inserting it once covers everything.
//
// The output has conflict markers in place of the scoped region, and the
// rest of the file is preserved byte-for-byte.
func spliceYAMLText(local, merged []byte, scope map[string]bool) ([]byte, error) {
	var localDoc yaml.Node
	if err := yaml.Unmarshal(local, &localDoc); err != nil {
		// Can't parse local either — nothing useful we can do. Return the
		// merged bytes verbatim so the user at least sees the conflict.
		return merged, nil
	}
	if localDoc.Kind != yaml.DocumentNode || len(localDoc.Content) == 0 {
		return merged, nil
	}
	localRoot := localDoc.Content[0]
	if localRoot.Kind != yaml.MappingNode {
		return merged, nil
	}

	ranges := topLevelKeyRanges(local, localRoot)
	if len(ranges) == 0 {
		return merged, nil
	}

	// Collect the scoped ranges in source order.
	var scoped []keyByteRange
	for _, r := range ranges {
		if scope[r.key] {
			scoped = append(scoped, r)
		}
	}
	if len(scoped) == 0 {
		// No scoped keys in local but merge produced scoped output —
		// append the merged text at EOF so additions survive.
		var buf bytes.Buffer
		buf.Write(local)
		if len(local) > 0 && local[len(local)-1] != '\n' {
			buf.WriteByte('\n')
		}
		buf.Write(merged)
		return buf.Bytes(), nil
	}

	// Build the output: walk local byte-by-byte, and when we hit the start
	// of the first scoped range, emit the full merged text; for subsequent
	// scoped ranges, emit nothing (they're already represented in merged).
	var out bytes.Buffer
	cursor := 0
	insertedMerged := false
	for _, r := range scoped {
		if r.startByte > cursor {
			out.Write(local[cursor:r.startByte])
		}
		if !insertedMerged {
			out.Write(merged)
			if len(merged) > 0 && merged[len(merged)-1] != '\n' {
				out.WriteByte('\n')
			}
			insertedMerged = true
		}
		cursor = r.endByte
	}
	if cursor < len(local) {
		out.Write(local[cursor:])
	}
	return out.Bytes(), nil
}
