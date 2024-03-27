package state

import (
	"os"

	"github.com/buyoio/b/pkg/binary"

	"gopkg.in/yaml.v2"
)

func LoadConfig() (*BinaryList, error) {
	path := binary.GetBinaryPath()
	if path == "" {
		return nil, nil
	}
	file := path + "/b.yaml"
	if _, err := os.Stat(file); err != nil {
		return nil, nil
	}
	config, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var list BinaryList
	if err := yaml.Unmarshal(config, &list); err != nil {
		return nil, err
	}
	return &list, nil
}
