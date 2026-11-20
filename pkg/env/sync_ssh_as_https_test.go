package env

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fentas/b/pkg/envmatch"
	"github.com/fentas/b/pkg/gitcache"
)

// offlineGitHub makes git reach the https URL of github.com/org/repo in a
// local bare repo, and refuse every network transport. No git command in
// these tests leaves the machine.
func offlineGitHub(t *testing.T, bare string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_ALLOW_PROTOCOL", "file")
	// No provider header: b's header env (GIT_CONFIG_COUNT=1) would
	// replace the insteadOf entry below.
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "url."+bare+".insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", "https://github.com/org/repo.git")
}

// With B_GIT_SSH_AS_HTTPS=1, an SSH env ref for github.com syncs over https.
// The cache directory and the lock key stay those of the ref as written,
// and a cache that an SSH clone made (its stored origin is the SSH URL) is
// fetched from the https URL and keeps its origin.
func TestSyncEnv_SSHAsHTTPS_FetchesOverHTTPS(t *testing.T) {
	const ref = "git@github.com:org/repo"
	const sshOrigin = "git@github.com:org/repo.git"
	for _, cached := range []bool{false, true} {
		name := "fresh cache"
		if cached {
			name = "cache cloned over SSH"
		}
		t.Run(name, func(t *testing.T) {
			bare, commit := setupLocalBareRepo(t)
			offlineGitHub(t, bare)
			t.Setenv(gitcache.SSHAsHTTPSEnv, "1")
			cacheRoot := t.TempDir()
			dir := gitcache.CacheDir(cacheRoot, ref)
			if cached {
				if out, err := exec.Command("git", "clone", "--bare", "-q", bare, dir).CombinedOutput(); err != nil {
					t.Fatalf("clone: %v\n%s", err, out)
				}
				if out, err := exec.Command("git", "-C", dir, "config", "remote.origin.url", sshOrigin).CombinedOutput(); err != nil {
					t.Fatalf("set origin: %v\n%s", err, out)
				}
			}

			project := t.TempDir()
			cfg := EnvConfig{
				Ref:      ref,
				Strategy: StrategyReplace,
				Files:    map[string]envmatch.GlobConfig{"cfg/a.yaml": {}},
			}
			res, err := SyncEnv(cfg, project, cacheRoot, nil)
			if err != nil {
				t.Fatalf("SyncEnv: %v", err)
			}
			if res.Ref != ref || res.Commit != commit {
				t.Errorf("lock key %q commit %q, want %q %q", res.Ref, res.Commit, ref, commit)
			}
			got, err := os.ReadFile(filepath.Join(project, "cfg", "a.yaml"))
			if err != nil || string(got) != "key: val\n" {
				t.Errorf("synced file = %q, %v", got, err)
			}
			entries, err := os.ReadDir(cacheRoot)
			if err != nil || len(entries) != 1 || filepath.Join(cacheRoot, entries[0].Name()) != dir {
				t.Errorf("cache root holds %v (%v), want only %s", entries, err, dir)
			}
			if cached {
				out, err := exec.Command("git", "-C", dir, "config", "remote.origin.url").Output()
				if err != nil || strings.TrimSpace(string(out)) != sshOrigin {
					t.Errorf("stored origin = %q (%v), want %q", strings.TrimSpace(string(out)), err, sshOrigin)
				}
			}
		})
	}
}

// With the knob off, the SSH ref stays SSH, and there is no fallback to
// https when SSH fails: here the protocol fence refuses SSH.
func TestSyncEnv_SSHAsHTTPS_OffHasNoFallback(t *testing.T) {
	bare, _ := setupLocalBareRepo(t)
	offlineGitHub(t, bare)
	t.Setenv(gitcache.SSHAsHTTPSEnv, "")
	project := t.TempDir()
	cfg := EnvConfig{
		Ref:      "git@github.com:org/repo",
		Strategy: StrategyReplace,
		Files:    map[string]envmatch.GlobConfig{"cfg/a.yaml": {}},
	}
	_, err := SyncEnv(cfg, project, t.TempDir(), nil)
	if err == nil {
		t.Fatal("SyncEnv passed with the knob off; an SSH ref must not fall back to https")
	}
	if !strings.Contains(err.Error(), "transport 'ssh' not allowed") {
		t.Errorf("the sync did not fail on the SSH transport: %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, "cfg", "a.yaml")); !os.IsNotExist(err) {
		t.Errorf("a file was synced with the knob off: %v", err)
	}
}
