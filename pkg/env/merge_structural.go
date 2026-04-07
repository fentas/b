package env

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Merge3WayStructural performs a structural three-way merge for YAML/JSON
// files by parsing all three sides into generic Go values and walking the
// trees key-by-key.
//
// Compared to the text-based `git merge-file` path, this eliminates the
// "spurious conflict" failure mode where local and upstream both add
// adjacent entries inside the same map: a textual diff sees two
// overlapping insertions on neighbouring lines and marks the whole hunk
// conflicted, even though the changes are semantically independent.
//
// The function returns:
//   - merged bytes, re-serialised in the format chosen by destPath's
//     extension (.json or .yaml). Note this is NOT byte-for-byte
//     identical to local: the structural merge round-trips through
//     yaml.v3 / encoding/json so key order, comments, and whitespace
//     in `local` are NOT preserved. The wider doMerge wiring runs the
//     text 3-way merge first and only falls back to the structural
//     path on conflicts, so a clean merge keeps local's bytes intact.
//   - hasConflict: true when any leaf-level conflict could not be auto-resolved
//   - error: only set when parsing/serialisation fails (i.e. the caller
//     should fall back to the text path)
//
// destPath is used purely to pick the output format extension; the
// helper does not preserve local's original formatting.
//
// Conflict resolution rules (per key):
//   - base==local==upstream → keep
//   - local==base, upstream changed → take upstream
//   - upstream==base, local changed → keep local
//   - both changed identically → keep (no conflict)
//   - both changed differently AND both are maps → recurse
//   - both changed differently otherwise → conflict (record key path)
//
// Deletes:
//   - present in base, missing on one side, unchanged on the other → delete
//   - present in base, missing on one side, changed on the other → conflict
func Merge3WayStructural(local, base, upstream []byte, destPath string) ([]byte, bool, error) {
	format := detectStructuralFormat(destPath)
	if format == "" {
		return nil, false, fmt.Errorf("structural merge: unsupported format")
	}

	localVal, err := parseStructural(local, format)
	if err != nil {
		return nil, false, fmt.Errorf("parse local: %w", err)
	}
	baseVal, err := parseStructural(base, format)
	if err != nil {
		return nil, false, fmt.Errorf("parse base: %w", err)
	}
	upstreamVal, err := parseStructural(upstream, format)
	if err != nil {
		return nil, false, fmt.Errorf("parse upstream: %w", err)
	}

	// Track whether ANY input was a real (non-empty) document. If at
	// least one side had bytes, the merged result must serialize as
	// a real document — even if it ends up empty — so that an
	// explicit `{}` upstream isn't silently dropped to empty bytes.
	// When every side started empty, the result stays empty.
	anyContent := !isEmptyBytes(local) || !isEmptyBytes(base) || !isEmptyBytes(upstream)

	merged, conflicts := mergeValues(baseVal, localVal, upstreamVal, nil)
	if len(conflicts) > 0 {
		// Re-serialise the partial merge so the caller can fall back to
		// the text path with confidence; the boolean signals conflict.
		out, err := serializeStructural(merged, format, anyContent)
		if err != nil {
			return nil, true, err
		}
		return out, true, nil
	}

	out, err := serializeStructural(merged, format, anyContent)
	if err != nil {
		return nil, false, err
	}
	return out, false, nil
}

func isEmptyBytes(b []byte) bool {
	return len(bytes.TrimSpace(b)) == 0
}

// detectStructuralFormat returns "json", "yaml", or "" based purely
// on the destination filename extension. The extension is
// authoritative — sniffing the bytes would risk routing a YAML file
// that happens to start with `{` (a flow-style mapping) through the
// JSON path and producing nonsense output.
func detectStructuralFormat(destPath string) string {
	switch strings.ToLower(filepath.Ext(destPath)) {
	case ".json":
		return "json"
	case ".yaml", ".yml":
		return "yaml"
	}
	return ""
}

// parseStructural decodes bytes into a generic value. Empty input maps to
// an empty map so the recursive merge has something to walk.
func parseStructural(data []byte, format string) (any, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return map[string]any{}, nil
	}
	switch format {
	case "json":
		var v any
		if err := json.Unmarshal(data, &v); err != nil {
			return nil, err
		}
		return normalizeValue(v), nil
	case "yaml":
		var v any
		if err := yaml.Unmarshal(data, &v); err != nil {
			return nil, err
		}
		return normalizeValue(v), nil
	}
	return nil, fmt.Errorf("unknown format %q", format)
}

// normalizeValue converts yaml.v3's map[interface{}]interface{} (which it
// no longer emits in v3, but be defensive) and ensures all maps are
// map[string]any so equality comparisons via reflect.DeepEqual are stable
// regardless of source format.
func normalizeValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, vv := range x {
			out[k] = normalizeValue(vv)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(x))
		for k, vv := range x {
			out[fmt.Sprint(k)] = normalizeValue(vv)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, vv := range x {
			out[i] = normalizeValue(vv)
		}
		return out
	}
	return v
}

