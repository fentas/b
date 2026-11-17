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

// Install compiles the module with `go install` and writes the binary to
// the file dest (installFile). The caller picks dest: the file that the
// b.yaml entry names.
func (g *GoInstall) Install(ref, version, dest string) error {
	if _, err := exec.LookPath("go"); err != nil {
		return fmt.Errorf("go not found on PATH (required for go:// provider)")
	}

	module := goModule(ref)
	if version == "" {
		version = "latest"
	}

	// Create a temporary GOBIN
	tmpDir, err := os.MkdirTemp("", "b-goinstall-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	installArg := module + "@" + version
	cmd := exec.Command("go", "install", installArg)
	cmd.Env = append(os.Environ(), "GOBIN="+tmpDir)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go install %s: %w", installArg, err)
	}

	// Find the compiled binary: go install names it after the package
	// (BinaryName). Else take the only file in GOBIN.
	name := BinaryName(ref)
	compiled := filepath.Join(tmpDir, name)
	if _, err := os.Stat(compiled); err != nil {
		entries, _ := os.ReadDir(tmpDir)
		if len(entries) != 1 {
			return fmt.Errorf("compiled binary %q not found in GOBIN", name)
		}
		compiled = filepath.Join(tmpDir, entries[0].Name())
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}

	// Copy, not rename: GOBIN can be on another filesystem.
	src, err := os.Open(compiled)
	if err != nil {
		return err
	}
	defer src.Close()
	return installFile(dest, src)
}

func goModule(ref string) string {
	r := strings.TrimPrefix(ref, "go://")
	r, _ = ParseRef(r)
	return r
}
