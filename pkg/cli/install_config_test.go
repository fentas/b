package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/fentas/b/pkg/binary"
	"github.com/fentas/b/pkg/state"
	"github.com/fentas/goodies/streams"
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
// first and lose its enforced version and its file.
func TestAddToConfig_UpdatesTheAliasEntry(t *testing.T) {
	t.Setenv("PATH_BIN", t.TempDir())
	configPath := filepath.Join(t.TempDir(), ".bin", "b.yaml")
	presets := []*binary.Binary{{Name: "rtool", Context: context.Background()}}
	io := &streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}}
	shared := NewSharedOptions(io, presets)
	shared.ConfigPath = configPath
	shared.Config = &state.State{Binaries: state.BinaryList{
		&binary.LocalBinary{Name: "tool", Alias: "rtool", Enforced: "v1.0.0", File: "/opt/x/tool"},
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
	if e.Name != "tool" || e.Alias != "rtool" || e.Enforced != "v1.0.0" || e.File != "/opt/x/tool" {
		t.Errorf("the entry: name %q, alias %q, enforced %q, file %q; want tool, rtool, v1.0.0, /opt/x/tool", e.Name, e.Alias, e.Enforced, e.File)
	}
}
