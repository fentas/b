package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/fentas/b/pkg/binary"
	"github.com/fentas/b/pkg/env"
	"github.com/fentas/b/pkg/lock"
	"github.com/fentas/b/pkg/state"
	"github.com/fentas/goodies/streams"
)

// syncWriter wraps an io.Writer with a mutex for thread-safe writes.
// Needed because progress.NewWriter spawns a goroutine that writes concurrently.
type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (sw *syncWriter) Write(p []byte) (int, error) {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	return sw.w.Write(p)
}

// --- helpers for mocking package-level hooks ---

// saveHooks saves current function variables and restores them via t.Cleanup.
// Tests using saveHooks must NOT call t.Parallel() (shared mutable state).
func saveHooks(t *testing.T) {
	t.Helper()
	origSyncEnv := syncEnvFunc
	origResolveRef := resolveRefFunc
	origEnsureClone := ensureCloneF
	origFetch := fetchFunc
	origShowFile := showFileFunc
	origDiffNoIndex := diffNoIndexF
	origIsTTY := isTTYFunc
	t.Cleanup(func() {
		syncEnvFunc = origSyncEnv
		resolveRefFunc = origResolveRef
		ensureCloneF = origEnsureClone
		fetchFunc = origFetch
		showFileFunc = origShowFile
		diffNoIndexF = origDiffNoIndex
		isTTYFunc = origIsTTY
	})
}

// --- Validate ---

func TestUpdateOptions_Validate(t *testing.T) {
	tests := []struct {
		strategy string
		wantErr  bool
	}{
		{"", false},
		{"replace", false},
		{"client", false},
		{"merge", false},
		{"invalid", true},
		{"REPLACE", true}, // case-sensitive
	}

	for _, tt := range tests {
		o := &UpdateOptions{Strategy: tt.strategy}
		err := o.Validate()
		if (err != nil) != tt.wantErr {
			t.Errorf("Validate(strategy=%q) error = %v, wantErr %v", tt.strategy, err, tt.wantErr)
		}
	}
}

func TestIsTTY(t *testing.T) {
	got := isTTY()
	_ = got // just verify no panic
}

func TestStrategyConstants(t *testing.T) {
	if env.StrategyReplace != "replace" {
		t.Errorf("StrategyReplace = %q", env.StrategyReplace)
	}
	if env.StrategyClient != "client" {
		t.Errorf("StrategyClient = %q", env.StrategyClient)
	}
	if env.StrategyMerge != "merge" {
		t.Errorf("StrategyMerge = %q", env.StrategyMerge)
	}
}

// --- NewUpdateCmd ---

func TestNewUpdateCmd(t *testing.T) {
	io := &streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}}
	shared := NewSharedOptions(io, nil)
	cmd := NewUpdateCmd(shared)

	if cmd.Use != "update [binary|env...]" {
		t.Errorf("Use = %q", cmd.Use)
	}
	if len(cmd.Aliases) == 0 || cmd.Aliases[0] != "u" {
		t.Errorf("Alias = %v", cmd.Aliases)
	}
	if cmd.Short == "" {
		t.Error("Short should not be empty")
	}
	if cmd.Long == "" {
		t.Error("Long should not be empty")
	}

	f := cmd.Flags().Lookup("strategy")
	if f == nil {
		t.Fatal("strategy flag not registered")
	}
	if f.DefValue != "" {
		t.Errorf("strategy default = %q", f.DefValue)
	}
}

func TestNewUpdateCmd_RunE_NoConfig(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PATH_BIN", tmpDir)

	io := &streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}}
	shared := NewSharedOptions(io, nil)
	cmd := NewUpdateCmd(shared)

	// No config, no args → Complete returns error
	err := cmd.RunE(cmd, []string{})
	if err == nil {
		t.Fatal("expected error for no config")
	}
	if !strings.Contains(err.Error(), "no b.yaml") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNewUpdateCmd_RunE_ValidateError(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PATH_BIN", tmpDir)

	io := &streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}}
	shared := NewSharedOptions(io, nil)
	shared.Config = &state.State{}
	cmd := NewUpdateCmd(shared)

	// Set invalid strategy via flag
	cmd.Flags().Set("strategy", "bogus")

	err := cmd.RunE(cmd, []string{})
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "invalid strategy") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- Complete ---

func TestUpdateComplete_NoConfig(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PATH_BIN", tmpDir)

	io := &streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}}
	shared := NewSharedOptions(io, nil)
	shared.Config = nil

	o := &UpdateOptions{SharedOptions: shared}
	err := o.Complete([]string{})
	if err == nil || !strings.Contains(err.Error(), "no b.yaml") {
		t.Errorf("expected no-config error, got: %v", err)
	}
}

func TestUpdateComplete_UnknownBinary(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PATH_BIN", tmpDir)

	io := &streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}}
	shared := NewSharedOptions(io, nil)
	shared.Config = &state.State{}

	o := &UpdateOptions{SharedOptions: shared}
	err := o.Complete([]string{"nosuchbin"})
	if err == nil || !strings.Contains(err.Error(), "unknown binary or env") {
		t.Errorf("expected unknown binary error, got: %v", err)
	}
}

func TestUpdateComplete_Resets(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PATH_BIN", tmpDir)

	presets := []*binary.Binary{{Name: "jq", Version: "1.7"}}
	io := &streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}}
	shared := NewSharedOptions(io, presets)

	o := &UpdateOptions{SharedOptions: shared}

	// First call
	if err := o.Complete([]string{"jq"}); err != nil {
		t.Fatalf("first Complete: %v", err)
	}
	if len(o.specifiedBinaries) != 1 {
		t.Fatalf("first call: specifiedBinaries = %d", len(o.specifiedBinaries))
	}

	// Second call should reset
	if err := o.Complete([]string{"jq"}); err != nil {
		t.Fatalf("second Complete: %v", err)
	}
	if len(o.specifiedBinaries) != 1 {
		t.Errorf("second call should reset: specifiedBinaries = %d, want 1", len(o.specifiedBinaries))
	}
}

func TestUpdateComplete_NoArgs_WithConfig(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PATH_BIN", tmpDir)

	io := &streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}}
	shared := NewSharedOptions(io, nil)
	shared.Config = &state.State{}

	o := &UpdateOptions{SharedOptions: shared}
	if err := o.Complete([]string{}); err != nil {
		t.Errorf("Complete() with config + no args should succeed: %v", err)
	}
}

// --- Alias tests from issue #79 ---

func TestUpdateAlias_VersionRetained(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PATH_BIN", tmpDir)

	presets := []*binary.Binary{{Name: "renvsubst", Version: "1.0"}}
	io := &streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}}
	shared := NewSharedOptions(io, presets)
	shared.Config = &state.State{
		Binaries: state.BinaryList{
			&binary.LocalBinary{Name: "envsubst", Alias: "renvsubst"},
		},
	}

	o := &UpdateOptions{SharedOptions: shared}
	if err := o.Complete([]string{"envsubst@2.0"}); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if len(o.specifiedBinaries) != 1 {
		t.Fatalf("specifiedBinaries = %d, want 1", len(o.specifiedBinaries))
	}
	if o.specifiedBinaries[0].Version != "2.0" {
		t.Errorf("stored version = %q, want %q (issue #79)", o.specifiedBinaries[0].Version, "2.0")
	}
	if o.specifiedBinaries[0].Alias != "envsubst" {
		t.Errorf("stored alias = %q, want %q", o.specifiedBinaries[0].Alias, "envsubst")
	}
}

