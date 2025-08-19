// Package state provides state management for b
package state

import (
	"github.com/fentas/b/pkg/binary"
)

type State struct {
	Binaries BinaryList `yaml:"binaries"`
}

// MarshalYAML implements the yaml.Marshaler interface for State
func (s *State) MarshalYAML() (interface{}, error) {
	binaries, err := s.Binaries.MarshalYAML()
	if err != nil {
		return nil, err
	}

	return map[string]interface{}{
		"binaries": binaries,
	}, nil
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

			result[b.Name] = config
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
