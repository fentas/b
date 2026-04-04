package gitcache

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestResolveGitURL_RemoteRef(t *testing.T) {
	r := ResolveGitURL("github.com/org/repo", "")
	if r.IsLocal {
		t.Error("expected remote")
	}
	if r.URL != "https://github.com/org/repo.git" {
		t.Errorf("URL = %q", r.URL)
	}
}

func TestResolveGitURL_RemoteWithLabel(t *testing.T) {
	r := ResolveGitURL("github.com/org/repo#monitoring", "")
	if r.URL != "https://github.com/org/repo.git" {
		t.Errorf("URL = %q, label should be stripped", r.URL)
	}
}

func TestResolveGitURL_RemoteWithVersion(t *testing.T) {
	r := ResolveGitURL("github.com/org/repo@v2.0", "")
	if r.URL != "https://github.com/org/repo.git" {
		t.Errorf("URL = %q, version should be stripped", r.URL)
	}
}

func TestResolveGitURL_RemoteWithVersionAndLabel(t *testing.T) {
	r := ResolveGitURL("github.com/org/repo@v2.0#monitoring", "")
	if r.URL != "https://github.com/org/repo.git" {
		t.Errorf("URL = %q", r.URL)
	}
}

func TestResolveGitURL_GitProtocol_LocalAbsolute(t *testing.T) {
	r := ResolveGitURL("git:///home/user/repo", "")
	if !r.IsLocal {
		t.Error("expected local")
	}
	if r.URL != "/home/user/repo" {
		t.Errorf("URL = %q", r.URL)
	}
}

func TestResolveGitURL_GitProtocol_LocalRelative(t *testing.T) {
	r := ResolveGitURL("git://../../lok8s", "/home/user/project/.bin")
	if !r.IsLocal {
		t.Error("expected local")
	}
	expected := filepath.Clean("/home/user/project/.bin/../../lok8s")
	if r.URL != expected {
		t.Errorf("URL = %q, want %q", r.URL, expected)
	}
}

func TestResolveGitURL_GitProtocol_LocalRelativeWithLabel(t *testing.T) {
	r := ResolveGitURL("git://../../lok8s#local-dev", "/home/user/project/.bin")
	if !r.IsLocal {
		t.Error("expected local")
	}
	expected := filepath.Clean("/home/user/project/.bin/../../lok8s")
	if r.URL != expected {
		t.Errorf("URL = %q, want %q", r.URL, expected)
	}
}

func TestResolveGitURL_GitProtocol_Remote(t *testing.T) {
	r := ResolveGitURL("git://github.com/org/repo:scripts/tool", "")
	if r.IsLocal {
		t.Error("expected remote")
	}
	if r.URL != "https://github.com/org/repo.git" {
		t.Errorf("URL = %q", r.URL)
	}
}

func TestResolveGitURL_GitProtocol_WithColon(t *testing.T) {
	// git://../../repo:filepath should strip the :filepath part
	r := ResolveGitURL("git://../../repo:scripts/tool", "/home/user/.bin")
	if !r.IsLocal {
		t.Error("expected local")
	}
	expected := filepath.Clean("/home/user/.bin/../../repo")
	if r.URL != expected {
		t.Errorf("URL = %q, want %q", r.URL, expected)
	}
}

func TestResolveGitURL_AbsoluteLocalPath(t *testing.T) {
	r := ResolveGitURL("/home/user/my-repo", "")
	if !r.IsLocal {
		t.Error("expected local")
	}
	if r.URL != "/home/user/my-repo" {
		t.Errorf("URL = %q", r.URL)
	}
}

func TestResolveGitURL_RelativePath(t *testing.T) {
	r := ResolveGitURL("../my-repo", "/home/user/project/.bin")
	if !r.IsLocal {
		t.Error("expected local")
	}
	expected := filepath.Clean("/home/user/project/.bin/../my-repo")
	if r.URL != expected {
		t.Errorf("URL = %q, want %q", r.URL, expected)
	}
}

func TestResolveGitURL_DotRelativePath(t *testing.T) {
	r := ResolveGitURL("./subdir/repo", "/home/user/.bin")
	if !r.IsLocal {
		t.Error("expected local")
	}
	expected := filepath.Clean("/home/user/.bin/subdir/repo")
	if r.URL != expected {
		t.Errorf("URL = %q, want %q", r.URL, expected)
	}
}

