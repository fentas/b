package provider

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func init() {
	Register(&Docker{})
}

// Docker extracts binaries from Docker/OCI images.
type Docker struct{}

func (d *Docker) Name() string { return "docker" }

func (d *Docker) Match(ref string) bool {
	return strings.HasPrefix(ref, "docker://")
}

func (d *Docker) LatestVersion(ref string) (string, error) {
	return "latest", nil
}

// FetchRelease is not used for Docker — use Install instead.
func (d *Docker) FetchRelease(ref, version string) (*Release, error) {
	return nil, fmt.Errorf("docker provider does not use FetchRelease; use Install()")
}

// Install pulls the image, creates a container, copies the binary out, and cleans up.
// searchPaths are the paths to search for the binary inside the container.
// If the ref includes ":/<path>", that path is used as the single search path.
func (d *Docker) Install(ref, version, destDir string, searchPaths []string) (string, error) {
	runtime, err := detectContainerRuntime()
	if err != nil {
		return "", err
	}

	rest := strings.TrimPrefix(ref, "docker://")
	image, refTag, inContainerPath := ParseImageRef(rest)

	tag := version
	if tag == "" {
		tag = refTag
	}
	if tag == "" {
		tag = "latest"
	}
	imageRef := image + ":" + tag
	name := BinaryName(ref)

	// Pull image
	cmd := exec.Command(runtime, "pull", imageRef)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("pulling image %s: %w", imageRef, err)
	}

	// Create container (don't start it)
	out, err := exec.Command(runtime, "create", imageRef).Output()
	if err != nil {
		return "", fmt.Errorf("creating container from %s: %w", imageRef, err)
	}
	containerID := strings.TrimSpace(string(out))
	defer exec.Command(runtime, "rm", containerID).Run()

	// Determine search paths: explicit ":/<path>" overrides everything.
	if inContainerPath != "" {
		searchPaths = []string{inContainerPath}
	} else if searchPaths == nil {
		searchPaths = []string{
			"/usr/local/bin/" + name,
			"/usr/bin/" + name,
			"/bin/" + name,
			"/app/" + name,
		}
	}

	dest := filepath.Join(destDir, name)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return "", err
	}

	for _, p := range searchPaths {
		cpCmd := exec.Command(runtime, "cp", containerID+":"+p, dest)
		if err := cpCmd.Run(); err == nil {
			// Found it
			if err := os.Chmod(dest, 0755); err != nil {
				return "", err
			}
			return dest, nil
		}
	}

	return "", fmt.Errorf("binary %q not found in image %s at paths: %v", name, imageRef, searchPaths)
}

// dockerImage returns the image name (without tag/path) for legacy callers
// and tests. Prefer ParseImageRef for new code.
func dockerImage(ref string) string {
	r := strings.TrimPrefix(ref, "docker://")
	image, _, _ := ParseImageRef(r)
	// Also strip docker-style "image:tag" when no explicit @ was given, but
	// only when the ':' is after the last '/' so registry ports like
	// "localhost:5000/org/image" are preserved.
	lastSlash := strings.LastIndex(image, "/")
	if i := strings.LastIndex(image, ":"); i > lastSlash && i > 0 {
		image = image[:i]
	}
	return image
}

func detectContainerRuntime() (string, error) {
	for _, rt := range []string{"docker", "podman", "nerdctl"} {
		if _, err := exec.LookPath(rt); err == nil {
			return rt, nil
		}
	}
	return "", fmt.Errorf("no container runtime found (docker, podman, or nerdctl required)")
}
