package state

// No imports needed since we're using the config discovery from config.go

func LoadConfig() (*BinaryList, error) {
	return LoadConfigWithDiscovery()
}
