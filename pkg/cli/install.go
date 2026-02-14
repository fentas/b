package cli

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/fatih/color"
	"github.com/fentas/goodies/progress"
	"github.com/fentas/goodies/templates"
	"github.com/spf13/cobra"

	"github.com/fentas/b/pkg/binary"
	"github.com/fentas/b/pkg/env"
	"github.com/fentas/b/pkg/envmatch"
	"github.com/fentas/b/pkg/gitcache"
	"github.com/fentas/b/pkg/lock"
	"github.com/fentas/b/pkg/path"
	"github.com/fentas/b/pkg/provider"
	"github.com/fentas/b/pkg/state"

	"gopkg.in/yaml.v2"
)

// envInstall holds a parsed SCP-style env install request.
type envInstall struct {
	ref     string // e.g. "github.com/org/infra"
	label   string // fragment label
	version string // tag/branch
	glob    string // e.g. "/manifests/hetzner/**"
	dest    string // e.g. "/hetzner"
}

// InstallOptions holds options for the install command
type InstallOptions struct {
	*SharedOptions
	Add               bool             // Add to b.yaml during install
	Fix               bool             // Pin version in b.yaml
	Alias             string           // Alias for the binary
	Asset             string           // Asset filter glob pattern
	specifiedBinaries []*binary.Binary // Binaries specified on command line
	envInstalls       []envInstall     // SCP-style env installs
	configEnvRefs     []string         // env refs to sync from config
}

