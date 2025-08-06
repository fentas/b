package cli

import (
	"sync"

	"github.com/fentas/goodies/templates"
	"github.com/spf13/cobra"

	"github.com/fentas/b/pkg/binary"
)

// ListOptions holds options for the list command
type ListOptions struct {
	*SharedOptions
}

// NewListCmd creates the list subcommand
func NewListCmd(shared *SharedOptions) *cobra.Command {
	o := &ListOptions{
		SharedOptions: shared,
	}

	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls", "l"},
		Short:   "List project binaries",
		Long:    "Lists all binaries defined in the project's b.yaml and their installation status",
		Example: templates.Examples(`
			# List all binaries from b.yaml
			b list

			# List as JSON
			b list --output json

			# List as YAML
			b list --output yaml
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

// Complete sets up the list operation
func (o *ListOptions) Complete(args []string) error {
	return nil
}

// Validate checks if the list operation is valid
func (o *ListOptions) Validate() error {
	return nil
}

// Run executes the list operation
func (o *ListOptions) Run() error {
	if o.Config == nil {
		return o.IO.Print([]*binary.LocalBinary{})
	}

	// Get local binary information
	locals, err := o.lookupLocals()
	if err != nil {
		return err
	}

	return o.IO.Print(locals)
}

// lookupLocals gets local binary information for all configured binaries
func (o *ListOptions) lookupLocals() ([]*binary.LocalBinary, error) {
	binariesFromConfig := o.GetBinariesFromConfig()

	wg := sync.WaitGroup{}
	ch := make(chan *binary.LocalBinary, len(binariesFromConfig))

	for _, b := range binariesFromConfig {
		wg.Add(1)
		go func(b *binary.Binary) {
			defer wg.Done()
			ch <- b.LocalBinary(true)
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
