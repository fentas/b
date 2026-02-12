package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/fentas/goodies/output"
	"github.com/fentas/goodies/streams"
	"github.com/spf13/cobra"

	"github.com/fentas/b/pkg/binary"
)

// NewRootCmd creates the new root command with subcommands
func NewRootCmd(binaries []*binary.Binary, io *streams.IO, version, versionPreRelease string) *cobra.Command {
	shared := NewSharedOptions(io, binaries)

	cmd := &cobra.Command{
		Use:   "b",
		Short: "Manage all binaries",
		Long:  "A tool to manage binary installations and updates 🧙",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// Handle version flag at root level
			if cmd.Flags().Changed("version") {
				v := version
				if versionPreRelease != "" {
					v = fmt.Sprintf("%s-%s", version, versionPreRelease)
				}
				fmt.Printf("%s", v)
				os.Exit(0)
			}

			// Load configuration for all subcommands
			return shared.LoadConfig()
		},
		SilenceUsage:  true,
		SilenceErrors: true,
		Run: func(cmd *cobra.Command, args []string) {
			// Show help when no subcommand is provided
			_ = cmd.Help()
		},
	}

	// Global flags
	configHelp := "Path to configuration file (default: auto-discover b.yaml)"
	if configPath, err := shared.getConfigPath(); err == nil && configPath != "" {
		// Make path relative to current directory
		if relPath, err := filepath.Rel(".", configPath); err == nil {
			configHelp = fmt.Sprintf("Path to configuration file (current: %s)", relPath)
		} else {
			configHelp = fmt.Sprintf("Path to configuration file (current: %s)", configPath)
		}
	}
	cmd.PersistentFlags().StringVarP(&shared.ConfigPath, "config", "c", "", configHelp)
	cmd.PersistentFlags().BoolVar(&shared.Force, "force", false, "Force operations, overwriting existing binaries")
	cmd.PersistentFlags().BoolVarP(&shared.Quiet, "quiet", "q", false, "Quiet mode")
	cmd.PersistentFlags().BoolP("version", "v", false, "Print version information and quit")

	// Add output format flag
	output.AddFlag(cmd, output.OptionJSON(), output.OptionYAML(), output.OptionFormat())

	// Add subcommands
	cmd.AddCommand(NewInstallCmd(shared))
	cmd.AddCommand(NewUpdateCmd(shared))
	cmd.AddCommand(NewListCmd(shared))
	cmd.AddCommand(NewSearchCmd(shared))
	cmd.AddCommand(NewInitCmd(shared))
	cmd.AddCommand(NewVersionCmd(shared))
	cmd.AddCommand(NewRequestCmd(shared))
	cmd.AddCommand(NewVerifyCmd(shared))
	cmd.AddCommand(NewCacheCmd(shared))

	// Set custom usage template to show aliases in command list
	cmd.SetUsageTemplate(getUsageTemplate())

	return cmd
}

// getUsageTemplate returns a custom usage template that shows aliases
func getUsageTemplate() string {
	return `Usage:{{if .Runnable}}
  {{.UseLine}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} [command]{{end}}{{if .HasExample}}

Examples:
{{.Example}}{{end}}{{if .HasAvailableSubCommands}}

Available Commands:{{range .Commands}}{{if .IsAvailableCommand}}
  {{$cmdName := .Name}}{{range .Aliases}}{{$cmdName = printf "%s, %s" $cmdName .}}{{end}}{{rpad $cmdName .NamePadding }}     {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

Flags:
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

Global Flags:
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasHelpSubCommands}}

Additional help topics:{{range .Commands}}{{if .IsAdditionalHelpTopicCommand}}
  {{rpad .CommandPath .CommandPathPadding}} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableSubCommands}}

Use "{{.CommandPath}} [command] --help" for more information about a command.{{end}}
`
}

// Execute runs the root command
func Execute(binaries []*binary.Binary, io *streams.IO, version, versionPreRelease string) error {
	root := NewRootCmd(binaries, io, version, versionPreRelease)
	return root.Execute()
}