func TestUpdateAlias_BinaryPath(t *testing.T) {
	presets := []*binary.Binary{{Name: "renvsubst", Version: "1.0"}}
	io := &streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}}
	shared := NewSharedOptions(io, presets)
	shared.Config = &state.State{
		Binaries: state.BinaryList{
			&binary.LocalBinary{Name: "envsubst", Alias: "renvsubst"},
		},
	}

	b, ok := shared.GetBinary("envsubst")
	if !ok {
		t.Fatal("expected to find envsubst via alias")
	}
	base := filepath.Base(b.BinaryPath())
	if base != "envsubst" {
		t.Errorf("BinaryPath base = %q, want %q", base, "envsubst")
	}
}

func TestUpdateAlias_GetBinariesFromConfig_Path(t *testing.T) {
	presets := []*binary.Binary{{Name: "renvsubst", Version: "1.0"}}
	io := &streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}}
	shared := NewSharedOptions(io, presets)
	shared.Config = &state.State{
		Binaries: state.BinaryList{
			&binary.LocalBinary{Name: "envsubst", Alias: "renvsubst"},
		},
	}

	binaries := shared.GetBinariesFromConfig()
	if len(binaries) != 1 {
		t.Fatalf("got %d binaries, want 1", len(binaries))
	}
	b := binaries[0]
	if b.Name != "renvsubst" {
		t.Errorf("Name = %q", b.Name)
	}
	if b.Alias != "envsubst" {
		t.Errorf("Alias = %q", b.Alias)
	}
	if filepath.Base(b.BinaryPath()) != "envsubst" {
		t.Errorf("BinaryPath base = %q, want envsubst", filepath.Base(b.BinaryPath()))
	}
}

func TestUpdateAlias_PresetVersionNotMutated(t *testing.T) {
	presets := []*binary.Binary{{Name: "renvsubst", Version: "1.0"}}
	io := &streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}}
	shared := NewSharedOptions(io, presets)
	shared.Config = &state.State{
		Binaries: state.BinaryList{
			&binary.LocalBinary{Name: "envsubst", Alias: "renvsubst"},
		},
	}

	b, _ := shared.GetBinary("envsubst")
	b.Version = "9.9"

	preset, _ := shared.GetBinary("renvsubst")
	if preset.Version != "1.0" {
		t.Errorf("preset version mutated: got %q, want %q", preset.Version, "1.0")
	}
}

func TestUpdateComplete_AliasVersionFromArg(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PATH_BIN", tmpDir)

	presets := []*binary.Binary{{Name: "renvsubst", Version: "1.0"}}
	io := &streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}}
	shared := NewSharedOptions(io, presets)
	shared.Config = &state.State{
		Binaries: state.BinaryList{
			&binary.LocalBinary{Name: "envsubst", Alias: "renvsubst"},
		},
	}

	o := &UpdateOptions{SharedOptions: shared}
	if err := o.Complete([]string{"envsubst@2.0"}); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if len(o.specifiedBinaries) != 1 {
		t.Fatalf("specifiedBinaries = %d, want 1", len(o.specifiedBinaries))
	}
	b := o.specifiedBinaries[0]
	if b.Version != "2.0" {
		t.Errorf("version = %q, want %q", b.Version, "2.0")
	}
	if b.Alias != "envsubst" {
		t.Errorf("alias = %q, want %q", b.Alias, "envsubst")
	}
	if b.Name != "renvsubst" {
		t.Errorf("name = %q, want %q", b.Name, "renvsubst")
	}
}

func TestUpdateComplete_EnvRefsStored(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PATH_BIN", tmpDir)

	presets := []*binary.Binary{{Name: "jq", Version: "1.7"}}
	io := &streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}}
	shared := NewSharedOptions(io, presets)
	shared.Config = &state.State{
		Binaries: state.BinaryList{&binary.LocalBinary{Name: "jq"}},
		Envs:     state.EnvList{{Key: "github.com/org/infra"}},
	}

	o := &UpdateOptions{SharedOptions: shared}
	if err := o.Complete([]string{"jq", "github.com/org/infra"}); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if len(o.specifiedBinaries) != 1 {
		t.Errorf("specifiedBinaries = %d, want 1", len(o.specifiedBinaries))
	}
	if len(o.specifiedEnvRefs) != 1 {
		t.Errorf("specifiedEnvRefs = %d, want 1", len(o.specifiedEnvRefs))
	}
	if o.specifiedBinaries[0].Name != "jq" {
		t.Errorf("binary name = %q", o.specifiedBinaries[0].Name)
	}
	if o.specifiedEnvRefs[0] != "github.com/org/infra" {
		t.Errorf("env ref = %q", o.specifiedEnvRefs[0])
	}
}

func TestUpdateComplete_PresetVersionFromArg(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PATH_BIN", tmpDir)

	presets := []*binary.Binary{{Name: "jq", Version: "1.6"}}
	io := &streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}}
	shared := NewSharedOptions(io, presets)

	o := &UpdateOptions{SharedOptions: shared}
	if err := o.Complete([]string{"jq@1.7"}); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if len(o.specifiedBinaries) != 1 {
		t.Fatalf("specifiedBinaries = %d, want 1", len(o.specifiedBinaries))
	}
	if o.specifiedBinaries[0].Version != "1.7" {
		t.Errorf("version = %q, want %q", o.specifiedBinaries[0].Version, "1.7")
	}
}

// --- Run / runAll / runSpecified ---

func TestRun_RoutesToRunSpecified(t *testing.T) {
	saveHooks(t)
	syncEnvFunc = func(cfg env.EnvConfig, projectRoot, cacheRoot string, lockEntry *lock.EnvEntry) (*env.SyncResult, error) {
		return &env.SyncResult{Skipped: true, Message: "up to date"}, nil
	}

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "b.yaml")
	os.WriteFile(configPath, []byte("binaries: {}\n"), 0644)
	lk := &lock.Lock{Version: 1}
	lock.WriteLock(tmpDir, lk, "test")

	var outBuf bytes.Buffer
	o := &UpdateOptions{
		SharedOptions: &SharedOptions{
			IO:               &streams.IO{Out: &outBuf, ErrOut: &bytes.Buffer{}},
			ConfigPath:       configPath,
			loadedConfigPath: configPath,
			Config: &state.State{
				Envs: state.EnvList{{Key: "github.com/org/infra"}},
			},
		},
		specifiedEnvRefs: []string{"github.com/org/infra"},
	}

	if err := o.Run(); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(outBuf.String(), "up to date") {
		t.Errorf("expected 'up to date' in output, got: %s", outBuf.String())
	}
}

func TestRunAll_NoBinariesOrEnvs(t *testing.T) {
	var outBuf bytes.Buffer
	o := &UpdateOptions{
		SharedOptions: &SharedOptions{
			IO:     &streams.IO{Out: &outBuf, ErrOut: &bytes.Buffer{}},
			Config: &state.State{},
		},
	}

	if err := o.runAll(); err != nil {
		t.Fatalf("runAll() error = %v", err)
	}
	if !strings.Contains(outBuf.String(), "No binaries or envs to update") {
		t.Errorf("expected empty message, got: %s", outBuf.String())
	}
}

