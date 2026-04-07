package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fentas/b/pkg/env"
	"github.com/fentas/b/pkg/envmatch"
	"github.com/fentas/b/pkg/lock"
	"github.com/fentas/b/pkg/state"
	"github.com/fentas/goodies/streams"
)

// --- Feature 2: env status ---

func TestNewEnvCmd(t *testing.T) {
	io := &streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}}
	shared := NewSharedOptions(io, nil)
	cmd := NewEnvCmd(shared)

	if cmd.Use != "env" {
		t.Errorf("Use = %q, want %q", cmd.Use, "env")
	}

	// Should have subcommands
	subs := cmd.Commands()
	names := make(map[string]bool)
	for _, s := range subs {
		names[s.Name()] = true
	}
	for _, want := range []string{"status", "remove", "match"} {
		if !names[want] {
			t.Errorf("missing subcommand %q", want)
		}
	}
}

func TestEnvStatus_NoConfig(t *testing.T) {
	out := &bytes.Buffer{}
	io := &streams.IO{Out: out, ErrOut: &bytes.Buffer{}}
	shared := NewSharedOptions(io, nil)

	o := &EnvStatusOptions{SharedOptions: shared}
	if err := o.Run(); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if !strings.Contains(out.String(), "No envs configured") {
		t.Errorf("output = %q, want 'No envs configured'", out.String())
	}
}

func TestEnvStatus_NotSynced(t *testing.T) {
	saveHooks(t)
	resolveRefFunc = func(url, version string) (string, error) {
		return "abc123def456", nil
	}

	tmpDir := t.TempDir()
	// Create empty lock file
	lock.WriteLock(tmpDir, &lock.Lock{}, "v1.0")

	out := &bytes.Buffer{}
	io := &streams.IO{Out: out, ErrOut: &bytes.Buffer{}}
	shared := NewSharedOptions(io, nil)
	shared.Config = &state.State{
		Envs: state.EnvList{
			{Key: "github.com/org/infra", Version: "v2.0"},
		},
	}
	shared.loadedConfigPath = filepath.Join(tmpDir, "b.yaml")

	o := &EnvStatusOptions{SharedOptions: shared}
	if err := o.Run(); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if !strings.Contains(out.String(), "not synced") {
		t.Errorf("output = %q, want 'not synced'", out.String())
	}
}

func TestEnvStatus_UpToDate(t *testing.T) {
	saveHooks(t)
	resolveRefFunc = func(url, version string) (string, error) {
		return "abc123def456", nil
	}

	tmpDir := t.TempDir()
	// Create a synced file
	destPath := filepath.Join(tmpDir, "test.yaml")
	os.WriteFile(destPath, []byte("content"), 0644)
	hash, _ := lock.SHA256File(destPath)

	lk := &lock.Lock{
		Envs: []lock.EnvEntry{
			{
				Ref:     "github.com/org/infra",
				Version: "v2.0",
				Commit:  "abc123def456",
				Files: []lock.LockFile{
					{Path: "test.yaml", Dest: "test.yaml", SHA256: hash},
				},
			},
		},
	}
	lock.WriteLock(tmpDir, lk, "v1.0")

	out := &bytes.Buffer{}
	io := &streams.IO{Out: out, ErrOut: &bytes.Buffer{}}
	shared := NewSharedOptions(io, nil)
	shared.Config = &state.State{
		Envs: state.EnvList{
			{Key: "github.com/org/infra", Version: "v2.0"},
		},
	}
	shared.loadedConfigPath = filepath.Join(tmpDir, "b.yaml")

	o := &EnvStatusOptions{SharedOptions: shared}
	if err := o.Run(); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if !strings.Contains(out.String(), "up to date") {
		t.Errorf("output = %q, want 'up to date'", out.String())
	}
}

func TestEnvStatus_UpstreamChanged(t *testing.T) {
	saveHooks(t)
	resolveRefFunc = func(url, version string) (string, error) {
		return "newcommit999", nil
	}

	tmpDir := t.TempDir()
	destPath := filepath.Join(tmpDir, "test.yaml")
	os.WriteFile(destPath, []byte("content"), 0644)
	hash, _ := lock.SHA256File(destPath)

	lk := &lock.Lock{
		Envs: []lock.EnvEntry{
			{
				Ref:    "github.com/org/infra",
				Commit: "oldcommit123",
				Files: []lock.LockFile{
					{Path: "test.yaml", Dest: "test.yaml", SHA256: hash},
				},
			},
		},
	}
	lock.WriteLock(tmpDir, lk, "v1.0")

	out := &bytes.Buffer{}
	io := &streams.IO{Out: out, ErrOut: &bytes.Buffer{}}
	shared := NewSharedOptions(io, nil)
	shared.Config = &state.State{
		Envs: state.EnvList{
			{Key: "github.com/org/infra"},
		},
	}
	shared.loadedConfigPath = filepath.Join(tmpDir, "b.yaml")

	o := &EnvStatusOptions{SharedOptions: shared}
	if err := o.Run(); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if !strings.Contains(out.String(), "upstream changed") {
		t.Errorf("output = %q, want 'upstream changed'", out.String())
	}
}

