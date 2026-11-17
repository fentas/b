package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fentas/b/pkg/binary"
	"github.com/fentas/b/pkg/state"
	"github.com/fentas/goodies/streams"
	"gopkg.in/yaml.v3"
)

// TestGetBinary_ConfigEntryOverPreset: a named binary takes the b.yaml entry
// of that name over the preset, with the same merge as the no-argument
// install, so `b install <name>` and `b install` install the same version.
// An explicit @version on the command line still wins.
func TestGetBinary_ConfigEntryOverPreset(t *testing.T) {
	t.Setenv("PATH_BIN", t.TempDir())
	presets := []*binary.Binary{
		{Name: "tool", Context: context.Background()},
		{Name: "other", Version: "v0.1.0", Context: context.Background()},
	}
	io := &streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}}
	shared := NewSharedOptions(io, presets)
	shared.Config = &state.State{Binaries: state.BinaryList{
		&binary.LocalBinary{Name: "tool", Version: "v1.2.3", File: "/opt/tools/tool"},
	}}

	b, ok := shared.GetBinary("tool")
	if !ok {
		t.Fatal("tool not found")
	}
	if b.Version != "v1.2.3" || b.File != "/opt/tools/tool" {
		t.Errorf("GetBinary(tool): version %q, file %q; want the b.yaml entry v1.2.3, /opt/tools/tool", b.Version, b.File)
	}
	all := shared.GetBinariesFromConfig()
	if len(all) != 1 {
		t.Fatalf("GetBinariesFromConfig: %d binaries, want 1", len(all))
	}
	if all[0].Version != b.Version || all[0].File != b.File {
		t.Errorf("the named lookup (%q, %q) and the no-argument path (%q, %q) differ", b.Version, b.File, all[0].Version, all[0].File)
	}
	if presets[0].Version != "" || presets[0].File != "" {
		t.Errorf("the preset was changed: version %q, file %q", presets[0].Version, presets[0].File)
	}
	if o, ok := shared.GetBinary("other"); !ok || o.Version != "v0.1.0" {
		t.Errorf("a preset without a b.yaml entry: %+v, %v", o, ok)
	}

	for _, tc := range []struct{ arg, want string }{
		{"tool", "v1.2.3"},
		{"tool@v9.0.0", "v9.0.0"},
	} {
		o := &InstallOptions{SharedOptions: shared}
		if err := o.Complete([]string{tc.arg}); err != nil {
			t.Fatalf("Complete(%s): %v", tc.arg, err)
		}
		if got := o.specifiedBinaries[0].Version; got != tc.want {
			t.Errorf("b install %s: version %q, want %q", tc.arg, got, tc.want)
		}
	}
}

// TestComplete_KeepsTheConfigAlias: `b install <name>` for a b.yaml entry
// that is an alias of a preset installs under the entry's name, as the
// no-argument install does. Only --alias replaces it.
func TestComplete_KeepsTheConfigAlias(t *testing.T) {
	binDir := t.TempDir()
	t.Setenv("PATH_BIN", binDir)
	presets := []*binary.Binary{
		{Name: "rtool", Context: context.Background()},
		{Name: "plain", Context: context.Background()},
	}
	io := &streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}}
	shared := NewSharedOptions(io, presets)
	shared.Config = &state.State{Binaries: state.BinaryList{
		&binary.LocalBinary{Name: "tool", Alias: "rtool"},
		&binary.LocalBinary{Name: "plain", Version: "v1.0.0"},
		&binary.LocalBinary{Name: "github.com/org/tool", Alias: "t", IsProviderRef: true},
	}}

	for _, tc := range []struct{ arg, flag, name, alias, file string }{
		{"tool", "", "rtool", "tool", "tool"},
		{"tool", "mytool", "rtool", "mytool", "mytool"},
		{"plain", "", "plain", "", "plain"},
		{"github.com/org/tool", "", "tool", "t", "t"},
	} {
		o := &InstallOptions{SharedOptions: shared, Alias: tc.flag}
		if err := o.Complete([]string{tc.arg}); err != nil {
			t.Fatalf("Complete(%s) with --alias %q: %v", tc.arg, tc.flag, err)
		}
		b := o.specifiedBinaries[0]
		if b.Name != tc.name || b.Alias != tc.alias {
			t.Errorf("b install %s, --alias %q: name %q, alias %q; want %q, %q", tc.arg, tc.flag, b.Name, b.Alias, tc.name, tc.alias)
		}
		if got, want := b.BinaryPath(), filepath.Join(binDir, tc.file); got != want {
			t.Errorf("b install %s, --alias %q: file %q, want %q", tc.arg, tc.flag, got, want)
		}
	}
}