func TestRunAll_NilConfig(t *testing.T) {
	var outBuf bytes.Buffer
	o := &UpdateOptions{
		SharedOptions: &SharedOptions{
			IO:     &streams.IO{Out: &outBuf, ErrOut: &bytes.Buffer{}},
			Config: nil,
		},
	}

	if err := o.runAll(); err != nil {
		t.Fatalf("runAll() error = %v", err)
	}
	if !strings.Contains(outBuf.String(), "No binaries or envs to update") {
		t.Errorf("expected empty message, got: %s", outBuf.String())
	}
}

func TestRunAll_WithEnvs(t *testing.T) {
	saveHooks(t)
	syncEnvFunc = func(cfg env.EnvConfig, projectRoot, cacheRoot string, lockEntry *lock.EnvEntry) (*env.SyncResult, error) {
		return &env.SyncResult{Skipped: true, Message: "up to date"}, nil
	}

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "b.yaml")
	os.WriteFile(configPath, []byte("binaries: {}\n"), 0644)
	lk := &lock.Lock{Version: 1}
	lock.WriteLock(tmpDir, lk, "test")

	var outBuf bytes.Buffer
	o := &UpdateOptions{
		SharedOptions: &SharedOptions{
			IO:               &streams.IO{Out: &outBuf, ErrOut: &bytes.Buffer{}},
			ConfigPath:       configPath,
			loadedConfigPath: configPath,
			Config: &state.State{
				Envs: state.EnvList{{Key: "github.com/org/infra"}},
			},
		},
	}

	if err := o.runAll(); err != nil {
		t.Fatalf("runAll() error = %v", err)
	}
	if !strings.Contains(outBuf.String(), "up to date") {
		t.Errorf("expected env output, got: %s", outBuf.String())
	}
}

func TestRunSpecified_OnlyEnvs(t *testing.T) {
	saveHooks(t)
	called := false
	syncEnvFunc = func(cfg env.EnvConfig, projectRoot, cacheRoot string, lockEntry *lock.EnvEntry) (*env.SyncResult, error) {
		called = true
		return &env.SyncResult{Skipped: true, Message: "ok"}, nil
	}

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "b.yaml")
	os.WriteFile(configPath, []byte("binaries: {}\n"), 0644)
	lk := &lock.Lock{Version: 1}
	lock.WriteLock(tmpDir, lk, "test")

	o := &UpdateOptions{
		SharedOptions: &SharedOptions{
			IO:               &streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}},
			ConfigPath:       configPath,
			loadedConfigPath: configPath,
			Config: &state.State{
				Envs: state.EnvList{{Key: "github.com/org/infra"}},
			},
		},
		specifiedEnvRefs: []string{"github.com/org/infra"},
	}

	if err := o.runSpecified(); err != nil {
		t.Fatalf("runSpecified() error = %v", err)
	}
	if !called {
		t.Error("syncEnvFunc not called")
	}
}

// --- updateEnvs ---

func TestUpdateEnvs_NilConfig(t *testing.T) {
	o := &UpdateOptions{
		SharedOptions: &SharedOptions{
			IO:     &streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}},
			Config: nil,
		},
	}
	if err := o.updateEnvs(nil); err != nil {
		t.Fatalf("expected nil error for nil config, got: %v", err)
	}
}

func TestUpdateEnvs_Skipped(t *testing.T) {
	saveHooks(t)
	syncEnvFunc = func(cfg env.EnvConfig, projectRoot, cacheRoot string, lockEntry *lock.EnvEntry) (*env.SyncResult, error) {
		return &env.SyncResult{Skipped: true, Message: "already up-to-date"}, nil
	}

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "b.yaml")
	os.WriteFile(configPath, []byte("binaries: {}\n"), 0644)
	lk := &lock.Lock{Version: 1}
	lock.WriteLock(tmpDir, lk, "test")

	var outBuf bytes.Buffer
	o := &UpdateOptions{
		SharedOptions: &SharedOptions{
			IO:               &streams.IO{Out: &outBuf, ErrOut: &bytes.Buffer{}},
			ConfigPath:       configPath,
			loadedConfigPath: configPath,
			Config: &state.State{
				Envs: state.EnvList{{Key: "github.com/org/repo"}},
			},
		},
	}

	if err := o.updateEnvs(nil); err != nil {
		t.Fatalf("updateEnvs error = %v", err)
	}
	if !strings.Contains(outBuf.String(), "already up-to-date") {
		t.Errorf("expected skip message, got: %s", outBuf.String())
	}
}

func TestUpdateEnvs_SyncError(t *testing.T) {
	saveHooks(t)
	syncEnvFunc = func(cfg env.EnvConfig, projectRoot, cacheRoot string, lockEntry *lock.EnvEntry) (*env.SyncResult, error) {
		return nil, fmt.Errorf("git clone failed")
	}

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "b.yaml")
	os.WriteFile(configPath, []byte("binaries: {}\n"), 0644)
	lk := &lock.Lock{Version: 1}
	lock.WriteLock(tmpDir, lk, "test")

	var errBuf bytes.Buffer
	o := &UpdateOptions{
		SharedOptions: &SharedOptions{
			IO:               &streams.IO{Out: &bytes.Buffer{}, ErrOut: &errBuf},
			ConfigPath:       configPath,
			loadedConfigPath: configPath,
			Config: &state.State{
				Envs: state.EnvList{{Key: "github.com/org/repo"}},
			},
		},
	}

	if err := o.updateEnvs(nil); err != nil {
		t.Fatalf("updateEnvs should not return error on per-env failure: %v", err)
	}
	if !strings.Contains(errBuf.String(), "git clone failed") {
		t.Errorf("expected error output, got: %s", errBuf.String())
	}
}

func TestUpdateEnvs_Updated(t *testing.T) {
	saveHooks(t)
	syncEnvFunc = func(cfg env.EnvConfig, projectRoot, cacheRoot string, lockEntry *lock.EnvEntry) (*env.SyncResult, error) {
		return &env.SyncResult{
			Ref:            cfg.Ref,
			Label:          cfg.Label,
			Commit:         "def4567890",
			PreviousCommit: "abc1234567",
			Message:        "2 files synced",
			Files: []lock.LockFile{
				{Path: "a.yaml", Dest: "a.yaml", Status: "replaced"},
				{Path: "b.yaml", Dest: "b.yaml", Status: "kept"},
			},
		}, nil
	}

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "b.yaml")
	os.WriteFile(configPath, []byte("binaries: {}\n"), 0644)
	lk := &lock.Lock{Version: 1}
	lock.WriteLock(tmpDir, lk, "test")

	var outBuf bytes.Buffer
	o := &UpdateOptions{
		SharedOptions: &SharedOptions{
			IO:               &streams.IO{Out: &outBuf, ErrOut: &bytes.Buffer{}},
			ConfigPath:       configPath,
			loadedConfigPath: configPath,
			Config: &state.State{
				Envs: state.EnvList{{Key: "github.com/org/repo"}},
			},
		},
	}

	if err := o.updateEnvs(nil); err != nil {
		t.Fatalf("updateEnvs error = %v", err)
	}
	out := outBuf.String()
	if !strings.Contains(out, "2 files synced") {
		t.Errorf("expected sync message, got: %s", out)
	}
	if !strings.Contains(out, "replaced") {
		t.Errorf("expected replaced file status, got: %s", out)
	}
	if !strings.Contains(out, "kept") {
		t.Errorf("expected kept file status, got: %s", out)
	}

	// Verify lock was written with the new entry
	lk2, _ := lock.ReadLock(tmpDir)
	entry := lk2.FindEnv("github.com/org/repo", "")
	if entry == nil {
		t.Fatal("expected env entry in lock after update")
	}
	if entry.Commit != "def4567890" {
		t.Errorf("lock commit = %q", entry.Commit)
	}
}