func TestEnvStatus_LocalDrift(t *testing.T) {
	saveHooks(t)
	resolveRefFunc = func(url, version string) (string, error) {
		return "samecommit", nil
	}

	tmpDir := t.TempDir()
	destPath := filepath.Join(tmpDir, "test.yaml")
	os.WriteFile(destPath, []byte("modified content"), 0644)

	lk := &lock.Lock{
		Envs: []lock.EnvEntry{
			{
				Ref:    "github.com/org/infra",
				Commit: "samecommit",
				Files: []lock.LockFile{
					{Path: "test.yaml", Dest: "test.yaml", SHA256: "originalhash"},
				},
			},
		},
	}
	lock.WriteLock(tmpDir, lk, "v1.0")

	out := &bytes.Buffer{}
	io := &streams.IO{Out: out, ErrOut: &bytes.Buffer{}}
	shared := NewSharedOptions(io, nil)
	shared.Config = &state.State{
		Envs: state.EnvList{
			{Key: "github.com/org/infra"},
		},
	}
	shared.loadedConfigPath = filepath.Join(tmpDir, "b.yaml")

	o := &EnvStatusOptions{SharedOptions: shared}
	if err := o.Run(); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if !strings.Contains(out.String(), "modified locally") {
		t.Errorf("output = %q, want 'modified locally'", out.String())
	}
}

func TestEnvStatus_PathTraversalInLock(t *testing.T) {
	saveHooks(t)
	resolveRefFunc = func(url, version string) (string, error) {
		return "samecommit", nil
	}

	tmpDir := t.TempDir()

	// Lock entry with a path that escapes project root
	lk := &lock.Lock{
		Envs: []lock.EnvEntry{
			{
				Ref:    "github.com/org/evil",
				Commit: "samecommit",
				Files: []lock.LockFile{
					{Path: "escape.yaml", Dest: "../../../etc/passwd", SHA256: "fakehash"},
				},
			},
		},
	}
	lock.WriteLock(tmpDir, lk, "v1.0")

	out := &bytes.Buffer{}
	io := &streams.IO{Out: out, ErrOut: &bytes.Buffer{}}
	shared := NewSharedOptions(io, nil)
	shared.Config = &state.State{
		Envs: state.EnvList{
			{Key: "github.com/org/evil"},
		},
	}
	shared.loadedConfigPath = filepath.Join(tmpDir, "b.yaml")

	o := &EnvStatusOptions{SharedOptions: shared}
	if err := o.Run(); err != nil {
		t.Fatalf("Run error: %v", err)
	}

	// Should count the escaping path as drift, not try to read /etc/passwd
	if !strings.Contains(out.String(), "modified locally") {
		t.Errorf("expected drift for path traversal entry, got: %q", out.String())
	}
}

// --- Feature 4: env remove ---

func TestEnvRemove_NormalizesKeyWithVersion(t *testing.T) {
	tmpDir := t.TempDir()
	lk := &lock.Lock{
		Envs: []lock.EnvEntry{
			{Ref: "github.com/org/infra", Commit: "abc"},
		},
	}
	lock.WriteLock(tmpDir, lk, "v1.0")

	out := &bytes.Buffer{}
	io := &streams.IO{Out: out, ErrOut: &bytes.Buffer{}}
	shared := NewSharedOptions(io, nil)
	shared.Config = &state.State{
		Envs: state.EnvList{
			{Key: "github.com/org/infra"},
		},
	}
	shared.loadedConfigPath = filepath.Join(tmpDir, "b.yaml")
	shared.bVersion = "v1.0"
	state.SaveConfig(shared.Config, filepath.Join(tmpDir, "b.yaml"))

	// Pass key with @version — should still remove from both lock and config
	o := &EnvRemoveOptions{SharedOptions: shared}
	if err := o.Run("github.com/org/infra@v2.0"); err != nil {
		t.Fatalf("Run error: %v", err)
	}

	lk2, _ := lock.ReadLock(tmpDir)
	if lk2.FindEnv("github.com/org/infra", "") != nil {
		t.Error("infra should be removed from lock")
	}
	if shared.Config.Envs.Get("github.com/org/infra") != nil {
		t.Error("infra should be removed from config")
	}
}

