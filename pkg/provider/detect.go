package provider

import (
	"fmt"
	"runtime"
	"strings"
)

// OS/arch aliases used to match release asset filenames.
var (
	osAliases = map[string][]string{
		"linux":   {"linux", "Linux", "linux-gnu"},
		"darwin":  {"darwin", "Darwin", "macOS", "macos", "osx", "OSX", "apple"},
		"windows": {"windows", "Windows", "win", "win64", "win32"},
	}
	archAliases = map[string][]string{
		"amd64": {"amd64", "x86_64", "x64", "64bit", "64-bit"},
		"arm64": {"arm64", "aarch64", "armv8"},
		"386":   {"386", "i386", "i686", "x86", "32bit", "32-bit"},
		"arm":   {"armv7", "armv6", "arm"},
	}
	// File extensions to filter out (not actual binaries).
	ignoreExtensions = []string{
		".sha256", ".sha256sum", ".sha512", ".sha512sum",
		".sig", ".asc", ".pem",
		".txt", ".md", ".json",
		".sbom", ".spdx",
		".deb", ".rpm", ".msi", ".pkg", ".apk",
	}
	// Archive extensions we can handle.
	archiveExtensions = []string{
		".tar.gz", ".tgz",
		".tar.xz", ".txz",
		".tar.bz2",
		".zip",
	}
)

// MatchAsset scores and selects the best release asset for the current
// OS/arch. Returns an error if no suitable asset is found.
func MatchAsset(assets []Asset, repoName string) (*Asset, error) {
	goos := runtime.GOOS
	goarch := runtime.GOARCH

	osNames := osAliases[goos]
	archNames := archAliases[goarch]

	if osNames == nil {
		osNames = []string{goos}
	}
	if archNames == nil {
		archNames = []string{goarch}
	}

	type scored struct {
		asset *Asset
		score int
	}

	var candidates []scored

	for i := range assets {
		a := &assets[i]
		name := a.Name
		lower := strings.ToLower(name)

		// Skip known non-binary extensions
		if shouldIgnore(lower) {
			continue
		}

		// Must match OS
		osMatch := false
		for _, alias := range osNames {
			if containsWord(lower, strings.ToLower(alias)) {
				osMatch = true
				break
			}
		}
		if !osMatch {
			continue
		}

		// Must match arch
		archMatch := false
		for _, alias := range archNames {
			if containsWord(lower, strings.ToLower(alias)) {
				archMatch = true
				break
			}
		}
		if !archMatch {
			continue
		}

		// Score: higher is better
		score := 10 // base: matched OS + arch

		// Prefer archives (more likely to contain the right binary)
		if isArchive(lower) {
			score += 5
		}

		// Prefer asset name containing repo name
		if repoName != "" && containsWord(lower, strings.ToLower(repoName)) {
			score += 3
		}

		// Prefer tar.gz over zip (more common in Go/Rust ecosystem)
		if strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz") {
			score += 1
		}

		candidates = append(candidates, scored{asset: a, score: score})
	}

	if len(candidates) == 0 {
		return nil, fmt.Errorf("no matching asset for %s/%s among %d assets", goos, goarch, len(assets))
	}

	// Pick highest score
	best := candidates[0]
	for _, c := range candidates[1:] {
		if c.score > best.score {
			best = c
		}
	}

	return best.asset, nil
}

// DetectArchiveType returns the archive type based on filename.
// Returns empty string if the file is not a recognized archive.
func DetectArchiveType(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return "tar.gz"
	case strings.HasSuffix(lower, ".tar.xz"), strings.HasSuffix(lower, ".txz"):
		return "tar.xz"
	case strings.HasSuffix(lower, ".tar.bz2"):
		return "tar.bz2"
	case strings.HasSuffix(lower, ".zip"):
		return "zip"
	}
	return ""
}

// shouldIgnore returns true if the filename should be skipped.
func shouldIgnore(lower string) bool {
	for _, ext := range ignoreExtensions {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

// isArchive returns true if the filename looks like an archive.
func isArchive(lower string) bool {
	for _, ext := range archiveExtensions {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

// containsWord checks if the name contains the word, bounded by
// non-alphanumeric characters (to avoid matching "arm" in "charm").
func containsWord(name, word string) bool {
	for offset := 0; offset < len(name); {
		idx := strings.Index(name[offset:], word)
		if idx < 0 {
			return false
		}
		abs := offset + idx
		// Check left boundary
		leftOK := abs == 0 || !isAlphaNum(name[abs-1])
		// Check right boundary
		end := abs + len(word)
		rightOK := end >= len(name) || !isAlphaNum(name[end])
		if leftOK && rightOK {
			return true
		}
		offset = abs + 1
	}
	return false
}

func isAlphaNum(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}