// TestAddToConfig_UpdatesTheAliasEntry: `b install <name>@<version> --add`
// for a b.yaml entry that is an alias of a preset updates that entry. It
// does not add a second entry of the same name, which would replace the
// first and lose its file and its onPost. (An entry with an enforced pin:
// TestAddToConfig_KeepsTheEnforcedPin.)
func TestAddToConfig_UpdatesTheAliasEntry(t *testing.T) {
	t.Setenv("PATH_BIN", t.TempDir())
	configPath := filepath.Join(t.TempDir(), ".bin", "b.yaml")
	presets := []*binary.Binary{{Name: "rtool", Context: context.Background()}}
	io := &streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}}
	shared := NewSharedOptions(io, presets)
	shared.ConfigPath = configPath
	shared.Config = &state.State{Binaries: state.BinaryList{
		&binary.LocalBinary{Name: "tool", Alias: "rtool", Version: "v1.0.0", File: "/opt/x/tool", OnPost: "echo tool"},
	}}

	o := &InstallOptions{SharedOptions: shared, Add: true}
	if err := o.Complete([]string{"tool@v2.0.0"}); err != nil {
		t.Fatalf("Complete(tool@v2.0.0): %v", err)
	}
	if err := o.addToConfig(o.specifiedBinaries); err != nil {
		t.Fatalf("addToConfig: %v", err)
	}
	cfg, err := state.LoadConfigFromPath(configPath)
	if err != nil {
		t.Fatalf("LoadConfigFromPath: %v", err)
	}
	if len(cfg.Binaries) != 1 {
		t.Fatalf("b.yaml has %d entries, want 1: %+v", len(cfg.Binaries), cfg.Binaries)
	}
	e := cfg.Binaries[0]
	if e.Name != "tool" || e.Alias != "rtool" || e.Version != "v2.0.0" || e.File != "/opt/x/tool" || e.OnPost != "echo tool" {
		t.Errorf("the entry: name %q, alias %q, version %q, file %q, onPost %q; want tool, rtool, v2.0.0, /opt/x/tool, echo tool", e.Name, e.Alias, e.Version, e.File, e.OnPost)
	}
}