func TestEnvRemove_RemovesFromLock(t *testing.T) {
	tmpDir := t.TempDir()
	lk := &lock.Lock{
		Envs: []lock.EnvEntry{
			{Ref: "github.com/org/infra", Commit: "abc"},
			{Ref: "github.com/org/other", Commit: "def"},
		},
	}
	lock.WriteLock(tmpDir, lk, "v1.0")

	out := &bytes.Buffer{}
	io := &streams.IO{Out: out, ErrOut: &bytes.Buffer{}}
	shared := NewSharedOptions(io, nil)
	shared.Config = &state.State{
		Envs: state.EnvList{
			{Key: "github.com/org/infra"},
			{Key: "github.com/org/other"},
		},
	}
	shared.loadedConfigPath = filepath.Join(tmpDir, "b.yaml")
	shared.bVersion = "v1.0"

	// Write config to disk so SaveConfig works
	state.SaveConfig(shared.Config, filepath.Join(tmpDir, "b.yaml"))

	o := &EnvRemoveOptions{SharedOptions: shared}
	if err := o.Run("github.com/org/infra"); err != nil {
		t.Fatalf("Run error: %v", err)
	}

	// Verify lock
	lk2, _ := lock.ReadLock(tmpDir)
	if lk2.FindEnv("github.com/org/infra", "") != nil {
		t.Error("infra should be removed from lock")
	}
	if lk2.FindEnv("github.com/org/other", "") == nil {
		t.Error("other should still be in lock")
	}
}

func TestEnvRemove_DeletesFiles(t *testing.T) {
	tmpDir := t.TempDir()

	// Create synced file
	destPath := filepath.Join(tmpDir, "deploy.yaml")
	os.WriteFile(destPath, []byte("content"), 0644)

	lk := &lock.Lock{
		Envs: []lock.EnvEntry{
			{
				Ref:    "github.com/org/infra",
				Commit: "abc",
				Files: []lock.LockFile{
					{Path: "manifests/deploy.yaml", Dest: "deploy.yaml"},
				},
			},
		},
	}
	lock.WriteLock(tmpDir, lk, "v1.0")

	out := &bytes.Buffer{}
	io := &streams.IO{Out: out, ErrOut: &bytes.Buffer{}}
	shared := NewSharedOptions(io, nil)
	shared.Config = &state.State{}
	shared.loadedConfigPath = filepath.Join(tmpDir, "b.yaml")
	shared.bVersion = "v1.0"

	o := &EnvRemoveOptions{SharedOptions: shared, DeleteFiles: true}
	if err := o.Run("github.com/org/infra"); err != nil {
		t.Fatalf("Run error: %v", err)
	}

	if _, err := os.Stat(destPath); !os.IsNotExist(err) {
		t.Error("synced file should be deleted")
	}
}

func TestEnvRemove_PathTraversal(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a file outside project root that a malicious lock entry might reference
	outsideFile := filepath.Join(tmpDir, "..", "shouldnotdelete")
	os.WriteFile(outsideFile, []byte("important"), 0644)
	defer os.Remove(outsideFile)

	lk := &lock.Lock{
		Envs: []lock.EnvEntry{
			{
				Ref:    "github.com/org/evil",
				Commit: "abc",
				Files: []lock.LockFile{
					{Path: "escape.yaml", Dest: "../shouldnotdelete"},
				},
			},
		},
	}
	lock.WriteLock(tmpDir, lk, "v1.0")

	errOut := &bytes.Buffer{}
	io := &streams.IO{Out: &bytes.Buffer{}, ErrOut: errOut}
	shared := NewSharedOptions(io, nil)
	shared.Config = &state.State{}
	shared.loadedConfigPath = filepath.Join(tmpDir, "b.yaml")
	shared.bVersion = "v1.0"

	o := &EnvRemoveOptions{SharedOptions: shared, DeleteFiles: true}
	o.Run("github.com/org/evil")

	// File outside project root should NOT be deleted
	if _, err := os.Stat(outsideFile); os.IsNotExist(err) {
		t.Error("file outside project root should not be deleted")
	}
	if !strings.Contains(errOut.String(), "outside project root") {
		t.Errorf("expected path traversal warning, got: %q", errOut.String())
	}
}

