package cli

import (
	"fmt"
	"os"
	"sync"

	"github.com/fentas/goodies/templates"
	"github.com/spf13/cobra"

	"github.com/fentas/b/pkg/binary"
)

// VersionOptions holds options for the version command
type VersionOptions struct {
	*SharedOptions
	Local bool // Only show local versions
	Check bool // Check if versions are up to date (exit code based)
}

// NewVersionCmd creates the version subcommand
func NewVersionCmd(shared *SharedOptions) *cobra.Command {
	o := &VersionOptions{
		SharedOptions: shared,
	}

	cmd := &cobra.Command{
		Use:     "version [binary]",
		Aliases: []string{"v"},
		Short:   "Show version information",
		Long:    "List all versions. If an argument is given, it just shows the version of the binary.",
		Example: templates.Examples(`
			# Show all versions
			b version

			# Show specific binary version
			b version jq

			# Show only local versions
			b version --local

			# Check if versions are up to date (exit code based)
			b version --quiet --check
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

	cmd.Flags().BoolVar(&o.Local, "local", false, "Only show local versions, no lookup for new versions")
	cmd.Flags().BoolVar(&o.Check, "check", false, "Check if versions are up to date (exit code based)")

	return cmd
}

// Complete sets up the version operation
func (o *VersionOptions) Complete(args []string) error {
	if len(args) > 1 {
		return fmt.Errorf("only one binary can be specified")
	}

	if len(args) == 1 {
		name := args[0]
		if _, ok := o.GetBinary(name); !ok {
			return fmt.Errorf("unknown binary: %s", name)
		}
	}

	return nil
}

// Validate checks if the version operation is valid
func (o *VersionOptions) Validate() error {
	return nil
}

// Run executes the version operation
func (o *VersionOptions) Run() error {
	var binariesToCheck []*binary.Binary

	if len(os.Args) > 2 && os.Args[2] != "version" && os.Args[2] != "v" {
		// Specific binary requested
		for _, arg := range os.Args[2:] {
			if b, ok := o.GetBinary(arg); ok {
				binariesToCheck = append(binariesToCheck, b)
				break
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
				continue
			}
			if l.Version == "" || (l.Latest != "" && l.Version != l.Latest) {
				notUpToDate = append(notUpToDate, l)
			}
		}
		if len(notUpToDate) > 0 {
			if !o.Quiet {
				o.IO.Print(notUpToDate)
			}
			os.Exit(1)
		}
		return nil
	}

	return o.IO.Print(locals)
}

// getVersionInfo gets version information for the specified binaries
func (o *VersionOptions) getVersionInfo(binaries []*binary.Binary) ([]*binary.LocalBinary, error) {
	wg := sync.WaitGroup{}
	ch := make(chan *binary.LocalBinary, len(binaries))

	for _, b := range binaries {
		wg.Add(1)
		go func(b *binary.Binary) {
			defer wg.Done()
			local := b.LocalBinary()
			
			// If not local-only mode, try to get latest version
			if !o.Local && b.VersionF != nil {
				if latest, err := b.VersionF(b); err == nil {
					local.Latest = latest
				}
			}
			
			ch <- local
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
