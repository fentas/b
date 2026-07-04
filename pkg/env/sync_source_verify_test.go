package env

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fentas/b/pkg/envmatch"
	"github.com/fentas/b/pkg/lock"
)

// sourceVerifyRepo builds a work repo + bare mirror with cfg/a.yaml=v1 (and
// any extra files), committed as commit A. The returned commitB func amends
// upstream (modify cfg/a.yaml to v2 and/or add files) and pushes commit B into
// the bare, returning its sha.
func sourceVerifyRepo(t *testing.T, extra map[string]string) (bare string, commitB func(changes map[string]string) string) {
	t.Helper()
	tmp := t.TempDir()
	work := filepath.Join(tmp, "work")
	bare = filepath.Join(tmp, "bare.git")
	run := func(args ...string) string {
		t.Helper()
		out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	run("git", "init", "-q", "-b", "main", work)
	run("git", "-C", work, "config", "user.email", "t@t.com")
	run("git", "-C", work, "config", "user.name", "T")
	files := map[string]string{"cfg/a.yaml": "v: 1\n"}
	for p, c := range extra {
		files[p] = c
	}
	for p, c := range files {
		full := filepath.Join(work, p)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0644); err != nil {
			t.Fatal(err)
		}
	}
	run("git", "-C", work, "add", "-A")
	run("git", "-C", work, "commit", "-q", "-m", "A", "--no-gpg-sign")
	run("git", "clone", "--bare", "-q", work, bare)

	commitB = func(changes map[string]string) string {
		t.Helper()
		for p, c := range changes {
			full := filepath.Join(work, p)
			if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte(c), 0644); err != nil {
				t.Fatal(err)
			}
		}
		run("git", "-C", work, "add", "-A")
		run("git", "-C", work, "commit", "-q", "-m", "B", "--no-gpg-sign")
		run("git", "-C", work, "push", "-q", bare, "main")
		out, err := exec.Command("git", "-C", bare, "rev-parse", "HEAD").Output()
		if err != nil {
			t.Fatal(err)
		}
		// TrimSpace, not [:40]: sha256-object-format repos emit 64-char ids.
		return strings.TrimSpace(string(out))
	}
	return bare, commitB
}

func lockFromResult(res *SyncResult) *lock.EnvEntry {
	le := &lock.EnvEntry{Commit: res.Commit, Files: make([]lock.LockFile, len(res.Files))}
	for i, f := range res.Files {
		le.Files[i] = lock.LockFile{Path: f.Path, Dest: f.Dest, SHA256: f.SHA256}
	}
	return le
}

