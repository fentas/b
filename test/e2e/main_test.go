package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestE2E_CLIBuild(t *testing.T) {
	// Test that the CLI can be built successfully
	binaryPath := filepath.Join(os.TempDir(), "b-e2e-test")
	defer os.Remove(binaryPath)

	cmd := exec.Command("go", "build", "-o", binaryPath, "./cmd/b")
	cmd.Dir = "../.."

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Failed to build CLI: %v\nOutput: %s", err, output)
	}

	// Verify binary was created and is executable
	info, err := os.Stat(binaryPath)
	if err != nil {
		t.Fatalf("CLI binary was not created: %v", err)
	}

	if info.Mode()&0111 == 0 {
		t.Error("CLI binary is not executable")
	}

	t.Logf("CLI built successfully at %s", binaryPath)
}

func TestE2E_InitWorkflow(t *testing.T) {
	// Create a temporary directory for the test project
	tempDir, err := os.MkdirTemp("", "b-e2e-init-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Build the CLI
	binaryPath := filepath.Join(os.TempDir(), "b-e2e-test")
	defer os.Remove(binaryPath)

	cmd := exec.Command("go", "build", "-o", binaryPath, "./cmd/b")
	cmd.Dir = "../.."

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Failed to build CLI: %v\nOutput: %s", err, output)
	}

	// Change to temp directory
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get current directory: %v", err)
	}
	defer func() { _ = os.Chdir(originalDir) }()

	err = os.Chdir(tempDir)
	if err != nil {
		t.Fatalf("Failed to change to temp directory: %v", err)
	}

	// Run init command
	cmd = exec.Command(binaryPath, "init")
	output, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Init command failed: %v\nOutput: %s", err, output)
	}

	// Verify config file was created
	configPath := filepath.Join(tempDir, ".bin", "b.yaml")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Error("Config file was not created by init command")
	}

	// Verify .gitignore was created
	gitignorePath := filepath.Join(tempDir, ".bin", ".gitignore")
	if _, err := os.Stat(gitignorePath); os.IsNotExist(err) {
		t.Error(".gitignore file was not created by init command")
	}

	// Verify .envrc was created
	envrcPath := filepath.Join(tempDir, ".envrc")
	if _, err := os.Stat(envrcPath); os.IsNotExist(err) {
		t.Error(".envrc file was not created by init command")
	}

	// Read and verify config content
	configContent, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("Failed to read config file: %v", err)
	}

	configStr := string(configContent)
	if !strings.Contains(configStr, "b: null") {
		t.Error("Config file does not contain self-reference to 'b' binary")
	}

	t.Logf("Init workflow completed successfully in %s", tempDir)
}

func TestE2E_ConfigDiscovery(t *testing.T) {
	// Create a temporary directory structure
	tempDir, err := os.MkdirTemp("", "b-e2e-discovery-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create nested project structure
	projectDir := filepath.Join(tempDir, "project", "subdir")
	err = os.MkdirAll(projectDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create project structure: %v", err)
	}

	// Create config in parent directory
	configDir := filepath.Join(tempDir, "project", ".bin")
	err = os.MkdirAll(configDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create config directory: %v", err)
	}

	configPath := filepath.Join(configDir, "b.yaml")
	configContent := []byte(`jq:
  version: "1.7"
kubectl:
  version: "latest"
`)
	err = os.WriteFile(configPath, configContent, 0644)
	if err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	// Build the CLI
	binaryPath := filepath.Join(os.TempDir(), "b-e2e-test")
	defer os.Remove(binaryPath)

	cmd := exec.Command("go", "build", "-o", binaryPath, "./cmd/b")
	cmd.Dir = "../.."

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Failed to build CLI: %v\nOutput: %s", err, output)
	}

	// Change to subdirectory
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get current directory: %v", err)
	}
	defer func() { _ = os.Chdir(originalDir) }()

	if err = os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to change to project subdirectory: %v", err)
	}

	// Run list command to test config discovery
	cmd = exec.Command(binaryPath, "list")
	output, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("List command failed: %v\nOutput: %s", err, output)
	}

	outputStr := string(output)
	if !strings.Contains(outputStr, "jq") || !strings.Contains(outputStr, "kubectl") {
		t.Errorf("List command did not find expected binaries. Output: %s", outputStr)
	}

	t.Logf("Config discovery test completed successfully")
}

