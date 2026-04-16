package provider

import (
	"archive/tar"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

func init() {
	Register(&OCI{})
}

// OCI extracts binaries from OCI images without a container runtime.
// Works with any OCI registry (Docker Hub, ghcr.io, quay.io, private).
//
// Syntax:
//
//	oci://<image>[@<tag>][:<path-in-image>]
//
// Examples:
//
//	oci://alpine
//	oci://ghcr.io/helm/helm@v3.18.6
//	oci://docker@cli:/usr/local/bin/docker
//
// The in-container path must begin with "/" so it is unambiguous with
// docker's own "image:tag" syntax (which we never use — tags go after "@").
type OCI struct{}

func (o *OCI) Name() string { return "oci" }

func (o *OCI) Match(ref string) bool {
	return strings.HasPrefix(ref, "oci://")
}

func (o *OCI) LatestVersion(ref string) (string, error) {
	return "latest", nil
}

// FetchRelease is not used for OCI — use Install instead.
func (o *OCI) FetchRelease(ref, version string) (*Release, error) {
	return nil, fmt.Errorf("oci provider does not use FetchRelease; use Install()")
}

// Install pulls a platform-matching image manifest and extracts a single
// binary file without invoking any container runtime.
func (o *OCI) Install(ref, version, destDir string) (string, error) {
	rest := strings.TrimPrefix(ref, "oci://")
	image, refTag, inContainerPath := ParseImageRef(rest)

	tag := version
	if tag == "" {
		tag = refTag
	}
	if tag == "" {
		tag = "latest"
	}
	binName := BinaryName(ref)

	nameRef, err := name.ParseReference(image + ":" + tag)
	if err != nil {
		return "", fmt.Errorf("parsing image ref %s:%s: %w", image, tag, err)
	}

	// remote.Image handles manifest-list/index resolution internally using the
	// provided platform (OS + arch + variant) so we don't need to reimplement
	// platform matching here.
	img, err := remote.Image(nameRef,
		remote.WithAuthFromKeychain(authn.DefaultKeychain),
		remote.WithPlatform(v1.Platform{
			OS:           runtime.GOOS,
			Architecture: runtime.GOARCH,
		}),
	)
	if err != nil {
		return "", fmt.Errorf("fetching image %s: %w", nameRef, err)
	}

	// Determine which paths to try inside the image.
	var searchPaths []string
	if inContainerPath != "" {
		searchPaths = []string{inContainerPath}
	} else {
		searchPaths = []string{
			"/usr/local/bin/" + binName,
			"/usr/bin/" + binName,
			"/bin/" + binName,
			"/app/" + binName,
		}
	}

	if err := os.MkdirAll(destDir, 0755); err != nil {
		return "", err
	}
	dest := filepath.Join(destDir, binName)

	layers, err := img.Layers()
	if err != nil {
		return "", fmt.Errorf("reading layers: %w", err)
	}
	// Walk layers newest-first so later overrides win.
	for i := len(layers) - 1; i >= 0; i-- {
		found, err := extractBinaryFromLayer(layers[i], searchPaths, dest)
		if err != nil {
			return "", err
		}
		if found {
			if err := os.Chmod(dest, 0755); err != nil {
				return "", err
			}
			return dest, nil
		}
	}

	return "", fmt.Errorf("binary %q not found in image %s at paths: %v", binName, nameRef, searchPaths)
}

// extractBinaryFromLayer scans a layer's tar stream once, looking for any of
// searchPaths. Returns true (and writes to dest) when a match is found.
// Earlier entries in searchPaths take priority; once a higher-priority match
// is found, the scan stops.
func extractBinaryFromLayer(l v1.Layer, searchPaths []string, dest string) (bool, error) {
	if len(searchPaths) == 0 {
		return false, nil
	}

	// Normalise candidates to absolute, cleaned form and assign priorities
	// (index in searchPaths; lower is better).
	targets := make(map[string]int, len(searchPaths))
	for i, sp := range searchPaths {
		targets[path.Clean("/"+strings.TrimPrefix(sp, "/"))] = i
	}

	rc, err := l.Uncompressed()
	if err != nil {
		return false, fmt.Errorf("reading layer contents: %w", err)
	}
	defer rc.Close()

	tr := tar.NewReader(rc)
	bestPriority := len(searchPaths) // sentinel: nothing found yet
	var tmpPath string
	cleanup := func() {
		if tmpPath != "" {
			_ = os.Remove(tmpPath)
			tmpPath = ""
		}
	}

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			cleanup()
			return false, fmt.Errorf("reading tar: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		normalized := path.Clean("/" + strings.TrimPrefix(hdr.Name, "/"))
		priority, ok := targets[normalized]
		if !ok || priority >= bestPriority {
			continue
		}
		// Write to a temp file first; rename once we're confident this is
		// the best match (since an even-higher-priority path may appear
		// later in the same tar stream).
		tmp, err := os.CreateTemp(filepath.Dir(dest), ".oci-extract-*")
		if err != nil {
			cleanup()
			return false, fmt.Errorf("creating temp file: %w", err)
		}
		if _, err := io.Copy(tmp, tr); err != nil {
			tmp.Close()
			_ = os.Remove(tmp.Name())
			cleanup()
			return false, fmt.Errorf("writing temp file: %w", err)
		}
		if err := tmp.Close(); err != nil {
			_ = os.Remove(tmp.Name())
			cleanup()
			return false, fmt.Errorf("closing temp file: %w", err)
		}
		cleanup() // remove any previous lower-priority candidate
		tmpPath = tmp.Name()
		bestPriority = priority
		if bestPriority == 0 {
			break // can't do better than the highest-priority path
		}
	}

	if tmpPath == "" {
		return false, nil
	}
	if err := os.Rename(tmpPath, dest); err != nil {
		_ = os.Remove(tmpPath)
		return false, fmt.Errorf("moving extracted file into place: %w", err)
	}
	return true, nil
}
