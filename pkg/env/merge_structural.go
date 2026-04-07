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
//   - merged bytes (re-serialised in the same format as `local`)
//   - hasConflict: true when any leaf-level conflict could not be auto-resolved
//   - error: only set when parsing/serialisation fails (i.e. the caller
//     should fall back to the text path)
//
// destPath is used purely to pick the output format (.json vs .yaml).
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
	format := detectStructuralFormat(destPath, local)
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

	merged, conflicts := mergeValues(baseVal, localVal, upstreamVal, nil)
	if len(conflicts) > 0 {
		// Re-serialise the partial merge so the caller can fall back to
		// the text path with confidence; the boolean signals conflict.
		out, err := serializeStructural(merged, format)
		if err != nil {
			return nil, true, err
		}
		return out, true, nil
	}

	out, err := serializeStructural(merged, format)
	if err != nil {
		return nil, false, err
	}
	return out, false, nil
}

// detectStructuralFormat returns "json", "yaml", or "" based on the
// destination filename. JSON is detected by extension only; anything
// matching .yaml/.yml is YAML; bare/unknown extensions return "".
func detectStructuralFormat(destPath string, sample []byte) string {
	ext := strings.ToLower(filepath.Ext(destPath))
	switch ext {
	case ".json":
		return "json"
	case ".yaml", ".yml":
		return "yaml"
	}
	// Fallback: try to sniff JSON.
	trim := bytes.TrimSpace(sample)
	if len(trim) > 0 && (trim[0] == '{' || trim[0] == '[') {
		return "json"
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

func serializeStructural(v any, format string) ([]byte, error) {
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
		_ = enc.Close()
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
	// Both changed differently. If both are maps, recurse key-wise.
	bm, baseIsMap := base.(map[string]any)
	lm, localIsMap := local.(map[string]any)
	um, upstreamIsMap := upstream.(map[string]any)
	if localIsMap && upstreamIsMap {
		if !baseIsMap {
			bm = map[string]any{}
		}
		return mergeMaps(bm, lm, um, path)
	}
	// Otherwise: leaf conflict.
	return local, []string{strings.Join(path, ".")}
}

// mergeMaps walks the union of keys across base/local/upstream and merges
// per-key. Conflicts bubble up with their dotted path so the caller can
// surface a useful error.
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
