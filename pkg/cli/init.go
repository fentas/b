package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/fentas/goodies/templates"
	"github.com/spf13/cobra"

	"github.com/fentas/b/pkg/binary"
	"github.com/fentas/b/pkg/path"
	"github.com/fentas/b/pkg/state"
)

// InitOptions holds options for the init command
type InitOptions struct {
	*SharedOptions
}

// NewInitCmd creates the init subcommand
func NewInitCmd(shared *SharedOptions) *cobra.Command {
	o := &InitOptions{
		SharedOptions: shared,
	}

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create new b.yaml config",
		Long:  "Creates a new .bin/b.yaml configuration file in the current directory (ENV Variables have precedence)",
		Example: templates.Examples(`
			# Create new b.yaml in current directory
			b init

			# Create with custom path
			b init --config ./custom/b.yaml
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

// Complete sets up the init operation
func (o *InitOptions) Complete(args []string) error {
	return nil
}

// Validate checks if the init operation is valid
func (o *InitOptions) Validate() error {
	return nil
}

// Run executes the init operation
func (o *InitOptions) Run() error {
	configPath := o.ConfigPath
	if configPath == "" {
		configPath = path.GetDefaultConfigPath()
	}

	// Check if config already exists
	if _, err := os.Stat(configPath); err == nil {
		if !o.Force {
			return fmt.Errorf("configuration file already exists at %s (use --force to overwrite)", configPath)
		}
	}

	// Create config with self-reference to 'b'
	if err := o.createConfigWithSelfReference(configPath); err != nil {
		return fmt.Errorf("failed to create configuration file: %w", err)
	}

	if !o.Quiet {
		fmt.Fprintf(o.IO.Out, "Created configuration file: %s\n", configPath)
	}

	// Create additional project files if this is a new/empty directory
	if err := o.createProjectFiles(); err != nil {
		return fmt.Errorf("failed to create project files: %w", err)
	}

	return nil
}

// createConfigWithSelfReference creates a b.yaml config with self-reference to 'b'
func (o *InitOptions) createConfigWithSelfReference(configPath string) error {
	// Ensure directory exists
	dir := filepath.Dir(configPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	// Create config with self-reference to 'b'
	config := &state.BinaryList{
		&binary.LocalBinary{
			Name: "b",
		},
	}

	return state.SaveConfig(config, configPath)
}

// createProjectFiles creates additional project files (.gitignore, .envrc) if needed
func (o *InitOptions) createProjectFiles() error {
	configPath := o.ConfigPath
	if configPath == "" {
		configPath = path.GetDefaultConfigPath()
	}

	// Create .gitignore in the same directory as b.yaml
	if err := o.createGitignore(filepath.Dir(configPath)); err != nil {
		return err
	}

	// Create .envrc for direnv integration in current directory
	if err := o.createEnvrc(); err != nil {
		return err
	}

	return nil
}

// createGitignore creates a .gitignore file in the specified directory
func (o *InitOptions) createGitignore(dir string) error {
	gitignorePath := filepath.Join(dir, ".gitignore")

	// Check if .gitignore already exists
	if _, err := os.Stat(gitignorePath); err == nil {
		if !o.Quiet {
			fmt.Fprintf(o.IO.Out, ".gitignore already exists, skipping\n")
		}
		return nil
	}

	gitignoreContent := `# Binary directory - ignore all binaries but keep config
*
!.gitignore
!b.yaml
!b
`

	if err := os.WriteFile(gitignorePath, []byte(gitignoreContent), 0644); err != nil {
		return err
	}

	if !o.Quiet {
		fmt.Fprintf(o.IO.Out, "Created .gitignore\n")
	}

	return nil
}

// createEnvrc creates a .envrc file for direnv integration
func (o *InitOptions) createEnvrc() error {
	envrcPath := ".envrc"

	// Check if .envrc already exists
	if _, err := os.Stat(envrcPath); err == nil {
		if !o.Quiet {
			fmt.Fprintf(o.IO.Out, ".envrc already exists. Add '${PATH_BIN:-\"${PATH_BASE}/.bin\"}' to your PATH\n")
		}
		return nil
	}

	// Check if direnv is installed
	if !o.isDirenvInstalled() {
		if !o.Quiet {
			fmt.Fprintf(o.IO.Out, "direnv not installed. Consider installing it for automatic environment setup\n")
		}
		return nil
	}

	envrcContent := `#!/usr/bin/env bash
# Automatically set up development environment with direnv
set -euo pipefail

: "${PATH_BASE:=\"$(git rev-parse --show-toplevel)\"}"
: "${PATH_BIN:=\"${PATH_BASE}/.bin\"}"
export PATH_BASE PATH_BIN

# shellcheck disable=SC2120
path::add() {
  command -v PATH_add 1>/dev/null || {
    _error "This can be only run within direnv"
    return
  }
  PATH_add "${1}"
}

export::env() {
  local env="${PATH_BASE}/${1}"
  # shellcheck disable=SC2046
  [ ! -f "${env}" ] || {

    export $(grep -v '^#' "${env}" | sed -E 's/\s+=\s+/=/g' | xargs -d '\n')
    
    # You can use the following to source the env file
    # Only use if you trust the env files
    #
    # if head -n 1 "${env}" | grep -q "bash"; then
    #   source "${env}"
    # else
    #   set -a
    #   source "${env}"
    #   set +a
    # fi

    ! command -v watch_file &>/dev/null ||
      watch_file "${env}"
  }
}

copy::template() {
  local -r file="${PATH_BASE}/${1}"
  [ -f "${file}" ] || {
    cp "${file}.template" "${file}" 2>/dev/null || :
  }
}

main() {
  # Use this if you want a template file with example values
  # make sure to ignore .env and .secrets in your .gitignore
  copy::template .env
  copy::template .secrets

  # This will load the environment variables from the .env and .secrets files
  export::env .env
  export::env .secrets

  # Make your binaries available in the PATH
  # This is handy to make sure that all developers have the same binaries available
  path::add "${PATH_BIN:-\"${PATH_BASE}/.bin\"}"
}

# Run the main function if this file is sourced by direnv
[ -z "${DIRENV_IN_ENVRC}" ] || main "${@}"
`

	if err := os.WriteFile(envrcPath, []byte(envrcContent), 0644); err != nil {
		return err
	}

	if !o.Quiet {
		fmt.Fprintf(o.IO.Out, "Created .envrc for direnv integration\n")
	}

	return nil
}

// isDirenvInstalled checks if direnv is available in the system
func (o *InitOptions) isDirenvInstalled() bool {
	_, err := exec.LookPath("direnv")
	return err == nil
}