// --- Feature 9: env match ---

func TestEnvMatch_ParsesArgs(t *testing.T) {
	io := &streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}}
	shared := NewSharedOptions(io, nil)
	cmd := NewEnvMatchCmd(shared)

	// Verify arg requirements
	if err := cmd.Args(cmd, []string{"ref"}); err == nil {
		t.Error("should require at least 2 args")
	}
	if err := cmd.Args(cmd, []string{"ref", "glob"}); err != nil {
		t.Errorf("2 args should be valid, got: %v", err)
	}
	if err := cmd.Args(cmd, []string{"ref", "glob", "dest"}); err != nil {
		t.Errorf("3 args should be valid, got: %v", err)
	}
	if err := cmd.Args(cmd, []string{"a", "b", "c", "d"}); err == nil {
		t.Error("4 args should be rejected")
	}
}

// --- Feature 1+10: update flags ---

func TestNewUpdateCmd_NewFlags(t *testing.T) {
	io := &streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}}
	shared := NewSharedOptions(io, nil)
	cmd := NewUpdateCmd(shared)

	for _, name := range []string{"dry-run", "rollback", "group"} {
		if f := cmd.Flags().Lookup(name); f == nil {
			t.Errorf("flag %q not registered", name)
		}
	}
}

// --- Feature 1: dry-run skips lock write ---

func TestUpdateEnvs_DryRun_SkipsLockWrite(t *testing.T) {
	saveHooks(t)

	tmpDir := t.TempDir()
	lock.WriteLock(tmpDir, &lock.Lock{}, "v1.0")

	syncEnvFunc = func(cfg env.EnvConfig, projectRoot, cacheRoot string, lockEntry *lock.EnvEntry) (*env.SyncResult, error) {
		if !cfg.DryRun {
			t.Error("expected DryRun to be true")
		}
		return &env.SyncResult{
			Ref:     "github.com/org/infra",
			Commit:  "newcommit",
			Files:   []lock.LockFile{{Path: "a.yaml", Dest: "a.yaml", Status: "replaced (dry-run)"}},
			Message: "1 file(s) synced",
		}, nil
	}

	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	io := &streams.IO{Out: out, ErrOut: errOut}
	shared := NewSharedOptions(io, nil)
	shared.Config = &state.State{
		Envs: state.EnvList{
			{Key: "github.com/org/infra"},
		},
	}
	shared.loadedConfigPath = filepath.Join(tmpDir, "b.yaml")
	shared.bVersion = "v1.0"

	o := &UpdateOptions{SharedOptions: shared, DryRun: true}
	if err := o.updateEnvs(nil); err != nil {
		t.Fatalf("updateEnvs error: %v", err)
	}

	// Lock should still be empty (dry-run doesn't write)
	lk, _ := lock.ReadLock(tmpDir)
	if len(lk.Envs) != 0 {
		t.Errorf("expected 0 envs in lock after dry-run, got %d", len(lk.Envs))
	}

	// New plan-based output: dry-run produces a plan summary line ending
	// in "→ N add, ..." rather than a literal "dry-run" tag in the
	// header. The lock-not-written assertion above is the load-bearing
	// behavior contract.
	if !strings.Contains(out.String(), "add,") {
		t.Errorf("output should contain plan summary, got: %q", out.String())
	}
}

// --- Feature 10: group filtering ---

func TestUpdateEnvs_GroupFilter(t *testing.T) {
	saveHooks(t)

	tmpDir := t.TempDir()
	lock.WriteLock(tmpDir, &lock.Lock{}, "v1.0")

	synced := []string{}
	syncEnvFunc = func(cfg env.EnvConfig, projectRoot, cacheRoot string, lockEntry *lock.EnvEntry) (*env.SyncResult, error) {
		synced = append(synced, cfg.Ref)
		return &env.SyncResult{
			Ref:     cfg.Ref,
			Commit:  "abc",
			Files:   []lock.LockFile{{Path: "a.yaml", Dest: "a.yaml", Status: "replaced"}},
			Message: "1 file(s) synced",
		}, nil
	}

	out := &bytes.Buffer{}
	io := &streams.IO{Out: out, ErrOut: &bytes.Buffer{}}
	shared := NewSharedOptions(io, nil)
	shared.Config = &state.State{
		Envs: state.EnvList{
			{Key: "github.com/org/dev-config", Group: "dev"},
			{Key: "github.com/org/prod-config", Group: "prod"},
			{Key: "github.com/org/shared"},
		},
	}
	shared.loadedConfigPath = filepath.Join(tmpDir, "b.yaml")
	shared.bVersion = "v1.0"

	o := &UpdateOptions{SharedOptions: shared, Group: "dev", Yes: true}
	if err := o.updateEnvs(nil); err != nil {
		t.Fatalf("updateEnvs error: %v", err)
	}

	if len(synced) != 1 {
		t.Fatalf("expected 1 env synced with group=dev, got %d: %v", len(synced), synced)
	}
	if synced[0] != "github.com/org/dev-config" {
		t.Errorf("synced = %q, want github.com/org/dev-config", synced[0])
	}
}

