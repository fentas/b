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
	"github.com/google/go-containerregistry/pkg/v1/types"
)

func init() {
	Register(&OCI{})
}

// OCI extracts binaries from OCI images without a container runtime.
// Works with any OCI registry (Docker Hub, ghcr.io, quay.io, private).
//
// Syntax:
//
//	oci://<image>[@<tag>][::<path-in-image>]
//
// Examples:
//
//	oci://alpine
//	oci://ghcr.io/helm/helm@v3.18.6
//	oci://docker@cli::/usr/local/bin/docker
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

// Install pulls the image manifest, selects a platform-matching layer,
// and extracts a single file without invoking any container runtime.
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

	opts := []remote.Option{
		remote.WithAuthFromKeychain(authn.DefaultKeychain),
		remote.WithPlatform(v1.Platform{
			OS:           runtime.GOOS,
			Architecture: runtime.GOARCH,
		}),
	}

	desc, err := remote.Get(nameRef, opts...)
	if err != nil {
		return "", fmt.Errorf("fetching manifest for %s: %w", nameRef, err)
	}

	img, err := resolveImage(desc, opts)
	if err != nil {
		return "", fmt.Errorf("resolving image %s: %w", nameRef, err)
	}

	// Determine which paths to try inside the image.
	searchPaths := []string{inContainerPath}
	if inContainerPath == "" {
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
		for _, sp := range searchPaths {
			found, err := extractFromLayer(layers[i], sp, dest)
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
	}

	return "", fmt.Errorf("binary %q not found in image %s at paths: %v", binName, nameRef, searchPaths)
}

// resolveImage returns an Image for a descriptor, selecting a platform-matching
// manifest when the descriptor is a manifest list/index.
func resolveImage(desc *remote.Descriptor, opts []remote.Option) (v1.Image, error) {
	switch desc.MediaType {
	case types.DockerManifestList, types.OCIImageIndex:
		idx, err := desc.ImageIndex()
		if err != nil {
			return nil, err
		}
		manifest, err := idx.IndexManifest()
		if err != nil {
			return nil, err
		}
		want := v1.Platform{OS: runtime.GOOS, Architecture: runtime.GOARCH}
		for _, m := range manifest.Manifests {
			if m.Platform != nil && m.Platform.OS == want.OS && m.Platform.Architecture == want.Architecture {
				return idx.Image(m.Digest)
			}
		}
		return nil, fmt.Errorf("no manifest matches platform %s/%s", want.OS, want.Architecture)
	default:
		return desc.Image()
	}
}

// extractFromLayer scans a single layer's tar stream for filePath and writes it to dest.
// Returns true if the file was found and extracted.
func extractFromLayer(l v1.Layer, filePath, dest string) (bool, error) {
	rc, err := l.Uncompressed()
	if err != nil {
		return false, err
	}
	defer rc.Close()

	target := strings.TrimPrefix(path.Clean(filePath), "/")
	tr := tar.NewReader(rc)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if strings.TrimPrefix(path.Clean(h.Name), "/") != target {
			continue
		}
		if h.Typeflag != tar.TypeReg {
			return false, fmt.Errorf("%s is not a regular file (typeflag=%c)", filePath, h.Typeflag)
		}
		out, err := os.Create(dest)
		if err != nil {
			return false, err
		}
		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			return false, err
		}
		return true, out.Close()
	}
}