// TestAddToConfig_UpdatesTheProviderRefEntry: a provider-ref entry is stored
// under the ref, with the binary's name as its alias. `b install <ref>@<v>
// --add` and `b install --add` update that entry and add no entry under the
// alias; --alias renames the binary in that entry.
func TestAddToConfig_UpdatesTheProviderRefEntry(t *testing.T) {
	for _, tc := range []struct {
		name    string
		args    []string
		flag    string
		version string
		alias   string
	}{
		{"named with a version", []string{"github.com/org/tool@v2.0.0"}, "", "v2.0.0", "t"},
		{"no argument", nil, "", "v1.0.0", "t"},
		{"named with --alias", []string{"github.com/org/tool"}, "p", "v1.0.0", "p"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PATH_BIN", t.TempDir())
			configPath := filepath.Join(t.TempDir(), ".bin", "b.yaml")
			io := &streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}}
			shared := NewSharedOptions(io, nil)
			shared.ConfigPath = configPath
			shared.Config = &state.State{Binaries: state.BinaryList{
				&binary.LocalBinary{Name: "github.com/org/tool", Alias: "t", Version: "v1.0.0", File: "/opt/x/tool", IsProviderRef: true},
			}}

			o := &InstallOptions{SharedOptions: shared, Add: true, Alias: tc.flag}
			if err := o.Complete(tc.args); err != nil {
				t.Fatalf("Complete(%v): %v", tc.args, err)
			}
			// As Run does: no argument installs (and adds) the b.yaml set
			bins := o.specifiedBinaries
			if len(tc.args) == 0 {
				bins = o.GetBinariesFromConfig()
			}
			if len(bins) != 1 {
				t.Fatalf("%d binaries to add, want 1", len(bins))
			}
			// The entry's file stays the binary's file, --alias or not
			if got := bins[0].BinaryPath(); got != "/opt/x/tool" {
				t.Errorf("the binary installs at %q, want the entry's file /opt/x/tool", got)
			}
			if err := o.addToConfig(bins); err != nil {
				t.Fatalf("addToConfig: %v", err)
			}
			cfg, err := state.LoadConfigFromPath(configPath)
			if err != nil {
				t.Fatalf("LoadConfigFromPath: %v", err)
			}
			if len(cfg.Binaries) != 1 {
				t.Fatalf("b.yaml has %d entries, want 1: %+v", len(cfg.Binaries), cfg.Binaries)
			}
			e := cfg.Binaries[0]
			if e.Name != "github.com/org/tool" || e.Alias != tc.alias || e.Version != tc.version || e.File != "/opt/x/tool" {
				t.Errorf("the entry: name %q, alias %q, version %q, file %q; want github.com/org/tool, %s, %s, /opt/x/tool (updated in place)", e.Name, e.Alias, e.Version, e.File, tc.alias, tc.version)
			}
		})
	}
}

// TestAddToConfig_NewEntriesResolve: a new entry that --add writes resolves
// to the binary it installed. A provider ref gets an entry under the ref; an
// alias of a preset gets its own entry, and an entry of the preset's name
// stays as it was.
func TestAddToConfig_NewEntriesResolve(t *testing.T) {
	for _, tc := range []struct {
		name      string
		start     state.BinaryList
		arg       string
		flag      string
		want      map[string]string // entry name -> its alias
		versions  map[string]string // entry name -> its version
		installed string            // the file of the installed binary
		files     []string
	}{
		{"a provider ref with --alias", nil, "github.com/org/new", "t",
			map[string]string{"github.com/org/new": "t"}, map[string]string{"github.com/org/new": ""}, "t", []string{"t"}},
		{"a preset with --alias beside its plain entry",
			state.BinaryList{&binary.LocalBinary{Name: "plain", Version: "v1.0.0", File: "/opt/plain"}}, "plain@v2.0.0", "p",
			map[string]string{"plain": "", "p": "plain"}, map[string]string{"plain": "v1.0.0", "p": "v2.0.0"}, "p", []string{"plain", "p"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			binDir := t.TempDir()
			t.Setenv("PATH_BIN", binDir)
			configPath := filepath.Join(t.TempDir(), ".bin", "b.yaml")
			presets := []*binary.Binary{{Name: "plain", Context: context.Background()}}
			io := &streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}}
			shared := NewSharedOptions(io, presets)
			shared.ConfigPath = configPath
			shared.Config = &state.State{Binaries: tc.start}

			o := &InstallOptions{SharedOptions: shared, Add: true, Alias: tc.flag}
			if err := o.Complete([]string{tc.arg}); err != nil {
				t.Fatalf("Complete(%s): %v", tc.arg, err)
			}
			if got, want := o.specifiedBinaries[0].BinaryPath(), filepath.Join(binDir, tc.installed); got != want {
				t.Errorf("b install %s --alias %s installs %q, want %q", tc.arg, tc.flag, got, want)
			}
			if err := o.addToConfig(o.specifiedBinaries); err != nil {
				t.Fatalf("addToConfig: %v", err)
			}
			cfg, err := state.LoadConfigFromPath(configPath)
			if err != nil {
				t.Fatalf("LoadConfigFromPath: %v", err)
			}
			got := map[string]string{}
			for _, e := range cfg.Binaries {
				got[e.Name] = e.Alias
			}
			if len(got) != len(tc.want) {
				t.Fatalf("b.yaml entries %v, want %v", got, tc.want)
			}
			for n, a := range tc.want {
				if ga, ok := got[n]; !ok || ga != a {
					t.Errorf("entry %q: alias %q (present %v), want %q", n, ga, ok, a)
				}
			}
			for _, e := range cfg.Binaries {
				if v := tc.versions[e.Name]; e.Version != v {
					t.Errorf("entry %q: version %q, want %q", e.Name, e.Version, v)
				}
				if e.Name == "plain" && e.File != "/opt/plain" {
					t.Errorf("the plain entry: file %q, want /opt/plain (unchanged)", e.File)
				}
			}

			// The no-argument install of the written b.yaml installs each
			// binary at its own file.
			reread := NewSharedOptions(io, presets)
			reread.Config = cfg
			all := reread.GetBinariesFromConfig()
			var files []string
			for _, b := range all {
				files = append(files, filepath.Base(b.BinaryPath()))
			}
			slices.Sort(files)
			want := slices.Clone(tc.files)
			slices.Sort(want)
			if !slices.Equal(files, want) {
				t.Errorf("the b.yaml installs %v, want %v", files, want)
			}
		})
	}
}

