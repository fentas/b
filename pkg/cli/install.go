package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

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

	"gopkg.in/yaml.v3"
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
	OnPost            string           // Shell command to run after install/update
	specifiedBinaries []*binary.Binary // Binaries specified on command line
	envInstalls       []envInstall     // SCP-style env installs
	configEnvRefs     []string         // env refs to sync from config

	// Set by installBinaries for updateLock: what the install did to each
	// binary's file, which downloads failed, and the files of failed
	// downloads that were removed (discardPartial).
	plans    map[*binary.Binary]installPlan
	failedMu sync.Mutex
	failed   map[*binary.Binary]bool
	removed  []string
}

// installPlan is what an install does to one binary's file, decided before
// any download.
type installPlan struct {
	download    bool   // download the binary: it is missing, --force, or another version is asked for
	keptVersion string // the version of a present file that is kept, from b.lock; "" when unknown
}

// planInstall decides whether an install replaces a present binary or keeps
// it. prev is the binary's b.lock entry, nil when there is none. A present
// file is kept when no version is asked for, or when b.lock says that file
// (by its sha256) is the version asked for. When another version is asked
// for, or b.lock does not describe the file, the binary is downloaded: a
// kept file must never be recorded under a version it is not. When b.lock
// says that the file is another binary (otherBinary), the binary is
// downloaded too, also when no version is asked for.
func planInstall(b *binary.Binary, prev *lock.BinEntry, force bool) installPlan {
	if force || !b.BinaryExists() {
		return installPlan{download: true}
	}
	known := ""
	if describesFile(prev, b.BinaryPath()) {
		if otherBinary(prev, b) {
			return installPlan{download: true}
		}
		known = prev.Version
	}
	requested := b.Version
	if requested == "latest" {
		requested = ""
	}
	if requested != "" && requested != known {
		return installPlan{download: true}
	}
	return installPlan{keptVersion: known}
}

// discardPartial removes what a failed download left at file. The download
// writes the file in place, so it can leave a part of the new binary where
// the old one was. A file that is not the one that was there before the
// download (before is its sha256, "" when there was none) is removed, so
// that no later install keeps it as installed. It reports whether it removed
// the file. A file that the download did not change stays.
func discardPartial(file, before string) bool {
	after, err := lock.SHA256File(file)
	if err != nil || after == before {
		return false
	}
	return os.Remove(file) == nil
}

// isProviderRef reports whether b is the binary of a provider ref, not a
// preset. (A b.yaml entry of a provider ref has IsProviderRef set.)
func isProviderRef(b *binary.Binary) bool {
	return b.AutoDetect && b.ProviderRef != ""
}