func TestUpdateEnvs_WithConflicts(t *testing.T) {
	saveHooks(t)
	syncEnvFunc = func(cfg env.EnvConfig, projectRoot, cacheRoot string, lockEntry *lock.EnvEntry) (*env.SyncResult, error) {
		return &env.SyncResult{
			Ref:       cfg.Ref,
			Commit:    "abc123",
			Message:   "synced with conflicts",
			Files:     []lock.LockFile{{Path: "a.yaml", Dest: "a.yaml", Status: "conflict"}},
			Conflicts: 1,
		}, nil
	}

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "b.yaml")
	os.WriteFile(configPath, []byte("binaries: {}\n"), 0644)
	lk := &lock.Lock{Version: 1}
	lock.WriteLock(tmpDir, lk, "test")

	var errBuf bytes.Buffer
	o := &UpdateOptions{
		SharedOptions: &SharedOptions{
			IO:               &streams.IO{Out: &bytes.Buffer{}, ErrOut: &errBuf},
			ConfigPath:       configPath,
			loadedConfigPath: configPath,
			Config: &state.State{
				Envs: state.EnvList{{Key: "github.com/org/repo"}},
			},
		},
	}

	if err := o.updateEnvs(nil); err != nil {
		t.Fatalf("updateEnvs error = %v", err)
	}
	if !strings.Contains(errBuf.String(), "1 file(s) have merge conflicts") {
		t.Errorf("expected conflict warning, got: %s", errBuf.String())
	}
}

func TestUpdateEnvs_RefFiltering(t *testing.T) {
	saveHooks(t)
	var calledRefs []string
	syncEnvFunc = func(cfg env.EnvConfig, projectRoot, cacheRoot string, lockEntry *lock.EnvEntry) (*env.SyncResult, error) {
		calledRefs = append(calledRefs, cfg.Ref)
		return &env.SyncResult{Skipped: true, Message: "ok"}, nil
	}

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "b.yaml")
	os.WriteFile(configPath, []byte("binaries: {}\n"), 0644)
	lk := &lock.Lock{Version: 1}
	lock.WriteLock(tmpDir, lk, "test")

	o := &UpdateOptions{
		SharedOptions: &SharedOptions{
			IO:               &streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}},
			ConfigPath:       configPath,
			loadedConfigPath: configPath,
			Config: &state.State{
				Envs: state.EnvList{
					{Key: "github.com/org/infra"},
					{Key: "github.com/org/other"},
				},
			},
		},
	}

	// Only update "github.com/org/infra"
	if err := o.updateEnvs([]string{"github.com/org/infra"}); err != nil {
		t.Fatalf("updateEnvs error = %v", err)
	}
	if len(calledRefs) != 1 || calledRefs[0] != "github.com/org/infra" {
		t.Errorf("expected only infra to be synced, got: %v", calledRefs)
	}
}

func TestUpdateEnvs_StrategyOverride(t *testing.T) {
	saveHooks(t)
	var capturedStrategy string
	syncEnvFunc = func(cfg env.EnvConfig, projectRoot, cacheRoot string, lockEntry *lock.EnvEntry) (*env.SyncResult, error) {
		capturedStrategy = cfg.Strategy
		return &env.SyncResult{Skipped: true, Message: "ok"}, nil
	}
	isTTYFunc = func() bool { return false }

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "b.yaml")
	os.WriteFile(configPath, []byte("binaries: {}\n"), 0644)
	lk := &lock.Lock{Version: 1}
	lock.WriteLock(tmpDir, lk, "test")

	o := &UpdateOptions{
		SharedOptions: &SharedOptions{
			IO:               &streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}},
			ConfigPath:       configPath,
			loadedConfigPath: configPath,
			Config: &state.State{
				Envs: state.EnvList{{Key: "github.com/org/repo", Strategy: "client"}},
			},
		},
		Strategy: "merge", // CLI override
	}

	o.updateEnvs(nil)
	if capturedStrategy != "merge" {
		t.Errorf("strategy = %q, want %q (CLI should override config)", capturedStrategy, "merge")
	}
}

func TestUpdateEnvs_TTYConflictResolver(t *testing.T) {
	saveHooks(t)
	syncEnvFunc = func(cfg env.EnvConfig, projectRoot, cacheRoot string, lockEntry *lock.EnvEntry) (*env.SyncResult, error) {
		// Verify ResolveConflict was set when TTY
		if cfg.ResolveConflict == nil {
			t.Error("expected ResolveConflict to be set for TTY + replace strategy")
		}
		return &env.SyncResult{Skipped: true, Message: "ok"}, nil
	}
	isTTYFunc = func() bool { return true }

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "b.yaml")
	os.WriteFile(configPath, []byte("binaries: {}\n"), 0644)
	lk := &lock.Lock{Version: 1}
	lock.WriteLock(tmpDir, lk, "test")

	o := &UpdateOptions{
		SharedOptions: &SharedOptions{
			IO:               &streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}},
			ConfigPath:       configPath,
			loadedConfigPath: configPath,
			Config: &state.State{
				Envs: state.EnvList{{Key: "github.com/org/repo"}},
			},
		},
		stdinReader: strings.NewReader("r\n"), // mock stdin
	}

	o.updateEnvs(nil)
}

// --- callUpdateBinaries ---

func TestCallUpdateBinaries_UsesHook(t *testing.T) {
	var called bool
	o := &UpdateOptions{
		SharedOptions:   &SharedOptions{IO: &streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}}},
		updateBinariesF: func(bins []*binary.Binary) error { called = true; return nil },
	}
	err := o.callUpdateBinaries(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("hook not called")
	}
}

func TestCallUpdateBinaries_DefaultPath(t *testing.T) {
	if raceEnabled {
		t.Skip("skipping: go-pretty progress.Render/Stop race (library bug)")
	}
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "jq")
	os.WriteFile(binPath, []byte("binary"), 0755)

	sw := &syncWriter{w: &bytes.Buffer{}}
	o := &UpdateOptions{
		SharedOptions: &SharedOptions{IO: &streams.IO{Out: sw, ErrOut: &bytes.Buffer{}}},
		// updateBinariesF is nil → uses o.updateBinaries
	}
	err := o.callUpdateBinaries([]*binary.Binary{{Name: "jq", Version: "1.7", File: binPath}})
	if err != nil {
		t.Fatal(err)
	}
}

