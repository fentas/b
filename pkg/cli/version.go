package cli

import (
	"fmt"
	"os"
	"sync"

	"github.com/fentas/goodies/templates"
	"github.com/spf13/cobra"

	"github.com/fentas/b/pkg/binary"
	"github.com/fentas/b/pkg/gitcache"
	"github.com/fentas/b/pkg/lock"
)

// VersionOptions holds options for the version command
type VersionOptions struct {
	*SharedOptions
	Local bool     // Only show local versions
	Check bool     // Check if versions are up to date (exit code based)
	args  []string // Parsed arguments from Complete
}

// NewVersionCmd creates the version subcommand
func NewVersionCmd(shared *SharedOptions) *cobra.Command {
	o := &VersionOptions{
		SharedOptions: shared,
	}

	cmd := &cobra.Command{
		Use:     "version [binary...]",
		Aliases: []string{"v"},
		Short:   "Show version information",
		Long:    "List all versions. If arguments are given, it shows the version of the specified binaries.",
		Example: templates.Examples(`
			# Show all versions
			b version

			# Show specific binary version
			b version jq

			# Show multiple binary versions
			b version jq kubectl helm

			# Show only local versions
			b version --local

			# Check if versions are up to date (exit code based)
			b version --quiet --check

			# Check specific binaries
			b version jq kubectl --check
		`),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := o.Complete(args); err != nil {
				return err
			}
			if err := o.Validate(); err != nil {
				return err
			}
			// If quiet mode is enabled, automatically enable check mode
			if o.Quiet {
				o.Check = true
			}
			return o.Run()
		},
	}

	cmd.Flags().BoolVar(&o.Local, "local", false, "Only show local versions, no lookup for new versions")
	cmd.Flags().BoolVar(&o.Check, "check", false, "Check if versions are up to date (exit code based)")

	return cmd
}

// Complete sets up the version operation
func (o *VersionOptions) Complete(args []string) error {
	// Validate all specified binaries exist
	for _, name := range args {
		if _, ok := o.GetBinary(name); !ok {
			return fmt.Errorf("unknown binary: %s", name)
		}
	}

	// Store the parsed arguments for use in Run
	o.args = args
	return nil
}

// Validate checks if the version operation is valid
func (o *VersionOptions) Validate() error {
	return nil
}

// Run executes the version operation
func (o *VersionOptions) Run() error {
	var binariesToCheck []*binary.Binary

	if len(o.args) > 0 {
		// Specific binaries requested
		for _, name := range o.args {
			if b, ok := o.GetBinary(name); ok {
				binariesToCheck = append(binariesToCheck, b)
			}
		}
	} else {
		// All binaries from config or all available
		if o.Config != nil {
			binariesToCheck = o.GetBinariesFromConfig()
		} else {
			binariesToCheck = o.Binaries
		}
	}

	// Get version information
	locals, err := o.getVersionInfo(binariesToCheck)
	if err != nil {
		return err
	}

	// Check mode - exit with error if any binary is not up to date
	if o.Check {
		notUpToDate := make([]*binary.LocalBinary, 0)
		for _, l := range locals {
			// Skip if version is pinned (enforced)
			if l.Enforced != "" && l.Enforced != "latest" {
				if l.Enforced != l.Version {
					notUpToDate = append(notUpToDate, l)
				}
				continue
			}
			if l.Version == "" || (l.Latest != "" && l.Version != l.Latest) {
				notUpToDate = append(notUpToDate, l)
			}
		}
		if len(notUpToDate) > 0 {
			o.IO.Print(notUpToDate)
			os.Exit(1)
		}
		return nil
	}

	if err := o.IO.Print(locals); err != nil {
		return err
	}

	// Show env versions (when no specific args or args match envs)
	if o.Config != nil && len(o.Config.Envs) > 0 && len(o.args) == 0 {
		o.showEnvVersions()
	}

	return nil
}

// showEnvVersions displays version information for configured envs.
func (o *VersionOptions) showEnvVersions() {
	lk, _ := lock.ReadLock(o.LockDir())

	fmt.Fprintln(o.IO.Out, "\nEnvironments:")
	for _, entry := range o.Config.Envs {
		label := gitcache.RefLabel(entry.Key)
		ref := gitcache.RefBase(entry.Key)

		lockEntry := lk.FindEnv(ref, label)

		version := entry.Version
		if version == "" {
			version = "(HEAD)"
		}

		if lockEntry != nil {
			fmt.Fprintf(o.IO.Out, "  %-40s %s (pinned: %s)\n", entry.Key, version, shortCommit(lockEntry.Commit))
		} else {
			fmt.Fprintf(o.IO.Out, "  %-40s %s (not synced)\n", entry.Key, version)
		}
	}
}

// getVersionInfo gets version information for the specified binaries
func (o *VersionOptions) getVersionInfo(binaries []*binary.Binary) ([]*binary.LocalBinary, error) {
	wg := sync.WaitGroup{}
	ch := make(chan *binary.LocalBinary, len(binaries))

	for _, b := range binaries {
		wg.Add(1)
		go func(b *binary.Binary) {
			defer wg.Done()
			ch <- b.LocalBinary(!o.Local)
		}(b)
	}

	go func() {
		wg.Wait()
		close(ch)
	}()

	var locals []*binary.LocalBinary
	for l := range ch {
		locals = append(locals, l)
	}

	return locals, nil
}
