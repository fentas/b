package cli

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/fentas/b/pkg/binary"
	"github.com/fentas/b/pkg/path"
	"github.com/fentas/b/pkg/provider"
	"github.com/fentas/b/pkg/state"
	"github.com/fentas/goodies/streams"
)

// SharedOptions contains common options used across subcommands
type SharedOptions struct {
	IO       *streams.IO
	Binaries []*binary.Binary
	Config   *state.State

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

// ApplyQuietMode redirects output to discard if quiet mode is enabled
func (o *SharedOptions) ApplyQuietMode() {
	if o.Quiet {
		o.IO.Out = io.Discard
	}
}

// LoadConfig loads the configuration file with enhanced discovery
func (o *SharedOptions) LoadConfig() error {
	o.ApplyQuietMode()

	var err error
	if o.ConfigPath != "" {
		o.Config, err = state.LoadConfigFromPath(o.ConfigPath)
	} else {
		o.Config, err = state.LoadConfig()
	}

	return err
}

// resolveBinary resolves a binary from config, handling references
func (o *SharedOptions) resolveBinary(lb *binary.LocalBinary) (*binary.Binary, bool) {
	b := &binary.Binary{}
	var ab *binary.Binary
	var ok bool

	// Handle reference field - if a binary references another, use the referenced binary
	if lb.Alias != "" {
		if ab, ok = o.lookup[lb.Alias]; ok {
			*b = *ab
			b.Alias = lb.Name
		}
	} else {
		if ab, ok = o.lookup[lb.Name]; ok {
			*b = *ab
		}
	}

	if ok {
		// Apply config overrides
		if lb.Version != "" {
			b.Version = lb.Version
		}
		if lb.Enforced != "" {
			b.Version = lb.Enforced
		}
		if lb.File != "" {
			b.File = lb.File
		}
	}

	return b, ok
}

// GetBinary returns a binary by name or provider ref.
func (o *SharedOptions) GetBinary(name string) (*binary.Binary, bool) {
	// First try direct lookup (preset)
	if b, ok := o.lookup[name]; ok {
		return b, ok
	}

	// If not found and we have config, check if this is a reference alias
	if o.Config != nil {
		for _, lb := range o.Config.Binaries {
			if lb.Name == name {
				return o.resolveBinary(lb)
			}
		}
	}

	// Check if this is a provider ref (e.g. github.com/derailed/k9s)
	if provider.IsProviderRef(name) {
		ref, version := provider.ParseRef(name)
		p, err := provider.Detect(ref)
		if err != nil {
			return nil, false
		}
		b := &binary.Binary{
			Name:         provider.BinaryName(ref),
			Version:      version,
			AutoDetect:   true,
			ProviderRef:  ref,
			ProviderType: p.Name(),
			VersionF: func(b *binary.Binary) (string, error) {
				return p.LatestVersion(ref)
			},
		}
		return b, true
	}

	return nil, false
}

// GetBinariesFromConfig returns binaries that are defined in the config
func (o *SharedOptions) GetBinariesFromConfig() []*binary.Binary {
	if o.Config == nil {
		return nil
	}

	var result []*binary.Binary
	for _, lb := range o.Config.Binaries {
		if lb.IsProviderRef {
			// Provider ref from config — create auto-detect Binary
			b, ok := o.GetBinary(lb.Name)
			if !ok {
				fmt.Fprintf(o.IO.ErrOut, "Warning: no provider matched '%s', skipping.\n", lb.Name)
				continue
			}
			// Apply config overrides
			if lb.Version != "" {
				b.Version = lb.Version
			}
			if lb.Enforced != "" {
				b.Version = lb.Enforced
			}
			if lb.File != "" {
				b.File = lb.File
			}
			if lb.Alias != "" {
				b.Alias = lb.Alias
			}
			result = append(result, b)
		} else if b, ok := o.resolveBinary(lb); ok {
			result = append(result, b)
		} else {
			fmt.Fprintf(o.IO.ErrOut, "Warning: referenced binary '%s' could not be resolved and will be skipped.\n", lb.Name)
		}
	}

	return result
}

// LockDir returns the directory where b.lock lives — next to b.yaml.
func (o *SharedOptions) LockDir() string {
	if o.ConfigPath != "" {
		return filepath.Dir(o.ConfigPath)
	}
	if p, _ := path.FindConfigFile(); p != "" {
		return filepath.Dir(p)
	}
	return filepath.Dir(path.GetDefaultConfigPath())
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
