package provider

import (
	"path/filepath"
	"strings"
	"testing"
)

// fakePassword is the password in the refs of these tests. It is not a real
// value.
const fakePassword = "fake-pw-value-for-tests"

// For a git ref with a password in its URL, the clone error of Git.Install
// shows "***@" in place of the user part (git.go, installFromRemote
// "cloning", and the git command line in it). The protocol fence refuses
// SSH, so no git command leaves the machine.
func TestGitInstall_CloneErrorHidesTheUserPart(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home) // gitcache.DefaultCacheRoot is under HOME
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, ".gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_COUNT", "0")
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(home))
	t.Setenv("GIT_ALLOW_PROTOCOL", "file")
	t.Setenv("GITHUB_TOKEN", "")
	t.Chdir(home)

	ref := "ssh://user:" + fakePassword + "@github.com/org/repo:tool.sh"
	err := (&Git{}).Install(ref, "", filepath.Join(t.TempDir(), "tool"))
	if err == nil {
		t.Fatal("Install passed; the protocol fence must refuse SSH")
	}
	msg := err.Error()
	if strings.Contains(msg, fakePassword) {
		t.Errorf("the error shows the password: %q", strings.ReplaceAll(msg, fakePassword, "<password>"))
	}
	if !strings.HasPrefix(msg, "cloning ssh://***@github.com/org/repo.git: git clone --bare --depth 1 -- ssh://***@github.com/org/repo.git ") ||
		!strings.Contains(msg, "transport 'ssh' not allowed") {
		t.Errorf("error %q, want the clone prefix and the git command line with ***@, and the SSH refusal of git",
			strings.ReplaceAll(msg, fakePassword, "<password>"))
	}
}
