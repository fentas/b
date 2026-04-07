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
	Profiles EnvList    `yaml:"profiles,omitempty"` // short-name profiles for upstream repos
}

// EnvEntry is a single env in b.yaml.
type EnvEntry struct {
	Key         string                         `yaml:"-"` // map key: in envs, full ref with optional label (e.g. "github.com/org/infra" or "github.com/org/infra#label"); in profiles, short name (e.g. "base")
	Description string                         `yaml:"description,omitempty"`
	Includes    []string                       `yaml:"includes,omitempty"` // compose from other profiles
	Version     string                         `yaml:"version,omitempty"`
	Ignore      []string                       `yaml:"ignore,omitempty"`
	Strategy    string                         `yaml:"strategy,omitempty"`
	Safety      string                         `yaml:"safety,omitempty"` // strict | prompt (default) | auto — see issue #125
	Group       string                         `yaml:"group,omitempty"`
	OnPreSync   string                         `yaml:"onPreSync,omitempty"`
	OnPostSync  string                         `yaml:"onPostSync,omitempty"`
	Files       map[string]envmatch.GlobConfig `yaml:"-"` // populated via custom EnvList unmarshal
}

// Safety levels for env updates. The default is SafetyPrompt.
const (
	// SafetyStrict refuses to apply any destructive change (overwrite,
	// delete, conflict). The plan is printed; if it contains any
	// destructive row the sync exits non-zero with no changes written.
	SafetyStrict = "strict"
	// SafetyPrompt prints the plan and asks the user to confirm before
	// applying. On non-TTY (CI/CD) it falls back to strict behavior.
	SafetyPrompt = "prompt"
	// SafetyAuto applies the plan without prompting. Equivalent to the
	// pre-#125 default behavior. Use only when you trust the upstream.
	SafetyAuto = "auto"
)

// NormalizeSafety returns the canonical safety value. Empty string and
// unknown values both fall back to SafetyPrompt — the safe default.
func NormalizeSafety(s string) string {
	switch s {
	case SafetyStrict, SafetyPrompt, SafetyAuto:
		return s
	default:
		return SafetyPrompt
	}
}

// EnvList is a list of env entries parsed from the envs map.
type EnvList []*EnvEntry

func (list *EnvList) UnmarshalYAML(unmarshal func(interface{}) error) error {
	// Parse as a typed map and convert to a slice.
	var raw map[string]*envEntryRaw
	if err := unmarshal(&raw); err != nil {
		return err
	}
	var data []*EnvEntry
	for key, r := range raw {
		e := &EnvEntry{Key: key}
		if r != nil {
			e.Description = r.Description
			e.Includes = r.Includes
			e.Version = r.Version
			e.Ignore = r.Ignore
			e.Strategy = r.Strategy
			e.Safety = r.Safety
			e.Group = r.Group
			e.OnPreSync = r.OnPreSync
			e.OnPostSync = r.OnPostSync
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
		if e.Description != "" {
			cfg["description"] = e.Description
		}
		if len(e.Includes) > 0 {
			cfg["includes"] = e.Includes
		}
		if e.Version != "" {
			cfg["version"] = e.Version
		}
		if len(e.Ignore) > 0 {
			cfg["ignore"] = e.Ignore
		}
		if e.Strategy != "" && e.Strategy != "replace" {
			cfg["strategy"] = e.Strategy
		}
		// Omit safety when it's the default ("prompt" or empty) so b.yaml
		// stays terse for the common case.
		if e.Safety != "" && e.Safety != SafetyPrompt {
			cfg["safety"] = e.Safety
		}
		if e.Group != "" {
			cfg["group"] = e.Group
		}
		if e.OnPreSync != "" {
			cfg["onPreSync"] = e.OnPreSync
		}
		if e.OnPostSync != "" {
			cfg["onPostSync"] = e.OnPostSync
		}
		if len(e.Files) > 0 {
			files := make(map[string]interface{})
			for glob, gc := range e.Files {
				if gc.Dest == "" && len(gc.Ignore) == 0 && len(gc.Select) == 0 {
					files[glob] = nil // bare key
				} else if len(gc.Ignore) == 0 && len(gc.Select) == 0 && gc.Dest != "" {
					files[glob] = gc.Dest // string shorthand
				} else {
					obj := map[string]interface{}{}
					if gc.Dest != "" {
						obj["dest"] = gc.Dest
					}
					if len(gc.Ignore) > 0 {
						obj["ignore"] = gc.Ignore
					}
					if len(gc.Select) > 0 {
						obj["select"] = gc.Select
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

// Remove removes the env entry with the given key. Returns true if found.
func (list *EnvList) Remove(key string) bool {
	for i, e := range *list {
		if e.Key == key {
			*list = append((*list)[:i], (*list)[i+1:]...)
			return true
		}
	}
	return false
}

// envEntryRaw is used for YAML unmarshaling before converting files map.
type envEntryRaw struct {
	Description string                 `yaml:"description,omitempty"`
	Includes    []string               `yaml:"includes,omitempty"`
	Version     string                 `yaml:"version,omitempty"`
	Ignore      []string               `yaml:"ignore,omitempty"`
	Strategy    string                 `yaml:"strategy,omitempty"`
	Safety      string                 `yaml:"safety,omitempty"`
	Group       string                 `yaml:"group,omitempty"`
	OnPreSync   string                 `yaml:"onPreSync,omitempty"`
	OnPostSync  string                 `yaml:"onPostSync,omitempty"`
	Files       map[string]interface{} `yaml:"files,omitempty"`
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
		case map[string]interface{}:
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
			if sel, ok := val["select"]; ok {
				if selList, ok := sel.([]interface{}); ok {
					for _, item := range selList {
						gc.Select = append(gc.Select, fmt.Sprintf("%v", item))
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

	if len(s.Profiles) > 0 {
		profiles, err := s.Profiles.MarshalYAML()
		if err != nil {
			return nil, err
		}
		result["profiles"] = profiles
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

			// Add asset filter if specified
			if b.Asset != "" {
				config["asset"] = b.Asset
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
