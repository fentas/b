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
// Splicing is only correct when `merged` contains the COMPLETE value
// for each in-scope top-level key. Nested selectors like `database.host`
// must NOT reach this function — the caller (SyncEnv) classifies them
// upstream and either errors out (for `merge`) or skips the splice
// entirely (for `replace`). If a nested selector did reach the splice,
// the in-scope view would be a truncated `{database: {host: ...}}`
// mapping, and replacing the consumer's full `database` node with that
// would silently drop sibling fields like `database.port`. The
// classification rules live in pkg/env/env.go's `allTopLevelSelectors`
// flag; this comment exists to keep splice.go honest about the
// preconditions it relies on.
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

// usesCRLF reports whether the file appears to use Windows-style CRLF
// line endings. We check for `\r\n` rather than just `\n` so a stray
// `\n` doesn't fool us. The byte-level splice uses this to keep
// emitted regions consistent with the local file's line endings.
func usesCRLF(b []byte) bool {
	return bytes.Contains(b, []byte("\r\n"))
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

// spliceYAML dispatches between three splice strategies, in order of
// fidelity:
//
//  1. Byte-level splice (best fidelity): when `merged` is valid YAML
//     AND the local file parses cleanly into a top-level mapping,
//     copy out-of-scope byte ranges from `local` verbatim and re-emit
//     each in-scope top-level `key: value` pair via the
//     marshalSingleTopLevelKey helper (which builds a one-key
//     synthetic document and runs it through yaml.v3's encoder).
//     Bytes outside the scoped ranges are preserved exactly, but
//     formatting WITHIN those ranges can change because yaml.v3
//     re-encodes the whole entry — including key quoting/style and
//     any key-line comments. Out-of-scope content is byte-identical
//     to the input — no spurious git-diff churn from yaml.v3 emitter
//     normalization outside the replaced ranges.
//
//  2. Structural splice (fallback for YAML quirks): when the byte-level
//     path can't be used (rare — e.g. an unrecognised local-file
//     shape), fall back to the Node-tree merge that round-trips the
//     whole document through yaml.Marshal. Out-of-scope keys keep
//     their content but lose exact whitespace.
//
//  3. Text splice (conflict path): when `merged` contains
//     `git merge-file` conflict markers, it doesn't parse as YAML at
//     all. Find the byte range of each scoped top-level key in `local`
//     using yaml.v3 Node Line/Column metadata and substitute the
//     conflicted text. Bytes outside the scoped ranges are preserved
//     verbatim.
//
// (1) was added as the format-preserving emitter follow-up to PR #126.
// Before it, every successful merge sync produced a noisy git diff
// because yaml.Marshal would re-emit the entire document with its own
// preferred whitespace and quoting style — even for keys the splice
// didn't touch.
func spliceYAML(local, merged []byte, selectors []string) ([]byte, error) {
	scope := topLevelKeysFromSelectors(selectors)

	// Path 1: byte-level splice. Requires valid YAML on both sides
	// (no conflict markers in `merged`) and a parseable
	// top-level-mapping `local`.
	if !containsConflictMarkers(merged) {
		out, err := spliceYAMLByteLevel(local, merged, scope)
		if err == nil {
			return out, nil
		}
		// Path 2: structural splice. Same parseability requirements
		// but rebuilds the whole document via yaml.Marshal. Used when
		// the byte-level splice can't handle the local shape (e.g.
		// flow-style mapping where Line metadata isn't reliable).
		out, err = spliceYAMLStructural(local, merged, scope)
		if err == nil {
			return out, nil
		}
		// Both structural paths failed. Fall through to the text
		// splice, which can recover from more weirdness.
	}

	// Path 3: text/conflict path.
	return spliceYAMLText(local, merged, scope)
}

// spliceYAMLByteLevel is the format-preserving fast path. It walks
// `local` byte-by-byte, copies out-of-scope ranges verbatim, and
// substitutes in-scope ranges with the marshaled value subtree from
// `merged`. The output preserves whitespace, quoting style, and
// comments on every byte that wasn't touched.
//
// Returns an error (instead of falling through to a slower path) when
// the local YAML can't be parsed or has a non-mapping root. The
// caller is expected to fall back to spliceYAMLStructural / spliceYAMLText.
func spliceYAMLByteLevel(local, merged []byte, scope map[string]bool) ([]byte, error) {
	// Parse local — we need yaml.v3 Node metadata to find byte ranges.
	var localDoc yaml.Node
	if err := yaml.Unmarshal(local, &localDoc); err != nil {
		return nil, fmt.Errorf("byte splice: parse local: %w", err)
	}
	// Empty document content: header-only file or whitespace. Defer
	// to the structural-path empty-doc handling rather than
	// duplicating it.
	if localDoc.Kind == 0 || len(localDoc.Content) == 0 {
		return nil, fmt.Errorf("byte splice: empty local document, defer to structural")
	}
	if localDoc.Kind != yaml.DocumentNode {
		return nil, fmt.Errorf("byte splice: unexpected local YAML structure")
	}
	localRoot := localDoc.Content[0]
	if localRoot.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("byte splice: local root is not a mapping (kind %d)", localRoot.Kind)
	}

	// Parse merged for the in-scope key values we're substituting in.
	var mergedDoc yaml.Node
	if err := yaml.Unmarshal(merged, &mergedDoc); err != nil {
		return nil, fmt.Errorf("byte splice: parse merged: %w", err)
	}
	if mergedDoc.Kind != yaml.DocumentNode || len(mergedDoc.Content) == 0 {
		return nil, fmt.Errorf("byte splice: merged has no content")
	}
	mergedRoot := mergedDoc.Content[0]
	if mergedRoot.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("byte splice: merged root is not a mapping")
	}

	// Build merged key→valueNode lookup, preserving source order for
	// deterministic addition placement.
	mergedByKey := make(map[string]*yaml.Node, len(mergedRoot.Content)/2)
	mergedOrder := make([]string, 0, len(mergedRoot.Content)/2)
	for i := 0; i+1 < len(mergedRoot.Content); i += 2 {
		k := mergedRoot.Content[i].Value
		mergedByKey[k] = mergedRoot.Content[i+1]
		mergedOrder = append(mergedOrder, k)
	}

	// Compute byte ranges for every top-level key in local. Reuses
	// the existing topLevelKeyRanges helper that already handles the
	// "blank/comment lines belong to the next key" attribution.
	ranges := topLevelKeyRanges(local, localRoot)
	if len(ranges) == 0 {
		return nil, fmt.Errorf("byte splice: no top-level key ranges in local")
	}
	// Validate that the byte ranges are sane before we start
	// slicing. yaml.v3 Line/Column metadata is occasionally missing
	// or stale (flow-style mappings, multiple keys on the same
	// line, single-line documents). Without this check a slice like
	// local[cursor:r.endByte] can panic at runtime instead of
	// returning an error and letting spliceYAML fall back to the
	// structural / text path.
	prevEnd := 0
	for i, r := range ranges {
		if r.startByte < 0 || r.endByte < 0 ||
			r.startByte > len(local) || r.endByte > len(local) ||
			r.startByte > r.endByte ||
			r.startByte < prevEnd {
			return nil, fmt.Errorf("byte splice: invalid range for key %q (start=%d end=%d prevEnd=%d len=%d)",
				r.key, r.startByte, r.endByte, prevEnd, len(local))
		}
		// At least one of the ranges must make forward progress, or
		// the splice loop would emit nothing useful.
		if i == 0 && r.endByte == 0 {
			return nil, fmt.Errorf("byte splice: zero-length first range for key %q", r.key)
		}
		prevEnd = r.endByte
	}

	// Walk local byte-by-byte. For each top-level key:
	//   - in-scope, in merged   → emit serialized merged value
	//   - in-scope, not in merged → drop (deletion)
	//   - out-of-scope          → copy verbatim
	var out bytes.Buffer
	cursor := 0
	consumed := make(map[string]bool, len(mergedByKey))

	for _, r := range ranges {
		// Out-of-scope keys: copy from cursor to end of this range
		// verbatim (this preserves both the key itself AND any
		// preceding bytes between the previous key's end and this
		// range's start, which is where leading comments/blanks for
		// out-of-scope keys live).
		if !scope[r.key] {
			out.Write(local[cursor:r.endByte])
			cursor = r.endByte
			continue
		}

		// In-scope key. First copy any bytes between cursor and the
		// start of this range (preserves any blank lines / comments
		// that the boundary attribution placed BEFORE this key but
		// AFTER the previous one). Then emit the substituted block.
		if r.startByte > cursor {
			out.Write(local[cursor:r.startByte])
		}

		mergedVal, ok := mergedByKey[r.key]
		if !ok {
			// Deletion: skip the range entirely. Don't emit
			// anything; the cursor advance below skips local's
			// version too.
			cursor = r.endByte
			continue
		}

		// Substitute: serialize the merged key:value pair as YAML
		// and emit it. We use a small synthetic mapping with just
		// this one key so the indentation comes out as a top-level
		// entry. Re-emit produces clean YAML for this slice but
		// leaves all other bytes untouched.
		serialized, serErr := marshalSingleTopLevelKey(r.key, mergedVal)
		if serErr != nil {
			return nil, fmt.Errorf("byte splice: marshal %q: %w", r.key, serErr)
		}
		// yaml.v3 always emits LF line endings. If local uses CRLF
		// we'd otherwise produce a mixed-ending file (CRLF in the
		// preserved bytes, LF in the spliced regions), which is
		// noisy in diffs and trips Windows-aware tooling.
		if usesCRLF(local) {
			serialized = bytes.ReplaceAll(serialized, []byte("\n"), []byte("\r\n"))
		}
		out.Write(serialized)
		consumed[r.key] = true
		cursor = r.endByte
	}

	// Tail bytes after the last top-level key (trailing whitespace,
	// trailing comments).
	if cursor < len(local) {
		out.Write(local[cursor:])
	}

	// Additions: any merged keys that weren't already in local. Append
	// them at the end in merged source order. Ensure a separating
	// newline first so we don't fuse with the previous trailing line.
	additions := make([]string, 0, len(mergedOrder))
	for _, k := range mergedOrder {
		if !consumed[k] && scope[k] {
			additions = append(additions, k)
		}
	}
	if len(additions) > 0 {
		crlf := usesCRLF(local)
		buf := out.Bytes()
		if len(buf) > 0 && buf[len(buf)-1] != '\n' {
			if crlf {
				out.WriteString("\r\n")
			} else {
				out.WriteByte('\n')
			}
		}
		for _, k := range additions {
			serialized, serErr := marshalSingleTopLevelKey(k, mergedByKey[k])
			if serErr != nil {
				return nil, fmt.Errorf("byte splice: marshal addition %q: %w", k, serErr)
			}
			if crlf {
				serialized = bytes.ReplaceAll(serialized, []byte("\n"), []byte("\r\n"))
			}
			out.Write(serialized)
		}
	}

	return out.Bytes(), nil
}

