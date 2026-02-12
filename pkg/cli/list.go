package cli

import (
	"fmt"
	"sync"

	"github.com/fentas/goodies/templates"
	"github.com/spf13/cobra"

	"github.com/fentas/b/pkg/binary"
	"github.com/fentas/b/pkg/gitcache"
	"github.com/fentas/b/pkg/lock"
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
		Short:   "List project binaries and envs",
		Long:    "Lists all binaries and envs defined in the project's b.yaml and their status",
		Example: templates.Examples(`
			# List all binaries and envs from b.yaml
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

	if err := o.IO.Print(locals); err != nil {
		return err
	}

	// List envs
	if len(o.Config.Envs) > 0 {
		o.listEnvs()
	}

	return nil
}

// listEnvs lists configured envs with their lock status.
func (o *ListOptions) listEnvs() {
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

		commit := ""
		fileCount := 0
		if lockEntry != nil {
			commit = shortCommit(lockEntry.Commit)
			fileCount = len(lockEntry.Files)
		}

		if commit != "" {
			fmt.Fprintf(o.IO.Out, "  %-40s %s @ %s (%d files)\n", entry.Key, version, commit, fileCount)
		} else {
			fmt.Fprintf(o.IO.Out, "  %-40s %s (not synced)\n", entry.Key, version)
		}

		// Show file destinations
		if lockEntry != nil {
			for _, f := range lockEntry.Files {
				fmt.Fprintf(o.IO.Out, "    → %s\n", f.Dest)
			}
		} else if entry.Files != nil {
			for glob, cfg := range entry.Files {
				dest := cfg.Dest
				if dest == "" {
					dest = "(preserve path)"
				}
				fmt.Fprintf(o.IO.Out, "    %s → %s\n", glob, dest)
			}
		}
	}
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
