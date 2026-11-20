package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fentas/b/pkg/gitcache"
	"github.com/fentas/b/pkg/lock"
	"github.com/fentas/goodies/streams"
)

// The commands below fetch from the URL that the ref resolves to now
// (gitcache.GitURL), not from the URL that the cache stored. In these tests
// the cache stores the SSH URL and holds the old commit only; the protocol
// fence refuses SSH, so a fetch from the stored URL fails, and the command
// cannot read the new commit.

const callerRef = "git@github.com:org/repo"

// gitT runs a git command for test setup and fails the test on an error.
func gitT(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// offlineUpstream isolates git (no git config of the user or the system, no
// network transport), makes git reach https://github.com/org/repo.git in a
// local bare repo, and makes the fenced cache of callerRef under the
// default cache root. The bare repo has two commits: v1 with
// manifests/a.yaml "a: 1", and HEAD with "a: 2", manifests/b.yaml and a
// b.yaml with the profile "base".
func offlineUpstream(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home) // gitcache.DefaultCacheRoot is under HOME
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, ".gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_COUNT", "0")
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(home))
	t.Setenv("GIT_ALLOW_PROTOCOL", "file")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv(gitcache.SSHAsHTTPSEnv, "1")
	t.Chdir(home)

	tmp := t.TempDir()
	work := filepath.Join(tmp, "work")
	bare := filepath.Join(tmp, "bare.git")
	gitT(t, "init", "-q", work)
	gitT(t, "-C", work, "config", "user.email", "t@t.com")
	gitT(t, "-C", work, "config", "user.name", "T")
	gitT(t, "-C", work, "config", "commit.gpgsign", "false")
	mustMkdir(t, filepath.Join(work, "manifests"))
	mustWrite(t, filepath.Join(work, "manifests", "a.yaml"), []byte("a: 1\n"))
	gitT(t, "-C", work, "add", "-A")
	gitT(t, "-C", work, "commit", "-q", "-m", "v1")
	gitT(t, "-C", work, "tag", "v1")
	mustWrite(t, filepath.Join(work, "manifests", "a.yaml"), []byte("a: 2\n"))
	mustWrite(t, filepath.Join(work, "manifests", "b.yaml"), []byte("b: 2\n"))
	mustWrite(t, filepath.Join(work, "b.yaml"), []byte(`profiles:
  base:
    description: Base profile
    files:
      "manifests/*.yaml": {dest: "manifests"}
`))
	gitT(t, "-C", work, "add", "-A")
	gitT(t, "-C", work, "commit", "-q", "-m", "v2")
	gitT(t, "clone", "--bare", "-q", work, bare)

	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "url."+bare+".insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", "https://github.com/org/repo.git")

	dir := gitcache.CacheDir(gitcache.DefaultCacheRoot(), gitcache.RefBase(callerRef))
	gitT(t, "clone", "--bare", "-q", "--depth", "1", "--branch", "v1", "file://"+bare, dir)
	gitT(t, "-C", dir, "config", "remote.origin.url", callerRef+".git")
}

func bufIO() (*streams.IO, *bytes.Buffer, *bytes.Buffer) {
	var out, errOut bytes.Buffer
	return &streams.IO{In: strings.NewReader(""), Out: &out, ErrOut: &errOut}, &out, &errOut
}

// b env match (env.go, EnvMatchOptions.Run).
func TestEnvMatch_FetchUsesResolvedURL(t *testing.T) {
	offlineUpstream(t)
	io, out, _ := bufIO()
	o := &EnvMatchOptions{SharedOptions: NewSharedOptions(io, nil)}
	if err := o.Run([]string{callerRef, "manifests/*.yaml"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "2 file(s) matched") {
		t.Errorf("output %q, want the 2 files at HEAD", out.String())
	}
}

// b env profiles (env.go, EnvProfilesOptions.Run).
func TestEnvProfiles_FetchUsesResolvedURL(t *testing.T) {
	offlineUpstream(t)
	io, out, _ := bufIO()
	o := &EnvProfilesOptions{SharedOptions: NewSharedOptions(io, nil)}
	if err := o.Run(callerRef); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "base") {
		t.Errorf("output %q, want the profile base of HEAD", out.String())
	}
}

// b env add (env.go, EnvAddOptions.fetchUpstream).
func TestEnvAdd_FetchUsesResolvedURL(t *testing.T) {
	offlineUpstream(t)
	io, _, _ := bufIO()
	o := &EnvAddOptions{SharedOptions: NewSharedOptions(io, nil)}
	up, err := o.fetchUpstream(gitcache.RefBase(callerRef), "", callerRef)
	if err != nil {
		t.Fatalf("fetchUpstream: %v", err)
	}
	if len(up.Profiles) != 1 || up.Profiles[0].Key != "base" {
		t.Errorf("profiles %v, want base", up.Profiles)
	}
}

// The b.yaml hint of b install (install.go, discoverUpstreamConfig).
func TestInstallHint_FetchUsesResolvedURL(t *testing.T) {
	offlineUpstream(t)
	io, _, _ := bufIO()
	o := &InstallOptions{SharedOptions: NewSharedOptions(io, nil)}
	if hint := o.discoverUpstreamConfig(callerRef); !strings.Contains(hint, "base") {
		t.Errorf("hint %q, want the profile base of HEAD", hint)
	}
}

// The [d]iff choice of b update (update.go, showDiff through fetchFunc).
func TestUpdateDiff_FetchUsesResolvedURL(t *testing.T) {
	offlineUpstream(t)
	saveHooks(t)
	local := filepath.Join(t.TempDir(), "a.yaml")
	mustWrite(t, local, []byte("a: local\n"))
	io, _, errOut := bufIO()
	o := &UpdateOptions{SharedOptions: NewSharedOptions(io, nil)}
	o.showDiff(callerRef, "manifests/a.yaml", local, &lock.Lock{})
	if !strings.Contains(errOut.String(), "+a: 2") {
		t.Errorf("diff output %q, want the upstream line of HEAD", errOut.String())
	}
}

// fetchFunc gets the resolved URL: the https URL of a GitHub SSH ref with
// B_GIT_SSH_AS_HTTPS=1.
func TestUpdateDiff_FetchFuncGetsResolvedURL(t *testing.T) {
	offlineUpstream(t)
	saveHooks(t)
	var gotURL string
	fetchFunc = func(cacheRoot, baseRef, url, commit string) error {
		gotURL = url
		return gitcache.FetchURL(cacheRoot, baseRef, url, commit)
	}
	local := filepath.Join(t.TempDir(), "a.yaml")
	if err := os.WriteFile(local, []byte("a: local\n"), 0644); err != nil {
		t.Fatal(err)
	}
	io, _, _ := bufIO()
	o := &UpdateOptions{SharedOptions: NewSharedOptions(io, nil)}
	o.showDiff(callerRef, "manifests/a.yaml", local, &lock.Lock{})
	if gotURL != "https://github.com/org/repo.git" {
		t.Errorf("fetchFunc got URL %q, want https://github.com/org/repo.git", gotURL)
	}
}
