package env

import (
	"fmt"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// PinAnnotation is the local-only field consumers add to a YAML map to
// opt out of upstream syncing for that specific key. It is namespaced
// under `b.` to avoid colliding with upstream schemas, and uses a dot
// rather than a colon for compatibility with YAML 1.2 plain keys.
//
//	binaries:
//	  kubectl:
//	    version: v1.30.0
//	    b.pin: true   # ignore upstream for this entry
//
// The annotation survives syncs because it lives in the consumer's
// file and upstream never specifies it. The pin restoration walks the
// to-be-written content after merge/splice and substitutes the local
// values back in for any path whose local map carries the annotation.
const PinAnnotation = "b.pin"

// applyPinsYAML restores pinned keys from `local` into `pending` for
// YAML files. A "pinned key" is a map node in `local` that has
// `b.pin: true` set; that map (including the annotation itself) wins
// over whatever upstream/merge produced for the same path. The path is
// the sequence of map keys from the document root to the pinned node.
//
// The function is a no-op when:
//   - the file is not YAML (caller filters by extension)
//   - local has no pinned keys
//   - pending parses cleanly but the pinned path isn't present
//
// Pin scope is per-map-node: if `kubectl` is pinned, the entire
// `kubectl:` map is preserved verbatim. Deeper pins only apply when
// the path is itself a nested map node that can carry the
// `b.pin: true` annotation; scalar fields like a typical
// `kubectl.version` value cannot be pinned directly because there is
// nowhere to attach the annotation. The implementation walks the
// local tree once to collect annotated map paths and then walks
// pending to substitute. Pinned paths that don't exist in pending
// (because upstream deleted them) are reinserted from local.
func applyPinsYAML(local, pending []byte, filePath string) ([]byte, error) {
	ext := strings.ToLower(filepath.Ext(filePath))
	if ext != ".yaml" && ext != ".yml" {
		return pending, nil
	}
	if len(local) == 0 {
		// No local file → nothing pinned to honor.
		return pending, nil
	}

	var localDoc yaml.Node
	if err := yaml.Unmarshal(local, &localDoc); err != nil {
		// Local is unparseable — leave pending alone. The merge path
		// already errored out for unparseable files; this is just
		// defensive.
		return pending, nil
	}
	pinned := collectPinnedPaths(&localDoc, nil)
	if len(pinned) == 0 {
		return pending, nil
	}

	// Pending may be empty (a brand-new file) or header-only (no
	// document content). Synthesize a minimal mapping document so
	// addPath has somewhere to attach pinned keys. The yaml.v3
	// encoder will emit it cleanly.
	var pendingDoc yaml.Node
	if len(pending) == 0 {
		pendingDoc = yaml.Node{
			Kind:    yaml.DocumentNode,
			Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}},
		}
	} else {
		if err := yaml.Unmarshal(pending, &pendingDoc); err != nil {
			// Pending has conflict markers or is otherwise unparseable.
			// We can't structurally substitute, so return as-is and let
			// the conflict-resolution path handle it. The user's pin
			// will take effect on the next clean sync.
			return pending, nil
		}
		// Header-only or empty document: yaml.v3 may leave the
		// document as the zero value (Kind == 0) rather than a
		// proper DocumentNode. Coerce both shapes into a synthetic
		// mapping document so setPath / addPath have somewhere to
		// walk into.
		if pendingDoc.Kind == 0 || (pendingDoc.Kind == yaml.DocumentNode && len(pendingDoc.Content) == 0) {
			pendingDoc = yaml.Node{
				Kind:    yaml.DocumentNode,
				Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}},
			}
		}
		if pendingDoc.Kind != yaml.DocumentNode {
			return pending, nil
		}
		if pendingDoc.Content[0].Kind != yaml.MappingNode {
			// Pending root is not a mapping (sequence, scalar) —
			// pinning doesn't apply, leave it alone.
			return pending, nil
		}
	}

	for _, p := range pinned {
		localNode := lookupPath(&localDoc, p.path)
		if localNode == nil {
			continue
		}
		if !setPath(&pendingDoc, p.path, localNode) {
			// Path didn't exist in pending — pinned key was deleted
			// upstream and consumer wants to keep it. Add it back.
			addPath(&pendingDoc, p.path, localNode)
		}
	}

	var buf strings.Builder
	enc := yaml.NewEncoder(&yamlStringWriter{&buf})
	enc.SetIndent(2)
	if err := enc.Encode(&pendingDoc); err != nil {
		return nil, fmt.Errorf("re-emit pinned doc: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("finalize pinned doc: %w", err)
	}
	return []byte(buf.String()), nil
}

