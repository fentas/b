package state

import (
	"os"
	"path/filepath"

	"github.com/fentas/b/pkg/binary"
	"github.com/fentas/b/pkg/path"
	"gopkg.in/yaml.v2"
)

// LoadConfigFromPath loads configuration from a specific path
func LoadConfigFromPath(configPath string) (*State, error) {
	if _, err := os.Stat(configPath); err != nil {
		return nil, err
	}

	config, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}

	var state State
	if err := yaml.Unmarshal(config, &state); err != nil {
		return nil, err
	}

	// Resolve relative file paths relative to config file location
	configDir := filepath.Dir(configPath)
	for _, b := range state.Binaries {
		if b.File != "" && !filepath.IsAbs(b.File) {
			b.File = filepath.Join(configDir, b.File)
		}
	}

	return &state, nil
}

// LoadConfig loads configuration with automatic discovery
func LoadConfig() (*State, error) {
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
func SaveConfig(config *State, configPath string) error {
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
	defaultConfig := State{
		Binaries: BinaryList{
			&binary.LocalBinary{
				Name: "b",
			},
		},
	}
	return SaveConfig(&defaultConfig, configPath)
}
