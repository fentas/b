package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/fentas/b/pkg/lock"
	"github.com/fentas/b/pkg/path"
	"github.com/fentas/goodies/templates"
	"github.com/spf13/cobra"
)

// VerifyOptions holds options for the verify command
type VerifyOptions struct {
	*SharedOptions
}

// NewVerifyCmd creates the verify subcommand
func NewVerifyCmd(shared *SharedOptions) *cobra.Command {
	o := &VerifyOptions{
		SharedOptions: shared,
	}

	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Verify installed binaries and env files against b.lock",
		Long:  "Check every managed artifact against b.lock checksums. Exit 0 if clean, 1 if mismatch.",
		Example: templates.Examples(`
			# Verify all managed artifacts
			b verify
		`),
		RunE: func(cmd *cobra.Command, args []string) error {
			return o.Run()
		},
	}

	return cmd
}

// Run executes the verify operation
func (o *VerifyOptions) Run() error {
	dir := o.LockDir()

	lk, err := lock.ReadLock(dir)
	if err != nil {
		return fmt.Errorf("reading b.lock: %w", err)
	}

	if len(lk.Binaries) == 0 && len(lk.Envs) == 0 {
		fmt.Fprintln(o.IO.Out, "No entries in b.lock — nothing to verify.")
		return nil
	}

	failures := 0

	// Verify binaries
	for _, entry := range lk.Binaries {
		binPath := path.GetBinaryPath()
		if binPath == "" {
			fmt.Fprintf(o.IO.Out, "  %-40s ? (no binary path)\n", entry.Name)
			failures++
			continue
		}
		filePath := filepath.Join(binPath, entry.Name)
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			fmt.Fprintf(o.IO.Out, "  %-40s ✗ missing\n", entry.Name)
			failures++
			continue
		}
		hash, err := lock.SHA256File(filePath)
		if err != nil {
			fmt.Fprintf(o.IO.Out, "  %-40s ✗ %v\n", entry.Name, err)
			failures++
			continue
		}
		if hash != entry.SHA256 {
			fmt.Fprintf(o.IO.Out, "  %-40s ✗ sha256 mismatch\n", entry.Name)
			failures++
		} else {
			fmt.Fprintf(o.IO.Out, "  %-40s ✓\n", entry.Name)
		}
	}

	// Verify env files (Phase 2)
	for _, env := range lk.Envs {
		fmt.Fprintf(o.IO.Out, "  %s\n", env.Ref)
		for _, f := range env.Files {
			if _, err := os.Stat(f.Dest); os.IsNotExist(err) {
				fmt.Fprintf(o.IO.Out, "    %-38s ✗ missing\n", f.Dest)
				failures++
				continue
			}
			hash, err := lock.SHA256File(f.Dest)
			if err != nil {
				fmt.Fprintf(o.IO.Out, "    %-38s ✗ %v\n", f.Dest, err)
				failures++
				continue
			}
			if hash != f.SHA256 {
				fmt.Fprintf(o.IO.Out, "    %-38s ✗ sha256 mismatch (local changes)\n", f.Dest)
				failures++
			} else {
				fmt.Fprintf(o.IO.Out, "    %-38s ✓\n", f.Dest)
			}
		}
	}

	if failures > 0 {
		fmt.Fprintf(o.IO.Out, "\nFAIL: %d artifact(s) differ from lock\n", failures)
		os.Exit(1)
	}
	fmt.Fprintln(o.IO.Out, "\nAll artifacts verified ✓")
	return nil
}
