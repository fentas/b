package cli

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/fatih/color"
	"github.com/fentas/goodies/progress"
	"github.com/fentas/goodies/templates"
	"github.com/spf13/cobra"

	"github.com/fentas/b/pkg/binary"
	"github.com/fentas/b/pkg/lock"
	"github.com/fentas/b/pkg/path"
	"github.com/fentas/b/pkg/state"
)

// InstallOptions holds options for the install command
type InstallOptions struct {
	*SharedOptions
	Add               bool             // Add to b.yaml during install
	Fix               bool             // Pin version in b.yaml
	Alias             string           // Alias for the binary
	specifiedBinaries []*binary.Binary // Binaries specified on command line
}

// NewInstallCmd creates the install subcommand
func NewInstallCmd(shared *SharedOptions) *cobra.Command {
	o := &InstallOptions{
		SharedOptions: shared,
	}

	cmd := &cobra.Command{
		Use:     "install [binary...]",
		Aliases: []string{"i"},
		Short:   "Install binaries",
		Long:    "Install binaries. If no arguments are given, installs all binaries from b.yaml",
		Example: templates.Examples(`
			# Install all binaries from b.yaml
			b install

			# Install specific binary
			b install jq

			# Install specific version
			b install jq@1.7

			# Install and add to b.yaml
			b install --add kubectl

			# Force install (overwrite existing)
			b install --force jq

			# Install with alias
			b install --alias envsubst renvsubst
		`),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := o.Complete(args); err != nil {
				return err
			}
			if err := o.Validate(); err != nil {
				return err
			}
			return o.Run()
		},
	}

	cmd.Flags().BoolVar(&o.Add, "add", false, "Add binary to b.yaml during install")
	cmd.Flags().BoolVar(&o.Fix, "fix", false, "Pin the specified version in b.yaml")
	cmd.Flags().StringVar(&o.Alias, "alias", "", "Alias for the binary")
	return cmd
}

// Complete sets up the install operation
func (o *InstallOptions) Complete(args []string) error {
	if err := o.ValidateBinaryPath(); err != nil {
		return err
	}

	if len(args) == 0 {
		// Install all from config
		if o.Config == nil {
			return fmt.Errorf("no b.yaml configuration found and no binaries specified")
		}
		return nil
	}

	// Validate specified binaries and check version availability
	for _, arg := range args {
		name, version := parseBinaryArg(arg)
		b, ok := o.GetBinary(name)
		if !ok {
			return fmt.Errorf("unknown binary: %s\n  Hint: use a provider ref like github.com/org/repo to install any release", name)
		}

		// Set version if specified (CLI version overrides config/detected)
		if version != "" {
			b.Version = version
		}

		b.Alias = o.Alias
		o.specifiedBinaries = append(o.specifiedBinaries, b)
	}

	return nil
}

// Validate checks if the install operation is valid
func (o *InstallOptions) Validate() error {
	return nil
}

// Run executes the install operation
func (o *InstallOptions) Run() error {
	var binariesToInstall []*binary.Binary

	if len(o.specifiedBinaries) > 0 {
		// Install only specified binaries (do NOT install others)
		binariesToInstall = o.specifiedBinaries
	} else {
		// Install all from config
		binariesToInstall = o.GetBinariesFromConfig()
	}

	if len(binariesToInstall) == 0 {
		fmt.Fprintln(o.IO.Out, "No binaries to install")
		return nil
	}

	// Install binaries
	if err := o.installBinaries(binariesToInstall); err != nil {
		return err
	}

	// Update b.lock
	if err := o.updateLock(binariesToInstall); err != nil {
		fmt.Fprintf(o.IO.ErrOut, "Warning: failed to update b.lock: %v\n", err)
	}

	// Add to config if requested
	if o.Add {
		return o.addToConfig(binariesToInstall)
	}

	return nil
}

// installBinaries installs the specified binaries with progress tracking
func (o *InstallOptions) installBinaries(binaries []*binary.Binary) error {
	wg := sync.WaitGroup{}
	pw := progress.NewWriter(progress.StyleDownload, o.IO.Out)
	pw.Style().Visibility.Percentage = true
	go pw.Render()
	defer pw.Stop()

	for _, b := range binaries {
		wg.Add(1)

		go func(b *binary.Binary) {
			defer wg.Done()

			tracker := pw.AddTracker(fmt.Sprintf("Installing %s", b.Name), 0)
			b.Tracker = tracker
			b.Writer = pw

			var err error
			if o.Force {
				err = b.DownloadBinary()
			} else {
				err = b.EnsureBinary(false) // Don't update, just ensure
			}

			name := b.Name
			if b.Alias != "" {
				name = b.Alias + " (" + color.New(color.FgYellow).Sprint(b.Name) + ")"
			}
			progress.ProgressDone(
				b.Tracker,
				name,
				err,
			)
		}(b)
	}

	wg.Wait()
	// Let the progress bar render
	time.Sleep(200 * time.Millisecond)
	return nil
}

// addToConfig adds binaries to the configuration file
func (o *InstallOptions) addToConfig(binaries []*binary.Binary) error {
	configPath := o.ConfigPath
	if configPath == "" {
		configPath = path.GetDefaultConfigPath()
	}

	// Load existing config or create new one
	config := o.Config
	if config == nil {
		config = &state.State{}
	}

	// Add binaries to config
	for _, b := range binaries {
		// Use provider ref as the config key if auto-detected
		configName := b.Name
		if b.AutoDetect && b.ProviderRef != "" {
			configName = b.ProviderRef
		}

		// Check if already exists
		found := false
		for i, existing := range config.Binaries {
			if existing.Name == configName {
				// Update version only if we have a specific version
				if b.Version != "" && b.Version != "latest" {
					config.Binaries[i].Version = b.Version
					if o.Fix {
						config.Binaries[i].Enforced = b.Version
					}
				}
				found = true
				break
			}
		}

		if !found {
			entry := &binary.LocalBinary{
				Name: configName,
			}
			// Only set version if it's not "latest" or empty
			if b.Version != "" && b.Version != "latest" {
				entry.Version = b.Version
				if o.Fix {
					entry.Enforced = b.Version
				}
			}
			if b.Alias != "" {
				entry.Name = b.Alias
				entry.Alias = configName
			}
			config.Binaries = append(config.Binaries, entry)
		}
	}

	return state.SaveConfig(config, configPath)
}

// updateLock updates b.lock with installed binary checksums
func (o *InstallOptions) updateLock(binaries []*binary.Binary) error {
	lockDir := o.LockDir()
	lk, err := lock.ReadLock(lockDir)
	if err != nil {
		return err
	}

	for _, b := range binaries {
		if b.File == "" {
			continue
		}
		hash, err := lock.SHA256File(b.File)
		if err != nil {
			continue
		}
		entry := lock.BinEntry{
			Name:    b.Name,
			Version: b.Version,
			SHA256:  hash,
		}
		if b.AutoDetect {
			entry.Source = b.ProviderRef
			entry.Provider = b.ProviderType
		} else {
			entry.Preset = true
			if b.GitHubRepo != "" {
				entry.Source = "github.com/" + b.GitHubRepo
			}
		}
		lk.UpsertBinary(entry)
	}

	return lock.WriteLock(lockDir, lk, ">=5.0.0")
}

// parseBinaryArg parses binary argument in format "name" or "name@version"
func parseBinaryArg(arg string) (name, version string) {
	parts := strings.SplitN(arg, "@", 2)
	name = parts[0]
	if len(parts) > 1 {
		version = parts[1]
	}
	return
}
