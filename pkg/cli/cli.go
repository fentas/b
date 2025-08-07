// Package cli provides a CLI for managing binaries
package cli

import (
	"io"
	"os"

	"github.com/fentas/goodies/cmdutil"
	"github.com/fentas/goodies/output"
	"github.com/fentas/goodies/streams"
	"github.com/fentas/goodies/templates"

	"github.com/fentas/b/pkg/binary"
	"github.com/fentas/b/pkg/path"
	"github.com/fentas/b/pkg/state"

	"github.com/spf13/cobra"
)

type CmdBinaryOptions struct {
	IO       *streams.IO
	Binaries []*binary.Binary
	NoConfig bool
	config   *state.BinaryList

	// Flags
	all       bool
	available bool
	ensure    map[*binary.Binary]*bool
	lookup    map[string]*binary.Binary
	force     bool
	update    bool
	install   bool
	check     bool
	quiet     bool
}

func NewCmdBinary(options *CmdBinaryOptions) *cobra.Command {
	if options == nil {
		options = &CmdBinaryOptions{}
	}
	options.ensure = make(map[*binary.Binary]*bool)
	options.lookup = make(map[string]*binary.Binary)
	for _, b := range options.Binaries {
		options.ensure[b] = new(bool)
		options.lookup[b.Name] = b
	}

	configExample := ""
	if !options.NoConfig {
		configExample = " and defined in b.yaml"
	}
	cmd := &cobra.Command{
		Use:   "b [flags] [...binaries]",
		Short: "Manage all binaries",
		Long:  "Ensure that all binaries needed are installed and up to date",
		PreRunE: func(cmd *cobra.Command, args []string) error {
			if options.quiet {
				options.IO.Out = io.Discard
			}

			path := path.GetBinaryPath()
			if path == "" {
				return cmdutil.UsageErrorf(cmd, "Could not find a suitable path to install binaries")
			}

			if !options.NoConfig {
				var err error
				options.config, err = state.LoadConfig()
				return err
			}

			return nil
		},
		Example: templates.Examples(`
			# Initialize a new project with b.yaml config
			b init

			# List all configured binaries` + configExample + `
			b list
			b ls

			# Install specific binaries
			b install jq
			b i kubectl helm

			# Install and add binary to config
			b install --add jq@1.7

			# Update all binaries
			b update
			b u

			# Update specific binaries
			b update jq kubectl

			# Search for available binaries
			b search terraform
			b s kube

			# Show version
			b version
			b v kind

			# Request a new binary
			b request
		`),
		Run: func(cmd *cobra.Command, args []string) {
			cmdutil.CheckErr(options.Complete(cmd, args))
			cmdutil.CheckErr(options.Validate(cmd))
			cmdutil.CheckErr(options.Run())
		},
	}
	options.AddFlags(cmd)
	output.AddFlag(cmd, output.OptionJSON(), output.OptionYAML(), output.OptionFormat())

	return cmd
}

func (o *CmdBinaryOptions) AddFlags(cmd *cobra.Command) {
	all := "Binaries installed and defined in b.yaml"
	if o.NoConfig {
		all = "All binaries"
	} else {
		cmd.Flags().BoolVar(&o.available, "list", false, "List all available binaries")
	}
	cmd.Flags().BoolVarP(&o.all, "all", "a", false, all)
	cmd.Flags().BoolVarP(&o.force, "force", "f", false, "Force download, overwriting existing binaries")
	cmd.Flags().BoolVarP(&o.update, "upgrade", "u", false, "Upgrade if already installed")
	cmd.Flags().BoolVarP(&o.install, "install", "i", false, "Install if not installed")
	cmd.Flags().BoolVarP(&o.check, "check", "c", false, "Check if binary is up to date")
	cmd.Flags().BoolVarP(&o.quiet, "quiet", "q", false, "Quiet mode")
}

func (o *CmdBinaryOptions) Complete(cmd *cobra.Command, args []string) error {
	if o.available {
		return nil
	}

	if len(args) > 0 {
		if o.all {
			return cmdutil.UsageErrorf(cmd, "Cannot use --all with arguments")
		}

		for _, arg := range args {
			b, ok := o.lookup[arg]
			if !ok {
				return cmdutil.UsageErrorf(cmd, "Unknown binary %s", arg)
			}
			*o.ensure[b] = true
		}
	}

	if o.config != nil {
		for _, lb := range *o.config {
			for b, do := range o.ensure {
				if lb.Name == b.Name {
					b.Version = lb.Version

					if o.all {
						*do = true
					}
					break
				}
			}
		}
	} else if o.all {
		for b, do := range o.ensure {
			if o.NoConfig || b.BinaryExists() {
				*do = true
			}
		}
	}

	return nil
}

func (o *CmdBinaryOptions) Validate(cmd *cobra.Command) error {
	if cmd.Flags().NFlag() == 0 {
		return cmdutil.UsageErrorf(cmd, "At least one flag must be set")
	}
	return nil
}

func (o *CmdBinaryOptions) Run() error {
	if o.available {
		return o.IO.Print(o.Binaries)
	}
	if o.install {
		return o.installBinaries()
	}
	out, err := o.lookupLocals()
	if err != nil {
		return err
	}
	notUpToDate := make([]*binary.LocalBinary, 0)
	if o.check {
		for _, b := range out {
			if b.Version == "" || b.Version != b.Latest {
				notUpToDate = append(notUpToDate, b)
			}
		}
		if len(notUpToDate) > 0 {
			o.IO.Print(notUpToDate)
			os.Exit(1)
		}
		return nil
	}

	return o.IO.Print(out)
}