// --- Feature 8: rollback ---

func TestUpdateEnvs_Rollback(t *testing.T) {
	saveHooks(t)

	tmpDir := t.TempDir()
	lk := &lock.Lock{
		Envs: []lock.EnvEntry{
			{
				Ref:            "github.com/org/infra",
				Commit:         "current123",
				PreviousCommit: "previous456",
			},
		},
	}
	lock.WriteLock(tmpDir, lk, "v1.0")

	var forcedCommit string
	syncEnvFunc = func(cfg env.EnvConfig, projectRoot, cacheRoot string, lockEntry *lock.EnvEntry) (*env.SyncResult, error) {
		forcedCommit = cfg.ForceCommit
		return &env.SyncResult{
			Ref:            cfg.Ref,
			Commit:         cfg.ForceCommit,
			PreviousCommit: "current123",
			Files:          []lock.LockFile{{Path: "a.yaml", Dest: "a.yaml", Status: "replaced"}},
			Message:        "1 file(s) synced",
		}, nil
	}

	out := &bytes.Buffer{}
	io := &streams.IO{Out: out, ErrOut: &bytes.Buffer{}}
	shared := NewSharedOptions(io, nil)
	shared.Config = &state.State{
		Envs: state.EnvList{
			{Key: "github.com/org/infra"},
		},
	}
	shared.loadedConfigPath = filepath.Join(tmpDir, "b.yaml")
	shared.bVersion = "v1.0"

	o := &UpdateOptions{SharedOptions: shared, Rollback: true, Yes: true}
	if err := o.updateEnvs(nil); err != nil {
		t.Fatalf("updateEnvs error: %v", err)
	}

	if forcedCommit != "previous456" {
		t.Errorf("ForceCommit = %q, want %q", forcedCommit, "previous456")
	}
	// Plan output no longer carries a literal "(rollback)" tag — the
	// behavior contract is that ForceCommit got set to the previous
	// commit, which is asserted above.
	_ = out
}

func TestUpdateEnvs_Rollback_NoPrevious(t *testing.T) {
	saveHooks(t)

	tmpDir := t.TempDir()
	lk := &lock.Lock{
		Envs: []lock.EnvEntry{
			{Ref: "github.com/org/infra", Commit: "current123"},
		},
	}
	lock.WriteLock(tmpDir, lk, "v1.0")

	syncEnvFunc = func(cfg env.EnvConfig, projectRoot, cacheRoot string, lockEntry *lock.EnvEntry) (*env.SyncResult, error) {
		t.Fatal("syncEnv should not be called when no previous commit")
		return nil, nil
	}

	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	io := &streams.IO{Out: out, ErrOut: errOut}
	shared := NewSharedOptions(io, nil)
	shared.Config = &state.State{
		Envs: state.EnvList{
			{Key: "github.com/org/infra"},
		},
	}
	shared.loadedConfigPath = filepath.Join(tmpDir, "b.yaml")
	shared.bVersion = "v1.0"

	o := &UpdateOptions{SharedOptions: shared, Rollback: true}
	o.updateEnvs(nil)

	if !strings.Contains(errOut.String(), "no previous commit") {
		t.Errorf("errOut = %q, want 'no previous commit'", errOut.String())
	}
}

// --- Feature 5: list conflicted files ---