// TestSyncEnv_HealsStaleLockAtBumpedCommit is the regression for the
// "lock advanced but tree didn't" state: lock.Commit points at the new commit
// while its per-file hashes AND the on-disk files still hold the old content
// (a lock corrupted by an older version). Layers 1–2 of the fast path see
// disk==lock and would skip forever; the source check (layer 3) must detect
// that lock+disk are stale relative to the pinned commit and re-sync.
func TestSyncEnv_HealsStaleLockAtBumpedCommit(t *testing.T) {
	bare, commitB := sourceVerifyRepo(t, nil)
	project := t.TempDir()
	cfg := EnvConfig{Ref: bare, Strategy: StrategyReplace,
		Files: map[string]envmatch.GlobConfig{"cfg/*.yaml": {Dest: "configs"}}}

	res, err := SyncEnv(cfg, project, t.TempDir(), nil)
	if err != nil {
		t.Fatalf("sync A: %v", err)
	}
	le := lockFromResult(res)

	// Upstream moves: same file set, changed content (isolates the blob check
	// from the managed-set check).
	newCommit := commitB(map[string]string{"cfg/a.yaml": "v: 2\n"})

	// Poison: commit pointer advanced, shas + disk still at A.
	le.Commit = newCommit

	res2, err := SyncEnv(cfg, project, t.TempDir(), le)
	if err != nil {
		t.Fatalf("sync B: %v", err)
	}
	if res2.Skipped {
		t.Fatal("must not skip: lock+disk are stale relative to the pinned commit")
	}
	data, err := os.ReadFile(filepath.Join(project, "configs", "a.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "v: 2\n" {
		t.Errorf("disk = %q, want healed to v: 2", data)
	}
	want := fmt.Sprintf("%x", sha256.Sum256([]byte("v: 2\n")))
	for _, f := range res2.Files {
		if f.Dest == "configs/a.yaml" && f.SHA256 != want {
			t.Errorf("re-recorded sha = %s, want sha of new content", f.SHA256)
		}
	}
}

// TestSyncEnv_ConfigChangeResyncsAtUnchangedCommit: adding a glob to b.yaml
// changes what belongs on disk even when the commit didn't move. Previously
// the fast path skipped silently (the old comment suggested a --force that
// never existed for envs).
func TestSyncEnv_ConfigChangeResyncsAtUnchangedCommit(t *testing.T) {
	bare, _ := sourceVerifyRepo(t, map[string]string{"extra/x.yaml": "x: 1\n"})
	project := t.TempDir()
	cfg := EnvConfig{Ref: bare, Strategy: StrategyReplace,
		Files: map[string]envmatch.GlobConfig{"cfg/*.yaml": {Dest: "configs"}}}

	res, err := SyncEnv(cfg, project, t.TempDir(), nil)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	le := lockFromResult(res)

	// Same commit, wider config: extra/* now included.
	cfg.Files["extra/*.yaml"] = envmatch.GlobConfig{Dest: "extras"}
	res2, err := SyncEnv(cfg, project, t.TempDir(), le)
	if err != nil {
		t.Fatalf("re-sync: %v", err)
	}
	if res2.Skipped {
		t.Fatal("must not skip when the glob config changed")
	}
	if _, err := os.Stat(filepath.Join(project, "extras", "x.yaml")); err != nil {
		t.Errorf("newly-matched file not written: %v", err)
	}
}

// TestSyncEnv_PinnedFileFastPathStaysSkippable: a file whose local copy
// carries `b.pin` annotations legitimately diverges from the upstream blob.
// Once the sync has stabilized (lock records the pinned target), the fast
// path must keep skipping — the source check defers to the lock for
// pin-carrying files instead of re-syncing on every run.
func TestSyncEnv_PinnedFileFastPathStaysSkippable(t *testing.T) {
	bare, _ := sourceVerifyRepo(t, map[string]string{"cfg/app.yaml": "app:\n    image: upstream\n"})
	project := t.TempDir()
	cfg := EnvConfig{Ref: bare, Strategy: StrategyReplace,
		Files: map[string]envmatch.GlobConfig{"cfg/*.yaml": {Dest: "configs"}}}

	res, err := SyncEnv(cfg, project, t.TempDir(), nil)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	le := lockFromResult(res)

	// Consumer pins the app map with a local override.
	pinned := "app:\n    b.pin: true\n    image: custom\n"
	if err := os.WriteFile(filepath.Join(project, "configs", "app.yaml"), []byte(pinned), 0644); err != nil {
		t.Fatal(err)
	}
	// Re-sync: local drift is detected (disk != lock), pins are honored, and
	// the lock re-records the pinned target.
	res2, err := SyncEnv(cfg, project, t.TempDir(), le)
	if err != nil {
		t.Fatalf("re-sync: %v", err)
	}
	if res2.Skipped {
		t.Fatal("pin edit should trigger one reconciling sync")
	}
	data, err := os.ReadFile(filepath.Join(project, "configs", "app.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	// Pin restoration re-marshals the YAML (indentation may normalize), so
	// assert semantically: the pinned override survived, upstream didn't win.
	if !strings.Contains(string(data), "b.pin: true") || !strings.Contains(string(data), "image: custom") {
		t.Fatalf("pinned content not preserved: %q", data)
	}
	if strings.Contains(string(data), "image: upstream") {
		t.Fatalf("upstream overwrote the pinned key: %q", data)
	}

	// Stabilized: subsequent runs at the same commit must skip, even though
	// the pinned file's bytes differ from the upstream blob.
	le2 := lockFromResult(res2)
	res3, err := SyncEnv(cfg, project, t.TempDir(), le2)
	if err != nil {
		t.Fatalf("third sync: %v", err)
	}
	if !res3.Skipped {
		t.Error("pinned file must not defeat the up-to-date fast path")
	}
}

// TestSyncEnv_PinMentionDoesNotExemptFromSourceCheck: a file that merely
// MENTIONS "b.pin" (comment/value) without a structural pin annotation must
// still be source-verified — a substring false positive must not reintroduce
// the undetectable stale-lock state for that file (Copilot round-2).
func TestSyncEnv_PinMentionDoesNotExemptFromSourceCheck(t *testing.T) {
	mention := "# docs: use b.pin to pin keys\nv: 1\n"
	bare, commitB := sourceVerifyRepo(t, map[string]string{"cfg/doc.yaml": mention})
	project := t.TempDir()
	cfg := EnvConfig{Ref: bare, Strategy: StrategyReplace,
		Files: map[string]envmatch.GlobConfig{"cfg/*.yaml": {Dest: "configs"}}}

	res, err := SyncEnv(cfg, project, t.TempDir(), nil)
	if err != nil {
		t.Fatalf("sync A: %v", err)
	}
	le := lockFromResult(res)

	// Upstream changes the mentioning file; poison the lock like the S3 state.
	newMention := "# docs: use b.pin to pin keys\nv: 2\n"
	le.Commit = commitB(map[string]string{"cfg/doc.yaml": newMention})

	res2, err := SyncEnv(cfg, project, t.TempDir(), le)
	if err != nil {
		t.Fatalf("sync B: %v", err)
	}
	if res2.Skipped {
		t.Fatal("a b.pin MENTION (no structural pin) must not exempt the file from source verification")
	}
	data, err := os.ReadFile(filepath.Join(project, "configs", "doc.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != newMention {
		t.Errorf("disk = %q, want healed to the new content", data)
	}
}

// TestSyncEnv_ClientStrategyFastPathUnaffected: under strategy client, local
// divergence from upstream is the contract — the source check must not apply,
// or every update would re-sync (and re-keep) forever.
func TestSyncEnv_ClientStrategyFastPathUnaffected(t *testing.T) {
	bare, _ := sourceVerifyRepo(t, nil)
	project := t.TempDir()
	cfg := EnvConfig{Ref: bare, Strategy: StrategyClient,
		Files: map[string]envmatch.GlobConfig{"cfg/*.yaml": {Dest: "configs"}}}

	res, err := SyncEnv(cfg, project, t.TempDir(), nil)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	le := lockFromResult(res)

	// Local edit; client keeps it and records the local hash.
	if err := os.WriteFile(filepath.Join(project, "configs", "a.yaml"), []byte("local: edit\n"), 0644); err != nil {
		t.Fatal(err)
	}
	res2, err := SyncEnv(cfg, project, t.TempDir(), le)
	if err != nil {
		t.Fatalf("re-sync: %v", err)
	}
	if res2.Skipped {
		t.Fatal("local edit should trigger one reconciling sync")
	}

	// Stabilized: disk matches the lock (kept hash) and diverges from source —
	// client envs must still hit the fast path.
	le2 := lockFromResult(res2)
	res3, err := SyncEnv(cfg, project, t.TempDir(), le2)
	if err != nil {
		t.Fatalf("third sync: %v", err)
	}
	if !res3.Skipped {
		t.Error("client-strategy env must keep skipping when disk matches the lock")
	}
	if got, _ := os.ReadFile(filepath.Join(project, "configs", "a.yaml")); string(got) != "local: edit\n" {
		t.Errorf("client strategy must preserve local content, got %q", got)
	}
}

// TestSyncEnv_SelectFileFastPathSkips: a select-filtered file's on-disk bytes
// are a scoped slice, never the raw upstream blob — the source check must
// defer to the lock hash for it, keeping the fast path stable.
func TestSyncEnv_SelectFileFastPathSkips(t *testing.T) {
	bare, _ := sourceVerifyRepo(t, map[string]string{"cfg/multi.yaml": "keep: 1\nother: 2\n"})
	project := t.TempDir()
	cfg := EnvConfig{Ref: bare, Strategy: StrategyReplace,
		Files: map[string]envmatch.GlobConfig{
			"cfg/a.yaml":     {Dest: "configs"},
			"cfg/multi.yaml": {Dest: "configs", Select: []string{".keep"}},
		}}

	res, err := SyncEnv(cfg, project, t.TempDir(), nil)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	le := lockFromResult(res)

	res2, err := SyncEnv(cfg, project, t.TempDir(), le)
	if err != nil {
		t.Fatalf("re-sync: %v", err)
	}
	if !res2.Skipped {
		t.Error("in-sync env with a select-filtered file must skip")
	}
}

// TestSyncEnv_ReplaceKeepIsOneShot documents the sharpened `replace`
// semantics: an interactive "keep" decision survives only until the next
// update. Under replace, source is authoritative — the source check re-detects
// the divergence at the same commit and the file is re-synced (previously the
// keep was silently absorbed until the next upstream commit). Durable local
// divergence belongs to strategy client/merge, select, or b.pin.
func TestSyncEnv_ReplaceKeepIsOneShot(t *testing.T) {
	bare, _ := sourceVerifyRepo(t, nil)
	project := t.TempDir()
	cfg := EnvConfig{Ref: bare, Strategy: StrategyReplace,
		Files: map[string]envmatch.GlobConfig{"cfg/*.yaml": {Dest: "configs"}}}

	res, err := SyncEnv(cfg, project, t.TempDir(), nil)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	le := lockFromResult(res)

	// Local edit, then an interactive "keep" during re-sync.
	if err := os.WriteFile(filepath.Join(project, "configs", "a.yaml"), []byte("mine: 1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg.ResolveConflict = func(sourcePath, destPath string) string { return StrategyClient }
	res2, err := SyncEnv(cfg, project, t.TempDir(), le)
	if err != nil {
		t.Fatalf("keep sync: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(project, "configs", "a.yaml")); string(got) != "mine: 1\n" {
		t.Fatalf("keep should preserve local content, got %q", got)
	}

	// Next update at the same commit: source check sees disk≠source and
	// re-syncs; without a resolver, replace restores upstream.
	cfg.ResolveConflict = nil
	le2 := lockFromResult(res2)
	res3, err := SyncEnv(cfg, project, t.TempDir(), le2)
	if err != nil {
		t.Fatalf("third sync: %v", err)
	}
	if res3.Skipped {
		t.Fatal("kept divergence under replace must resurface, not be absorbed")
	}
	if got, _ := os.ReadFile(filepath.Join(project, "configs", "a.yaml")); string(got) != "v: 1\n" {
		t.Errorf("replace should restore upstream content, got %q", got)
	}
}