// lockName is the name of a binary's b.lock row: the name of the file that
// it installs (fileName), its alias when it has one, else its name, or the
// path of its file relative to the binary directory when b.yaml puts it
// elsewhere. So two b.yaml entries of one preset, or two provider refs with
// the same binary name, each have their own row (lockrow.go).
func lockName(b *binary.Binary) string {
	name := b.Name
	if b.Alias != "" {
		name = b.Alias
	}
	return fileName(b.File, name)
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
	cmd.Flags().StringVar(&o.Alias, "alias", "", "Alias for the binary (one binary argument only)")
	cmd.Flags().StringVar(&o.Asset, "asset", "", "Glob pattern to filter release assets (e.g. \"argsh-so-*\")")
	cmd.Flags().StringVar(&o.OnPost, "on-post", "", "Shell command to run after install/update (saved to b.yaml with --add)")
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
		return checkConfigFiles(o.Config)
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

		// Binary install. b.yaml must give each file one entry
		// (checkConfigFiles); with --add, the b.yaml that --add writes
		// (below). --alias names the file of one binary, so it takes only
		// one.
		if len(o.specifiedBinaries) == 0 && !o.Add {
			if err := checkConfigFiles(o.Config); err != nil {
				return err
			}
		}
		if o.Alias != "" && len(o.specifiedBinaries) > 0 {
			return fmt.Errorf("--alias %s names one binary, but %s is a second one\n  Hint: install each binary on its own, with its own --alias", o.Alias, arg)
		}
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

		// --alias overrides; without it, the alias of the b.yaml entry stays.
		// An --alias that already names another binary is refused: an entry
		// of another binary always, another preset without an entry unless
		// --force (its file is then overwritten). A preset
		// installed under --alias is the binary of the entry of that name
		// (the one --add writes): it takes that entry's settings as the
		// no-argument install does (version, enforced pin, file, onPost).
		// When there is no such entry, it keeps the settings of the entry of
		// the argument (version, enforced pin, onPost), but gets its own
		// file. A version on the command line and --on-post win (below). A
		// provider ref keeps its one entry, file included.
		if o.Alias != "" {
			if e := o.aliasOwner(b); e != nil {
				return fmt.Errorf("--alias %s: the b.yaml entry %s is another binary, not %s\n  Hint: pick another alias, or install %s by its own name", o.Alias, e.Name, b.Name, e.Name)
			}
			if o.aliasPreset(b) && !o.Force {
				return fmt.Errorf("--alias %s: %s is the preset %s, another binary than %s\n  Hint: pick another alias, or add --force to install %s over the file of %s", o.Alias, o.Alias, o.Alias, b.Name, b.Name, o.Alias)
			}
			if !isProviderRef(b) {
				b.File = ""
				if e := o.configEntry(o.Alias); e != nil {
					if rb, ok := o.resolveBinary(e); ok {
						b = rb
					}
					b.File = e.File
				}
			}
			b.Alias = o.Alias
		}

		// A version on the command line wins over the b.yaml entry.
		if version != "" {
			b.Version = version
		}
		if o.Add && !o.Fix {
			if err := o.checkPin(b); err != nil {
				return err
			}
			// checkPin accepts the version of the pin also in another
			// spelling (3.0.0 for v3.0.0). Install and record it under the
			// pin's spelling: that is the version that b installs for the
			// entry, and the one b.lock holds for its file. Another
			// spelling would download the binary again (or ask the
			// provider for a release that does not exist), and put a
			// second spelling into b.yaml.
			if pin := o.enforcedPin(b); pin != "" && sameVersion(pin, b.Version) {
				b.Version = pin
			}
		}
		if o.Asset != "" {
			b.AssetFilter = o.Asset
		}
		if o.OnPost != "" {
			b.OnPost = o.OnPost
		}
		o.specifiedBinaries = append(o.specifiedBinaries, b)
	}

	// Refuse an --add that would write a b.yaml in which two entries
	// install the same file, before any download: the next run would
	// refuse that b.yaml.
	if o.Add && len(o.specifiedBinaries) > 0 {
		return checkConfigFiles(o.withAdded(o.specifiedBinaries))
	}
	return nil
}

// configEntry is the b.yaml entry of that name, nil when there is none.
func (o *InstallOptions) configEntry(name string) *binary.LocalBinary {
	if o.Config == nil {
		return nil
	}
	for _, lb := range o.Config.Binaries {
		if lb.Name == name {
			return lb
		}
	}
	return nil
}

// configKey is the name of the b.yaml entry of a binary, the entry that
// --add writes: the ref of a provider ref, the alias of a preset installed
// under an alias, the preset's name otherwise.
func configKey(b *binary.Binary) string {
	if isProviderRef(b) {
		return b.ProviderRef
	}
	if b.Alias != "" {
		return b.Alias
	}
	return b.Name
}

// checkPin refuses an --add that would set the version of a b.yaml entry
// to a value its enforced pin contradicts. The pin wins at every later
// install, so the entry would name a version that b never installs. A
// version with or without a leading "v" is the same version.
func (o *InstallOptions) checkPin(b *binary.Binary) error {
	if b.Version == "" || b.Version == "latest" {
		return nil
	}
	e := o.configEntry(configKey(b))
	if e == nil || e.Enforced == "" || sameVersion(e.Enforced, b.Version) {
		return nil
	}
	return fmt.Errorf("--add: the b.yaml entry %s is pinned to %s (enforced), not %s\n  Hint: add --fix to move the pin, or install without --add", e.Name, e.Enforced, b.Version)
}