func TestUpdateEnvs_ListConflictedFiles(t *testing.T) {
	saveHooks(t)

	tmpDir := t.TempDir()
	lock.WriteLock(tmpDir, &lock.Lock{}, "v1.0")

	syncEnvFunc = func(cfg env.EnvConfig, projectRoot, cacheRoot string, lockEntry *lock.EnvEntry) (*env.SyncResult, error) {
		return &env.SyncResult{
			Ref:    "github.com/org/infra",
			Commit: "newcommit",
			Files: []lock.LockFile{
				{Path: "a.yaml", Dest: "ok.yaml", Status: "replaced"},
				{Path: "b.yaml", Dest: "conflict1.yaml", Status: "conflict"},
				{Path: "c.yaml", Dest: "conflict2.yaml", Status: "conflict"},
			},
			Message:   "1 replaced, 2 conflict(s)",
			Conflicts: 2,
		}, nil
	}

	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	io := &streams.IO{Out: out, ErrOut: errOut}
	shared := NewSharedOptions(io, nil)
	shared.Config = &state.State{
		Envs: state.EnvList{
			{Key: "github.com/org/infra"},
		},
	}
	shared.loadedConfigPath = filepath.Join(tmpDir, "b.yaml")
	shared.bVersion = "v1.0"

	// Yes:true short-circuits the safety prompt so the conflict listing
	// path is exercised. Without --yes the non-TTY default safety
	// (prompt → strict on non-TTY) would refuse to apply.
	o := &UpdateOptions{SharedOptions: shared, Yes: true}
	o.updateEnvs(nil)

	errStr := errOut.String()
	if !strings.Contains(errStr, "conflict1.yaml") {
		t.Errorf("errOut should list conflict1.yaml, got: %q", errStr)
	}
	if !strings.Contains(errStr, "conflict2.yaml") {
		t.Errorf("errOut should list conflict2.yaml, got: %q", errStr)
	}
	if !strings.Contains(errStr, "2 file(s) have merge conflicts") {
		t.Errorf("errOut should mention conflict count, got: %q", errStr)
	}
}

// --- Feature 7: hooks passed to config ---

func TestUpdateEnvs_PassesHooks(t *testing.T) {
	saveHooks(t)

	tmpDir := t.TempDir()
	lock.WriteLock(tmpDir, &lock.Lock{}, "v1.0")

	var gotPreSync, gotPostSync string
	syncEnvFunc = func(cfg env.EnvConfig, projectRoot, cacheRoot string, lockEntry *lock.EnvEntry) (*env.SyncResult, error) {
		gotPreSync = cfg.OnPreSync
		gotPostSync = cfg.OnPostSync
		return &env.SyncResult{
			Ref:     cfg.Ref,
			Commit:  "abc",
			Files:   []lock.LockFile{{Path: "a.yaml", Dest: "a.yaml", Status: "replaced"}},
			Message: "1 file(s) synced",
		}, nil
	}

	out := &bytes.Buffer{}
	io := &streams.IO{Out: out, ErrOut: &bytes.Buffer{}}
	shared := NewSharedOptions(io, nil)
	shared.Config = &state.State{
		Envs: state.EnvList{
			{
				Key:        "github.com/org/infra",
				OnPreSync:  "echo pre",
				OnPostSync: "echo post",
			},
		},
	}
	shared.loadedConfigPath = filepath.Join(tmpDir, "b.yaml")
	shared.bVersion = "v1.0"

	o := &UpdateOptions{SharedOptions: shared}
	o.updateEnvs(nil)

	if gotPreSync != "echo pre" {
		t.Errorf("OnPreSync = %q, want %q", gotPreSync, "echo pre")
	}
	if gotPostSync != "echo post" {
		t.Errorf("OnPostSync = %q, want %q", gotPostSync, "echo post")
	}
}

// --- Feature: printFileStatus handles unchanged ---

func TestPrintFileStatus_Unchanged(t *testing.T) {
	out := &bytes.Buffer{}
	io := &streams.IO{Out: out, ErrOut: &bytes.Buffer{}}
	shared := NewSharedOptions(io, nil)
	o := &UpdateOptions{SharedOptions: shared}

	o.printFileStatus(lock.LockFile{Dest: "test.yaml", Status: "unchanged"})
	if !strings.Contains(out.String(), "unchanged") {
		t.Errorf("output = %q, want 'unchanged'", out.String())
	}
}

func TestPrintFileStatus_DryRun(t *testing.T) {
	out := &bytes.Buffer{}
	io := &streams.IO{Out: out, ErrOut: &bytes.Buffer{}}
	shared := NewSharedOptions(io, nil)
	o := &UpdateOptions{SharedOptions: shared}

	o.printFileStatus(lock.LockFile{Dest: "test.yaml", Status: "replaced (dry-run)"})
	if !strings.Contains(out.String(), "dry-run") {
		t.Errorf("output = %q, want 'dry-run'", out.String())
	}
}