// TestGetBinary_ProviderRefAliasIsNotAPreset: the alias of a provider-ref
// entry names the binary. A preset of that name does not replace the ref,
// and the named and the no-argument install give the same binary.
func TestGetBinary_ProviderRefAliasIsNotAPreset(t *testing.T) {
	binDir := t.TempDir()
	t.Setenv("PATH_BIN", binDir)
	presets := []*binary.Binary{{Name: "rtool", Context: context.Background()}}
	io := &streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}}
	shared := NewSharedOptions(io, presets)
	shared.Config = &state.State{Binaries: state.BinaryList{
		&binary.LocalBinary{Name: "github.com/org/rtool", Alias: "rtool", IsProviderRef: true},
	}}

	o := &InstallOptions{SharedOptions: shared}
	if err := o.Complete([]string{"github.com/org/rtool"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	named := o.specifiedBinaries[0]
	all := shared.GetBinariesFromConfig()
	if len(all) != 1 {
		t.Fatalf("GetBinariesFromConfig: %d binaries, want 1", len(all))
	}
	for _, b := range []*binary.Binary{named, all[0]} {
		if b.ProviderRef != "github.com/org/rtool" || !b.AutoDetect {
			t.Errorf("binary %q: provider ref %q, auto-detect %v; want the ref, not the preset", b.Name, b.ProviderRef, b.AutoDetect)
		}
		if got, want := b.BinaryPath(), filepath.Join(binDir, "rtool"); got != want {
			t.Errorf("binary %q: file %q, want %q", b.Name, got, want)
		}
	}
}

// TestComplete_AliasTakesTheFileOfItsEntry: a preset installed under --alias
// is the binary of the b.yaml entry of that name, which --add writes. It
// installs at that entry's file, or at its own file when there is no such
// entry, and the written b.yaml resolves to the installed file. An --alias
// that already names another binary is refused, and nothing is installed:
// an entry of that name of another binary, or a provider-ref entry whose
// binary has that name (its alias, or the name from the ref). A provider
// ref installed with --alias gets the same refusal; its own entry is not
// another binary.
func TestComplete_AliasTakesTheFileOfItsEntry(t *testing.T) {
	start := []*binary.LocalBinary{
		{Name: "plain", Version: "v1.0.0", File: "/opt/plain"},
		{Name: "x", Alias: "plain", Version: "v1.0.0", File: "/opt/x"},
		{Name: "y", Alias: "other", Version: "v1.0.0", File: "/opt/y"},
		{Name: "other", Version: "v1.0.0", File: "/opt/other"},
		{Name: "github.com/org/plain", Alias: "rplain", File: "/opt/rplain"},
		{Name: "github.com/org/tool", Alias: "tool"},
		{Name: "github.com/org/bare"},
	}
	onlyX := []*binary.LocalBinary{{Name: "x", Alias: "plain", Version: "v1.0.0", File: "/opt/x"}}
	for _, tc := range []struct {
		name, arg, flag string
		config          []*binary.LocalBinary // nil = start
		file            string                // "" = the default file, PATH_BIN/<alias>
		entry           string                // the entry --add writes; "" = the alias
		refused         bool
	}{
		{name: "its own name", arg: "plain@v2.0.0", flag: "plain", file: "/opt/plain"},
		{name: "an alias entry with a file", arg: "plain@v2.0.0", flag: "x", file: "/opt/x"},
		{name: "a new name", arg: "plain@v2.0.0", flag: "p"},
		{name: "its own name, only an alias entry of it", arg: "plain@v2.0.0", flag: "plain", config: onlyX},
		{name: "an alias entry of another preset", arg: "plain@v2.0.0", flag: "y", refused: true},
		{name: "the entry of another preset", arg: "plain@v2.0.0", flag: "other", refused: true},
		{name: "the entry of a provider ref of that name", arg: "plain@v2.0.0", flag: "github.com/org/plain", refused: true},
		{name: "an alias entry under a new name", arg: "x", flag: "p"},
		{name: "the alias of a provider ref", arg: "plain@v2.0.0", flag: "tool", refused: true},
		{name: "the binary name of a provider ref without an alias", arg: "plain@v2.0.0", flag: "bare", refused: true},
		{name: "a provider ref under the alias of another ref", arg: "github.com/org/bare", flag: "tool", refused: true},
		{name: "a provider ref under the entry of a preset", arg: "github.com/org/bare", flag: "other", refused: true},
		{name: "a provider ref under an alias entry of a preset", arg: "github.com/org/bare", flag: "x", refused: true},
		{name: "a provider ref under an alias entry of the preset of its name", arg: "github.com/org/plain", flag: "x", refused: true},
		{name: "a provider ref under its own alias", arg: "github.com/org/tool", flag: "tool", entry: "github.com/org/tool"},
		{name: "a provider ref under a new name", arg: "github.com/org/tool", flag: "t9", entry: "github.com/org/tool"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			binDir := t.TempDir()
			t.Setenv("PATH_BIN", binDir)
			configPath := filepath.Join(t.TempDir(), ".bin", "b.yaml")
			presets := []*binary.Binary{
				{Name: "plain", Context: context.Background()},
				{Name: "other", Context: context.Background()},
			}
			io := &streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}}
			shared := NewSharedOptions(io, presets)
			shared.ConfigPath = configPath
			entries := tc.config
			if entries == nil {
				entries = start
			}
			list := state.BinaryList{}
			for _, e := range entries {
				c := *e
				c.IsProviderRef = strings.Contains(c.Name, "/") // as the loader sets it
				list = append(list, &c)
			}
			shared.Config = &state.State{Binaries: list}

			o := &InstallOptions{SharedOptions: shared, Add: true, Alias: tc.flag}
			err := o.Complete([]string{tc.arg})
			if tc.refused {
				if err == nil {
					t.Fatalf("b install %s --alias %s: no error, want a refusal (installs %q)", tc.arg, tc.flag, o.specifiedBinaries[0].BinaryPath())
				}
				if !strings.Contains(err.Error(), "is another binary") {
					t.Errorf("b install %s --alias %s: error %q, want the other-binary refusal", tc.arg, tc.flag, err)
				}
				if len(o.specifiedBinaries) != 0 {
					t.Errorf("b install %s --alias %s: %d binaries to install after the refusal, want 0", tc.arg, tc.flag, len(o.specifiedBinaries))
				}
				return
			}
			if err != nil {
				t.Fatalf("Complete(%s): %v", tc.arg, err)
			}
			want := tc.file
			if want == "" {
				want = filepath.Join(binDir, tc.flag)
			}
			installed := o.specifiedBinaries[0].BinaryPath()
			if installed != want {
				t.Errorf("b install %s --alias %s installs %q, want %q", tc.arg, tc.flag, installed, want)
			}
			if err := o.addToConfig(o.specifiedBinaries); err != nil {
				t.Fatalf("addToConfig: %v", err)
			}
			cfg, err := state.LoadConfigFromPath(configPath)
			if err != nil {
				t.Fatalf("LoadConfigFromPath: %v", err)
			}
			for _, e := range cfg.Binaries {
				if e.Name == tc.flag && e.Alias == e.Name {
					t.Errorf("the written entry %q aliases itself", e.Name)
				}
			}
			entry := tc.entry
			if entry == "" {
				entry = tc.flag
			}
			reread := NewSharedOptions(io, presets)
			reread.Config = cfg
			b, ok := reread.GetBinary(entry)
			if !ok {
				t.Fatalf("the written b.yaml has no entry %q", entry)
			}
			if got := b.BinaryPath(); got != installed {
				t.Errorf("entry %q resolves to %q, but the binary was installed at %q", tc.flag, got, installed)
			}
		})
	}
}