// NewInstallCmd creates the install subcommand
func NewInstallCmd(shared *SharedOptions) *cobra.Command {
	o := &InstallOptions{
		SharedOptions: shared,
	}

	cmd := &cobra.Command{
		Use:     "install [binary|ref:/glob dest...]",
		Aliases: []string{"i"},
		Short:   "Install binaries and sync env files",
		Long:    "Install binaries or sync env files. If no arguments are given, installs all from b.yaml.",
		Example: templates.Examples(`
			# Install all binaries and envs from b.yaml
			b install

			# Install specific binary
			b install jq

			# Install from GitHub release
			b install github.com/derailed/k9s

			# Install a specific release asset by glob pattern
			b install --asset "argsh-so-*" arg-sh/argsh

			# Install env files (SCP-style)
			b install github.com/org/infra:/manifests/hetzner/** /hetzner

			# Install env files with version
			b install github.com/org/infra@v2.0:/manifests/base/** .

			# Install + save to b.yaml
			b install --add github.com/org/infra@v2.0:/manifests/hetzner/** /hetzner
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

	cmd.Flags().BoolVar(&o.Add, "add", false, "Add binary to b.yaml during install")
	cmd.Flags().BoolVar(&o.Fix, "fix", false, "Pin the specified version in b.yaml")
	cmd.Flags().StringVar(&o.Alias, "alias", "", "Alias for the binary")
	cmd.Flags().StringVar(&o.Asset, "asset", "", "Glob pattern to filter release assets (e.g. \"argsh-so-*\")")
	return cmd
}

// Complete sets up the install operation
func (o *InstallOptions) Complete(args []string) error {
	if err := o.ValidateBinaryPath(); err != nil {
		return err
	}

	if len(args) == 0 {
		// Install all from config
		if o.Config == nil {
			return fmt.Errorf("no b.yaml configuration found and no binaries specified")
		}
		return nil
	}

	// Parse args — detect SCP-style env installs vs binary installs
	for i := 0; i < len(args); i++ {
		arg := args[i]

		// Check for SCP syntax: ref@version:/glob [dest]
		if ei, consumed, ok := parseSCPArg(arg, args[i+1:]); ok {
			o.envInstalls = append(o.envInstalls, ei)
			i += consumed // skip dest arg if consumed
			continue
		}

		// Check if this ref matches a configured env (no SCP syntax)
		refBase, _ := parseBinaryArg(arg)
		if o.Config != nil && o.Config.Envs.Get(refBase) != nil {
			o.configEnvRefs = append(o.configEnvRefs, refBase)
			continue
		}

		// Binary install
		name, version := parseBinaryArg(arg)
		b, ok := o.GetBinary(name)
		if !ok {
			// If it looks like a provider ref, check for upstream b.yaml
			if provider.IsProviderRef(name) {
				if hint := o.discoverUpstreamConfig(name); hint != "" {
					return fmt.Errorf("no releases found for %s, but the repo has a b.yaml:\n%s\n  Hint: use SCP syntax to sync files, e.g.:\n    b install %s:/<glob> <dest>", name, hint, name)
				}
			}
			return fmt.Errorf("unknown binary: %s\n  Hint: use a provider ref like github.com/org/repo to install any release\n  Hint: use ref:/glob dest for env file sync", name)
		}

		if version != "" {
			b.Version = version
		}

		b.Alias = o.Alias
		if o.Asset != "" {
			b.AssetFilter = o.Asset
		}
		o.specifiedBinaries = append(o.specifiedBinaries, b)
	}

	return nil
}

// Validate checks if the install operation is valid
func (o *InstallOptions) Validate() error {
	return nil
}

// Run executes the install operation
func (o *InstallOptions) Run() error {
	// Handle env installs (SCP-style or config-based)
	if len(o.envInstalls) > 0 || len(o.configEnvRefs) > 0 {
		if err := o.runEnvInstalls(); err != nil {
			return err
		}
	}

	// Handle binary installs
	var binariesToInstall []*binary.Binary

	if len(o.specifiedBinaries) > 0 {
		binariesToInstall = o.specifiedBinaries
	} else if len(o.envInstalls) == 0 && len(o.configEnvRefs) == 0 {
		// Install all from config (binaries + envs)
		binariesToInstall = o.GetBinariesFromConfig()

		// Also sync all configured envs
		if o.Config != nil && len(o.Config.Envs) > 0 {
			if err := o.syncConfigEnvs(nil); err != nil {
				return err
			}
		}
	}

	if len(binariesToInstall) == 0 && len(o.envInstalls) == 0 && len(o.configEnvRefs) == 0 {
		fmt.Fprintln(o.IO.Out, "No binaries or envs to install")
		return nil
	}

	if len(binariesToInstall) > 0 {
		if err := o.installBinaries(binariesToInstall); err != nil {
			return err
		}

		if err := o.updateLock(binariesToInstall); err != nil {
			fmt.Fprintf(o.IO.ErrOut, "Warning: failed to update b.lock: %v\n", err)
		}

		if o.Add {
			return o.addToConfig(binariesToInstall)
		}
	}

	return nil
}

// installBinaries installs the specified binaries with progress tracking
func (o *InstallOptions) installBinaries(binaries []*binary.Binary) error {
	// Wire interactive asset selector with a shared mutex so that
	// concurrent goroutines never interleave stdin prompts.
	var promptMu sync.Mutex
	for _, b := range binaries {
		if b.AutoDetect && b.SelectAsset == nil {
			b.SelectAsset = guardedAssetSelector(&promptMu, b, o.Quiet, o.IO)
		}
	}

	wg := sync.WaitGroup{}
	pw := progress.NewWriter(progress.StyleDownload, o.IO.Out)
	pw.Style().Visibility.Percentage = true
	go pw.Render()
	defer pw.Stop()

	for _, b := range binaries {
		wg.Add(1)

		go func(b *binary.Binary) {
			defer wg.Done()

			tracker := pw.AddTracker(fmt.Sprintf("Installing %s", b.Name), 0)
			b.Tracker = tracker
			b.Writer = pw

			var err error
			if o.Force {
				err = b.DownloadBinary()
			} else {
				err = b.EnsureBinary(false) // Don't update, just ensure
			}

			name := b.Name
			if b.Alias != "" {
				name = b.Alias + " (" + color.New(color.FgYellow).Sprint(b.Name) + ")"
			}
			progress.ProgressDone(
				b.Tracker,
				name,
				err,
			)
		}(b)
	}

	wg.Wait()
	// Let the progress bar render
	time.Sleep(200 * time.Millisecond)
	return nil
}

// addToConfig adds binaries to the configuration file
func (o *InstallOptions) addToConfig(binaries []*binary.Binary) error {
	configPath, err := o.getConfigPath()
	if err != nil || configPath == "" {
		configPath = path.GetDefaultConfigPath()
	}

	// Load existing config or create new one
	config := o.Config
	if config == nil {
		config = &state.State{}
	}

	// Add binaries to config
	for _, b := range binaries {
		// Use provider ref as the config key if auto-detected
		configName := b.Name
		if b.AutoDetect && b.ProviderRef != "" {
			configName = b.ProviderRef
		}

		// Check if already exists
		found := false
		for i, existing := range config.Binaries {
			if existing.Name == configName {
				// Update version only if we have a specific version
				if b.Version != "" && b.Version != "latest" {
					config.Binaries[i].Version = b.Version
					if o.Fix {
						config.Binaries[i].Enforced = b.Version
					}
				}
				found = true
				break
			}
		}

		if !found {
			entry := &binary.LocalBinary{
				Name: configName,
			}
			// Only set version if it's not "latest" or empty
			if b.Version != "" && b.Version != "latest" {
				entry.Version = b.Version
				if o.Fix {
					entry.Enforced = b.Version
				}
			}
			if b.Alias != "" {
				entry.Name = b.Alias
				entry.Alias = configName
			}
			if b.AssetFilter != "" {
				entry.Asset = b.AssetFilter
			}
			config.Binaries = append(config.Binaries, entry)
		}
	}

	return state.SaveConfig(config, configPath)
}

// updateLock updates b.lock with installed binary checksums
func (o *InstallOptions) updateLock(binaries []*binary.Binary) error {
	lockDir := o.LockDir()
	lk, err := lock.ReadLock(lockDir)
	if err != nil {
		return err
	}

	for _, b := range binaries {
		if b.File == "" {
			continue
		}
		hash, err := lock.SHA256File(b.File)
		if err != nil {
			continue
		}
		entry := lock.BinEntry{
			Name:    b.Name,
			Version: b.Version,
			SHA256:  hash,
		}
		if b.AutoDetect {
			entry.Source = b.ProviderRef
			entry.Provider = b.ProviderType
		} else {
			entry.Preset = true
			if b.GitHubRepo != "" {
				entry.Source = "github.com/" + b.GitHubRepo
			}
		}
		lk.UpsertBinary(entry)
	}

	return lock.WriteLock(lockDir, lk, o.bVersion)
}

// parseBinaryArg parses binary argument in format "name" or "name@version"
func parseBinaryArg(arg string) (name, version string) {
	parts := strings.SplitN(arg, "@", 2)
	name = parts[0]
	if len(parts) > 1 {
		version = parts[1]
	}
	return
}

// parseSCPArg tries to parse an SCP-style env install:
//
//	ref@version:/glob [dest]
//
// Returns the parsed envInstall, how many additional args were consumed, and whether it matched.
func parseSCPArg(arg string, remaining []string) (envInstall, int, bool) {
	// Look for colon that signals SCP syntax — must come after a ref (contains /)
	// and before a glob. Skip protocol prefixes (go://, docker://).
	colonIdx := -1
	for i := range arg {
		if arg[i] == ':' {
			// Skip protocol prefixes (e.g. go://, docker://)
			if i+2 < len(arg) && arg[i+1] == '/' && arg[i+2] == '/' {
				continue
			}
			// Must be preceded by something that looks like a ref (contains /)
			prefix := arg[:i]
			if strings.Contains(prefix, "/") || strings.Contains(prefix, ".") {
				colonIdx = i
				break
			}
		}
	}
	if colonIdx == -1 {
		return envInstall{}, 0, false
	}

	refPart := arg[:colonIdx]
	glob := strings.TrimPrefix(arg[colonIdx+1:], "/")

	// Parse ref@version
	ref := refPart
	version := ""
	if atIdx := strings.LastIndex(refPart, "@"); atIdx != -1 {
		ref = refPart[:atIdx]
		version = refPart[atIdx+1:]
	}

	// Parse fragment label
	label := gitcache.RefLabel(ref)
	ref = gitcache.RefBase(ref)

	// Dest is the next arg (if present and doesn't look like a flag or another ref)
	dest := ""
	consumed := 0
	if len(remaining) > 0 && !strings.HasPrefix(remaining[0], "-") {
		dest = remaining[0]
		consumed = 1
	}

	return envInstall{
		ref:     ref,
		label:   label,
		version: version,
		glob:    glob,
		dest:    dest,
	}, consumed, true
}

// runEnvInstalls handles SCP-style and config-ref env installs.
func (o *InstallOptions) runEnvInstalls() error {
	lockDir := o.LockDir()
	projectRoot := lockDir // dest paths are relative to where b.yaml lives
	lk, err := lock.ReadLock(lockDir)
	if err != nil {
		return err
	}

	// Handle SCP-style installs
	for _, ei := range o.envInstalls {
		cfg := env.EnvConfig{
			Ref:     ei.ref,
			Label:   ei.label,
			Version: ei.version,
			Files: map[string]envmatch.GlobConfig{
				ei.glob: {Dest: ei.dest},
			},
		}

		// SCP-style installs always sync fresh (user explicitly requested files)
		result, err := env.SyncEnv(cfg, projectRoot, "", nil)
		if err != nil {
			return fmt.Errorf("syncing %s: %w", ei.ref, err)
		}

		if result.Skipped {
			fmt.Fprintf(o.IO.Out, "  %-40s %s\n", ei.ref, result.Message)
		} else {
			fmt.Fprintf(o.IO.Out, "  %-40s %s → %s (%s)\n", ei.ref, shortCommit(result.PreviousCommit), shortCommit(result.Commit), result.Message)
			for _, f := range result.Files {
				fmt.Fprintf(o.IO.Out, "    → %-36s ✓ synced\n", f.Dest)
			}
		}

		// Update lock
		lk.UpsertEnv(lock.EnvEntry{
			Ref:            result.Ref,
			Label:          result.Label,
			Version:        result.Version,
			Commit:         result.Commit,
			PreviousCommit: result.PreviousCommit,
			Files:          result.Files,
		})

		// Add to config if requested
		if o.Add {
			if err := o.addEnvToConfig(ei); err != nil {
				return err
			}
		}
	}

	// Handle config-ref env installs (e.g. `b install github.com/org/infra`)
	if len(o.configEnvRefs) > 0 {
		if err := o.syncConfigEnvs(o.configEnvRefs); err != nil {
			return err
		}
		// Re-read lock since syncConfigEnvs writes it
		return nil
	}

	return lock.WriteLock(lockDir, lk, o.bVersion)
}

// syncConfigEnvs syncs envs defined in b.yaml. If refs is nil, syncs all.
func (o *InstallOptions) syncConfigEnvs(refs []string) error {
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
		// If specific refs requested, filter
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
		result, err := env.SyncEnv(cfg, projectRoot, "", lockEntry)
		if err != nil {
			fmt.Fprintf(o.IO.ErrOut, "  %-40s ✗ %v\n", entry.Key, err)
			continue
		}

		if result.Skipped {
			fmt.Fprintf(o.IO.Out, "  %-40s %s\n", entry.Key, result.Message)
			continue // don't overwrite lock entry when up-to-date
		}

		fmt.Fprintf(o.IO.Out, "  %-40s %s → %s (%s)\n", entry.Key, shortCommit(result.PreviousCommit), shortCommit(result.Commit), result.Message)
		for _, f := range result.Files {
			fmt.Fprintf(o.IO.Out, "    → %-36s ✓ synced\n", f.Dest)
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

	return lock.WriteLock(lockDir, lk, o.bVersion)
}

// addEnvToConfig writes an env entry to b.yaml from an SCP-style install.
func (o *InstallOptions) addEnvToConfig(ei envInstall) error {
	configPath, err := o.getConfigPath()
	if err != nil || configPath == "" {
		configPath = path.GetDefaultConfigPath()
	}

	config := o.Config
	if config == nil {
		config = &state.State{}
	}

	key := ei.ref
	if ei.label != "" {
		key += "#" + ei.label
	}

	// Check if exists
	existing := config.Envs.Get(key)
	if existing != nil {
		// Update version and add glob
		if ei.version != "" {
			existing.Version = ei.version
		}
		if existing.Files == nil {
			existing.Files = make(map[string]envmatch.GlobConfig)
		}
		existing.Files[ei.glob] = envmatch.GlobConfig{Dest: ei.dest}
	} else {
		entry := &state.EnvEntry{
			Key:     key,
			Version: ei.version,
			Files: map[string]envmatch.GlobConfig{
				ei.glob: {Dest: ei.dest},
			},
		}
		config.Envs = append(config.Envs, entry)
	}

	return state.SaveConfig(config, configPath)
}

// discoverUpstreamConfig checks if a ref's upstream repo has a b.yaml file.
// Returns a formatted hint string with discovered file groups, or "" if none found.
func (o *InstallOptions) discoverUpstreamConfig(ref string) string {
	baseRef := gitcache.RefBase(ref)
	url := gitcache.GitURL(ref)

	// Resolve HEAD to get the latest commit
	commit, err := gitcache.ResolveRef(url, "")
	if err != nil {
		return ""
	}

	cacheRoot := gitcache.DefaultCacheRoot()
	if err := gitcache.EnsureClone(cacheRoot, baseRef, url); err != nil {
		return ""
	}
	if err := gitcache.Fetch(cacheRoot, baseRef, commit); err != nil {
		return ""
	}

	// Check for b.yaml in the root
	content, err := gitcache.ShowFile(cacheRoot, baseRef, commit, "b.yaml")
	if err != nil {
		// Try .bin/b.yaml
		content, err = gitcache.ShowFile(cacheRoot, baseRef, commit, ".bin/b.yaml")
		if err != nil {
			return ""
		}
	}

	// Parse to find env or file groups
	var upstream state.State
	if parseErr := yaml.Unmarshal(content, &upstream); parseErr != nil {
		return ""
	}

	var lines []string
	if len(upstream.Envs) > 0 {
		lines = append(lines, "  Environments:")
		for _, e := range upstream.Envs {
			lines = append(lines, fmt.Sprintf("    - %s", e.Key))
		}
	}
	if len(upstream.Binaries) > 0 {
		lines = append(lines, "  Binaries:")
		for _, b := range upstream.Binaries {
			lines = append(lines, fmt.Sprintf("    - %s", b.Name))
		}
	}

	// Also list top-level directories as potential file groups
	tree, err := gitcache.ListTree(cacheRoot, baseRef, commit)
	if err == nil && len(tree) > 0 {
		dirs := make(map[string]int)
		for _, p := range tree {
			parts := strings.SplitN(p, "/", 2)
			if len(parts) > 1 {
				dirs[parts[0]]++
			}
		}
		if len(dirs) > 0 {
			lines = append(lines, "  Top-level directories:")
			for dir, count := range dirs {
				lines = append(lines, fmt.Sprintf("    - %s/ (%d files)", dir, count))
			}
		}
	}

	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n")
}

// shortCommit returns the first 7 characters of a commit hash, or "(new)" if empty.
func shortCommit(commit string) string {
	if commit == "" {
		return "(new)"
	}
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}
