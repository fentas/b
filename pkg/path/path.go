package path

import (
	"os"
	"path/filepath"
)

// GetDefaultConfigPath returns the default config path for the current directory
func GetDefaultConfigPath() string {
	path := GetBinaryPath()
	if path == "" {
		return ".bin/b.yaml"
	}
	return filepath.Join(path, "b.yaml")
}

// GetBinaryPath returns the binary path without importing pkg/binary to avoid import cycle.
// Priority: PATH_BIN > PATH_BASE > <git-root>/.bin > <cwd>/.bin
func GetBinaryPath() string {
	if p := os.Getenv("PATH_BIN"); p != "" {
		return p
	}
	if p := os.Getenv("PATH_BASE"); p != "" {
		return p
	}
	if gitRoot, err := GetGitRootDirectory(); err == nil {
		return filepath.Join(gitRoot, ".bin")
	}
	// Fallback: use CWD/.bin so `b install` works outside a git repo.
	if cwd, err := os.Getwd(); err == nil {
		return filepath.Join(cwd, ".bin")
	}
	return ""
}

// FindConfigFile searches for b.yaml file in current directory and parent directories
func FindConfigFile() (string, error) {
	// Start from current directory
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	// Walk up the directory tree
	for {
		configPath := filepath.Join(dir, ".bin", "b.yaml")
		if _, err := os.Stat(configPath); err == nil {
			return configPath, nil
		}

		// Try without .bin directory
		configPath = filepath.Join(dir, "b.yaml")
		if _, err := os.Stat(configPath); err == nil {
			return configPath, nil
		}

		// Move to parent directory
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached root directory
			break
		}
		dir = parent
	}

	return "", nil // No config found, but not an error
}

// GetGitRootDirectory finds the git root directory without importing pkg/binary
func GetGitRootDirectory() (string, error) {
	// Start from current directory and walk up to find .git
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	for {
		gitPath := filepath.Join(dir, ".git")
		if _, err := os.Stat(gitPath); err == nil {
			return dir, nil
		}

		// Move to parent directory
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached root directory
			break
		}
		dir = parent
	}

	return "", os.ErrNotExist
}
