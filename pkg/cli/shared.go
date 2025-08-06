package cli

import (
	"io"

	"github.com/fentas/b/pkg/binary"
	"github.com/fentas/b/pkg/path"
	"github.com/fentas/b/pkg/state"
	"github.com/fentas/goodies/streams"
)

// SharedOptions contains common options used across subcommands
type SharedOptions struct {
	IO       *streams.IO
	Binaries []*binary.Binary
	Config   *state.BinaryList

	// Global flags
	ConfigPath string
	Force      bool
	Quiet      bool
	Output     string

	// Internal
	lookup map[string]*binary.Binary
}

// NewSharedOptions creates a new SharedOptions with default values
func NewSharedOptions(io *streams.IO, binaries []*binary.Binary) *SharedOptions {
	opts := &SharedOptions{
		IO:       io,
		Binaries: binaries,
		lookup:   make(map[string]*binary.Binary),
	}

	// Build lookup map
	for _, b := range binaries {
		opts.lookup[b.Name] = b
	}

	return opts
}

// LoadConfig loads the configuration file with enhanced discovery
func (o *SharedOptions) LoadConfig() error {
	if o.Quiet {
		o.IO.Out = io.Discard
	}

	var err error
	if o.ConfigPath != "" {
		o.Config, err = state.LoadConfigFromPath(o.ConfigPath)
	} else {
		o.Config, err = state.LoadConfig()
	}

	return err
}

// GetBinary returns a binary by name
func (o *SharedOptions) GetBinary(name string) (*binary.Binary, bool) {
	b, ok := o.lookup[name]
	return b, ok
}

// GetBinariesFromConfig returns binaries that are defined in the config
func (o *SharedOptions) GetBinariesFromConfig() []*binary.Binary {
	if o.Config == nil {
		return nil
	}

	var result []*binary.Binary
	for _, lb := range *o.Config {
		if b, ok := o.lookup[lb.Name]; ok {
			// Set version from config
			b.Version = lb.Version
			result = append(result, b)
		}
	}

	return result
}

// ValidateBinaryPath ensures we have a valid binary installation path
func (o *SharedOptions) ValidateBinaryPath() error {
	path := path.GetBinaryPath()
	if path == "" {
		return ErrNoBinaryPath
	}
	return nil
}

// getConfigPath returns the current config path being used
func (o *SharedOptions) getConfigPath() (string, error) {
	if o.ConfigPath != "" {
		return o.ConfigPath, nil
	}
	return path.FindConfigFile()
}
