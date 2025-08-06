package testutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fentas/goodies/streams"

	"github.com/fentas/b/pkg/binary"
	"github.com/fentas/b/pkg/state"
)

// TempDir creates a temporary directory for testing
func TempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "b-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() {
		os.RemoveAll(dir)
	})
	return dir
}

// TempFile creates a temporary file for testing
func TempFile(t *testing.T, name, content string) string {
	t.Helper()
	dir := TempDir(t)
	path := filepath.Join(dir, name)
	
	// Create directory if needed
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("failed to create dir for temp file: %v", err)
	}
	
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}
	return path
}

// MockBinary creates a mock binary for testing
func MockBinary(name, version string) *binary.Binary {
	return &binary.Binary{
		Name:    name,
		Version: version,
	}
}

// MockConfig creates a test configuration
func MockConfig(binaries ...string) *state.BinaryList {
	config := &state.BinaryList{}
	for _, name := range binaries {
		*config = append(*config, &binary.LocalBinary{
			Name:    name,
			Version: "latest",
		})
	}
	return config
}

// MockConfigWithVersions creates a test configuration with specific versions
func MockConfigWithVersions(binaries map[string]string) *state.BinaryList {
	config := &state.BinaryList{}
	for name, version := range binaries {
		*config = append(*config, &binary.LocalBinary{
			Name:    name,
			Version: version,
		})
	}
	return config
}

// AssertFileExists checks if a file exists
func AssertFileExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatalf("expected file to exist: %s", path)
	}
}

// AssertFileNotExists checks if a file does not exist
func AssertFileNotExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err == nil {
		t.Fatalf("expected file to not exist: %s", path)
	}
}

// AssertFileContent validates file content
func AssertFileContent(t *testing.T, path, expected string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read file %s: %v", path, err)
	}
	if string(content) != expected {
		t.Fatalf("file content mismatch in %s:\nexpected: %q\nactual: %q", path, expected, string(content))
	}
}

// AssertFileContains checks if file contains specific content
func AssertFileContains(t *testing.T, path, expected string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read file %s: %v", path, err)
	}
	if !contains(string(content), expected) {
		t.Fatalf("file %s does not contain expected content: %q", path, expected)
	}
}

// MockIO creates a mock IO for testing
func MockIO() *streams.IO {
	return &streams.IO{
		In:     os.Stdin,
		Out:    os.Stdout,
		ErrOut: os.Stderr,
	}
}

// MockIOWithBuffers creates a mock IO with buffer capture
func MockIOWithBuffers() (*streams.IO, *MockBuffer, *MockBuffer) {
	outBuf := &MockBuffer{}
	errBuf := &MockBuffer{}
	return &streams.IO{
		In:     os.Stdin,
		Out:    outBuf,
		ErrOut: errBuf,
	}, outBuf, errBuf
}

// MockBuffer implements io.Writer for testing
type MockBuffer struct {
	content []byte
}

func (b *MockBuffer) Write(p []byte) (n int, err error) {
	b.content = append(b.content, p...)
	return len(p), nil
}

func (b *MockBuffer) String() string {
	return string(b.content)
}

func (b *MockBuffer) Reset() {
	b.content = nil
}

// contains is a helper function to check if a string contains a substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || 
		(len(substr) <= len(s) && s[:len(substr)] == substr) ||
		(len(substr) <= len(s) && s[len(s)-len(substr):] == substr) ||
		containsAt(s, substr))
}

func containsAt(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// ChangeDir changes to a directory and restores on cleanup
func ChangeDir(t *testing.T, dir string) {
	t.Helper()
	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current dir: %v", err)
	}
	
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("failed to change dir to %s: %v", dir, err)
	}
	
	t.Cleanup(func() {
		os.Chdir(oldDir)
	})
}

// CreateTestProject creates a test project structure
func CreateTestProject(t *testing.T, config *state.BinaryList) string {
	t.Helper()
	dir := TempDir(t)
	
	if config != nil {
		configPath := filepath.Join(dir, ".bin", "b.yaml")
		if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
			t.Fatalf("failed to create config dir: %v", err)
		}
		if err := state.SaveConfig(config, configPath); err != nil {
			t.Fatalf("failed to save test config: %v", err)
		}
	}
	
	return dir
}
