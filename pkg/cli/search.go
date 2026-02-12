package cli

import (
	"fmt"
	"strings"

	"github.com/fentas/goodies/templates"
	"github.com/spf13/cobra"

	"github.com/fentas/b/pkg/binary"
	"github.com/fentas/b/pkg/provider"
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
		Long: `Discovers all binaries available for installation. Can be filtered with a query.

In addition to pre-packaged binaries, you can install any GitHub/GitLab release:
  b install github.com/org/repo
  b install gitlab.com/org/repo

Or sync environment files from any git repository:
  b install github.com/org/repo:/path/** dest`,
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

	if err := o.IO.Print(results); err != nil {
		return err
	}

	// If query looks like a provider ref, show a hint
	if o.Query != "" && provider.IsProviderRef(o.Query) {
		fmt.Fprintf(o.IO.Out, "\n  Tip: %q looks like a provider ref. Install it directly:\n", o.Query)
		fmt.Fprintf(o.IO.Out, "    b install %s\n", o.Query)
		return nil
	}

	// Show hint about provider refs when no results found
	if len(results) == 0 && o.Query != "" {
		fmt.Fprintf(o.IO.Out, "\n  No pre-packaged binaries match %q.\n", o.Query)
		fmt.Fprintf(o.IO.Out, "  You can install any GitHub/GitLab release directly:\n")
		fmt.Fprintf(o.IO.Out, "    b install github.com/org/%s\n", o.Query)
	}

	return nil
}
