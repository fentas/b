package cli

import (
	"fmt"
	"net/url"
	"os/exec"
	"runtime"

	"github.com/fentas/goodies/templates"
	"github.com/spf13/cobra"
)

// RequestOptions holds options for the request command
type RequestOptions struct {
	*SharedOptions
	BinaryName string
}

// NewRequestCmd creates the request subcommand
func NewRequestCmd(shared *SharedOptions) *cobra.Command {
	o := &RequestOptions{
		SharedOptions: shared,
	}

	cmd := &cobra.Command{
		Use:   "request <binary-name>",
		Short: "Request a binary",
		Long:  "Request a binary by creating a GitHub issue with a prefilled template",
		Example: templates.Examples(`
			# Request a new binary
			b request terraform

			# Request with description
			b request "my-custom-tool"
		`),
		Args: cobra.ExactArgs(1),
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

// Complete sets up the request operation
func (o *RequestOptions) Complete(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("exactly one binary name must be specified")
	}
	o.BinaryName = args[0]
	return nil
}

// Validate checks if the request operation is valid
func (o *RequestOptions) Validate() error {
	if o.BinaryName == "" {
		return fmt.Errorf("binary name cannot be empty")
	}
	return nil
}

// Run executes the request operation
func (o *RequestOptions) Run() error {
	// Create GitHub issue URL with prefilled template
	issueURL := o.createIssueURL()

	if !o.Quiet {
		fmt.Fprintf(o.IO.Out, "Opening GitHub issue for binary request: %s\n", o.BinaryName)
		fmt.Fprintf(o.IO.Out, "URL: %s\n", issueURL)
	}

	// Open URL in default browser
	return o.openURL(issueURL)
}

// createIssueURL creates a GitHub issue URL with prefilled template
func (o *RequestOptions) createIssueURL() string {
	baseURL := "https://github.com/fentas/b/issues/new"

	// Use GitHub issue template with prefilled binary name
	params := url.Values{}
	params.Add("template", "binary-request.yml")
	params.Add("title", fmt.Sprintf("Binary Request: %s", o.BinaryName))
	params.Add("labels", "request")

	// Pre-fill the binary name field if the template supports it
	// GitHub will use this to populate the form field
	params.Add("binary-name", o.BinaryName)

	return fmt.Sprintf("%s?%s", baseURL, params.Encode())
}

// openURL opens a URL in the default browser
func (o *RequestOptions) openURL(url string) error {
	var cmd string
	var args []string

	switch runtime.GOOS {
	case "windows":
		cmd = "cmd"
		args = []string{"/c", "start"}
	case "darwin":
		cmd = "open"
	default: // "linux", "freebsd", "openbsd", "netbsd"
		cmd = "xdg-open"
	}
	args = append(args, url)

	return exec.Command(cmd, args...).Start()
}
