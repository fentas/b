package cli

import (
	"bytes"
	"context"
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
	t.Setenv("PATH_BIN", t.TempDir())
	presets := []*binary.Binary{{Name: "rtool", Context: context.Background()}}
	io := &streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}}
	shared := NewSharedOptions(io, presets)
	shared.Config = &state.State{Binaries: state.BinaryList{
		&binary.LocalBinary{Name: "tool", Alias: "rtool"},
	}}

	for _, tc := range []struct{ flag, want string }{
		{"", "tool"},
		{"mytool", "mytool"},
	} {
		o := &InstallOptions{SharedOptions: shared, Alias: tc.flag}
		if err := o.Complete([]string{"tool"}); err != nil {
			t.Fatalf("Complete(tool) with --alias %q: %v", tc.flag, err)
		}
		b := o.specifiedBinaries[0]
		if b.Name != "rtool" || b.Alias != tc.want {
			t.Errorf("b install tool, --alias %q: name %q, alias %q; want rtool, %q", tc.flag, b.Name, b.Alias, tc.want)
		}
	}
}
