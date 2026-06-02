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

	// LoadConfigFromPath resolves relative `file:` values against the config
	// dir so the running process sees a usable on-disk location. Reverse that
	// here so a load→modify→save round-trip (e.g. `b install --add`) keeps the
	// user's relative `file:` values relative instead of rewriting them to the
	// resolved path. relativizeFiles works on a copy, so the caller's in-memory
	// state is left untouched (it must stay resolved, and may be read
	// concurrently).
	config = relativizeFiles(config, dir)

	// Try comment-preserving save if file exists
	if _, err := os.Stat(configPath); err == nil {
		return SaveConfigPreserving(config, configPath)
	}

	return saveConfigClean(config, configPath)
}

// relativizeFiles returns a copy of config in which each binary's File path
// that lives under configDir is rewritten relative to configDir — the inverse
// of the join performed in LoadConfigFromPath. The input config and its
// binaries are never mutated; unchanged binaries are shared by pointer.
//
// LoadConfigFromPath joins relative `file:` values against configDir; when
// configPath itself is relative (e.g. `--config ./custom/b.yaml` or the
// `.bin/b.yaml` default) the joined path stays relative, so we cannot gate on
// filepath.IsAbs here. filepath.Rel handles both cases: it relativizes a path
// under configDir and errors (or yields a `..`/`.` prefix) for anything that
// isn't — including a user-supplied absolute path against a relative configDir
// — which we skip, leaving such values untouched.
func relativizeFiles(config *State, configDir string) *State {
	if config == nil {
		return nil
	}
	out := *config // shallow copy; Envs/Profiles are untouched and shared
	bins := make(BinaryList, len(config.Binaries))
	for i, b := range config.Binaries {
		if b == nil || b.File == "" {
			bins[i] = b
			continue
		}
		rel, err := filepath.Rel(configDir, b.File)
		if err != nil || rel == "." || rel == ".." ||
			strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			// Outside (or equal to) the config dir — leave it as-is.
			bins[i] = b
			continue
		}
		clone := *b
		clone.File = rel
		bins[i] = &clone
	}
	out.Binaries = bins
	return &out
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