// aliasSettingsConfig is a b.yaml with the own entry of the preset plain and
// two alias entries of it, as the loader builds it.
func aliasSettingsConfig(t *testing.T) *state.State {
	t.Helper()
	var cfg state.State
	if err := yaml.Unmarshal([]byte(`binaries:
  plain:
    version: v1.0.0
    onPost: echo plain
  x:
    alias: plain
    enforced: v3.0.0
    file: /opt/x
    onPost: echo x
  z:
    alias: plain
    version: v4.0.0
`), &cfg); err != nil {
		t.Fatal(err)
	}
	return &cfg
}

// TestComplete_AliasTakesTheSettingsOfItsEntry: a preset installed under an
// --alias that names an entry of the same preset is the binary of that
// entry. It takes that entry's version, enforced pin, file and onPost, as
// the no-argument install does; only a version on the command line wins.
// The settings of the entry named by the argument do not carry over.
func TestComplete_AliasTakesTheSettingsOfItsEntry(t *testing.T) {
	for _, tc := range []struct {
		arg, flag, version, file, onPost string // file "" = PATH_BIN/<flag>
		presetFile                       string // the preset's file, as BinaryPath leaves it
	}{
		{"plain", "x", "v3.0.0", "/opt/x", "echo x", ""},
		{"plain@v2.0.0", "x", "v2.0.0", "/opt/x", "echo x", ""},
		{"plain", "z", "v4.0.0", "", "", ""},
		{"plain", "z", "v4.0.0", "", "", "/cached/plain"},
		{"x", "plain", "v1.0.0", "", "echo plain", ""},
		{"x", "x", "v3.0.0", "/opt/x", "echo x", ""},
		{"z", "x", "v3.0.0", "/opt/x", "echo x", ""},
	} {
		t.Run(tc.arg+" --alias "+tc.flag+" "+tc.presetFile, func(t *testing.T) {
			binDir := t.TempDir()
			t.Setenv("PATH_BIN", binDir)
			presets := []*binary.Binary{{Name: "plain", File: tc.presetFile, Context: context.Background()}}
			shared := NewSharedOptions(&streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}}, presets)
			shared.Config = aliasSettingsConfig(t)

			o := &InstallOptions{SharedOptions: shared, Alias: tc.flag}
			if err := o.Complete([]string{tc.arg}); err != nil {
				t.Fatalf("Complete: %v", err)
			}
			b := o.specifiedBinaries[0]
			file := tc.file
			if file == "" {
				file = filepath.Join(binDir, tc.flag)
			}
			if b.Name != "plain" || b.Alias != tc.flag || b.Version != tc.version || b.BinaryPath() != file || b.OnPost != tc.onPost {
				t.Errorf("name %q, alias %q, version %q, file %q, onPost %q; want plain, %s, %s, %s, %q", b.Name, b.Alias, b.Version, b.BinaryPath(), b.OnPost, tc.flag, tc.version, file, tc.onPost)
			}
		})
	}
}

