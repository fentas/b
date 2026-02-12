package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/fentas/goodies/templates"
	"github.com/spf13/cobra"

	"github.com/fentas/b/pkg/gitcache"
)

// CacheOptions holds options for the cache command
type CacheOptions struct {
	*SharedOptions
}

// NewCacheCmd creates the cache subcommand with subcommands
func NewCacheCmd(shared *SharedOptions) *cobra.Command {
	o := &CacheOptions{
		SharedOptions: shared,
	}

	cmd := &cobra.Command{
		Use:   "cache",
		Short: "Manage the local git cache",
		Long:  "Manage the local git cache used for env file syncing.",
	}

	cmd.AddCommand(newCacheCleanCmd(o))
	cmd.AddCommand(newCachePathCmd(o))

	return cmd
}

// newCacheCleanCmd creates the cache clean subcommand
func newCacheCleanCmd(o *CacheOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "clean",
		Short: "Remove cached git repositories",
		Long:  "Remove all cached git repositories used for env file syncing.",
		Example: templates.Examples(`
			# Remove all cached repos
			b cache clean
		`),
		RunE: func(cmd *cobra.Command, args []string) error {
			return o.runClean()
		},
	}
}

// newCachePathCmd creates the cache path subcommand
func newCachePathCmd(o *CacheOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "Print the cache directory path",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(o.IO.Out, gitcache.DefaultCacheRoot())
			return nil
		},
	}
}

// runClean removes the git cache directory and reports freed space.
func (o *CacheOptions) runClean() error {
	cacheRoot := gitcache.DefaultCacheRoot()

	// Compute size before removal
	size, err := dirSize(cacheRoot)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintln(o.IO.Out, "Cache is already clean (nothing to remove)")
			return nil
		}
		return fmt.Errorf("reading cache: %w", err)
	}

	if err := os.RemoveAll(cacheRoot); err != nil {
		return fmt.Errorf("removing cache: %w", err)
	}

	fmt.Fprintf(o.IO.Out, "Removed %s (%s freed)\n", cacheRoot, formatSize(size))
	return nil
}

// dirSize returns the total size in bytes of all files in a directory tree.
func dirSize(path string) (int64, error) {
	var total int64
	err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total, err
}

// formatSize formats bytes as a human-readable string.
func formatSize(bytes int64) string {
	const (
		kb = 1024
		mb = kb * 1024
		gb = mb * 1024
	)
	switch {
	case bytes >= gb:
		return fmt.Sprintf("%.1f GB", float64(bytes)/float64(gb))
	case bytes >= mb:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(mb))
	case bytes >= kb:
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(kb))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}
