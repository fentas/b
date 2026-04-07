package env

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fentas/b/pkg/envmatch"
	"github.com/fentas/b/pkg/lock"
)

// setupLocalBareRepo creates a work repo with files, commits, then clones to a bare repo.
// Returns the bare dir and HEAD commit.
func setupLocalBareRepo(t *testing.T) (string, string) {
	t.Helper()
	tmp := t.TempDir()
	work := filepath.Join(tmp, "work")
	bare := filepath.Join(tmp, "bare.git")

	run := func(args ...string) {
		t.Helper()
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%v failed: %v\n%s", args, err, out)
		}
	}
	run("git", "init", "-q", work)
	run("git", "-C", work, "config", "user.email", "t@t.com")
	run("git", "-C", work, "config", "user.name", "T")
	run("git", "-C", work, "config", "commit.gpgsign", "false")

	if err := os.MkdirAll(filepath.Join(work, "cfg"), 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	for _, f := range []struct {
		path, content string
	}{
		{filepath.Join(work, "cfg", "a.yaml"), "key: val\n"},
		{filepath.Join(work, "cfg", "b.yaml"), "other: thing\n"},
		{filepath.Join(work, "README.md"), "readme"},
	} {
		if err := os.WriteFile(f.path, []byte(f.content), 0644); err != nil {
			t.Fatalf("WriteFile %s: %v", f.path, err)
		}
	}
	run("git", "-C", work, "add", "-A")
	run("git", "-C", work, "commit", "-m", "init", "--no-gpg-sign")

	run("git", "clone", "--bare", "-q", work, bare)
	commitOut, err := exec.Command("git", "-C", bare, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	return bare, strings.TrimSpace(string(commitOut))
}

func TestSyncEnv_LocalReplace(t *testing.T) {
	bare, _ := setupLocalBareRepo(t)
	project := t.TempDir()

	cfg := EnvConfig{
		Ref:      bare, // absolute path → IsLocal=true
		Strategy: StrategyReplace,
		Files: map[string]envmatch.GlobConfig{
			"cfg/*.yaml": {Dest: "configs"},
		},
	}

	res, err := SyncEnv(cfg, project, t.TempDir(), nil)
	if err != nil {
		t.Fatalf("SyncEnv: %v", err)
	}
	if len(res.Files) != 2 {
		t.Errorf("want 2 files, got %d", len(res.Files))
	}
	// Files should exist
	for _, f := range []string{"a.yaml", "b.yaml"} {
		p := filepath.Join(project, "configs", f)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("missing %s: %v", p, err)
		}
	}
}

func TestSyncEnv_LocalSkippedWhenUpToDate(t *testing.T) {
	bare, commit := setupLocalBareRepo(t)
	project := t.TempDir()

	cfg := EnvConfig{
		Ref: bare,
		Files: map[string]envmatch.GlobConfig{
			"cfg/*.yaml": {Dest: "out"},
		},
	}

	// Lock entry with the same commit → skip path
	lockEntry := &lock.EnvEntry{Commit: commit}
	res, err := SyncEnv(cfg, project, t.TempDir(), lockEntry)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !res.Skipped {
		t.Errorf("expected Skipped=true")
	}
}

func TestSyncEnv_LocalDryRun(t *testing.T) {
	bare, _ := setupLocalBareRepo(t)
	project := t.TempDir()

	cfg := EnvConfig{
		Ref:    bare,
		DryRun: true,
		Files: map[string]envmatch.GlobConfig{
			"cfg/*.yaml": {Dest: "out"},
		},
	}

	res, err := SyncEnv(cfg, project, t.TempDir(), nil)
	if err != nil {
		t.Fatalf("%v", err)
	}
	// Files should NOT exist in dry-run
	if _, err := os.Stat(filepath.Join(project, "out", "a.yaml")); err == nil {
		t.Error("dry-run should not write files")
	}
	if res.Skipped {
		t.Error("dry-run should not be marked Skipped")
	}
}

func TestSafeShort(t *testing.T) {
	if got := safeShort("1234567890abcdef"); got != "1234567890ab" {
		t.Errorf("got %q", got)
	}
	if got := safeShort("short"); got != "short" {
		t.Errorf("got %q", got)
	}
}

func TestHookStdoutStderr(t *testing.T) {
	// Defaults
	if hookStdout(EnvConfig{}) != os.Stdout {
		t.Error("want os.Stdout default")
	}
	if hookStderr(EnvConfig{}) != os.Stderr {
		t.Error("want os.Stderr default")
	}
	// Custom
	out := &byteWriter{}
	if hookStdout(EnvConfig{Stdout: out}) != out {
		t.Error("want custom stdout")
	}
	if hookStderr(EnvConfig{Stderr: out}) != out {
		t.Error("want custom stderr")
	}
}

type byteWriter struct{ b []byte }

func (b *byteWriter) Write(p []byte) (int, error) { b.b = append(b.b, p...); return len(p), nil }

