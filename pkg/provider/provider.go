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

// DigestResolver is an optional interface providers can implement to
// report a stable content identity ("digest") for a given ref+version —
// notably docker:// / oci:// image manifest digests. b.lock stores this
// alongside the source ref and `b update` uses it to detect whether a
// mutable tag (e.g. "cli", "latest") has been repushed upstream: same
// digest → skip re-download, different digest → re-pull.
//
// Return ("", nil) if the digest can't be determined for this ref (e.g.
// network error or private-registry-without-auth). Callers treat an
// empty digest as "don't know" — they must NOT fall back to assuming
// "unchanged".
type DigestResolver interface {
	ResolveDigest(ref, version string) (string, error)
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
//
// For docker:// or oci:// refs, the optional ":/<in-container-path>" suffix
// is preserved on base (stripped only for the tag scan), and the tag ends
// up in version. Docker-style "image:tag" is also accepted as a copy-paste
// convenience — a ':' is treated as a tag separator when it occurs after
// the last '/' (so registry ports like "localhost:5000/org/img" still parse
// correctly). Examples:
//
//	"docker://docker@cli:/usr/local/bin/docker"  →
//	   ("docker://docker:/usr/local/bin/docker", "cli")
//	"oci://ghcr.io/org/img:v1:/bin/tool"         →
//	   ("oci://ghcr.io/org/img:/bin/tool", "v1")
func ParseRef(ref string) (base, version string) {
	if strings.HasPrefix(ref, "docker://") || strings.HasPrefix(ref, "oci://") {
		imgPart, pathPart := SplitImagePath(ref)
		// Prefer explicit "@tag".
		if i := strings.LastIndex(imgPart, "@"); i > 0 {
			return imgPart[:i] + pathPart, imgPart[i+1:]
		}
		// Tolerate docker-style "image:tag" — but only when the ':' is after
		// the last '/' so registry ports are preserved.
		lastSlash := strings.LastIndex(imgPart, "/")
		if i := strings.LastIndex(imgPart, ":"); i > lastSlash && i > 0 {
			return imgPart[:i] + pathPart, imgPart[i+1:]
		}
		return ref, ""
	}
	if i := strings.LastIndex(ref, "@"); i > 0 {
		return ref[:i], ref[i+1:]
	}
	return ref, ""
}

// SplitImagePath locates the ":/<path>" suffix of a docker:// or oci:// ref
// and returns (imagePart, pathPart). pathPart is either empty or starts with
// ":/". Uses the last ":/" so registry ports (":443/") in the middle aren't
// mistaken for the path separator. Skips the protocol prefix's own ":/" so
// "oci://alpine" doesn't match on the scheme separator.
func SplitImagePath(ref string) (imagePart, pathPart string) {
	start := 0
	if i := strings.Index(ref, "://"); i >= 0 {
		start = i + 3
	}
	if i := strings.LastIndex(ref[start:], ":/"); i >= 0 {
		abs := start + i
		return ref[:abs], ref[abs:]
	}
	return ref, ""
}

// ParseImageRef parses a docker:// or oci:// ref into (image, tag, path).
//
//	alpine                             → ("alpine", "", "")
//	alpine@3.19                        → ("alpine", "3.19", "")
//	alpine:3.19                        → ("alpine", "3.19", "")   // docker-style, tolerated
//	docker@cli:/usr/local/bin/docker   → ("docker", "cli", "/usr/local/bin/docker")
//	ghcr.io/org/img@v1:/bin/tool       → ("ghcr.io/org/img", "v1", "/bin/tool")
//	localhost:5000/org/img             → ("localhost:5000/org/img", "", "")
//
// The prefix (docker:// or oci://) must already be stripped. Docker-style
// "image:tag" is accepted for convenience (a copy-paste from docker docs)
// but the preferred syntax remains "@tag" to stay consistent across providers.
func ParseImageRef(ref string) (image, tag, inContainerPath string) {
	imagePart, pathPart := SplitImagePath(ref)
	if pathPart != "" {
		inContainerPath = pathPart[1:] // drop leading ":"
		ref = imagePart
	}
	if i := strings.LastIndex(ref, "@"); i > 0 {
		tag = ref[i+1:]
		ref = ref[:i]
	} else {
		// Also accept docker-style "image:tag" — only when ':' is after the
		// last '/' so registry ports ("localhost:5000/org/img") are preserved.
		lastSlash := strings.LastIndex(ref, "/")
		if i := strings.LastIndex(ref, ":"); i > lastSlash && i > 0 {
			tag = ref[i+1:]
			ref = ref[:i]
		}
	}
	image = ref
	return
}

// IsReleaseProvider returns true if the provider uses FetchRelease for downloads
// (i.e. GitHub, GitLab, Gitea). Returns false for go://, docker://, oci://, git://.
func IsReleaseProvider(p Provider) bool {
	if p == nil {
		return false
	}
	switch p.(type) {
	case *GoInstall, *Docker, *OCI, *Git:
		return false
	default:
		return true
	}
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
//	"docker://docker@cli:/usr/local/bin/docker" → "docker"
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

	// docker:// or oci:// with ":/<path>" — binary name is basename of path.
	// Fall through to default derivation if the path is empty or a directory.
	if strings.HasPrefix(ref, "docker://") || strings.HasPrefix(ref, "oci://") {
		_, pathPart := SplitImagePath(ref)
		if pathPart != "" {
			p := pathPart[1:] // drop leading ":"
			if p != "" && !strings.HasSuffix(p, "/") {
				parts := strings.Split(p, "/")
				if last := parts[len(parts)-1]; last != "" {
					return last
				}
			}
		}
	}

	// For docker:// / oci:// that fell through (empty or directory path),
	// derive the name from the image part only — the in-container path
	// shouldn't influence the name.
	r := ref
	if strings.HasPrefix(ref, "docker://") || strings.HasPrefix(ref, "oci://") {
		imgPart, _ := SplitImagePath(ref)
		r = imgPart
	}
	// Strip protocol prefix
	if i := strings.Index(r, "://"); i >= 0 {
		r = r[i+3:]
	}
	// Strip version
	if i := strings.LastIndex(r, "@"); i > 0 {
		r = r[:i]
	}
	// Tolerate trailing slashes like "github.com/org/repo/" before further
	// parsing so they don't break tag stripping or the final segment split.
	r = strings.TrimRight(r, "/")
	// Strip docker-style "image:tag" — only when ":" occurs after the last "/"
	// so registry ports like "localhost:5000/org/image" are preserved.
	lastSlash := strings.LastIndex(r, "/")
	if i := strings.LastIndex(r, ":"); i > lastSlash && i > 0 {
		r = r[:i]
	}
	// Last path segment.
	parts := strings.Split(r, "/")
	return parts[len(parts)-1]
}
