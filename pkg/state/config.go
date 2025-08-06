package state

import (
	"os"
	"path/filepath"

	"github.com/fentas/b/pkg/binary"
	"github.com/fentas/b/pkg/path"
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

// LoadConfig loads configuration with automatic discovery
func LoadConfig() (*BinaryList, error) {
	configPath, err := path.FindConfigFile()
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
	defaultConfig := BinaryList{
		&binary.LocalBinary{
			Name: "b",
		},
	}
	return SaveConfig(&defaultConfig, configPath)
}