// --- updateBinaries ---

// The following tests call updateBinaries directly which triggers a race in
// the go-pretty progress library (Render/Stop race). Skipped under -race.

func TestUpdateBinaries_WithBinary(t *testing.T) {
	if raceEnabled {
		t.Skip("skipping: go-pretty progress.Render/Stop race (library bug)")
	}
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "jq")
	os.WriteFile(binPath, []byte("binary"), 0755)

	sw := &syncWriter{w: &bytes.Buffer{}}
	o := &UpdateOptions{
		SharedOptions: &SharedOptions{
			IO: &streams.IO{Out: sw, ErrOut: &bytes.Buffer{}},
		},
	}

	b := &binary.Binary{
		Name:    "jq",
		Version: "1.7",
		File:    binPath,
	}

	err := o.updateBinaries([]*binary.Binary{b})
	if err != nil {
		t.Fatalf("updateBinaries error = %v", err)
	}
}

func TestUpdateBinaries_WithAlias(t *testing.T) {
	if raceEnabled {
		t.Skip("skipping: go-pretty progress.Render/Stop race (library bug)")
	}
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "envsubst")
	os.WriteFile(binPath, []byte("binary"), 0755)

	sw := &syncWriter{w: &bytes.Buffer{}}
	o := &UpdateOptions{
		SharedOptions: &SharedOptions{
			IO: &streams.IO{Out: sw, ErrOut: &bytes.Buffer{}},
		},
	}

	b := &binary.Binary{
		Name:    "renvsubst",
		Alias:   "envsubst",
		Version: "1.0",
		File:    binPath,
	}

	err := o.updateBinaries([]*binary.Binary{b})
	if err != nil {
		t.Fatalf("updateBinaries error = %v", err)
	}
}

func TestUpdateBinaries_Force(t *testing.T) {
	if raceEnabled {
		t.Skip("skipping: go-pretty progress.Render/Stop race (library bug)")
	}
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "jq")
	os.WriteFile(binPath, []byte("binary"), 0755)

	sw := &syncWriter{w: &bytes.Buffer{}}
	o := &UpdateOptions{
		SharedOptions: &SharedOptions{
			IO:    &streams.IO{Out: sw, ErrOut: &bytes.Buffer{}},
			Force: true,
		},
	}

	b := &binary.Binary{
		Name:    "jq",
		Version: "1.7",
		File:    binPath,
	}

	err := o.updateBinaries([]*binary.Binary{b})
	if err != nil {
		t.Fatalf("updateBinaries error = %v", err)
	}
}

// --- interactiveConflictResolver ---

func TestInteractiveResolver_Replace(t *testing.T) {
	var errBuf bytes.Buffer
	o := &UpdateOptions{
		SharedOptions: &SharedOptions{
			IO: &streams.IO{Out: &bytes.Buffer{}, ErrOut: &errBuf},
		},
		stdinReader: strings.NewReader("r\n"),
	}

	resolver := o.interactiveConflictResolver("github.com/org/repo", &lock.Lock{})
	result := resolver("src/a.yaml", "dest/a.yaml")
	if result != env.StrategyReplace {
		t.Errorf("result = %q, want %q", result, env.StrategyReplace)
	}
}

func TestInteractiveResolver_ReplaceFullWord(t *testing.T) {
	o := &UpdateOptions{
		SharedOptions: &SharedOptions{
			IO: &streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}},
		},
		stdinReader: strings.NewReader("replace\n"),
	}

	resolver := o.interactiveConflictResolver("ref", &lock.Lock{})
	if got := resolver("s", "d"); got != env.StrategyReplace {
		t.Errorf("got %q", got)
	}
}

func TestInteractiveResolver_Keep(t *testing.T) {
	o := &UpdateOptions{
		SharedOptions: &SharedOptions{
			IO: &streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}},
		},
		stdinReader: strings.NewReader("k\n"),
	}

	resolver := o.interactiveConflictResolver("ref", &lock.Lock{})
	if got := resolver("s", "d"); got != env.StrategyClient {
		t.Errorf("got %q, want %q", got, env.StrategyClient)
	}
}

func TestInteractiveResolver_Merge(t *testing.T) {
	o := &UpdateOptions{
		SharedOptions: &SharedOptions{
			IO: &streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}},
		},
		stdinReader: strings.NewReader("m\n"),
	}

	resolver := o.interactiveConflictResolver("ref", &lock.Lock{})
	if got := resolver("s", "d"); got != env.StrategyMerge {
		t.Errorf("got %q, want %q", got, env.StrategyMerge)
	}
}

func TestInteractiveResolver_InvalidThenReplace(t *testing.T) {
	var errBuf bytes.Buffer
	o := &UpdateOptions{
		SharedOptions: &SharedOptions{
			IO: &streams.IO{Out: &bytes.Buffer{}, ErrOut: &errBuf},
		},
		stdinReader: strings.NewReader("x\nr\n"),
	}

	resolver := o.interactiveConflictResolver("ref", &lock.Lock{})
	result := resolver("s", "d")
	if result != env.StrategyReplace {
		t.Errorf("got %q", result)
	}
	if !strings.Contains(errBuf.String(), "Invalid choice") {
		t.Errorf("expected invalid choice warning, got: %s", errBuf.String())
	}
}

func TestInteractiveResolver_EOF(t *testing.T) {
	o := &UpdateOptions{
		SharedOptions: &SharedOptions{
			IO: &streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}},
		},
		stdinReader: strings.NewReader(""), // immediate EOF
	}

	resolver := o.interactiveConflictResolver("ref", &lock.Lock{})
	result := resolver("s", "d")
	if result != env.StrategyReplace {
		t.Errorf("got %q, want %q (default on EOF)", result, env.StrategyReplace)
	}
}

func TestInteractiveResolver_DiffThenReplace(t *testing.T) {
	saveHooks(t)
	// showDiff will fail because file doesn't exist, but that's fine
	resolveRefFunc = func(url, version string) (string, error) {
		return "", fmt.Errorf("no network")
	}

	var errBuf bytes.Buffer
	o := &UpdateOptions{
		SharedOptions: &SharedOptions{
			IO: &streams.IO{Out: &bytes.Buffer{}, ErrOut: &errBuf},
		},
		stdinReader: strings.NewReader("d\nr\n"),
	}

	resolver := o.interactiveConflictResolver("github.com/org/repo", &lock.Lock{})
	result := resolver("src.yaml", "/nonexistent/file.yaml")
	if result != env.StrategyReplace {
		t.Errorf("got %q", result)
	}
	// showDiff should have printed an error about reading local file
	if !strings.Contains(errBuf.String(), "Error reading local file") {
		t.Errorf("expected file read error from showDiff, got: %s", errBuf.String())
	}
}

// --- showDiff ---

func TestShowDiff_LocalFileError(t *testing.T) {
	var errBuf bytes.Buffer
	o := &UpdateOptions{
		SharedOptions: &SharedOptions{
			IO: &streams.IO{Out: &bytes.Buffer{}, ErrOut: &errBuf},
		},
	}

	o.showDiff("github.com/org/repo", "src.yaml", "/nonexistent/file.yaml", &lock.Lock{})
	if !strings.Contains(errBuf.String(), "Error reading local file") {
		t.Errorf("expected file error, got: %s", errBuf.String())
	}
}