// --- env profiles ---

func TestNewEnvCmd_HasProfilesAndAdd(t *testing.T) {
	io := &streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}}
	shared := NewSharedOptions(io, nil)
	cmd := NewEnvCmd(shared)

	subs := cmd.Commands()
	names := make(map[string]bool)
	for _, s := range subs {
		names[s.Name()] = true
	}
	for _, want := range []string{"profiles", "add"} {
		if !names[want] {
			t.Errorf("missing subcommand %q", want)
		}
	}
}

func TestEnvProfilesCmd_Args(t *testing.T) {
	io := &streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}}
	shared := NewSharedOptions(io, nil)
	cmd := NewEnvProfilesCmd(shared)

	if err := cmd.Args(cmd, []string{}); err == nil {
		t.Error("should require exactly 1 arg")
	}
	if err := cmd.Args(cmd, []string{"ref"}); err != nil {
		t.Errorf("1 arg should be valid: %v", err)
	}
	if err := cmd.Args(cmd, []string{"a", "b"}); err == nil {
		t.Error("2 args should be rejected")
	}
}

func TestEnvAddCmd_Args(t *testing.T) {
	io := &streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}}
	shared := NewSharedOptions(io, nil)
	cmd := NewEnvAddCmd(shared)

	// 0 args rejected, 1 arg valid, 2 args rejected
	if err := cmd.Args(cmd, []string{}); err == nil {
		t.Error("0 args should be rejected")
	}
	if err := cmd.Args(cmd, []string{"ref#profile"}); err != nil {
		t.Errorf("1 arg should be valid: %v", err)
	}
	if err := cmd.Args(cmd, []string{"a", "b"}); err == nil {
		t.Error("2 args should be rejected")
	}

	// Check flags exist
	if f := cmd.Flags().Lookup("version"); f == nil {
		t.Error("--version flag not registered")
	}
}

// --- helpers ---

func TestSummarizeFiles(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]envmatch.GlobConfig
		want  string
	}{
		{"nil", nil, ""},
		{"empty", map[string]envmatch.GlobConfig{}, ""},
		{"single", map[string]envmatch.GlobConfig{
			"manifests/base/**": {},
		}, "base/**"},
		{"two", map[string]envmatch.GlobConfig{
			"manifests/base/**":    {},
			"manifests/hetzner/**": {},
		}, "base/**, hetzner/**"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := summarizeFiles(tt.files)
			if got != tt.want {
				t.Errorf("summarizeFiles() = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- fetchUpstreamConfig error handling ---

func TestFetchUpstreamConfig_ReturnsNotFoundSentinel(t *testing.T) {
	tmpDir := t.TempDir()
	_, err := fetchUpstreamConfig(tmpDir, "nonexistent", "abc123")
	if err == nil {
		t.Fatal("expected error for missing config")
	}
	if !errors.Is(err, errConfigNotFound) {
		t.Errorf("expected errConfigNotFound, got: %v", err)
	}
}

// --- env add early exit ---

func TestEnvAdd_ExistingEntryDetected(t *testing.T) {
	out := &bytes.Buffer{}
	io := &streams.IO{Out: out, ErrOut: &bytes.Buffer{}}
	shared := NewSharedOptions(io, nil)
	shared.Config = &state.State{
		Envs: state.EnvList{
			{Key: "github.com/org/infra#base"},
		},
	}

	source := &state.EnvEntry{Key: "base", Files: map[string]envmatch.GlobConfig{"a/**": {}}}
	upstream := &state.State{Profiles: state.EnvList{source}}

	o := &EnvAddOptions{SharedOptions: shared}
	err := o.addProfile("github.com/org/infra", "base", "", source, upstream)
	if err == nil {
		t.Fatal("expected error for existing entry")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("expected 'already exists' error, got: %v", err)
	}
}

// --- profiles top-level key in CLI ---

func TestEnvAdd_RequiresLabel(t *testing.T) {
	out := &bytes.Buffer{}
	io := &streams.IO{Out: out, ErrOut: &bytes.Buffer{}}
	shared := NewSharedOptions(io, nil)

	o := &EnvAddOptions{SharedOptions: shared}
	err := o.Run("github.com/org/infra")
	if err == nil {
		t.Fatal("expected error for missing label")
	}
	if !strings.Contains(err.Error(), "profile name required") {
		t.Errorf("expected 'profile name required' error, got: %v", err)
	}
}

func TestIsGitNotFound_Comprehensive(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"path does not exist", fmt.Errorf("fatal: path 'b.yaml' does not exist in 'HEAD'"), true},
		{"no such file or directory", fmt.Errorf("fatal: cannot change to '/x': No such file or directory"), true},
		{"bad object", fmt.Errorf("fatal: bad object abc123"), false},
		{"permission denied", fmt.Errorf("error: permission denied"), false},
		{"empty error", fmt.Errorf(""), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isGitNotFound(tt.err); got != tt.want {
				t.Errorf("isGitNotFound() = %v, want %v", got, tt.want)
			}
		})
	}
}

// --- interactive mode ---

func TestEnvAddInteractive_FlagRegistered(t *testing.T) {
	io := &streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}}
	shared := NewSharedOptions(io, nil)
	cmd := NewEnvAddCmd(shared)

	if f := cmd.Flags().Lookup("interactive"); f == nil {
		t.Error("--interactive flag not registered")
	}
	if f := cmd.Flags().ShorthandLookup("i"); f == nil {
		t.Error("-i shorthand not registered")
	}
}

