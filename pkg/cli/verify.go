package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/fentas/b/pkg/env"
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

// msgOutside is what b verify says for a b.lock row whose file it does not
// read (lockFiles).
const msgOutside = "outside the project, the b.yaml directory and the binary directory (not read)"

// Run executes the verify operation
func (o *VerifyOptions) Run() error {
	lk, err := o.readLock()
	if err != nil {
		return err
	}

	if len(lk.Binaries) == 0 && len(lk.Envs) == 0 {
		fmt.Fprintln(o.IO.Out, "No entries in b.lock — nothing to verify.")
		return nil
	}

	failures := 0

	// Verify binaries. b reads only the regular file of a row inside the
	// project, the directory of b.yaml or the binary directory, or a file
	// that a b.yaml entry installs (lockFiles). A dangling symlink, a
	// symlink loop or a path through a file is a missing file.
	files := o.lockFiles()
	for _, entry := range lk.Binaries {
		binPath := path.GetBinaryPath()
		if binPath == "" {
			fmt.Fprintf(o.IO.Out, "  %-40s ? (no binary path)\n", entry.Name)
			failures++
			continue
		}
		filePath, state, err := files.find(entry.Name)
		switch state {
		case rowFileMissing:
			fmt.Fprintf(o.IO.Out, "  %-40s ✗ missing\n", entry.Name)
			failures++
			continue
		case rowFileOutside:
			fmt.Fprintf(o.IO.Out, "  %-40s ✗ %s\n", entry.Name, msgOutside)
			failures++
			continue
		case rowFileUnknown:
			fmt.Fprintf(o.IO.Out, "  %-40s ✗ %v\n", entry.Name, err)
			failures++
			continue
		}
		hash, err := regularFileSHA256(filePath)
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

	// Env file dests are stored relative to the project root (the base SyncEnv
	// writes against — pkg/env/env.go), NOT the config dir. Resolving against
	// LockDir made `b verify` report every env file "missing" on the default
	// .bin/ layout, where lockDir (.bin) differs from the project root.
	projectRoot := o.ProjectRoot()

	// Verify env files
	for _, envEntry := range lk.Envs {
		label := ""
		if envEntry.Label != "" {
			label = "#" + envEntry.Label
		}
		fmt.Fprintf(o.IO.Out, "  %s%s\n", envEntry.Ref, label)
		for _, f := range envEntry.Files {
			destPath := f.Dest
			if !filepath.IsAbs(destPath) {
				destPath = filepath.Join(projectRoot, destPath)
			}
			destPath = filepath.Clean(destPath)
			// Refuse to read paths that escape the project root — a malicious or
			// hand-edited lock must not make verify stat arbitrary files. Matches
			// the guard in env status/remove/resolve.
			if err := env.ValidatePathUnderRoot(projectRoot, destPath); err != nil {
				fmt.Fprintf(o.IO.Out, "    %-38s ✗ escapes project root\n", f.Dest)
				failures++
				continue
			}
			if _, err := os.Stat(destPath); os.IsNotExist(err) {
				fmt.Fprintf(o.IO.Out, "    %-38s ✗ missing\n", f.Dest)
				failures++
				continue
			}
			// Read only a regular file, without blocking on a FIFO.
			hash, err := fileSHA256(destPath)
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
		return fmt.Errorf("%d artifact(s) differ from lock", failures)
	}
	fmt.Fprintln(o.IO.Out, "\nAll artifacts verified ✓")
	return nil
}
