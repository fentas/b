// Package provider defines the interface for binary providers
// and the provider registry for auto-detecting how to fetch a binary.
package provider

import (
	"fmt"
	"strings"
)

// Provider can fetch release information for a given ref.
type Provider interface {
	// Name returns the provider name (e.g. "github", "gitlab").
	Name() string
	// Match reports whether this provider handles the given ref.
	Match(ref string) bool
	// LatestVersion returns the latest release version/tag for the ref.
	LatestVersion(ref string) (string, error)
	// FetchRelease returns release metadata for a specific version.
	// If version is empty, fetches the latest release.
	FetchRelease(ref, version string) (*Release, error)
}

// Release holds metadata about a release from any provider.
type Release struct {
	Version string
	Assets  []Asset
}

// Asset is a single downloadable file in a release.
type Asset struct {
	Name string
	URL  string
	Size int64
}

// registry holds all registered providers in order of specificity.
var registry []Provider

// Register adds a provider to the registry.
func Register(p Provider) {
	registry = append(registry, p)
}

// Detect returns the first provider that matches the given ref.
func Detect(ref string) (Provider, error) {
	for _, p := range registry {
		if p.Match(ref) {
			return p, nil
		}
	}
	return nil, fmt.Errorf("no provider matched ref %q", ref)
}

// ParseRef splits a ref like "github.com/org/repo@v1.0" into
// (github.com/org/repo, v1.0). Version may be empty.
func ParseRef(ref string) (base, version string) {
	if i := strings.LastIndex(ref, "@"); i > 0 {
		return ref[:i], ref[i+1:]
	}
	return ref, ""
}

// IsProviderRef returns true if the string looks like a provider ref
// (contains a slash or a protocol prefix) rather than a preset name.
func IsProviderRef(s string) bool {
	if strings.Contains(s, "://") {
		return true
	}
	if strings.Contains(s, "/") {
		return true
	}
	return false
}

// BinaryName derives a binary name from a provider ref.
// e.g. "github.com/derailed/k9s" → "k9s",
//
//	"go://github.com/jrhouston/tfk8s" → "tfk8s",
//	"docker://hashicorp/terraform" → "terraform"
func BinaryName(ref string) string {
	// git:// refs use the filepath part (after :) as the binary name
	if strings.HasPrefix(ref, "git://") {
		r := strings.TrimPrefix(ref, "git://")
		// Strip version
		if i := strings.LastIndex(r, "@"); i > 0 {
			r = r[:i]
		}
		// The part after : is the filepath in the repo
		if i := strings.Index(r, ":"); i >= 0 {
			filePart := r[i+1:]
			parts := strings.Split(filePart, "/")
			return parts[len(parts)-1]
		}
	}

	// Strip protocol prefix
	r := ref
	if i := strings.Index(r, "://"); i >= 0 {
		r = r[i+3:]
	}
	// Strip version
	if i := strings.LastIndex(r, "@"); i > 0 {
		r = r[:i]
	}
	// Strip colon (docker image:tag handled by version)
	if i := strings.LastIndex(r, ":"); i > 0 {
		r = r[:i]
	}
	// Last path segment
	parts := strings.Split(r, "/")
	return parts[len(parts)-1]
}