func TestEnvAddInteractive_NotTTY(t *testing.T) {
	saveHooks(t)
	isTTYFunc = func() bool { return false }

	io := &streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}}
	shared := NewSharedOptions(io, nil)
	o := &EnvAddOptions{SharedOptions: shared, Interactive: true}
	err := o.Run("github.com/org/infra")
	if err == nil {
		t.Fatal("expected error for non-TTY")
	}
	if !strings.Contains(err.Error(), "terminal") {
		t.Errorf("expected terminal error, got: %v", err)
	}
}

func TestEnvAdd_ResolvesIncludes(t *testing.T) {
	tmpDir := t.TempDir()
	out := &bytes.Buffer{}
	io := &streams.IO{Out: out, ErrOut: &bytes.Buffer{}}
	shared := NewSharedOptions(io, nil)
	shared.Config = &state.State{}
	shared.loadedConfigPath = filepath.Join(tmpDir, "b.yaml")

	base := &state.EnvEntry{
		Key:   "base",
		Files: map[string]envmatch.GlobConfig{"manifests/base/**": {Dest: "base/"}},
	}
	staging := &state.EnvEntry{
		Key:         "staging",
		Description: "Staging preset",
		Includes:    []string{"base"},
		Ignore:      []string{"**/prod-*"},
	}
	upstream := &state.State{
		Profiles: state.EnvList{base, staging},
	}

	o := &EnvAddOptions{SharedOptions: shared}
	if err := o.addProfile("github.com/org/infra", "staging", "v2.0", staging, upstream); err != nil {
		t.Fatalf("addProfile error: %v", err)
	}

	// Verify the added entry has merged files from base
	added := shared.Config.Envs.Get("github.com/org/infra#staging")
	if added == nil {
		t.Fatal("expected entry in config")
	}
	if _, ok := added.Files["manifests/base/**"]; !ok {
		t.Error("expected base files to be included")
	}
	if len(added.Ignore) == 0 || added.Ignore[0] != "**/prod-*" {
		t.Errorf("expected ignore from staging, got: %v", added.Ignore)
	}
	if added.Description != "Staging preset" {
		t.Errorf("description = %q", added.Description)
	}
}

// needed by env_test.go since it accesses unexported field
func TestEnvAdd_EmptyFilesAfterResolve(t *testing.T) {
	tmpDir := t.TempDir()
	out := &bytes.Buffer{}
	io := &streams.IO{Out: out, ErrOut: &bytes.Buffer{}}
	shared := NewSharedOptions(io, nil)
	shared.Config = &state.State{}
	shared.loadedConfigPath = filepath.Join(tmpDir, "b.yaml")

	// Profile with includes but no files anywhere
	empty := &state.EnvEntry{Key: "empty"}
	top := &state.EnvEntry{
		Key:      "top",
		Includes: []string{"empty"},
	}
	upstream := &state.State{Profiles: state.EnvList{empty, top}}

	o := &EnvAddOptions{SharedOptions: shared}
	err := o.addProfile("github.com/org/infra", "top", "", top, upstream)
	if err == nil {
		t.Fatal("expected error for empty files")
	}
	if !strings.Contains(err.Error(), "no file globs") {
		t.Errorf("expected 'no file globs' error, got: %v", err)
	}
}

var _ = envmatch.GlobConfig{}
