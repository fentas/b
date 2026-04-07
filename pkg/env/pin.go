package env

import (
	"bytes"
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
//   - pending is unparseable (e.g. has conflict markers) — handled
//     on a future clean sync
//
// Pinned paths that exist in `local` but NOT in `pending` are
// reinserted via addPath, so a key the consumer pinned that
// upstream then deleted is preserved.
//
// Pin scope is per-map-node: if `kubectl` is pinned, the entire
// `kubectl:` map is replaced with the local version. Deeper pins
// only apply when the path is itself a nested map node that can
// carry the `b.pin: true` annotation; scalar fields like a typical
// `kubectl.version` value cannot be pinned directly because there
// is nowhere to attach the annotation. Pinned paths that don't
// exist in pending (because upstream deleted them) are reinserted
// from local.
//
// Formatting caveat: when pin restoration actually substitutes a
// subtree, the file is round-tripped through the yaml.v3 encoder,
// so comments and whitespace on the affected file are NOT
// preserved (yaml.v3 has no format-preserving emitter for this
// kind of edit-in-place). When every pinned subtree already
// matches what the splice produced, applyPinsYAML returns the
// splice's bytes verbatim — so the common no-drift case keeps
// splice's byte-preservation guarantees.
func applyPinsYAML(local, pending []byte, filePath string) ([]byte, error) {
	ext := strings.ToLower(filepath.Ext(filePath))
	if ext != ".yaml" && ext != ".yml" {
		return pending, nil
	}
	if len(local) == 0 {
		// No local file → nothing pinned to honor.
		return pending, nil
	}
	// Cheap pre-check: a file with no `b.pin` substring anywhere
	// can't possibly carry a pin annotation, and we'd spend the
	// yaml.Unmarshal cost on every sync of every YAML file just to
	// learn that. Substring matches can produce false positives
	// (e.g. a key literally named "b.pin" elsewhere), but those
	// just trigger the slow path — the structural collectPinnedPaths
	// is the source of truth.
	if !bytes.Contains(local, []byte(PinAnnotation)) {
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

	changed := false
	for _, p := range pinned {
		localNode := lookupPath(&localDoc, p.path)
		if localNode == nil {
			continue
		}
		// Skip when pending already has the exact same subtree at
		// this path. This is the common case for syncs where the
		// pinned keys haven't drifted yet, and skipping the
		// substitution lets the function fall through to the
		// "return pending unchanged" branch — which preserves
		// every byte that the splice carefully laid out, instead
		// of round-tripping the whole document through yaml.v3
		// and dropping comments / whitespace.
		if pendingNode := lookupPath(&pendingDoc, p.path); pendingNode != nil &&
			yamlNodesStructurallyEqual(pendingNode, localNode) {
			continue
		}
		if !setPath(&pendingDoc, p.path, localNode) {
			// Path didn't exist in pending — pinned key was deleted
			// upstream and consumer wants to keep it. Add it back.
			addPath(&pendingDoc, p.path, localNode)
		}
		changed = true
	}
	if !changed {
		// No pinned subtree needed substitution: every pin already
		// matches what the splice produced. Return the splice's
		// bytes verbatim instead of round-tripping through the
		// yaml.v3 encoder, which would otherwise reformat the
		// whole file.
		return pending, nil
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

// yamlNodesStructurallyEqual reports whether two yaml.Node trees
// encode to the same canonical bytes. We use the encoder rather than
// comparing fields directly so style/comment differences (which the
// pin restoration explicitly doesn't care about) don't trip the
// equality check.
func yamlNodesStructurallyEqual(a, b *yaml.Node) bool {
	enc := func(n *yaml.Node) []byte {
		var buf strings.Builder
		e := yaml.NewEncoder(&yamlStringWriter{&buf})
		e.SetIndent(2)
		if err := e.Encode(n); err != nil {
			return nil
		}
		// Close can flush buffered errors. If it fails the
		// encoded bytes may be incomplete, in which case the
		// equality check would compare a partial encoding and
		// produce a wrong answer (either skipping a needed
		// substitution or doing an unnecessary one). Return
		// nil so the outer comparison falls through to the
		// "not equal" branch and the substitution path runs
		// — that's the safer side.
		if err := e.Close(); err != nil {
			return nil
		}
		return []byte(buf.String())
	}
	ab, bb := enc(a), enc(b)
	return ab != nil && bb != nil && bytes.Equal(ab, bb)
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