func TestE2E_HelpAndVersion(t *testing.T) {
	// Build the CLI
	binaryPath := filepath.Join(os.TempDir(), "b-e2e-test")
	defer os.Remove(binaryPath)

	cmd := exec.Command("go", "build", "-o", binaryPath, "./cmd/b")
	cmd.Dir = "../.."

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Failed to build CLI: %v\nOutput: %s", err, output)
	}

	// Test help command
	cmd = exec.Command(binaryPath, "--help")
	output, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Help command failed: %v\nOutput: %s", err, output)
	}

	helpOutput := string(output)
	expectedHelpStrings := []string{"Usage:", "Commands:", "Flags:"}
	for _, expected := range expectedHelpStrings {
		if !strings.Contains(helpOutput, expected) {
			t.Errorf("Help output missing expected string: %s", expected)
		}
	}

	// Test version command
	cmd = exec.Command(binaryPath, "version")
	output, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Version command failed: %v\nOutput: %s", err, output)
	}

	versionOutput := string(output)
	if len(versionOutput) == 0 {
		t.Error("Version command produced no output")
	}

	t.Logf("Help and version commands work correctly")
}

func TestE2E_ErrorHandling(t *testing.T) {
	// Build the CLI
	binaryPath := filepath.Join(os.TempDir(), "b-e2e-test")
	defer os.Remove(binaryPath)

	cmd := exec.Command("go", "build", "-o", binaryPath, "./cmd/b")
	cmd.Dir = "../.."

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Failed to build CLI: %v\nOutput: %s", err, output)
	}

	// Test invalid command
	cmd = exec.Command(binaryPath, "invalid-command")
	_, err = cmd.CombinedOutput()
	if err == nil {
		t.Error("Expected error for invalid command")
	}

	// Test list command without config (should handle gracefully)
	tempDir, err := os.MkdirTemp("", "b-e2e-error-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get current directory: %v", err)
	}
	defer func() { _ = os.Chdir(originalDir) }()

	err = os.Chdir(tempDir)
	if err != nil {
		t.Fatalf("Failed to change to temp directory: %v", err)
	}

	cmd = exec.Command(binaryPath, "list")
	output, _ = cmd.CombinedOutput()
	// Should handle missing config gracefully
	outputStr := string(output)
	if strings.Contains(outputStr, "panic") {
		t.Errorf("List command panicked when no config exists: %s", outputStr)
	}

	t.Logf("Error handling test completed")
}

func TestE2E_Performance(t *testing.T) {
	// Build the CLI
	binaryPath := filepath.Join(os.TempDir(), "b-e2e-test")
	defer os.Remove(binaryPath)

	cmd := exec.Command("go", "build", "-o", binaryPath, "./cmd/b")
	cmd.Dir = "../.."

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Failed to build CLI: %v\nOutput: %s", err, output)
	}

	// Test help command performance
	start := time.Now()
	cmd = exec.Command(binaryPath, "--help")
	_, err = cmd.CombinedOutput()
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("Help command failed: %v", err)
	}

	// Should complete quickly (under 5 seconds)
	if duration > 5*time.Second {
		t.Errorf("Help command took too long: %v", duration)
	}

	// Test version command performance
	start = time.Now()
	cmd = exec.Command(binaryPath, "version")
	_, err = cmd.CombinedOutput()
	duration = time.Since(start)

	if err != nil {
		t.Fatalf("Version command failed: %v", err)
	}

	if duration > 5*time.Second {
		t.Errorf("Version command took too long: %v", duration)
	}

	t.Logf("Performance test completed - commands execute quickly")
}
