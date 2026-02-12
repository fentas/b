package provider

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func init() {
	Register(&GoInstall{})
}

// GoInstall compiles binaries from source using `go install`.
type GoInstall struct{}

func (g *GoInstall) Name() string { return "go" }

func (g *GoInstall) Match(ref string) bool {
	return strings.HasPrefix(ref, "go://")
}

func (g *GoInstall) LatestVersion(ref string) (string, error) {
	// For Go install, "latest" is the version string.
	return "latest", nil
}

// FetchRelease is not used for Go install — use Install instead.
func (g *GoInstall) FetchRelease(ref, version string) (*Release, error) {
	return nil, fmt.Errorf("go install provider does not use FetchRelease; use Install()")
}

// Install compiles the module and returns the path to the compiled binary.
func (g *GoInstall) Install(ref, version, destDir string) (string, error) {
	if _, err := exec.LookPath("go"); err != nil {
		return "", fmt.Errorf("go not found on PATH (required for go:// provider)")
	}

	module := goModule(ref)
	if version == "" {
		version = "latest"
	}

	// Create a temporary GOBIN
	tmpDir, err := os.MkdirTemp("", "b-goinstall-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmpDir)

	installArg := module + "@" + version
	cmd := exec.Command("go", "install", installArg)
	cmd.Env = append(os.Environ(), "GOBIN="+tmpDir)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("go install %s: %w", installArg, err)
	}

	// Find the compiled binary (last segment of module path)
	name := BinaryName(ref)
	compiled := filepath.Join(tmpDir, name)
	if _, err := os.Stat(compiled); err != nil {
		// Try to find any executable in tmpDir
		entries, _ := os.ReadDir(tmpDir)
		if len(entries) == 1 {
			compiled = filepath.Join(tmpDir, entries[0].Name())
			name = entries[0].Name()
		} else {
			return "", fmt.Errorf("compiled binary %q not found in GOBIN", name)
		}
	}

	// Move to dest
	dest := filepath.Join(destDir, name)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return "", err
	}

	// Read and write (rename doesn't work across filesystems)
	data, err := os.ReadFile(compiled)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(dest, data, 0755); err != nil {
		return "", err
	}

	return dest, nil
}

func goModule(ref string) string {
	r := strings.TrimPrefix(ref, "go://")
	r, _ = ParseRef(r)
	return r
}
