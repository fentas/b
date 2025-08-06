package state

import (
	"os"
	"path/filepath"

	"github.com/fentas/b/pkg/binary"
	"gopkg.in/yaml.v2"
)

// LoadConfigFromPath loads configuration from a specific path
func LoadConfigFromPath(configPath string) (*BinaryList, error) {
	if _, err := os.Stat(configPath); err != nil {
		return nil, err
	}
	
	config, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}
	
	var list BinaryList
	if err := yaml.Unmarshal(config, &list); err != nil {
		return nil, err
	}
	
	return &list, nil
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

// LoadConfigWithDiscovery loads configuration with automatic discovery
func LoadConfigWithDiscovery() (*BinaryList, error) {
	configPath, err := FindConfigFile()
	if err != nil {
		return nil, err
	}
	
	if configPath == "" {
		return nil, nil // No config found
	}
	
	return LoadConfigFromPath(configPath)
}

// SaveConfig saves the configuration to the specified path
func SaveConfig(config *BinaryList, configPath string) error {
	// Ensure directory exists
	dir := filepath.Dir(configPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	
	data, err := yaml.Marshal(config)
	if err != nil {
		return err
	}
	
	return os.WriteFile(configPath, data, 0644)
}

// CreateDefaultConfig creates a default b.yaml configuration file
func CreateDefaultConfig(configPath string) error {
	defaultConfig := BinaryList{}
	return SaveConfig(&defaultConfig, configPath)
}

// GetDefaultConfigPath returns the default config path for the current directory
func GetDefaultConfigPath() string {
	path := binary.GetBinaryPath()
	if path == "" {
		return ".bin/b.yaml"
	}
	return filepath.Join(path, "b.yaml")
}
