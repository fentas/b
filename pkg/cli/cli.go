package cli

import (
	"os"

	"github.com/buyoio/goodies/cmdutil"
	"github.com/buyoio/goodies/output"
	"github.com/buyoio/goodies/streams"
	"github.com/buyoio/goodies/templates"

	"github.com/buyoio/b/pkg/binary"
	"github.com/buyoio/b/pkg/state"

	"github.com/spf13/cobra"
)

type CmdBinaryOptions struct {
	IO       *streams.IO
	Binaries []*binary.Binary
	config   *state.BinaryList

	// Flags
	all       bool
	available bool
	ensure    map[*binary.Binary]*bool
	force     bool
	install   bool
	check     bool
}

func NewCmdBinary(options *CmdBinaryOptions) *cobra.Command {
	if options == nil {
		options = &CmdBinaryOptions{}
	}
	options.ensure = make(map[*binary.Binary]*bool)
	for _, b := range options.Binaries {
		options.ensure[b] = new(bool)
	}

	cmd := &cobra.Command{
		Use:   "b",
		Short: "Manage all binaries",
		Long:  "Ensure that all binaries needed are installed and up to date",
		PreRunE: func(cmd *cobra.Command, args []string) error {
			path := binary.GetBinaryPath()
			if path == "" {
				return cmdutil.UsageErrorf(cmd, "Could not find a suitable path to install binaries")
			}
			var err error
			options.config, err = state.LoadConfig()
			return err
		},
		Example: templates.Examples(`
			# List all installed binaries and defined in b.yaml
			b --all

			# Print as JSON
			b -ao json

			# Install all binaries
			b -a --install

			# Install or update jq
			b -iu --jq

			# Upgrade all binaries
			b -aiu

			# List all available binaries
			b --list

			# Checks (silent) if all binaries are up to date
			b -acq || echo "Some binaries are not up to date"
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
	cmd.Flags().BoolVarP(&o.all, "all", "a", false, "Binaries installed and defined in b.yaml")
	for _, b := range o.Binaries {
		cmd.Flags().BoolVar(o.ensure[b], b.Name, false, b.Name+" binary")
	}

	cmd.Flags().BoolVarP(&o.force, "upgrade", "u", false, "Upgrade if already installed")
	cmd.Flags().BoolVarP(&o.install, "install", "i", false, "Install if not installed")
	cmd.Flags().BoolVar(&o.available, "list", false, "List all available binaries")
	cmd.Flags().BoolVarP(&o.check, "check", "c", false, "Check if binary is up to date")
}

func (o *CmdBinaryOptions) Complete(cmd *cobra.Command, args []string) error {
	if o.available {
		return nil
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
			if b.BinaryExists() {
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