func TestSyncEnv_LocalMergeAndClient(t *testing.T) {
	// Setup: clone bare with an initial commit, sync to project,
	// then modify locally AND upstream, re-sync with merge strategy.
	tmp := t.TempDir()
	work := filepath.Join(tmp, "work")
	bare := filepath.Join(tmp, "bare.git")
	run := func(args ...string) {
		t.Helper()
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	run("git", "init", "-q", work)
	run("git", "-C", work, "config", "user.email", "t@t.com")
	run("git", "-C", work, "config", "user.name", "T")
	run("git", "-C", work, "config", "commit.gpgsign", "false")

	if err := os.WriteFile(filepath.Join(work, "cfg.yaml"), []byte("a: 1\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	run("git", "-C", work, "add", "-A")
	run("git", "-C", work, "commit", "-m", "v1", "--no-gpg-sign")
	run("git", "clone", "--bare", "-q", work, bare)
	firstCommit, err := exec.Command("git", "-C", bare, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	firstSha := strings.TrimSpace(string(firstCommit))

	project := t.TempDir()
	cfg := EnvConfig{
		Ref: bare,
		Files: map[string]envmatch.GlobConfig{
			"cfg.yaml": {Dest: "out"},
		},
	}
	if _, err := SyncEnv(cfg, project, t.TempDir(), nil); err != nil {
		t.Fatalf("first sync: %v", err)
	}

	// Modify upstream
	if err := os.WriteFile(filepath.Join(work, "cfg.yaml"), []byte("a: 1\nb: 2\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	run("git", "-C", work, "commit", "-am", "v2", "--no-gpg-sign")
	// Update bare from work
	run("git", "-C", work, "push", "--quiet", bare, "HEAD")

	// Modify the local file (to trigger merge/client path)
	localFile := filepath.Join(project, "out", "cfg.yaml")
	if err := os.WriteFile(localFile, []byte("c: 3\na: 1\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// With StrategyClient — keeps local
	lockEntry := mockLockEntryWithFile(firstSha, "cfg.yaml", "cfg.yaml")
	cfgClient := cfg
	cfgClient.Strategy = StrategyClient
	if _, err := SyncEnv(cfgClient, project, t.TempDir(), lockEntry); err != nil {
		t.Errorf("client sync: %v", err)
	}

	// Merge strategy — will attempt three-way merge
	cfgMerge := cfg
	cfgMerge.Strategy = StrategyMerge
	// Reset local
	if err := os.WriteFile(localFile, []byte("c: 3\na: 1\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	// Three-way merge: lockEntry.Commit (firstSha) IS in the bare repo,
	// so doMerge() fetches base="a: 1", local="c: 3\na: 1\n", upstream=
	// "a: 1\nb: 2\n" and produces a clean merge. Run should not fail.
	if _, err := SyncEnv(cfgMerge, project, t.TempDir(), lockEntry); err != nil {
		t.Errorf("merge sync: %v", err)
	}
}

func mockLockEntryWithFile(commit, src, dest string) *lock.EnvEntry {
	return &lock.EnvEntry{
		Commit: commit,
		Files: []lock.LockFile{
			{Path: src, Dest: dest, SHA256: "dummy-hash-to-trigger-drift"},
		},
	}
}

func TestValidatePathUnderRoot(t *testing.T) {
	root := t.TempDir()
	// Valid
	if err := ValidatePathUnderRoot(root, filepath.Join(root, "sub", "file")); err != nil {
		t.Errorf("valid: %v", err)
	}
	// Traversal
	if err := ValidatePathUnderRoot(root, "/etc/passwd"); err == nil {
		t.Error("expected traversal error")
	}
}

func TestSyncEnv_WithHooks(t *testing.T) {
	bare, _ := setupLocalBareRepo(t)
	project := t.TempDir()
	cfg := EnvConfig{
		Ref:        bare,
		OnPreSync:  "true",
		OnPostSync: "true",
		Files: map[string]envmatch.GlobConfig{
			"cfg/*.yaml": {Dest: "out"},
		},
	}
	if _, err := SyncEnv(cfg, project, t.TempDir(), nil); err != nil {
		t.Errorf("%v", err)
	}

	// Pre-sync hook failure
	cfgBad := cfg
	cfgBad.OnPreSync = "false"
	if _, err := SyncEnv(cfgBad, project, t.TempDir(), nil); err == nil {
		t.Error("expected pre-sync failure")
	}
}

func TestSyncEnv_ForceCommit(t *testing.T) {
	bare, commit := setupLocalBareRepo(t)
	project := t.TempDir()
	cfg := EnvConfig{
		Ref:         bare,
		ForceCommit: commit,
		Files: map[string]envmatch.GlobConfig{
			"cfg/*.yaml": {Dest: "out"},
		},
	}
	if _, err := SyncEnv(cfg, project, t.TempDir(), nil); err != nil {
		t.Errorf("%v", err)
	}
}

func TestSyncEnv_NoMatch(t *testing.T) {
	bare, _ := setupLocalBareRepo(t)
	project := t.TempDir()

	cfg := EnvConfig{
		Ref: bare,
		Files: map[string]envmatch.GlobConfig{
			"no-such-path/*": {Dest: "out"},
		},
	}
	_, err := SyncEnv(cfg, project, t.TempDir(), nil)
	if err == nil {
		t.Error("expected no-match error")
	}
}
