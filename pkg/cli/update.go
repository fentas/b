package cli

import (
	"fmt"
	"sync"
	"time"

	"github.com/fentas/goodies/progress"
	"github.com/fentas/goodies/templates"
	"github.com/spf13/cobra"

	"github.com/fentas/b/pkg/binary"
)

// UpdateOptions holds options for the update command
type UpdateOptions struct {
	*SharedOptions
}

// NewUpdateCmd creates the update subcommand
func NewUpdateCmd(shared *SharedOptions) *cobra.Command {
	o := &UpdateOptions{
		SharedOptions: shared,
	}

	cmd := &cobra.Command{
		Use:     "update [binary...]",
		Aliases: []string{"u"},
		Short:   "Update binaries",
		Long:    "Update binaries. If no arguments are given, updates all binaries from b.yaml",
		Example: templates.Examples(`
			# Update all binaries from b.yaml
			b update

			# Update specific binary
			b update jq

			# Force update (overwrite existing)
			b update --force kubectl
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

	return cmd
}

// Complete sets up the update operation
func (o *UpdateOptions) Complete(args []string) error {
	if err := o.ValidateBinaryPath(); err != nil {
		return err
	}

	if len(args) == 0 {
		// Update all from config
		if o.Config == nil {
			return fmt.Errorf("no b.yaml configuration found and no binaries specified")
		}
		return nil
	}

	// Validate specified binaries
	for _, arg := range args {
		name, version := parseBinaryArg(arg)
		if _, ok := o.GetBinary(name); !ok {
			return fmt.Errorf("unknown binary: %s", name)
		}
		// Set version if specified
		if version != "" {
			if b, ok := o.GetBinary(name); ok {
				b.Version = version
			}
		}
	}

	return nil
}

// Validate checks if the update operation is valid
func (o *UpdateOptions) Validate() error {
	return nil
}

// Run executes the update operation
func (o *UpdateOptions) Run() error {
	var binariesToUpdate []*binary.Binary

	if len(o.Binaries) == 0 {
		// Update all from config
		binariesToUpdate = o.GetBinariesFromConfig()
	} else {
		// Update specified binaries
		binariesToUpdate = append(binariesToUpdate, o.Binaries...)
	}

	if len(binariesToUpdate) == 0 {
		if !o.Quiet {
			fmt.Fprintln(o.IO.Out, "No binaries to update")
		}
		return nil
	}

	// Update binaries
	return o.updateBinaries(binariesToUpdate)
}

// updateBinaries updates the specified binaries with progress tracking
func (o *UpdateOptions) updateBinaries(binaries []*binary.Binary) error {
	wg := sync.WaitGroup{}
	pw := progress.NewWriter(progress.StyleDownload, o.IO.Out)
	pw.Style().Visibility.Percentage = true
	go pw.Render()
	defer pw.Stop()

	for _, b := range binaries {
		wg.Add(1)

		go func(b *binary.Binary) {
			defer wg.Done()

			tracker := pw.AddTracker(fmt.Sprintf("Updating %s", b.Name), 0)
			b.Tracker = tracker
			b.Writer = pw

			var err error
			if o.Force {
				err = b.DownloadBinary()
			} else {
				err = b.EnsureBinary(true) // Force update
			}

			progress.ProgressDone(
				b.Tracker,
				fmt.Sprintf("%s updated", b.Name),
				err,
			)
		}(b)
	}

	wg.Wait()
	// Let the progress bar render
	time.Sleep(200 * time.Millisecond)
	return nil
}
