package provider

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fentas/b/pkg/gitcache"
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

// Git.Install fetches the commit from the URL that the ref resolves to now,
// not from the URL that the cache stored (git.go, installFromRemote). The
// cache here stores the SSH URL and holds the old commit only; the protocol
// fence refuses SSH, so a fetch from the stored URL fails and the file at
// the new commit cannot be read.
func TestGitInstall_FetchUsesResolvedURL(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home) // gitcache.DefaultCacheRoot is under HOME
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, ".gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_COUNT", "0")
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(home))
	t.Setenv("GIT_ALLOW_PROTOCOL", "file")
	t.Setenv("GITHUB_TOKEN", "") // b's header env would replace the insteadOf entry below
	t.Setenv(gitcache.SSHAsHTTPSEnv, "1")
	t.Chdir(home)

	tmp := t.TempDir()
	work := filepath.Join(tmp, "work")
	bare := filepath.Join(tmp, "bare.git")
	gitT(t, "init", "-q", work)
	gitT(t, "-C", work, "config", "user.email", "t@t.com")
	gitT(t, "-C", work, "config", "user.name", "T")
	gitT(t, "-C", work, "config", "commit.gpgsign", "false")
	for i, content := range []string{"v1\n", "v2\n"} {
		if err := os.WriteFile(filepath.Join(work, "tool.sh"), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		gitT(t, "-C", work, "add", "-A")
		gitT(t, "-C", work, "commit", "-q", "-m", content)
		if i == 0 {
			gitT(t, "-C", work, "tag", "v1")
		}
	}
	gitT(t, "clone", "--bare", "-q", work, bare)

	// git reaches https://github.com/org/repo.git in the local bare repo.
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "url."+bare+".insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", "https://github.com/org/repo.git")

	const repo = "git@github.com:org/repo"
	dir := gitcache.CacheDir(gitcache.DefaultCacheRoot(), repo)
	gitT(t, "clone", "--bare", "-q", "--depth", "1", "--branch", "v1", "file://"+bare, dir)
	gitT(t, "-C", dir, "config", "remote.origin.url", repo+".git")

	dest := filepath.Join(t.TempDir(), "tool")
	if err := (&Git{}).Install(repo+":tool.sh", "", dest); err != nil {
		t.Fatalf("Install: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil || string(got) != "v2\n" {
		t.Errorf("installed %q (%v), want the file at HEAD", got, err)
	}
}
