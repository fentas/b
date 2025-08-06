package cli

import (
	"strings"

	"github.com/fentas/goodies/templates"
	"github.com/spf13/cobra"

	"github.com/fentas/b/pkg/binary"
)

// SearchOptions holds options for the search command
type SearchOptions struct {
	*SharedOptions
	Query string
}

// NewSearchCmd creates the search subcommand
func NewSearchCmd(shared *SharedOptions) *cobra.Command {
	o := &SearchOptions{
		SharedOptions: shared,
	}

	cmd := &cobra.Command{
		Use:     "search [query]",
		Aliases: []string{"s"},
		Short:   "Search available binaries",
		Long:    "Discovers all binaries available for installation. Can be filtered with a query.",
		Example: templates.Examples(`
			# List all available binaries
			b search

			# Search for specific binaries
			b search jq

			# Search as JSON
			b search --output json
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

// Complete sets up the search operation
func (o *SearchOptions) Complete(args []string) error {
	if len(args) > 0 {
		o.Query = strings.Join(args, " ")
	}
	return nil
}

// Validate checks if the search operation is valid
func (o *SearchOptions) Validate() error {
	return nil
}

// Run executes the search operation
func (o *SearchOptions) Run() error {
	var results []*binary.Binary

	// Filter binaries based on query
	for _, b := range o.Binaries {
		if o.Query == "" || strings.Contains(strings.ToLower(b.Name), strings.ToLower(o.Query)) {
			results = append(results, b)
		}
	}

	return o.IO.Print(results)
}
