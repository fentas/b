// Package state provides state management for b
package state

import (
	"fmt"
	"strings"

	"github.com/fentas/b/pkg/binary"
	"github.com/fentas/b/pkg/envmatch"
)

type State struct {
	Binaries BinaryList `yaml:"binaries"`
	Envs     EnvList    `yaml:"envs,omitempty"`
}

// EnvEntry is a single env in b.yaml.
type EnvEntry struct {
	Key      string                         `yaml:"-"` // map key (e.g. "github.com/org/infra#label")
	Version  string                         `yaml:"version,omitempty"`
	Ignore   []string                       `yaml:"ignore,omitempty"`
	Strategy string                         `yaml:"strategy,omitempty"`
	Files    map[string]envmatch.GlobConfig `yaml:"-"`               // custom unmarshal
	RawFiles map[string]interface{}         `yaml:"files,omitempty"` // for marshal roundtrip
}

// EnvList is a list of env entries parsed from the envs map.
type EnvList []*EnvEntry

func (list *EnvList) UnmarshalYAML(unmarshal func(interface{}) error) error {
	// We need ordered access, but yaml.v2 gives map[interface{}]interface{}.
	// Parse as a raw map first.
	var raw map[string]*envEntryRaw
	if err := unmarshal(&raw); err != nil {
		return err
	}
	var data []*EnvEntry
	for key, r := range raw {
		e := &EnvEntry{Key: key}
		if r != nil {
			e.Version = r.Version
			e.Ignore = r.Ignore
			e.Strategy = r.Strategy
			e.Files = parseFilesMap(r.Files)
		}
		data = append(data, e)
	}
	*list = data
	return nil
}

func (list *EnvList) MarshalYAML() (interface{}, error) {
	result := make(map[string]interface{})
	for _, e := range *list {
		cfg := make(map[string]interface{})
		if e.Version != "" {
			cfg["version"] = e.Version
		}
		if len(e.Ignore) > 0 {
			cfg["ignore"] = e.Ignore
		}
		if e.Strategy != "" && e.Strategy != "replace" {
			cfg["strategy"] = e.Strategy
		}
		if len(e.Files) > 0 {
			files := make(map[string]interface{})
			for glob, gc := range e.Files {
				if gc.Dest == "" && len(gc.Ignore) == 0 {
					files[glob] = nil // bare key
				} else if len(gc.Ignore) == 0 {
					files[glob] = gc.Dest // string shorthand
				} else {
					obj := map[string]interface{}{"dest": gc.Dest}
					if len(gc.Ignore) > 0 {
						obj["ignore"] = gc.Ignore
					}
					files[glob] = obj
				}
			}
			cfg["files"] = files
		}
		if len(cfg) > 0 {
			result[e.Key] = cfg
		} else {
			result[e.Key] = &struct{}{}
		}
	}
	return result, nil
}

// Get returns the env entry for a given key, or nil.
func (list *EnvList) Get(key string) *EnvEntry {
	for _, e := range *list {
		if e.Key == key {
			return e
		}
	}
	return nil
}

// envEntryRaw is used for YAML unmarshaling before converting files map.
type envEntryRaw struct {
	Version  string                 `yaml:"version,omitempty"`
	Ignore   []string               `yaml:"ignore,omitempty"`
	Strategy string                 `yaml:"strategy,omitempty"`
	Files    map[string]interface{} `yaml:"files,omitempty"`
}

// parseFilesMap converts the raw files map into typed GlobConfig entries.
// Values can be: null → GlobConfig{}, string → GlobConfig{Dest: s},
// map → GlobConfig{Dest: ..., Ignore: [...]}
func parseFilesMap(raw map[string]interface{}) map[string]envmatch.GlobConfig {
	if raw == nil {
		return nil
	}
	result := make(map[string]envmatch.GlobConfig, len(raw))
	for glob, v := range raw {
		switch val := v.(type) {
		case nil:
			result[glob] = envmatch.GlobConfig{}
		case string:
			result[glob] = envmatch.GlobConfig{Dest: val}
		case map[interface{}]interface{}:
			gc := envmatch.GlobConfig{}
			if d, ok := val["dest"]; ok {
				gc.Dest = fmt.Sprintf("%v", d)
			}
			if ign, ok := val["ignore"]; ok {
				if ignList, ok := ign.([]interface{}); ok {
					for _, item := range ignList {
						gc.Ignore = append(gc.Ignore, fmt.Sprintf("%v", item))
					}
				}
			}
			result[glob] = gc
		}
	}
	return result
}

// MarshalYAML implements the yaml.Marshaler interface for State
func (s *State) MarshalYAML() (interface{}, error) {
	result := make(map[string]interface{})

	binaries, err := s.Binaries.MarshalYAML()
	if err != nil {
		return nil, err
	}
	result["binaries"] = binaries

	if len(s.Envs) > 0 {
		envs, err := s.Envs.MarshalYAML()
		if err != nil {
			return nil, err
		}
		result["envs"] = envs
	}

	return result, nil
}

// UnmarshalYAML implements the yaml.Unmarshaler interface for State
func (s *State) UnmarshalYAML(unmarshal func(interface{}) error) error {
	type Alias State
	var aux Alias

	if err := unmarshal(&aux); err != nil {
		return err
	}

	*s = State(aux)
	return nil
}

type BinaryList []*binary.LocalBinary

func (list *BinaryList) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var raw map[string]*binary.LocalBinary
	if err := unmarshal(&raw); err != nil {
		return err
	}
	var data []*binary.LocalBinary
	for name, b := range raw {
		if b == nil {
			b = &binary.LocalBinary{}
		}
		b.Name = name
		// Detect provider refs: contains "/" or "://"
		if strings.Contains(name, "/") || strings.Contains(name, "://") {
			b.IsProviderRef = true
		}
		data = append(data, b)
	}
	*list = data
	return nil
}

func (list *BinaryList) MarshalYAML() (interface{}, error) {
	result := make(map[string]interface{})
	for _, b := range *list {
		if b.Name != "" {
			// Build the binary configuration
			config := make(map[string]string)

			// Add version if enforced
			if b.Enforced != "" {
				config["version"] = b.Enforced
			}

			// Add alias if specified
			if b.Alias != "" {
				config["alias"] = b.Alias
			}

			// Add file if specified
			if b.File != "" {
				config["file"] = b.File
			}

			// If we have any configuration, use it; otherwise use empty struct
			if len(config) > 0 {
				result[b.Name] = config
			} else {
				result[b.Name] = &struct{}{}
			}
		}
	}
	return result, nil
}

func (list *BinaryList) Get(name string) *binary.LocalBinary {
	for _, b := range *list {
		if b.Name == name {
			return b
		}
	}
	return nil
}