func TestShowDiff_ResolveRefError(t *testing.T) {
	saveHooks(t)
	resolveRefFunc = func(url, version string) (string, error) {
		return "", fmt.Errorf("network error")
	}

	tmpDir := t.TempDir()
	localFile := filepath.Join(tmpDir, "config.yaml")
	os.WriteFile(localFile, []byte("local content"), 0644)

	var errBuf bytes.Buffer
	o := &UpdateOptions{
		SharedOptions: &SharedOptions{
			IO: &streams.IO{Out: &bytes.Buffer{}, ErrOut: &errBuf},
		},
	}

	o.showDiff("github.com/org/repo", "src.yaml", localFile, &lock.Lock{})
	if !strings.Contains(errBuf.String(), "Cannot resolve upstream for diff") {
		t.Errorf("expected resolve error, got: %s", errBuf.String())
	}
}

func TestShowDiff_CloneError(t *testing.T) {
	saveHooks(t)
	resolveRefFunc = func(url, version string) (string, error) {
		return "abc123", nil
	}
	ensureCloneF = func(cacheRoot, baseRef, url string) error {
		return fmt.Errorf("clone failed")
	}

	tmpDir := t.TempDir()
	localFile := filepath.Join(tmpDir, "config.yaml")
	os.WriteFile(localFile, []byte("local content"), 0644)

	var errBuf bytes.Buffer
	o := &UpdateOptions{
		SharedOptions: &SharedOptions{
			IO: &streams.IO{Out: &bytes.Buffer{}, ErrOut: &errBuf},
		},
	}

	o.showDiff("github.com/org/repo", "src.yaml", localFile, &lock.Lock{})
	if !strings.Contains(errBuf.String(), "Cannot clone upstream for diff") {
		t.Errorf("expected clone error, got: %s", errBuf.String())
	}
}

func TestShowDiff_FetchError(t *testing.T) {
	saveHooks(t)
	resolveRefFunc = func(url, version string) (string, error) { return "abc123", nil }
	ensureCloneF = func(cacheRoot, baseRef, url string) error { return nil }
	fetchFunc = func(cacheRoot, baseRef, commit string) error {
		return fmt.Errorf("fetch failed")
	}

	tmpDir := t.TempDir()
	localFile := filepath.Join(tmpDir, "config.yaml")
	os.WriteFile(localFile, []byte("local"), 0644)

	var errBuf bytes.Buffer
	o := &UpdateOptions{
		SharedOptions: &SharedOptions{
			IO: &streams.IO{Out: &bytes.Buffer{}, ErrOut: &errBuf},
		},
	}

	o.showDiff("github.com/org/repo", "src.yaml", localFile, &lock.Lock{})
	if !strings.Contains(errBuf.String(), "Cannot fetch upstream for diff") {
		t.Errorf("expected fetch error, got: %s", errBuf.String())
	}
}

func TestShowDiff_ShowFileError(t *testing.T) {
	saveHooks(t)
	resolveRefFunc = func(url, version string) (string, error) { return "abc123", nil }
	ensureCloneF = func(cacheRoot, baseRef, url string) error { return nil }
	fetchFunc = func(cacheRoot, baseRef, commit string) error { return nil }
	showFileFunc = func(cacheRoot, baseRef, commit, path string) ([]byte, error) {
		return nil, fmt.Errorf("file not found in tree")
	}

	tmpDir := t.TempDir()
	localFile := filepath.Join(tmpDir, "config.yaml")
	os.WriteFile(localFile, []byte("local"), 0644)

	var errBuf bytes.Buffer
	o := &UpdateOptions{
		SharedOptions: &SharedOptions{
			IO: &streams.IO{Out: &bytes.Buffer{}, ErrOut: &errBuf},
		},
	}

	o.showDiff("github.com/org/repo", "src.yaml", localFile, &lock.Lock{})
	if !strings.Contains(errBuf.String(), "Cannot read upstream file for diff") {
		t.Errorf("expected show file error, got: %s", errBuf.String())
	}
}

func TestShowDiff_DiffError(t *testing.T) {
	saveHooks(t)
	resolveRefFunc = func(url, version string) (string, error) { return "abc123", nil }
	ensureCloneF = func(cacheRoot, baseRef, url string) error { return nil }
	fetchFunc = func(cacheRoot, baseRef, commit string) error { return nil }
	showFileFunc = func(cacheRoot, baseRef, commit, path string) ([]byte, error) {
		return []byte("upstream"), nil
	}
	diffNoIndexF = func(a, b []byte, nameA, nameB string) (string, error) {
		return "", fmt.Errorf("diff failed")
	}

	tmpDir := t.TempDir()
	localFile := filepath.Join(tmpDir, "config.yaml")
	os.WriteFile(localFile, []byte("local"), 0644)

	var errBuf bytes.Buffer
	o := &UpdateOptions{
		SharedOptions: &SharedOptions{
			IO: &streams.IO{Out: &bytes.Buffer{}, ErrOut: &errBuf},
		},
	}

	o.showDiff("github.com/org/repo", "src.yaml", localFile, &lock.Lock{})
	if !strings.Contains(errBuf.String(), "Error computing diff") {
		t.Errorf("expected diff error, got: %s", errBuf.String())
	}
}

func TestShowDiff_NoDifferences(t *testing.T) {
	saveHooks(t)
	resolveRefFunc = func(url, version string) (string, error) { return "abc123", nil }
	ensureCloneF = func(cacheRoot, baseRef, url string) error { return nil }
	fetchFunc = func(cacheRoot, baseRef, commit string) error { return nil }
	showFileFunc = func(cacheRoot, baseRef, commit, path string) ([]byte, error) {
		return []byte("same"), nil
	}
	diffNoIndexF = func(a, b []byte, nameA, nameB string) (string, error) {
		return "", nil
	}

	tmpDir := t.TempDir()
	localFile := filepath.Join(tmpDir, "config.yaml")
	os.WriteFile(localFile, []byte("same"), 0644)

	var errBuf bytes.Buffer
	o := &UpdateOptions{
		SharedOptions: &SharedOptions{
			IO: &streams.IO{Out: &bytes.Buffer{}, ErrOut: &errBuf},
		},
	}

	o.showDiff("github.com/org/repo", "src.yaml", localFile, &lock.Lock{})
	if !strings.Contains(errBuf.String(), "(no differences)") {
		t.Errorf("expected no differences, got: %s", errBuf.String())
	}
}

func TestShowDiff_WithDifferences(t *testing.T) {
	saveHooks(t)
	resolveRefFunc = func(url, version string) (string, error) { return "abc123", nil }
	ensureCloneF = func(cacheRoot, baseRef, url string) error { return nil }
	fetchFunc = func(cacheRoot, baseRef, commit string) error { return nil }
	showFileFunc = func(cacheRoot, baseRef, commit, path string) ([]byte, error) {
		return []byte("upstream"), nil
	}
	diffNoIndexF = func(a, b []byte, nameA, nameB string) (string, error) {
		return "--- local\n+++ upstream\n-old\n+new\n", nil
	}

	tmpDir := t.TempDir()
	localFile := filepath.Join(tmpDir, "config.yaml")
	os.WriteFile(localFile, []byte("local"), 0644)

	var errBuf bytes.Buffer
	o := &UpdateOptions{
		SharedOptions: &SharedOptions{
			IO: &streams.IO{Out: &bytes.Buffer{}, ErrOut: &errBuf},
		},
	}

	o.showDiff("github.com/org/repo", "src.yaml", localFile, &lock.Lock{})
	if !strings.Contains(errBuf.String(), "+new") {
		t.Errorf("expected diff output, got: %s", errBuf.String())
	}
}

