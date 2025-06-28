package state

import (
	"github.com/fentas/b/pkg/binary"
)

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

func (list *BinaryList) Get(name string) *binary.LocalBinary {
	for _, b := range *list {
		if b.Name == name {
			return b
		}
	}
	return nil
}