// enforcedPin is the enforced pin of the b.yaml entry that --add writes for
// b, "" when that entry has no pin or there is no such entry.
func (o *InstallOptions) enforcedPin(b *binary.Binary) string {
	if e := o.configEntry(configKey(b)); e != nil {
		return e.Enforced
	}
	return ""
}

// sameVersion reports whether two versions are the same, with or without a
// leading "v": 3.0.0 and v3.0.0 are the same.
func sameVersion(a, b string) bool {
	return strings.TrimPrefix(a, "v") == strings.TrimPrefix(b, "v")
}

// aliasOwner is the b.yaml entry of another binary that the name --alias
// already belongs to, nil when there is none. That is an entry of that name
// which is not an entry of b's preset (for a provider ref b: any entry of
// that name), or a provider-ref entry other than b's own that installs the
// file of that name (entryFileName).
func (o *InstallOptions) aliasOwner(b *binary.Binary) *binary.LocalBinary {
	if o.Config == nil {
		return nil
	}
	isRef := isProviderRef(b)
	for _, e := range o.Config.Binaries {
		if !e.IsProviderRef {
			if e.Name == o.Alias && (isRef || !entryOfPreset(e, b.Name)) {
				return e
			}
			continue
		}
		if e.Name == b.ProviderRef {
			continue // b's own entry; a preset has no ref
		}
		if e.Name == o.Alias || entryFileName(e) == o.Alias {
			return e
		}
	}
	return nil
}

// aliasPreset reports whether the name --alias is the name of another preset
// that has no b.yaml entry of that name: a preset of that name other than
// b's own (for a provider ref b: any preset of that name). Its binary
// installs at that name, so the alias would take its file. When b.yaml has
// an entry of that name, the entry decides (aliasOwner).
func (o *InstallOptions) aliasPreset(b *binary.Binary) bool {
	if o.configEntry(o.Alias) != nil {
		return false
	}
	if _, ok := o.lookup[o.Alias]; !ok {
		return false
	}
	return isProviderRef(b) || o.Alias != b.Name
}

// entryOfPreset reports whether a b.yaml entry that is not a provider ref is
// the binary of that preset: the preset's own entry, or an alias entry of it.
func entryOfPreset(e *binary.LocalBinary, preset string) bool {
	if e.Alias == "" {
		return e.Name == preset
	}
	return e.Alias == preset
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
	// A run without arguments installs all of b.yaml. It also removes the
	// b.lock rows that describe no file (pruneLock).
	all := len(o.specifiedBinaries) == 0 && len(o.envInstalls) == 0 && len(o.configEnvRefs) == 0

	if len(o.specifiedBinaries) > 0 {
		binariesToInstall = o.specifiedBinaries
	} else if all {
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
		if all {
			o.reportPruned(o.pruneLock())
		}
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
		if all {
			o.reportPruned(o.pruneLock())
		}

		if o.Add {
			return o.addToConfig(binariesToInstall)
		}
	}

	return nil
}