// --- checkEnvConflicts ---

func TestCheckEnvConflicts(t *testing.T) {
	tmpDir := t.TempDir()
	lk := &lock.Lock{
		Version: 1,
		Envs: []lock.EnvEntry{
			{
				Ref: "github.com/org/infra", Commit: "abc123",
				Files: []lock.LockFile{
					{Path: "base/config.yaml", Dest: "config.yaml", SHA256: "aaa"},
					{Path: "base/ingress.yaml", Dest: "ingress.yaml", SHA256: "bbb"},
				},
			},
			{
				Ref: "github.com/org/overrides", Commit: "def456",
				Files: []lock.LockFile{
					{Path: "override/config.yaml", Dest: "config.yaml", SHA256: "ccc"},
				},
			},
		},
	}
	lock.WriteLock(tmpDir, lk, "test")

	configPath := filepath.Join(tmpDir, "b.yaml")
	os.WriteFile(configPath, []byte("binaries: {}\n"), 0644)

	var errBuf bytes.Buffer
	o := &UpdateOptions{
		SharedOptions: &SharedOptions{
			IO:               &streams.IO{Out: &bytes.Buffer{}, ErrOut: &errBuf},
			ConfigPath:       configPath,
			loadedConfigPath: configPath,
			Config:           nil,
		},
	}

	// nil Config → exits early
	o.checkEnvConflicts(nil)
	if errBuf.Len() > 0 {
		t.Errorf("nil config should produce no output")
	}
}

func TestCheckEnvConflicts_WithLabel(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "b.yaml")
	os.WriteFile(configPath, []byte("binaries: {}\n"), 0644)

	lk := &lock.Lock{
		Version: 1,
		Envs: []lock.EnvEntry{
			{
				Ref: "github.com/org/infra", Label: "base", Commit: "abc",
				Files: []lock.LockFile{{Path: "a.yaml", Dest: "config.yaml", SHA256: "aaa"}},
			},
			{
				Ref: "github.com/org/infra", Label: "override", Commit: "def",
				Files: []lock.LockFile{{Path: "b.yaml", Dest: "config.yaml", SHA256: "bbb"}},
			},
		},
	}
	lock.WriteLock(tmpDir, lk, "test")

	var errBuf bytes.Buffer
	o := &UpdateOptions{
		SharedOptions: &SharedOptions{
			IO:               &streams.IO{Out: &bytes.Buffer{}, ErrOut: &errBuf},
			ConfigPath:       configPath,
			loadedConfigPath: configPath,
			Config: &state.State{
				Envs: state.EnvList{
					{Key: "github.com/org/infra#base"},
					{Key: "github.com/org/infra#override"},
				},
			},
		},
	}

	o.checkEnvConflicts(nil)
	if !strings.Contains(errBuf.String(), "Conflict") {
		t.Errorf("expected conflict for label entries, got: %s", errBuf.String())
	}
	if !strings.Contains(errBuf.String(), "#base") || !strings.Contains(errBuf.String(), "#override") {
		t.Errorf("expected label refs in conflict message, got: %s", errBuf.String())
	}
}

func TestCheckEnvConflicts_NilLock(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "b.yaml")
	os.WriteFile(configPath, []byte("binaries: {}\n"), 0644)
	// No lock file → ReadLock returns nil

	var errBuf bytes.Buffer
	o := &UpdateOptions{
		SharedOptions: &SharedOptions{
			IO:               &streams.IO{Out: &bytes.Buffer{}, ErrOut: &errBuf},
			ConfigPath:       configPath,
			loadedConfigPath: configPath,
			Config: &state.State{
				Envs: state.EnvList{
					{Key: "github.com/org/infra"},
					{Key: "github.com/org/other"},
				},
			},
		},
	}

	o.checkEnvConflicts(nil) // should not panic
	if errBuf.Len() > 0 {
		t.Errorf("nil lock should produce no output, got: %s", errBuf.String())
	}
}

// --- NewUpdateCmd RunE success (covers return o.Run()) ---

func TestNewUpdateCmd_RunE_Success(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PATH_BIN", tmpDir)

	var outBuf bytes.Buffer
	io := &streams.IO{Out: &outBuf, ErrOut: &bytes.Buffer{}}
	shared := NewSharedOptions(io, nil)
	shared.Config = &state.State{} // empty config → "No binaries or envs to update"

	cmd := NewUpdateCmd(shared)
	err := cmd.RunE(cmd, []string{})
	if err != nil {
		t.Fatalf("RunE() error = %v", err)
	}
	if !strings.Contains(outBuf.String(), "No binaries or envs") {
		t.Errorf("expected empty message, got: %s", outBuf.String())
	}
}

// --- Complete: ValidateBinaryPath error ---

func TestUpdateComplete_ValidateBinaryPathError(t *testing.T) {
	// NOT parallel-safe: modifies process-wide cwd via os.Chdir.
	t.Setenv("PATH_BIN", "")
	t.Setenv("PATH_BASE", "")

	// chdir to a non-git temp dir so GetBinaryPath returns ""
	origDir, _ := os.Getwd()
	tmpDir := t.TempDir()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	io := &streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}}
	shared := NewSharedOptions(io, nil)
	o := &UpdateOptions{SharedOptions: shared}
	err := o.Complete([]string{})
	if err == nil {
		t.Fatal("expected ValidateBinaryPath error")
	}
}

// --- Run routes to runAll ---

func TestRun_RoutesToRunAll(t *testing.T) {
	var outBuf bytes.Buffer
	o := &UpdateOptions{
		SharedOptions: &SharedOptions{
			IO:     &streams.IO{Out: &outBuf, ErrOut: &bytes.Buffer{}},
			Config: &state.State{},
		},
	}
	// No specifiedBinaries or specifiedEnvRefs → runAll
	if err := o.Run(); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(outBuf.String(), "No binaries or envs") {
		t.Errorf("expected runAll path, got: %s", outBuf.String())
	}
}

// --- runAll with binaries from config ---

func TestRunAll_WithBinaries(t *testing.T) {
	presets := []*binary.Binary{{Name: "jq", Version: "1.7"}}
	var outBuf bytes.Buffer
	io := &streams.IO{Out: &outBuf, ErrOut: &bytes.Buffer{}}
	shared := NewSharedOptions(io, presets)
	shared.Config = &state.State{
		Binaries: state.BinaryList{&binary.LocalBinary{Name: "jq"}},
	}

	o := &UpdateOptions{
		SharedOptions:   shared,
		updateBinariesF: func(bins []*binary.Binary) error { return nil },
	}
	if err := o.runAll(); err != nil {
		t.Fatalf("runAll() error = %v", err)
	}
}