type yamlStringWriter struct{ b *strings.Builder }

func (w *yamlStringWriter) Write(p []byte) (int, error) {
	w.b.Write(p)
	return len(p), nil
}

// pinnedPath records one annotated key in the local document.
type pinnedPath struct {
	path []string
}

// collectPinnedPaths walks a yaml.Node tree and returns the dotted
// path of every map that contains a `b.pin: true` annotation. The
// returned path is the sequence of map keys leading TO the pinned
// map (not including b.pin itself).
func collectPinnedPaths(n *yaml.Node, prefix []string) []pinnedPath {
	if n == nil {
		return nil
	}
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		return collectPinnedPaths(n.Content[0], prefix)
	}
	if n.Kind != yaml.MappingNode {
		return nil
	}
	var out []pinnedPath
	// First pass: is THIS map pinned?
	for i := 0; i+1 < len(n.Content); i += 2 {
		k := n.Content[i]
		v := n.Content[i+1]
		if k.Value == PinAnnotation && isTrueScalar(v) && len(prefix) > 0 {
			cp := make([]string, len(prefix))
			copy(cp, prefix)
			out = append(out, pinnedPath{path: cp})
			break
		}
	}
	// Recurse into children regardless, so nested pins are also found.
	for i := 0; i+1 < len(n.Content); i += 2 {
		k := n.Content[i]
		v := n.Content[i+1]
		if v.Kind == yaml.MappingNode {
			out = append(out, collectPinnedPaths(v, append(prefix, k.Value))...)
		}
	}
	return out
}

func isTrueScalar(n *yaml.Node) bool {
	if n == nil || n.Kind != yaml.ScalarNode {
		return false
	}
	v := strings.ToLower(strings.TrimSpace(n.Value))
	return v == "true" || v == "yes" || v == "on"
}

// lookupPath descends a yaml document tree along the given key path
// and returns the value node, or nil if any segment is missing.
func lookupPath(doc *yaml.Node, path []string) *yaml.Node {
	n := doc
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		n = n.Content[0]
	}
	for _, seg := range path {
		if n == nil || n.Kind != yaml.MappingNode {
			return nil
		}
		var found *yaml.Node
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == seg {
				found = n.Content[i+1]
				break
			}
		}
		if found == nil {
			return nil
		}
		n = found
	}
	return n
}

// setPath replaces the value at the given path with `replacement` in
// the document tree. Returns true on success, false if the path didn't
// exist (caller can fall through to addPath).
func setPath(doc *yaml.Node, path []string, replacement *yaml.Node) bool {
	if len(path) == 0 {
		return false
	}
	n := doc
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		n = n.Content[0]
	}
	for i, seg := range path {
		if n == nil || n.Kind != yaml.MappingNode {
			return false
		}
		for j := 0; j+1 < len(n.Content); j += 2 {
			if n.Content[j].Value == seg {
				if i == len(path)-1 {
					n.Content[j+1] = replacement
					return true
				}
				n = n.Content[j+1]
				goto next
			}
		}
		return false
	next:
	}
	return false
}

// addPath inserts a value at the given path, creating intermediate
// maps as needed. Used when the pinned key was deleted upstream and
// the consumer's annotation says "keep it anyway".
func addPath(doc *yaml.Node, path []string, value *yaml.Node) {
	if len(path) == 0 {
		return
	}
	n := doc
	if n.Kind == yaml.DocumentNode {
		if len(n.Content) == 0 {
			n.Content = append(n.Content, &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"})
		}
		n = n.Content[0]
	}
	for i, seg := range path {
		if n.Kind != yaml.MappingNode {
			return
		}
		var found *yaml.Node
		for j := 0; j+1 < len(n.Content); j += 2 {
			if n.Content[j].Value == seg {
				found = n.Content[j+1]
				break
			}
		}
		if i == len(path)-1 {
			if found != nil {
				// already exists; should have been handled by setPath,
				// but be defensive and replace.
				for j := 0; j+1 < len(n.Content); j += 2 {
					if n.Content[j].Value == seg {
						n.Content[j+1] = value
						return
					}
				}
			}
			n.Content = append(n.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: seg},
				value)
			return
		}
		if found == nil {
			child := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			n.Content = append(n.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: seg},
				child)
			n = child
			continue
		}
		n = found
	}
}
