package state

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/fentas/b/pkg/binary"
	"github.com/fentas/b/pkg/path"
	"gopkg.in/yaml.v3"
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

// SaveConfig saves the configuration to the specified path.
// If the file already exists, preserves comments and formatting.
func SaveConfig(config *State, configPath string) error {
	dir := filepath.Dir(configPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	// LoadConfigFromPath absolutizes relative `file:` paths so the running
	// process sees a real on-disk location. Reverse that here so a
	// load→modify→save round-trip (e.g. `b install --add`) keeps the user's
	// relative `file:` values relative instead of rewriting them to absolute
	// paths. Mutate-and-restore avoids changing the caller's in-memory state,
	// which must stay absolute for the rest of the process.
	defer relativizeFiles(config, dir)()

	// Try comment-preserving save if file exists
	if _, err := os.Stat(configPath); err == nil {
		return SaveConfigPreserving(config, configPath)
	}

	return saveConfigClean(config, configPath)
}

// relativizeFiles temporarily converts each binary's absolute File path that
// lives under configDir back to a path relative to configDir, returning a
// function that restores the original values. Paths that are already relative,
// or absolute paths outside configDir, are left untouched. This is the inverse
// of the join performed in LoadConfigFromPath.
func relativizeFiles(config *State, configDir string) func() {
	if config == nil {
		return func() {}
	}
	type saved struct {
		b    *binary.LocalBinary
		file string
	}
	var originals []saved
	for _, b := range config.Binaries {
		if b == nil || b.File == "" || !filepath.IsAbs(b.File) {
			continue
		}
		rel, err := filepath.Rel(configDir, b.File)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			// Outside the config dir — keep it absolute.
			continue
		}
		originals = append(originals, saved{b, b.File})
		b.File = rel
	}
	return func() {
		for _, s := range originals {
			s.b.File = s.file
		}
	}
}

// saveConfigClean does a plain marshal+write without preserving comments.
// Assumes parent directory already exists (caller ensures this).
func saveConfigClean(config *State, configPath string) error {
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