func TestRunAll_BinariesError(t *testing.T) {
	presets := []*binary.Binary{{Name: "jq", Version: "1.7"}}
	io := &streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}}
	shared := NewSharedOptions(io, presets)
	shared.Config = &state.State{
		Binaries: state.BinaryList{&binary.LocalBinary{Name: "jq"}},
	}

	o := &UpdateOptions{
		SharedOptions:   shared,
		updateBinariesF: func(bins []*binary.Binary) error { return fmt.Errorf("download failed") },
	}
	err := o.runAll()
	if err == nil || !strings.Contains(err.Error(), "download failed") {
		t.Errorf("expected download error, got: %v", err)
	}
}

// --- runAll with envs that error ---

func TestRunAll_EnvsUpdateError(t *testing.T) {
	saveHooks(t)
	syncEnvFunc = func(cfg env.EnvConfig, projectRoot, cacheRoot string, lockEntry *lock.EnvEntry) (*env.SyncResult, error) {
		return &env.SyncResult{Skipped: true, Message: "ok"}, nil
	}

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "b.yaml")
	os.WriteFile(configPath, []byte("binaries: {}\n"), 0644)
	// Write corrupt lock file → ReadLock returns error
	os.WriteFile(filepath.Join(tmpDir, "b.lock"), []byte("not json{{{"), 0644)

	o := &UpdateOptions{
		SharedOptions: &SharedOptions{
			IO:               &streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}},
			ConfigPath:       configPath,
			loadedConfigPath: configPath,
			Config: &state.State{
				Envs: state.EnvList{{Key: "github.com/org/repo"}},
			},
		},
	}

	err := o.runAll()
	if err == nil {
		t.Fatal("expected error from corrupt lock")
	}
}

// --- runSpecified with binaries ---

func TestRunSpecified_WithBinaries(t *testing.T) {
	o := &UpdateOptions{
		SharedOptions: &SharedOptions{
			IO: &streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}},
		},
		specifiedBinaries: []*binary.Binary{{Name: "jq", Version: "1.7"}},
		updateBinariesF:   func(bins []*binary.Binary) error { return nil },
	}

	if err := o.runSpecified(); err != nil {
		t.Fatalf("runSpecified() error = %v", err)
	}
}

func TestRunSpecified_BinariesError(t *testing.T) {
	o := &UpdateOptions{
		SharedOptions: &SharedOptions{
			IO: &streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}},
		},
		specifiedBinaries: []*binary.Binary{{Name: "jq"}},
		updateBinariesF:   func(bins []*binary.Binary) error { return fmt.Errorf("network error") },
	}

	err := o.runSpecified()
	if err == nil || !strings.Contains(err.Error(), "network error") {
		t.Errorf("expected network error, got: %v", err)
	}
}

// --- runSpecified with envs error ---

func TestRunSpecified_EnvsError(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "b.yaml")
	os.WriteFile(configPath, []byte("binaries: {}\n"), 0644)
	// Corrupt lock
	os.WriteFile(filepath.Join(tmpDir, "b.lock"), []byte("not json{{{"), 0644)

	o := &UpdateOptions{
		SharedOptions: &SharedOptions{
			IO:               &streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}},
			ConfigPath:       configPath,
			loadedConfigPath: configPath,
			Config: &state.State{
				Envs: state.EnvList{{Key: "github.com/org/repo"}},
			},
		},
		specifiedEnvRefs: []string{"github.com/org/repo"},
	}

	err := o.runSpecified()
	if err == nil {
		t.Fatal("expected error from corrupt lock")
	}
}

// --- updateEnvs ReadLock error ---

func TestUpdateEnvs_ReadLockError(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "b.yaml")
	os.WriteFile(configPath, []byte("binaries: {}\n"), 0644)
	// Corrupt lock file
	os.WriteFile(filepath.Join(tmpDir, "b.lock"), []byte("{invalid json"), 0644)

	o := &UpdateOptions{
		SharedOptions: &SharedOptions{
			IO:               &streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}},
			ConfigPath:       configPath,
			loadedConfigPath: configPath,
			Config: &state.State{
				Envs: state.EnvList{{Key: "github.com/org/repo"}},
			},
		},
	}

	err := o.updateEnvs(nil)
	if err == nil {
		t.Fatal("expected ReadLock error for corrupt lock file")
	}
}

// --- interactiveConflictResolver with nil stdinReader (os.Stdin fallback) ---

func TestInteractiveResolver_NilStdinReader(t *testing.T) {
	// NOT parallel-safe: replaces process-wide os.Stdin.
	oldStdin := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = oldStdin })

	go func() {
		w.Write([]byte("r\n"))
		w.Close()
	}()

	o := &UpdateOptions{
		SharedOptions: &SharedOptions{
			IO: &streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}},
		},
		// stdinReader intentionally nil → falls back to os.Stdin
	}

	resolver := o.interactiveConflictResolver("ref", &lock.Lock{})
	result := resolver("s", "d")
	if result != env.StrategyReplace {
		t.Errorf("got %q, want %q", result, env.StrategyReplace)
	}
}

// --- checkEnvConflicts with corrupt lock (ReadLock returns nil) ---

func TestCheckEnvConflicts_ReadLockError(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "b.yaml")
	os.WriteFile(configPath, []byte("binaries: {}\n"), 0644)
	// Corrupt lock → ReadLock returns nil, error
	os.WriteFile(filepath.Join(tmpDir, "b.lock"), []byte("not json"), 0644)

	var errBuf bytes.Buffer
	o := &UpdateOptions{
		SharedOptions: &SharedOptions{
			IO:               &streams.IO{Out: &bytes.Buffer{}, ErrOut: &errBuf},
			ConfigPath:       configPath,
			loadedConfigPath: configPath,
			Config: &state.State{
				Envs: state.EnvList{
					{Key: "github.com/org/a"},
					{Key: "github.com/org/b"},
				},
			},
		},
	}

	o.checkEnvConflicts(nil) // should not panic, silently exits
	// No conflict output since lock is nil
}

// --- printFileStatus ---

func TestPrintFileStatus_AllStatuses(t *testing.T) {
	tests := []struct {
		status   string
		inOut    string
		inErrOut string
	}{
		{"replaced", "replaced", ""},
		{"kept", "kept", ""},
		{"merged", "merged", ""},
		{"conflict", "", "conflict"},
		{"replaced (local changes overwritten)", "", "replaced"},
		{"", "replaced", ""}, // default
	}
	for _, tt := range tests {
		var outBuf, errBuf bytes.Buffer
		o := &UpdateOptions{
			SharedOptions: &SharedOptions{
				IO: &streams.IO{Out: &outBuf, ErrOut: &errBuf},
			},
		}
		o.printFileStatus(lock.LockFile{Dest: "test.yaml", Status: tt.status})
		if tt.inOut != "" && !strings.Contains(outBuf.String(), tt.inOut) {
			t.Errorf("printFileStatus(%q): out = %q, want %q", tt.status, outBuf.String(), tt.inOut)
		}
		if tt.inErrOut != "" && !strings.Contains(errBuf.String(), tt.inErrOut) {
			t.Errorf("printFileStatus(%q): errOut = %q, want %q", tt.status, errBuf.String(), tt.inErrOut)
		}
	}
}