// TestCheckPin: only a version that --add writes can contradict a pin.
func TestCheckPin(t *testing.T) {
	shared := NewSharedOptions(&streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}}, nil)
	shared.Config = aliasSettingsConfig(t)
	o := &InstallOptions{SharedOptions: shared}
	for version, refused := range map[string]bool{"": false, "latest": false, "v3.0.0": false, "v2.0.0": true} {
		err := o.checkPin(&binary.Binary{Name: "plain", Alias: "x", Version: version})
		if (err != nil) != refused {
			t.Errorf("version %q: error %v, want refused %v", version, err, refused)
		}
	}
}

// TestAddToConfig_KeepsTheEnforcedPin: --add does not set the version of a
// b.yaml entry to a value its enforced pin contradicts. Without a version
// on the command line the entry's pin is the version; another version is
// refused before anything is installed, unless --fix moves the pin too.
func TestAddToConfig_KeepsTheEnforcedPin(t *testing.T) {
	for _, tc := range []struct {
		name, arg, flag   string
		fix               bool
		refused           bool
		version, enforced string // the entry x after --add
	}{
		{name: "the entry's pin", arg: "plain", flag: "x", version: "v3.0.0", enforced: "v3.0.0"},
		{name: "another version under --alias", arg: "plain@v2.0.0", flag: "x", refused: true},
		{name: "another version by its name", arg: "x@v2.0.0", refused: true},
		{name: "another version with --fix", arg: "plain@v2.0.0", flag: "x", fix: true, version: "v2.0.0", enforced: "v2.0.0"},
		{name: "the pinned version by its name", arg: "x@v3.0.0", version: "v3.0.0", enforced: "v3.0.0"},
		{name: "latest by its name, which --add does not write", arg: "x@latest", version: "", enforced: "v3.0.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PATH_BIN", t.TempDir())
			configPath := filepath.Join(t.TempDir(), ".bin", "b.yaml")
			presets := []*binary.Binary{{Name: "plain", Context: context.Background()}}
			shared := NewSharedOptions(&streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}}, presets)
			shared.ConfigPath = configPath
			shared.Config = aliasSettingsConfig(t)

			o := &InstallOptions{SharedOptions: shared, Add: true, Fix: tc.fix, Alias: tc.flag}
			err := o.Complete([]string{tc.arg})
			if tc.refused {
				if err == nil || !strings.Contains(err.Error(), "enforced") {
					t.Fatalf("error %v, want the refusal of the enforced pin", err)
				}
				if len(o.specifiedBinaries) != 0 {
					t.Errorf("%d binaries to install after the refusal, want 0", len(o.specifiedBinaries))
				}
				return
			}
			if err != nil {
				t.Fatalf("Complete: %v", err)
			}
			if err := o.addToConfig(o.specifiedBinaries); err != nil {
				t.Fatalf("addToConfig: %v", err)
			}
			cfg, err := state.LoadConfigFromPath(configPath)
			if err != nil {
				t.Fatalf("LoadConfigFromPath: %v", err)
			}
			for _, e := range cfg.Binaries {
				switch e.Name {
				case "x":
					if e.Version != tc.version || e.Enforced != tc.enforced || e.File != "/opt/x" {
						t.Errorf("entry x: version %q, enforced %q, file %q; want %s, %s, /opt/x", e.Version, e.Enforced, e.File, tc.version, tc.enforced)
					}
				case "plain":
					if e.Version != "v1.0.0" {
						t.Errorf("entry plain: version %q, want v1.0.0 (unchanged)", e.Version)
					}
				}
			}
		})
	}
}
