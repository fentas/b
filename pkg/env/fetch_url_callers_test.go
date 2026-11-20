package env

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fentas/b/pkg/envmatch"
	"github.com/fentas/b/pkg/gitcache"
	"github.com/fentas/b/pkg/lock"
)

// The fetches of SyncEnv go to the URL that the ref resolves to now, not to
// the URL that the cache stored. In these tests the cache stores the SSH URL,
// which the protocol fence refuses, so a fetch from the stored URL fails.

const (
	callerRef = "git@github.com:org/repo"
	sshOrigin = "git@github.com:org/repo.git"
)

// gitT runs a git command for test setup and fails the test on an error.
func gitT(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// twoCommitBare makes a bare repo with two commits of cfg/a.yaml: c1 (tag
// v1, "key: val") and c2 (HEAD, "key: new").
func twoCommitBare(t *testing.T) (bare, c1, c2 string) {
	t.Helper()
	tmp := t.TempDir()
	work := filepath.Join(tmp, "work")
	bare = filepath.Join(tmp, "bare.git")
	gitT(t, "init", "-q", work)
	gitT(t, "-C", work, "config", "user.email", "t@t.com")
	gitT(t, "-C", work, "config", "user.name", "T")
	gitT(t, "-C", work, "config", "commit.gpgsign", "false")
	if err := os.MkdirAll(filepath.Join(work, "cfg"), 0755); err != nil {
		t.Fatal(err)
	}
	for i, content := range []string{"key: val\n", "key: new\n"} {
		if err := os.WriteFile(filepath.Join(work, "cfg", "a.yaml"), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		gitT(t, "-C", work, "add", "-A")
		gitT(t, "-C", work, "commit", "-q", "-m", content)
		if i == 0 {
			gitT(t, "-C", work, "tag", "v1")
		}
	}
	gitT(t, "clone", "--bare", "-q", work, bare)
	return bare, gitT(t, "-C", bare, "rev-parse", "v1^{commit}"), gitT(t, "-C", bare, "rev-parse", "HEAD")
}

// fencedCache makes the cache of callerRef as a clone of bare at one
// commit only (branch: a tag, or "" for HEAD), and stores the SSH URL in it,
// as a clone over SSH does.
func fencedCache(t *testing.T, cacheRoot, bare, branch string) {
	t.Helper()
	args := []string{"clone", "--bare", "-q", "--depth", "1"}
	if branch != "" {
		args = append(args, "--branch", branch)
	}
	dir := gitcache.CacheDir(cacheRoot, callerRef)
	gitT(t, append(args, "file://"+bare, dir)...)
	gitT(t, "-C", dir, "config", "remote.origin.url", sshOrigin)
}

func lockEntryOf(res *SyncResult) *lock.EnvEntry {
	return &lock.EnvEntry{Ref: res.Ref, Version: res.Version, Commit: res.Commit, Files: res.Files}
}

// With the merge strategy, SyncEnv fetches the commit of the lock (the
// merge base) from the resolved URL (env.go, the previousCommit fetch).
func TestSyncEnv_MergeBaseFetchUsesResolvedURL(t *testing.T) {
	isolateGit(t)
	bare, c1, c2 := twoCommitBare(t)
	offlineGitHub(t, bare)
	t.Setenv(gitcache.SSHAsHTTPSEnv, "1")
	project := t.TempDir()
	cfg := EnvConfig{
		Ref:      callerRef,
		Version:  "v1",
		Strategy: StrategyReplace,
		Files:    map[string]envmatch.GlobConfig{"cfg/a.yaml": {}},
	}
	first, err := SyncEnv(cfg, project, t.TempDir(), nil)
	if err != nil || first.Commit != c1 {
		t.Fatalf("first SyncEnv: commit %v, %v, want %s", first, err, c1)
	}

	cacheRoot := t.TempDir()
	fencedCache(t, cacheRoot, bare, "") // holds c2 only
	if gitcache.HasCommit(cacheRoot, callerRef, c1) {
		t.Fatal("setup: the cache holds c1 before the sync")
	}
	cfg.Version = ""
	cfg.Strategy = StrategyMerge
	res, err := SyncEnv(cfg, project, cacheRoot, lockEntryOf(first))
	if err != nil {
		t.Fatalf("SyncEnv: %v", err)
	}
	if res.Commit != c2 || res.PreviousCommit != c1 {
		t.Errorf("commit %q previous %q, want %q %q: the merge base was not fetched", res.Commit, res.PreviousCommit, c2, c1)
	}
	if !gitcache.HasCommit(cacheRoot, callerRef, c1) {
		t.Error("the cache has no merge base after the sync")
	}
}

// When the lock commit is not in the cache, the up-to-date check fetches it
// from the resolved URL (env.go, lockedStateInSync).
func TestSyncEnv_UpToDateCheckFetchUsesResolvedURL(t *testing.T) {
	isolateGit(t)
	bare, _, c2 := twoCommitBare(t)
	offlineGitHub(t, bare)
	t.Setenv(gitcache.SSHAsHTTPSEnv, "1")
	project := t.TempDir()
	cfg := EnvConfig{
		Ref:      callerRef,
		Strategy: StrategyReplace,
		Files:    map[string]envmatch.GlobConfig{"cfg/a.yaml": {}},
	}
	first, err := SyncEnv(cfg, project, t.TempDir(), nil)
	if err != nil || first.Commit != c2 {
		t.Fatalf("first SyncEnv: %v, %v, want commit %s", first, err, c2)
	}

	cacheRoot := t.TempDir()
	fencedCache(t, cacheRoot, bare, "v1") // holds c1 only
	res, err := SyncEnv(cfg, project, cacheRoot, lockEntryOf(first))
	if err != nil {
		t.Fatalf("SyncEnv: %v", err)
	}
	if !res.Skipped {
		t.Errorf("SyncEnv did not skip an env that is up to date: %q", res.Message)
	}
	if !gitcache.HasCommit(cacheRoot, callerRef, c2) {
		t.Error("the up-to-date check did not fetch the lock commit from the resolved URL")
	}
}
