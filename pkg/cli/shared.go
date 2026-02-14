package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fatih/color"
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
	bVersion         string // version of b itself, for lock metadata
	lookup           map[string]*binary.Binary
	loadedConfigPath string // path where config was actually loaded from
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
		o.loadedConfigPath = o.ConfigPath
		o.Config, err = state.LoadConfigFromPath(o.ConfigPath)
	} else {
		// Discover and remember the path
		if p, findErr := path.FindConfigFile(); findErr == nil && p != "" {
			o.loadedConfigPath = p
		}
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
	var configEntry *binary.LocalBinary
	if o.Config != nil {
		for _, lb := range o.Config.Binaries {
			if lb.Name == name {
				if b, ok := o.resolveBinary(lb); ok {
					return b, true
				}
				configEntry = lb
				break
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
		// Apply config overrides if this ref came from config
		if configEntry != nil {
			if configEntry.Version != "" && version == "" {
				b.Version = configEntry.Version
			}
			if configEntry.Enforced != "" {
				b.Version = configEntry.Enforced
			}
			if configEntry.File != "" {
				b.File = configEntry.File
			}
			if configEntry.Asset != "" {
				b.AssetFilter = configEntry.Asset
			}
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
			if lb.Asset != "" {
				b.AssetFilter = lb.Asset
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
	if o.loadedConfigPath != "" {
		return filepath.Dir(o.loadedConfigPath)
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
	if o.loadedConfigPath != "" {
		return o.loadedConfigPath, nil
	}
	return path.FindConfigFile()
}

// defaultAssetSelector returns a SelectAssetFunc that either prompts interactively
// (TTY, non-quiet) or warns and picks the best match (quiet/non-TTY).
func defaultAssetSelector(bin *binary.Binary, quiet bool, io *streams.IO) binary.SelectAssetFunc {
	return func(candidates []provider.Scored) (*provider.Asset, error) {
		if quiet || !isTTYFunc() {
			// Non-interactive: warn and pick first (highest score)
			names := make([]string, 0, len(candidates))
			for _, c := range candidates {
				if c.Score == candidates[0].Score {
					names = append(names, c.Asset.Name)
				}
			}
			fmt.Fprintf(io.ErrOut, "Warning: %s: %d assets match with same score: %s\n",
				bin.Name, len(names), strings.Join(names, ", "))
			fmt.Fprintf(io.ErrOut, "  Hint: use --asset <glob> to select a specific asset\n")
			return candidates[0].Asset, nil
		}

		// Interactive: prompt the user to pick
		fmt.Fprintf(io.ErrOut, "\nMultiple assets match for %s. Select one:\n", color.New(color.Bold).Sprint(bin.Name))
		topScore := candidates[0].Score
		var choices []provider.Scored
		for _, c := range candidates {
			if c.Score == topScore {
				choices = append(choices, c)
			}
		}
		for i, c := range choices {
			size := formatSize(c.Asset.Size)
			fmt.Fprintf(io.ErrOut, "  [%d] %s  (%s)\n", i+1, c.Asset.Name, size)
		}
		fmt.Fprintf(io.ErrOut, "Choice [1-%d]: ", len(choices))

		var input string
		if _, err := fmt.Fscanln(os.Stdin, &input); err != nil {
			return candidates[0].Asset, nil // default to first on EOF
		}
		input = strings.TrimSpace(input)
		var idx int
		if _, err := fmt.Sscanf(input, "%d", &idx); err != nil || idx < 1 || idx > len(choices) {
			fmt.Fprintf(io.ErrOut, "Invalid choice, using %s\n", choices[0].Asset.Name)
			return choices[0].Asset, nil
		}
		return choices[idx-1].Asset, nil
	}
}

// guardedAssetSelector wraps defaultAssetSelector with a mutex so that
// concurrent goroutines never interleave interactive stdin prompts.
func guardedAssetSelector(mu *sync.Mutex, bin *binary.Binary, quiet bool, io *streams.IO) binary.SelectAssetFunc {
	inner := defaultAssetSelector(bin, quiet, io)
	return func(candidates []provider.Scored) (*provider.Asset, error) {
		mu.Lock()
		defer mu.Unlock()
		return inner(candidates)
	}
}
