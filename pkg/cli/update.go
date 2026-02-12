package cli

import (
	"fmt"
	"sync"
	"time"

	"github.com/fentas/goodies/progress"
	"github.com/fentas/goodies/templates"
	"github.com/spf13/cobra"

	"github.com/fentas/b/pkg/binary"
	"github.com/fentas/b/pkg/env"
	"github.com/fentas/b/pkg/gitcache"
	"github.com/fentas/b/pkg/lock"
)

// UpdateOptions holds options for the update command
type UpdateOptions struct {
	*SharedOptions
	specifiedArgs []string // args from CLI (binary names or env refs)
}

// NewUpdateCmd creates the update subcommand
func NewUpdateCmd(shared *SharedOptions) *cobra.Command {
	o := &UpdateOptions{
		SharedOptions: shared,
	}

	cmd := &cobra.Command{
		Use:     "update [binary|env...]",
		Aliases: []string{"u"},
		Short:   "Update binaries and env files",
		Long:    "Update binaries and env files. If no arguments are given, updates all from b.yaml.",
		Example: templates.Examples(`
			# Update all binaries and envs from b.yaml
			b update

			# Update specific binary
			b update jq

			# Update specific env
			b update github.com/org/infra

			# Force update (overwrite existing)
			b update --force kubectl
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

// Complete sets up the update operation
func (o *UpdateOptions) Complete(args []string) error {
	if err := o.ValidateBinaryPath(); err != nil {
		return err
	}

	if len(args) == 0 {
		// Update all from config
		if o.Config == nil {
			return fmt.Errorf("no b.yaml configuration found and no binaries specified")
		}
		return nil
	}

	o.specifiedArgs = args

	// Validate specified args (binaries or env refs)
	for _, arg := range args {
		name, version := parseBinaryArg(arg)

		// Check if it's an env ref
		if o.Config != nil && o.Config.Envs.Get(name) != nil {
			continue
		}

		// Check if it's a binary
		if _, ok := o.GetBinary(name); !ok {
			return fmt.Errorf("unknown binary or env: %s", name)
		}
		if version != "" {
			if b, ok := o.GetBinary(name); ok {
				b.Version = version
			}
		}
	}

	return nil
}

// Validate checks if the update operation is valid
func (o *UpdateOptions) Validate() error {
	return nil
}

// Run executes the update operation
func (o *UpdateOptions) Run() error {
	if len(o.specifiedArgs) > 0 {
		return o.runSpecified()
	}
	return o.runAll()
}

// runAll updates all binaries and envs from config.
func (o *UpdateOptions) runAll() error {
	// Update binaries
	binariesToUpdate := o.GetBinariesFromConfig()
	if len(binariesToUpdate) > 0 {
		if err := o.updateBinaries(binariesToUpdate); err != nil {
			return err
		}
	}

	// Update envs
	if o.Config != nil && len(o.Config.Envs) > 0 {
		if err := o.updateEnvs(nil); err != nil {
			return err
		}
	}

	if len(binariesToUpdate) == 0 && (o.Config == nil || len(o.Config.Envs) == 0) {
		fmt.Fprintln(o.IO.Out, "No binaries or envs to update")
	}

	return nil
}

// runSpecified updates only the specified binaries/envs.
func (o *UpdateOptions) runSpecified() error {
	var binariesToUpdate []*binary.Binary
	var envRefs []string

	for _, arg := range o.specifiedArgs {
		name, _ := parseBinaryArg(arg)

		if o.Config != nil && o.Config.Envs.Get(name) != nil {
			envRefs = append(envRefs, name)
			continue
		}

		if b, ok := o.GetBinary(name); ok {
			binariesToUpdate = append(binariesToUpdate, b)
		}
	}

	if len(binariesToUpdate) > 0 {
		if err := o.updateBinaries(binariesToUpdate); err != nil {
			return err
		}
	}

	if len(envRefs) > 0 {
		if err := o.updateEnvs(envRefs); err != nil {
			return err
		}
	}

	return nil
}

// updateEnvs updates env entries from config. If refs is nil, updates all.
func (o *UpdateOptions) updateEnvs(refs []string) error {
	if o.Config == nil {
		return nil
	}

	lockDir := o.LockDir()
	projectRoot := lockDir
	lk, err := lock.ReadLock(lockDir)
	if err != nil {
		return err
	}

	for _, entry := range o.Config.Envs {
		if refs != nil {
			found := false
			for _, r := range refs {
				if entry.Key == r {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}

		label := gitcache.RefLabel(entry.Key)
		ref := gitcache.RefBase(entry.Key)

		cfg := env.EnvConfig{
			Ref:      ref,
			Label:    label,
			Version:  entry.Version,
			Ignore:   entry.Ignore,
			Strategy: entry.Strategy,
			Files:    entry.Files,
		}

		lockEntry := lk.FindEnv(ref, label)

		// For update: clear the version pin to get latest commit
		// (ResolveRef with empty version resolves HEAD)
		if cfg.Version == "" {
			// Already will resolve HEAD
		}

		result, err := env.SyncEnv(cfg, projectRoot, "", lockEntry)
		if err != nil {
			fmt.Fprintf(o.IO.ErrOut, "  %-40s ✗ %v\n", entry.Key, err)
			continue
		}

		if result.Skipped {
			fmt.Fprintf(o.IO.Out, "  %-40s %s\n", entry.Key, result.Message)
			continue // don't overwrite lock entry when up-to-date
		}

		// Check for local changes that were overwritten (replace strategy)
		if lockEntry != nil {
			o.reportLocalChanges(lockEntry, result)
		}
		fmt.Fprintf(o.IO.Out, "  %-40s %s → %s (%s)\n", entry.Key, shortCommit(result.PreviousCommit), shortCommit(result.Commit), result.Message)
		for _, f := range result.Files {
			fmt.Fprintf(o.IO.Out, "    → %-36s ✓ replaced\n", f.Dest)
		}

		lk.UpsertEnv(lock.EnvEntry{
			Ref:            result.Ref,
			Label:          result.Label,
			Version:        result.Version,
			Commit:         result.Commit,
			PreviousCommit: result.PreviousCommit,
			Files:          result.Files,
		})
	}

	return lock.WriteLock(lockDir, lk, ">=5.0.0")
}

// reportLocalChanges warns about files that had local changes before being overwritten.
func (o *UpdateOptions) reportLocalChanges(lockEntry *lock.EnvEntry, result *env.SyncResult) {
	projectRoot := o.LockDir()
	for _, oldFile := range lockEntry.Files {
		// Check if local file SHA differs from lock (local changes)
		localPath := oldFile.Dest
		if localPath != "" && localPath[0] != '/' {
			localPath = projectRoot + "/" + localPath
		}
		hash, err := lock.SHA256File(localPath)
		if err != nil {
			continue
		}
		if hash != oldFile.SHA256 {
			fmt.Fprintf(o.IO.ErrOut, "    ⚠ %-36s (local changes overwritten)\n", oldFile.Dest)
		}
	}
}

// updateBinaries updates the specified binaries with progress tracking
func (o *UpdateOptions) updateBinaries(binaries []*binary.Binary) error {
	wg := sync.WaitGroup{}
	pw := progress.NewWriter(progress.StyleDownload, o.IO.Out)
	pw.Style().Visibility.Percentage = true
	go pw.Render()
	defer pw.Stop()

	for _, b := range binaries {
		wg.Add(1)

		go func(b *binary.Binary) {
			defer wg.Done()

			tracker := pw.AddTracker(fmt.Sprintf("Updating %s", b.Name), 0)
			b.Tracker = tracker
			b.Writer = pw

			var err error
			if o.Force {
				err = b.DownloadBinary()
			} else {
				err = b.EnsureBinary(true) // Force update
			}

			progress.ProgressDone(
				b.Tracker,
				fmt.Sprintf("%s updated", b.Name),
				err,
			)
		}(b)
	}

	wg.Wait()
	time.Sleep(200 * time.Millisecond)
	return nil
}