func TestResolveGitURL_GitlabRef(t *testing.T) {
	r := ResolveGitURL("gitlab.com/group/project", "")
	if r.IsLocal {
		t.Error("expected remote")
	}
	if r.URL != "https://gitlab.com/group/project.git" {
		t.Errorf("URL = %q", r.URL)
	}
}

// --- Auth injection ---

func TestInjectAuth_GitHub(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "ghp_test123")
	url := injectAuth("https://github.com/org/repo.git", "github.com/org/repo")
	if url != "https://x-access-token:ghp_test123@github.com/org/repo.git" {
		t.Errorf("URL = %q", url)
	}
}

func TestInjectAuth_GitLab(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "glpat-test")
	url := injectAuth("https://gitlab.com/org/repo.git", "gitlab.com/org/repo")
	if url != "https://x-access-token:glpat-test@gitlab.com/org/repo.git" {
		t.Errorf("URL = %q", url)
	}
}

func TestInjectAuth_Gitea(t *testing.T) {
	t.Setenv("GITEA_TOKEN", "giteatok")
	url := injectAuth("https://codeberg.org/org/repo.git", "codeberg.org/org/repo")
	if url != "https://x-access-token:giteatok@codeberg.org/org/repo.git" {
		t.Errorf("URL = %q", url)
	}
}

func TestInjectAuth_NoToken(t *testing.T) {
	// Ensure no tokens are set
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GITLAB_TOKEN", "")
	t.Setenv("GITEA_TOKEN", "")
	url := injectAuth("https://github.com/org/repo.git", "github.com/org/repo")
	if url != "https://github.com/org/repo.git" {
		t.Errorf("URL should be unchanged, got %q", url)
	}
}

func TestInjectAuth_UnknownHost(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "ghp_test")
	url := injectAuth("https://custom.host.com/org/repo.git", "custom.host.com/org/repo")
	if url != "https://custom.host.com/org/repo.git" {
		t.Errorf("URL should be unchanged for unknown host, got %q", url)
	}
}

func TestResolveGitURL_AuthInjection(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "ghp_integrated")
	r := ResolveGitURL("github.com/org/repo", "")
	if r.IsLocal {
		t.Error("expected remote")
	}
	if r.URL != "https://x-access-token:ghp_integrated@github.com/org/repo.git" {
		t.Errorf("URL = %q", r.URL)
	}
}

func TestResolveGitURL_LocalNoAuth(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "ghp_shouldnotappear")
	r := ResolveGitURL("/home/user/repo", "")
	if r.URL != "/home/user/repo" {
		t.Errorf("local repo should not have auth injected, got %q", r.URL)
	}
}

// --- ResolveLocalRef ---

func TestResolveLocalRef_Integration(t *testing.T) {
	// Create a temp git repo
	tmpDir := t.TempDir()
	for _, cmd := range [][]string{
		{"git", "init", tmpDir},
		{"git", "-C", tmpDir, "config", "user.email", "test@test.com"},
		{"git", "-C", tmpDir, "config", "user.name", "Test"},
	} {
		if out, err := runOutput(cmd...); err != nil {
			t.Fatalf("%v: %v\n%s", cmd, err, out)
		}
	}

	// Create a file and commit
	os.WriteFile(filepath.Join(tmpDir, "test.txt"), []byte("hello"), 0644)
	for _, cmd := range [][]string{
		{"git", "-C", tmpDir, "add", "-A"},
		{"git", "-C", tmpDir, "commit", "-m", "init", "--no-gpg-sign"},
	} {
		if out, err := runOutput(cmd...); err != nil {
			t.Fatalf("%v: %v\n%s", cmd, err, out)
		}
	}

	commit, err := ResolveLocalRef(tmpDir, "")
	if err != nil {
		t.Fatalf("ResolveLocalRef error: %v", err)
	}
	if len(commit) < 7 {
		t.Errorf("commit too short: %q", commit)
	}

	// HEAD should resolve same
	commitHead, err := ResolveLocalRef(tmpDir, "HEAD")
	if err != nil {
		t.Fatalf("ResolveLocalRef HEAD error: %v", err)
	}
	if commit != commitHead {
		t.Errorf("empty version and HEAD should match: %q vs %q", commit, commitHead)
	}
}

// runOutput is a test helper that runs a command and returns combined output.
func runOutput(args ...string) (string, error) {
	cmd := exec.Command(args[0], args[1:]...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
