// Package envmatch matches glob patterns against a flat file tree
// (from git ls-tree) and computes destination paths.
package envmatch

import (
	"path/filepath"
	"sort"
	"strings"
)

// GlobConfig holds per-glob configuration from b.yaml files map values.
type GlobConfig struct {
	Dest   string   // destination prefix (replaces glob prefix)
	Ignore []string // per-glob ignore patterns (additive to global)
}

// MatchedFile is a single file matched by a glob, with its computed destination.
type MatchedFile struct {
	SourcePath string // path in the upstream repo
	DestPath   string // local destination path
	GlobKey    string // which glob matched this file
}

// MatchGlobs matches globs against a file tree and returns matched files
// with computed destination paths.
//
// globs: map of glob pattern → config (from b.yaml files section)
// globalIgnore: patterns to exclude from all globs
// tree: list of all file paths in the repo (from git ls-tree)
func MatchGlobs(tree []string, globs map[string]GlobConfig, globalIgnore []string) []MatchedFile {
	seen := make(map[string]bool)
	var result []MatchedFile

	// Sort glob keys for deterministic output
	keys := make([]string, 0, len(globs))
	for k := range globs {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, glob := range keys {
		cfg := globs[glob]
		prefix := globPrefix(glob)

		for _, path := range tree {
			if seen[path] {
				continue
			}
			if !matchGlob(glob, path) {
				continue
			}
			if isIgnored(path, globalIgnore, cfg.Ignore) {
				continue
			}

			dest := computeDest(path, prefix, cfg.Dest)
			seen[path] = true
			result = append(result, MatchedFile{
				SourcePath: path,
				DestPath:   dest,
				GlobKey:    glob,
			})
		}
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].DestPath < result[j].DestPath
	})
	return result
}

// globPrefix returns the non-wildcard prefix of a glob pattern.
// e.g. "manifests/hetzner/**" → "manifests/hetzner/"
// e.g. "configs/ingress.yaml" → "configs/ingress.yaml" (literal)
// e.g. "**/*.yaml" → ""
func globPrefix(glob string) string {
	// Find first wildcard character
	for i, c := range glob {
		if c == '*' || c == '?' || c == '[' {
			// Return everything up to and including the last /
			prefix := glob[:i]
			if j := strings.LastIndex(prefix, "/"); j != -1 {
				return prefix[:j+1]
			}
			return ""
		}
	}
	// No wildcards — literal path
	return glob
}

// matchGlob matches a path against a glob pattern, supporting ** for recursive.
func matchGlob(pattern, path string) bool {
	// Handle ** (recursive match)
	if strings.Contains(pattern, "**") {
		return matchDoublestar(pattern, path)
	}
	ok, _ := filepath.Match(pattern, path)
	return ok
}

// matchDoublestar handles ** glob patterns.
func matchDoublestar(pattern, path string) bool {
	// Split pattern on **
	parts := strings.SplitN(pattern, "**", 2)
	prefix := parts[0]
	suffix := ""
	if len(parts) > 1 {
		suffix = parts[1]
	}

	// Path must start with the prefix
	if prefix != "" && !strings.HasPrefix(path, prefix) {
		return false
	}

	// If no suffix (or just /), match everything under prefix
	if suffix == "" || suffix == "/" {
		return true
	}

	// Remove leading / from suffix for matching
	suffix = strings.TrimPrefix(suffix, "/")

	// The remaining path after prefix must match the suffix pattern
	rest := strings.TrimPrefix(path, prefix)

	// Try matching suffix against each possible "tail" of rest
	// e.g. for rest = "a/b/c.yaml" and suffix = "*.yaml"
	// try: "a/b/c.yaml", "b/c.yaml", "c.yaml"
	parts2 := strings.Split(rest, "/")
	for i := range parts2 {
		tail := strings.Join(parts2[i:], "/")
		ok, _ := filepath.Match(suffix, tail)
		if ok {
			return true
		}
	}
	return false
}

// computeDest computes the destination path for a matched file.
//
// For globs with a dest: strip the glob prefix, prepend dest.
//   "manifests/hetzner/deploy.yaml" with prefix "manifests/hetzner/" dest "/hetzner"
//   → "/hetzner/deploy.yaml"
//
// For globs without dest (bare key): keep original path.
//   "manifests/base/deploy.yaml" → "manifests/base/deploy.yaml"
//
// For literal paths: dest is a prefix directory.
//   "configs/ingress.yaml" with dest "/config" → "/config/ingress.yaml"
func computeDest(sourcePath, prefix, dest string) string {
	if dest == "" {
		// No dest specified — preserve original path
		return sourcePath
	}

	// Strip the glob prefix from source path
	relative := strings.TrimPrefix(sourcePath, prefix)

	// For literal paths (prefix == full source), relative is empty
	// Use the filename
	if relative == "" {
		relative = filepath.Base(sourcePath)
	}

	// Clean up dest and combine
	dest = strings.TrimSuffix(dest, "/")
	return dest + "/" + relative
}

// isIgnored checks if a path matches any ignore pattern.
func isIgnored(path string, globalIgnore, localIgnore []string) bool {
	for _, pattern := range globalIgnore {
		if matchIgnore(pattern, path) {
			return true
		}
	}
	for _, pattern := range localIgnore {
		if matchIgnore(pattern, path) {
			return true
		}
	}
	return false
}

// matchIgnore matches a path against an ignore pattern.
// Supports ** for recursive, and matches against the full path and the basename.
func matchIgnore(pattern, path string) bool {
	if strings.Contains(pattern, "**") {
		return matchDoublestar(pattern, path)
	}
	// Try matching against full path
	if ok, _ := filepath.Match(pattern, path); ok {
		return true
	}
	// Try matching against just the filename
	if ok, _ := filepath.Match(pattern, filepath.Base(path)); ok {
		return true
	}
	return false
}