// marshalSingleTopLevelKey serializes one key:value pair as a
// top-level YAML mapping entry. The output starts with `key:` at
// column 0 and includes a trailing newline. Used by the byte-level
// splice to emit only the in-scope keys without re-emitting the
// surrounding document.
func marshalSingleTopLevelKey(key string, value *yaml.Node) ([]byte, error) {
	doc := &yaml.Node{
		Kind: yaml.DocumentNode,
		Content: []*yaml.Node{{
			Kind: yaml.MappingNode,
			Tag:  "!!map",
			Content: []*yaml.Node{
				{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
				value,
			},
		}},
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// spliceYAMLStructural is the fast path: parse both sides, rewrite the
// local tree in place, re-emit.
func spliceYAMLStructural(local, merged []byte, scope map[string]bool) ([]byte, error) {
	var localDoc yaml.Node
	if err := yaml.Unmarshal(local, &localDoc); err != nil {
		return nil, fmt.Errorf("parsing local YAML for splice: %w", err)
	}

	// Empty document content: there's nothing to splice INTO. But a
	// "valid YAML file with no document content" can still contain
	// header comments and whitespace that yaml.v3 doesn't surface
	// as Document.Content. Preserve those original bytes by
	// concatenating local + merged instead of dropping local
	// entirely.
	if localDoc.Kind == 0 || len(localDoc.Content) == 0 {
		if len(local) == 0 {
			return merged, nil
		}
		if len(merged) == 0 {
			return append([]byte(nil), local...), nil
		}
		out := append([]byte(nil), local...)
		if out[len(out)-1] != '\n' {
			out = append(out, '\n')
		}
		out = append(out, merged...)
		return out, nil
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
		// file with only the filtered scope.
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
//
// Boundary attribution: blank lines and unindented `#` comment lines
// immediately preceding the next top-level key are attributed to that
// next key, not to the previous one. This matters for the text-splice
// path: if a comment block sits between an in-scope key and an
// out-of-scope key, that comment belongs (semantically) to the
// out-of-scope key and must not be replaced when the in-scope key's
// range is rewritten..
func topLevelKeyRanges(source []byte, root *yaml.Node) []keyByteRange {
	lineOffsets := computeLineOffsets(source)
	total := len(root.Content)
	var ranges []keyByteRange
	for i := 0; i < total-1; i += 2 {
		keyNode := root.Content[i]
		startByte := lineStart(lineOffsets, keyNode.Line, len(source))

		// End of this entry = start of the next top-level key, or EOF.
		// For non-final entries, walk backwards from the next key's
		// line to skip blank/comment lines and attribute them to the
		// next key instead.
		var endByte int
		if i+2 < total-1 {
			nextKey := root.Content[i+2]
			endByte = lineStart(lineOffsets, nextKey.Line, len(source))
			endByte = trimTrailingBlankAndCommentLines(source, startByte, endByte)
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

// lineStart returns the byte offset at which the given 1-based line
// begins. `srcLen` must be the length of the source used to compute
// `offsets`. If `line` is <= 0, it returns 0. If `line` is past the
// last recorded line, it clamps to `srcLen` (true EOF), not the start
// of the last line.
func lineStart(offsets []int, line int, srcLen int) int {
	if line <= 0 {
		return 0
	}
	if line-1 >= len(offsets) {
		return srcLen
	}
	return offsets[line-1]
}

// trimTrailingBlankAndCommentLines walks backwards from `end` toward
// `floor` and returns the new end such that any trailing blank or
// unindented `#` comment lines (i.e. lines that "belong" to the
// following block) are excluded from the previous key's range. The
// floor is the start of the current key's range so we never trim
// before that..
func trimTrailingBlankAndCommentLines(source []byte, floor, end int) int {
	if end <= floor {
		return end
	}
	cur := end
	for cur > floor {
		// Find the start of the previous line.
		lineEnd := cur - 1
		// `lineEnd` should now be a `\n`. If `cur` is right after
		// a key line (no trailing newline) handle that too.
		if lineEnd >= 0 && source[lineEnd] != '\n' {
			// Already at a non-newline byte; nothing to trim.
			return cur
		}
		// Step back over the newline to find the previous line's end.
		prevEnd := lineEnd
		// Find prev line start.
		prevStart := prevEnd
		for prevStart > floor && source[prevStart-1] != '\n' {
			prevStart--
		}
		// Inspect the previous line: source[prevStart:prevEnd]
		line := source[prevStart:prevEnd]
		if !isBlankOrUnindentedComment(line) {
			return cur
		}
		// This blank/comment line belongs to the next key. Move
		// `cur` back so it's excluded from the previous key's range.
		cur = prevStart
	}
	return cur
}

// isBlankOrUnindentedComment reports whether a line (without its
// trailing newline) is blank or an unindented `#`-comment. Indented
// comments are NOT considered, because indented content is part of
// the previous key's value.
func isBlankOrUnindentedComment(line []byte) bool {
	if len(line) == 0 {
		return true
	}
	// All-whitespace line.
	allWS := true
	for _, b := range line {
		if b != ' ' && b != '\t' && b != '\r' {
			allWS = false
			break
		}
	}
	if allWS {
		return true
	}
	// Unindented comment.
	if line[0] == '#' {
		return true
	}
	return false
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
// Per-key splicing matters when scoped keys are non-contiguous in
// the local file: a coarser "insert once at the first scoped range,
// suppress subsequent ranges" strategy would reorder content because
// merged-side keys would all collapse to the first scoped position.
func spliceYAMLText(local, merged []byte, scope map[string]bool) ([]byte, error) {
	var localDoc yaml.Node
	if err := yaml.Unmarshal(local, &localDoc); err != nil {
		// Can't parse local either. Returning `merged` would silently
		// overwrite the local file with just the filtered scope (the
		// #126 data-loss bug). Return an error so the caller surfaces
		// it instead.
		//
		// Common cause: the local file already has unresolved
		// `git merge-file` conflict markers from a previous sync.
		// Detect that case and tell the user to resolve them first
		// rather than just yelling about a YAML parse error.
		if containsConflictMarkers(local) {
			return nil, fmt.Errorf("text splice: local YAML has unresolved conflict markers from a previous sync — resolve them and re-run (yaml parse error: %w)", err)
		}
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
		// No replaceable top-level key ranges (e.g. local was an
		// empty `{}` mapping). Preserve original bytes and append
		// merged so any leading comments stay with the file.
		if len(local) == 0 {
			return merged, nil
		}
		if len(merged) == 0 {
			return append([]byte(nil), local...), nil
		}
		out := append([]byte(nil), local...)
		if out[len(out)-1] != '\n' {
			out = append(out, '\n')
		}
		out = append(out, merged...)
		return out, nil
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
	// semantics..
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
//     special characters. These are rare in YAML files used as b
//     config, but misclassification can cause text-splice omissions:
//     a scoped key whose name the scanner fails to detect is treated
//     as absent from `merged` and gets removed from local (deletion
//     path), and merged keys the scanner fails to detect are simply
//     not emitted as additions. There is no fallback that relocates
//     such content..
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
		// silently dropped during splicing.
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