// installBinaries installs the specified binaries with progress tracking
func (o *InstallOptions) installBinaries(binaries []*binary.Binary) error {
	// Decide per binary whether to download it or keep the present file
	// (planInstall), from b.lock. A missing b.lock describes no file, so a
	// requested version is downloaded. A b.lock that is there but cannot be
	// read is an error: taken as empty, it would make each run download every
	// pinned binary again, and the install could not write it either.
	lk, err := lock.ReadLock(o.LockDir())
	if err != nil {
		return fmt.Errorf("cannot read b.lock: %w\n  Hint: repair %s, or remove it; b then downloads each pinned binary once and writes a new b.lock", err, filepath.Join(o.LockDir(), "b.lock"))
	}
	o.moveOldRows(lk, binaries)
	o.plans = make(map[*binary.Binary]installPlan, len(binaries))
	o.failed = make(map[*binary.Binary]bool)
	o.removed = nil
	var toDownload []*binary.Binary
	for _, b := range binaries {
		// moveOldRows moved the rows of an older b to these names.
		plan := planInstall(b, lk.FindBinary(lockName(b)), o.Force)
		o.plans[b] = plan
		if plan.download {
			toDownload = append(toDownload, b)
		}
	}

	// Pre-resolve ambiguous assets before starting progress bars, for the
	// binaries that will download.
	if len(toDownload) > 0 {
		resolveAmbiguousAssets(toDownload, o.Quiet, o.IO)
	}

	// Wire fallback selector for any remaining ambiguous cases
	var promptMu sync.Mutex
	for _, b := range binaries {
		if b.AutoDetect && b.SelectAsset == nil {
			b.SelectAsset = guardedAssetSelector(&promptMu, b, o.Quiet, o.IO)
		}
	}

	wg := sync.WaitGroup{}
	pw := newProgressRun(o.IO.Out)

	for _, b := range binaries {
		wg.Add(1)

		go func(b *binary.Binary) {
			defer wg.Done()

			tracker := pw.AddTracker(fmt.Sprintf("Installing %s", b.Name), 0)
			b.Tracker = tracker
			b.Writer = pw.Writer

			// Download or keep the file, as planned. The onPost hook runs
			// only when a download happened, not when the file was kept.
			var err error
			if o.plans[b].download {
				file := b.BinaryPath()
				before, _ := lock.SHA256File(file)
				err = b.DownloadBinary()
				if err != nil {
					removed := discardPartial(file, before)
					o.failedMu.Lock()
					o.failed[b] = true
					if removed {
						o.removed = append(o.removed, file)
					}
					o.failedMu.Unlock()
				}
			}
			downloaded := err == nil && o.plans[b].download

			// Run onPost hook only when a download actually happened.
			if downloaded && b.OnPost != "" {
				if hookErr := binary.RunHook(b.OnPost, o.ProjectRoot(), "install", b.Name, b.Version, b.BinaryPath(), o.IO.ErrOut, o.IO.ErrOut); hookErr != nil {
					fmt.Fprintf(o.IO.ErrOut, "Warning: onPost hook for %s failed: %v\n", b.Name, hookErr)
				}
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
	// Render the last frame
	pw.Stop()
	sort.Strings(o.removed)
	for _, file := range o.removed {
		fmt.Fprintf(o.IO.ErrOut, "Removed %s: its download failed and left an incomplete file\n", file)
	}
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
	o.addEntries(config, binaries)
	return state.SaveConfig(config, configPath)
}

// withAdded is a copy of b.yaml with the entries that --add writes for
// binaries (addEntries). o.Config does not change.
func (o *InstallOptions) withAdded(binaries []*binary.Binary) *state.State {
	config := &state.State{}
	if o.Config != nil {
		*config = *o.Config
		config.Binaries = make(state.BinaryList, len(o.Config.Binaries))
		for i, e := range o.Config.Binaries {
			if e != nil {
				copied := *e
				e = &copied
			}
			config.Binaries[i] = e
		}
	}
	o.addEntries(config, binaries)
	return config
}

// addEntries writes the entries that --add writes for binaries into config.
func (o *InstallOptions) addEntries(config *state.State, binaries []*binary.Binary) {
	for _, b := range binaries {
		// Check if already exists: a provider ref has one entry, under the
		// ref, with the binary's name as its alias; an alias of a preset has
		// its own entry under the alias (as a new entry below gets it)
		isRef := isProviderRef(b)
		key := configKey(b)
		found := false
		for i, existing := range config.Binaries {
			if existing.Name == key {
				// Update version only if we have a specific version
				if b.Version != "" && b.Version != "latest" {
					config.Binaries[i].Version = b.Version
					if o.Fix {
						config.Binaries[i].Enforced = b.Version
					}
				}
				// --alias renames the binary of a provider ref
				if isRef && o.Alias != "" {
					config.Binaries[i].Alias = o.Alias
				}
				// --on-post and --asset (or an asset picked at the prompt)
				// go into the entry, as they go into a new one. Without
				// them, the binary has the entry's own values. An enforced
				// pin holds the version only, so they apply to a pinned
				// entry too.
				if b.OnPost != "" {
					config.Binaries[i].OnPost = b.OnPost
				}
				if b.AssetFilter != "" {
					config.Binaries[i].Asset = b.AssetFilter
				}
				found = true
				break
			}
		}

		if !found {
			entry := &binary.LocalBinary{
				Name:          key,
				IsProviderRef: isRef,
			}
			// Only set version if it's not "latest" or empty
			if b.Version != "" && b.Version != "latest" {
				entry.Version = b.Version
				if o.Fix {
					entry.Enforced = b.Version
				}
			}
			// A provider ref names its binary with the alias; an alias
			// entry of a preset names the preset with it.
			switch {
			case isRef:
				entry.Alias = b.Alias
			case b.Alias != "" && b.Alias != b.Name:
				entry.Alias = b.Name
			}
			if b.AssetFilter != "" {
				entry.Asset = b.AssetFilter
			}
			if b.OnPost != "" {
				entry.OnPost = b.OnPost
			}
			config.Binaries = append(config.Binaries, entry)
		}
	}
}

// updateLock updates b.lock with installed binary checksums
func (o *InstallOptions) updateLock(binaries []*binary.Binary) error {
	lockDir := o.LockDir()
	lk, err := lock.ReadLock(lockDir)
	if err != nil {
		return err
	}

	// installBinaries moved the rows of an older b to the names of the files
	// (moveOldRows) before the downloads.
	for _, b := range binaries {
		if b.File == "" {
			continue
		}
		name := lockName(b)
		var prev *lock.BinEntry
		if row := lk.FindBinary(name); row != nil {
			copied := *row
			prev = &copied
		}
		// A failed download writes no entry. While a file is there (the file
		// that the download did not change), its entry stays as it was. When
		// no file is there (it was missing, or discardPartial removed what the
		// download left), the entry goes: it would describe a file that is
		// not there.
		if o.failed[b] {
			if _, err := os.Stat(b.File); os.IsNotExist(err) {
				lk.RemoveBinary(name)
			}
			continue
		}
		hash, err := lock.SHA256File(b.File)
		if err != nil {
			continue
		}
		// A kept file keeps the version that b.lock gives it ("" when b.lock
		// does not describe it), not the version of the command line.
		version := b.Version
		if plan, ok := o.plans[b]; ok && !plan.download {
			version = plan.keptVersion
		}
		entry := lock.BinEntry{
			Name:    name,
			Binary:  lockBinary(b),
			Version: version,
			SHA256:  hash,
		}
		if b.AutoDetect {
			entry.Source = b.ProviderRef
			entry.Provider = b.ProviderType
			// For providers that expose a stable content digest (docker://,
			// oci://) record it so `b update` can skip re-pulls when the
			// tag's manifest hasn't moved upstream. ResolveDigest has a
			// two-shape contract:
			//   - transient/registry/auth → ("", nil): preserve the
			//     previous digest so the skip state isn't lost across
			//     outages.
			//   - malformed ref → ("", err): surface as a warning so the
			//     user sees the actionable problem.
			//
			// Carry a previous digest forward only when the previous lock
			// entry refers to the same Source/Provider — otherwise we'd
			// associate an old image's digest with a new Source, which
			// could produce coincidental false "up to date" skips later.
			preserveDigest := func() {
				if prev == nil || prev.Source != entry.Source {
					return
				}
				if prev.Provider != "" && entry.Provider != "" && prev.Provider != entry.Provider {
					return
				}
				entry.Digest = prev.Digest
			}
			if p, err := provider.Detect(b.ProviderRef); err == nil {
				if dr, ok := p.(provider.DigestResolver); ok {
					digest, dErr := dr.ResolveDigest(b.ProviderRef, b.Version)
					switch {
					case dErr != nil:
						fmt.Fprintf(o.IO.ErrOut, "Warning: resolving digest for %s (%s): %v\n", b.Name, b.ProviderRef, dErr)
						preserveDigest()
					case digest != "":
						entry.Digest = digest
					default:
						preserveDigest()
					}
				}
			}
		} else {
			entry.Preset = true
			entry.Source = presetSource(b)
		}
		lk.UpsertBinary(entry)
	}

	return lock.WriteLock(lockDir, lk, o.bVersion)
}

// parseBinaryArg parses binary argument in format "name" or "name@version".
// Delegates to provider.ParseRef which already handles the docker:// or oci://
// quirk of preserving a ":/<path>" suffix on name.
func parseBinaryArg(arg string) (name, version string) {
	return provider.ParseRef(arg)
}

// parseSCPArg tries to parse an SCP-style env install:
//
//	ref@version:/glob [dest]
//
// Returns the parsed envInstall, how many additional args were consumed, and whether it matched.
func parseSCPArg(arg string, remaining []string) (envInstall, int, bool) {
	// docker:// and oci:// are always binary installs (never env SCP), even
	// though they may contain ":/path" for in-container binary location.
	if strings.HasPrefix(arg, "docker://") || strings.HasPrefix(arg, "oci://") {
		return envInstall{}, 0, false
	}

	// Look for colon that signals SCP syntax — must come after a ref (contains /)
	// and before a glob. Skip the "://" of protocol prefixes (go://, git://).
	colonIdx := -1
	for i := range arg {
		if arg[i] == ':' {
			// Skip protocol prefixes (e.g. go://, git://)
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
	projectRoot := o.ProjectRoot()
	lk, err := lock.ReadLock(lockDir)
	if err != nil {
		return err
	}

	// Handle SCP-style installs
	for _, ei := range o.envInstalls {
		cfg := env.EnvConfig{
			Ref:       ei.ref,
			Label:     ei.label,
			Version:   ei.version,
			ConfigDir: lockDir,
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
	projectRoot := o.ProjectRoot()
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
			Ref:       ref,
			Label:     label,
			Version:   entry.Version,
			ConfigDir: lockDir,
			Ignore:    entry.Ignore,
			Strategy:  entry.Strategy,
			Files:     entry.Files,
		}

		lockEntry := lk.FindEnv(ref, label)
		result, err := env.SyncEnv(cfg, projectRoot, "", lockEntry)
		if err != nil {
			fmt.Fprintf(o.IO.ErrOut, "  %-40s ✗ %s\n", entry.Key, firstLine(err.Error()))
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
	// Show profiles (preferred) or envs
	if len(upstream.Profiles) > 0 {
		lines = append(lines, "  Profiles:")
		sorted := make([]*state.EnvEntry, len(upstream.Profiles))
		copy(sorted, upstream.Profiles)
		sort.Slice(sorted, func(i, j int) bool {
			return sorted[i].Key < sorted[j].Key
		})
		for _, e := range sorted {
			if e.Description != "" {
				lines = append(lines, fmt.Sprintf("    - %-30s %s", e.Key, e.Description))
			} else {
				lines = append(lines, fmt.Sprintf("    - %s", e.Key))
			}
		}
		lines = append(lines, fmt.Sprintf("  Hint: run `b env profiles %s` to see details", ref))
	} else if len(upstream.Envs) > 0 {
		lines = append(lines, "  Environments:")
		sorted := make([]*state.EnvEntry, len(upstream.Envs))
		copy(sorted, upstream.Envs)
		sort.Slice(sorted, func(i, j int) bool {
			return sorted[i].Key < sorted[j].Key
		})
		for _, e := range sorted {
			if e.Description != "" {
				lines = append(lines, fmt.Sprintf("    - %-30s %s", e.Key, e.Description))
			} else {
				lines = append(lines, fmt.Sprintf("    - %s", e.Key))
			}
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
