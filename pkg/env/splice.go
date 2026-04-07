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
// are replaced with the merged values. Out-of-scope top-level keys in
// `local` are preserved; YAML comments and key ordering are preserved on
// a best-effort basis. The structural fast path round-trips through
// yaml.v3's emitter, which can normalize whitespace and quoting style
// even for unchanged keys, so the output is not guaranteed to be
// byte-identical to the input. The text fallback path (used when the
// merge produced conflict markers) preserves bytes verbatim.
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
//     limitation of yaml.v3 that the structural splice cannot work around
//     here. A format-preserving emitter is tracked as a separate
//     follow-up.
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
		// JSON scope selection is supported by filterContent, but
		// splicing the scoped result back into the full local document
		// is not implemented yet. Returning `merged` here would
		// overwrite the on-disk file with only the selected scope and
		// drop out-of-scope content — the #122 data-loss bug. Fail fast
		// until JSON splice support exists. The wording mentions
		// "select" rather than "merge" because spliceSelectedScope is
		// also called from the non-merge replace path.
		return nil, fmt.Errorf("scoped select/splicing is not supported for JSON files yet (%s) — remove the select filter or move the data to YAML", filePath)
	default:
		// Unknown extension: select isn't supported on these in
		// filterContent either, so this branch only fires from internal
		// callers. Pass through.
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

	// Empty local file: emit the merged content verbatim. There's
	// nothing to splice into and nothing to lose.
	if localDoc.Kind == 0 || len(localDoc.Content) == 0 {
		return merged, nil
	}
	if localDoc.Kind != yaml.DocumentNode {
		return nil, fmt.Errorf("unexpected local YAML structure for splice")
	}
	localRoot := localDoc.Content[0]
	if localRoot.Kind != yaml.MappingNode {
		// Local YAML root is a scalar or sequence, not a mapping. We
		// can't splice scoped top-level keys into a non-mapping. Return
		// an error so the caller falls back to the text splice (which
		// preserves bytes) instead of silently overwriting the local
		// file with only the filtered scope. Per copilot review on
		// PR #126 round 2.
		return nil, fmt.Errorf("local YAML root is not a mapping (kind %d), cannot splice scoped keys", localRoot.Kind)
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
		startByte := lineStart(lineOffsets, keyNode.Line, len(source))

		// End of this entry = start of the next top-level key, or EOF.
		var endByte int
		if i+2 < total-1 {
			nextKey := root.Content[i+2]
			endByte = lineStart(lineOffsets, nextKey.Line, len(source))
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

// lineStart returns the byte offset at which the 1-based line begins.
// `srcLen` is the length of the source the offsets were computed from.
// If line is <= 0 it returns 0; if line is past the last recorded line
// it clamps to srcLen (true EOF), not the start of the last line.
//
// The srcLen parameter was added in response to copilot review on
// PR #126: previously the function returned offsets[len(offsets)-1] for
// out-of-range lines, which is the start of the LAST line, not EOF —
// contradicting the doc comment.
func lineStart(offsets []int, line int, srcLen int) int {
	if line <= 0 {
		return 0
	}
	if line-1 >= len(offsets) {
		return srcLen
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
//  2. Heuristically split `merged` into per-top-level-key byte ranges by
//     scanning for unindented `key:` lines. This works even when one of
//     those ranges contains conflict markers — we just need to find the
//     line where the next top-level key starts.
//  3. For each scoped top-level key in local, replace its byte range with
//     the corresponding range from merged. Out-of-scope ranges in local
//     are left untouched. If a scoped local key is NOT found in merged
//     (either because the merge decided it should be removed, or
//     because the heuristic scanner missed it for an exotic input —
//     quoted keys, document directives, etc.), the local range is
//     dropped (treated as a deletion). This matches the structural
//     splice's "key absent in merged" behavior.
//  4. Any keys present in `merged` but absent from `local` are
//     appended at the end (additions, in merged source order).
//
// Per-key splicing was added in response to copilot review on PR #126,
// which pointed out that the previous "insert once at the first scoped
// range, suppress subsequent ranges" approach reordered content when
// scoped keys were non-contiguous in the local file.
func spliceYAMLText(local, merged []byte, scope map[string]bool) ([]byte, error) {
	var localDoc yaml.Node
	if err := yaml.Unmarshal(local, &localDoc); err != nil {
		// Can't parse local either. Returning `merged` would silently
		// overwrite the local file with just the filtered scope (the
		// #126 data-loss bug). Return an error so the caller surfaces
		// it instead.
		return nil, fmt.Errorf("text splice: local YAML is not parseable: %w", err)
	}
	if localDoc.Kind != yaml.DocumentNode || len(localDoc.Content) == 0 {
		return nil, fmt.Errorf("text splice: local YAML has no document content")
	}
	localRoot := localDoc.Content[0]
	if localRoot.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("text splice: local YAML root is not a mapping (kind %d), cannot preserve out-of-scope content", localRoot.Kind)
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

	// Build per-key ranges from merged via the heuristic scanner.
	// scanTopLevelKeyRanges also returns a deterministic key order
	// (source order in `merged`) so trailing additions can be
	// appended in the same order the merge produced them.
	mergedByKey, mergedOrder := scanTopLevelKeyRangesOrdered(merged)

	// Walk local byte-by-byte. For each scoped range:
	//   - if the key exists in merged: emit merged's slice for that key
	//     (which may carry conflict markers)
	//   - if the key does NOT exist in merged: skip it (treat as deletion
	//     — the merge result decided this key should not exist)
	// Track which merged keys we've consumed so we can append the rest
	// (additions: keys present in merged but absent from local) after
	// the loop. This matches the structural splice's add/remove
	// semantics. Per copilot review on PR #126 round 3.
	var out bytes.Buffer
	cursor := 0
	consumed := make(map[string]bool, len(scoped))
	for _, r := range scoped {
		if r.startByte > cursor {
			out.Write(local[cursor:r.startByte])
		}
		if mergedSlice, ok := mergedByKey[r.key]; ok {
			out.Write(mergedSlice)
			if len(mergedSlice) > 0 && mergedSlice[len(mergedSlice)-1] != '\n' {
				out.WriteByte('\n')
			}
			consumed[r.key] = true
		}
		// If not in merged, emit nothing — the merge decided this
		// scoped key should be removed.
		cursor = r.endByte
	}
	if cursor < len(local) {
		out.Write(local[cursor:])
	}

	// Additions: append any merged keys that weren't already in local.
	// They go at the end of the file in merged-source order. Ensure a
	// trailing newline before appending so we don't fuse with the
	// previous line.
	if out.Len() > 0 {
		if buf := out.Bytes(); buf[len(buf)-1] != '\n' {
			out.WriteByte('\n')
		}
	}
	for _, k := range mergedOrder {
		if consumed[k] || !scope[k] {
			continue
		}
		slice := mergedByKey[k]
		out.Write(slice)
		if len(slice) > 0 && slice[len(slice)-1] != '\n' {
			out.WriteByte('\n')
		}
	}
	return out.Bytes(), nil
}

// scanTopLevelKeyRanges is a heuristic byte-range extractor for YAML
// top-level keys. It walks `src` line by line and treats every line
// matching `^[A-Za-z0-9_-]+:` (no leading whitespace) as the start of
// a new top-level key. The key's byte range runs from the start of its
// line to the start of the next top-level key (or EOF). The first key's
// range is extended back to the start of `src` so leading content
// (header comments, conflict markers) is preserved.
//
// This is intentionally tolerant: it does NOT parse YAML, so it works on
// inputs that contain `git merge-file` conflict markers in nested values.
// Conflict markers themselves start with `<<<<<<<` / `=======` /
// `>>>>>>>`, none of which match the top-level-key regex, so they don't
// confuse the scanner.
//
// Limitations:
//   - Doesn't handle quoted keys, multi-line keys, or keys containing
//     special characters. These are rare in YAML files used as b config
//     and the cost of misclassification is "merged content gets dropped
//     into the first scoped range as a fallback" — not data loss.
//   - Doesn't handle YAML documents starting with `---` directives.
func scanTopLevelKeyRanges(src []byte) map[string][]byte {
	m, _ := scanTopLevelKeyRangesOrdered(src)
	return m
}

// scanTopLevelKeyRangesOrdered is like scanTopLevelKeyRanges but also
// returns the keys in source order. The order lets the text splice
// append "addition" keys (present in merged, absent in local) in the
// same order the merge produced them.
func scanTopLevelKeyRangesOrdered(src []byte) (map[string][]byte, []string) {
	out := make(map[string][]byte)
	var order []string
	if len(src) == 0 {
		return out, order
	}
	type keyStart struct {
		name  string
		start int
	}
	var keys []keyStart

	lineStart := 0
	for i := 0; i <= len(src); i++ {
		if i == len(src) || src[i] == '\n' {
			if k := tryParseTopLevelKey(src[lineStart:i]); k != "" {
				keys = append(keys, keyStart{name: k, start: lineStart})
			}
			lineStart = i + 1
		}
	}

	for i, k := range keys {
		// For the first key, extend the range backwards to include any
		// leading bytes (header comments, conflict markers above the
		// first key, blank lines). Without this, those bytes would be
		// silently dropped during splicing. Per copilot review on
		// PR #126 round 2.
		start := k.start
		if i == 0 {
			start = 0
		}
		end := len(src)
		if i+1 < len(keys) {
			end = keys[i+1].start
		}
		out[k.name] = src[start:end]
		order = append(order, k.name)
	}
	return out, order
}

// tryParseTopLevelKey returns the key name if `line` is a valid
// top-level YAML key declaration (no leading whitespace, identifier
// chars followed by `:`), or "" otherwise.
func tryParseTopLevelKey(line []byte) string {
	if len(line) == 0 {
		return ""
	}
	// Reject leading whitespace.
	if line[0] == ' ' || line[0] == '\t' {
		return ""
	}
	// Reject comment lines.
	if line[0] == '#' {
		return ""
	}
	// Identifier scan.
	end := 0
	for end < len(line) {
		c := line[end]
		isIdent := (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') ||
			(c >= '0' && c <= '9') || c == '_' || c == '-'
		if !isIdent {
			break
		}
		end++
	}
	if end == 0 {
		return ""
	}
	if end >= len(line) || line[end] != ':' {
		return ""
	}
	// After the colon must come EOL, space, or tab — anything else and
	// it's not a key (e.g. `https://...` should not match).
	if end+1 < len(line) {
		c := line[end+1]
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			return ""
		}
	}
	return string(line[:end])
}