// serializeStructural serializes v in the given format. When
// anyContent is true the function always emits a real document even
// for an empty map (so a real `{}` doesn't get silently dropped to
// empty bytes). When anyContent is false an empty map maps back to
// empty bytes — the "every side started empty → empty output"
// shortcut.
func serializeStructural(v any, format string, anyContent bool) ([]byte, error) {
	if !anyContent {
		if m, ok := v.(map[string]any); ok && len(m) == 0 {
			return nil, nil
		}
	}
	switch format {
	case "json":
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetIndent("", "  ")
		if err := enc.Encode(v); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	case "yaml":
		var buf bytes.Buffer
		enc := yaml.NewEncoder(&buf)
		enc.SetIndent(2)
		if err := enc.Encode(v); err != nil {
			return nil, err
		}
		if err := enc.Close(); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}
	return nil, fmt.Errorf("unknown format %q", format)
}

// mergeValues recursively merges three values. path is the JSON-pointer-ish
// breadcrumb used in conflict messages.
func mergeValues(base, local, upstream any, path []string) (any, []string) {
	// Trivial: all equal.
	if reflect.DeepEqual(local, upstream) {
		return local, nil
	}
	// One side unchanged from base → take the other.
	if reflect.DeepEqual(base, local) {
		return upstream, nil
	}
	if reflect.DeepEqual(base, upstream) {
		return local, nil
	}
	// Both changed differently. We recurse into maps in two cases:
	//   - all three sides are maps (the normal nested case)
	//   - base is absent (nil) and both local and upstream added a
	//     map at this path. This is a "both added concurrently"
	//     case, not a type change, so we can safely recurse with a
	//     synthetic empty base map and let mergeMaps reconcile the
	//     keys.
	// A non-nil non-map base + map local + map upstream is a real
	// type change (scalar → map on both sides) and stays a leaf
	// conflict the user has to resolve.
	bm, baseIsMap := base.(map[string]any)
	lm, localIsMap := local.(map[string]any)
	um, upstreamIsMap := upstream.(map[string]any)
	if localIsMap && upstreamIsMap {
		switch {
		case baseIsMap:
			return mergeMaps(bm, lm, um, path)
		case base == nil:
			return mergeMaps(map[string]any{}, lm, um, path)
		}
	}
	// Otherwise: leaf conflict.
	return local, []string{strings.Join(path, ".")}
}

// mergeMaps walks the union of keys across base/local/upstream and merges
// per-key. Conflicts bubble up with their dotted path so the caller can
// surface a useful error.
//
// Output ordering: keys are sorted alphabetically. The result is a
// Go map[string]any, so any ordering chosen here is squashed when
// the JSON / YAML encoder re-emits the document — encoding/json
// sorts alphabetically and yaml.v3 does too. Sorting here makes the
// conflict-list ordering deterministic for tests; it does not, by
// itself, change what the consumer sees on disk. The wider key-
// reordering caveat is documented on doMerge: callers fall back to
// the structural merge only when the text merge has already
// produced conflicts, so the trade-off is "messy diff" versus
// "unresolved conflict markers", and the messy diff wins.
func mergeMaps(base, local, upstream map[string]any, path []string) (map[string]any, []string) {
	keys := make(map[string]struct{}, len(base)+len(local)+len(upstream))
	for k := range base {
		keys[k] = struct{}{}
	}
	for k := range local {
		keys[k] = struct{}{}
	}
	for k := range upstream {
		keys[k] = struct{}{}
	}
	// Sorted for deterministic output and conflict ordering.
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)

	out := make(map[string]any, len(sorted))
	var conflicts []string
	for _, k := range sorted {
		bv, bok := base[k]
		lv, lok := local[k]
		uv, uok := upstream[k]

		switch {
		case lok && uok:
			merged, sub := mergeValues(bv, lv, uv, append(path, k))
			out[k] = merged
			conflicts = append(conflicts, sub...)
		case !lok && !uok:
			// Both deleted (or never existed) → drop.
		case lok && !uok:
			// Only on local side. If the key didn't exist in base
			// either, it's a clean add by local. Otherwise upstream
			// deleted it: accept the delete iff local is unchanged
			// from base, else delete/modify conflict.
			if !bok {
				out[k] = lv
			} else if reflect.DeepEqual(bv, lv) {
				// drop: clean delete from upstream
			} else {
				out[k] = lv
				conflicts = append(conflicts, strings.Join(append(path, k), "."))
			}
		case !lok && uok:
			// Symmetric: clean add by upstream, or local deleted.
			if !bok {
				out[k] = uv
			} else if reflect.DeepEqual(bv, uv) {
				// drop: clean delete from local
			} else {
				out[k] = uv
				conflicts = append(conflicts, strings.Join(append(path, k), "."))
			}
		}
	}
	return out, conflicts
}
