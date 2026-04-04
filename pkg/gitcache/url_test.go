package gitcache

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// --- Comprehensive resolution table ---

func TestResolveGitURL_AllVariants(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GITLAB_TOKEN", "")
	t.Setenv("GITEA_TOKEN", "")

	tests := []struct {
		name      string
		ref       string
		configDir string
		wantURL   string
		isLocal   bool
		isSSH     bool
	}{
		// Remote HTTPS
		{"remote github", "github.com/org/repo", "", "https://github.com/org/repo.git", false, false},
		{"remote with label", "github.com/org/repo#monitoring", "", "https://github.com/org/repo.git", false, false},
		{"remote with version", "github.com/org/repo@v2.0", "", "https://github.com/org/repo.git", false, false},
		{"remote with version+label", "github.com/org/repo@v2.0#label", "", "https://github.com/org/repo.git", false, false},
		{"remote gitlab", "gitlab.com/group/project", "", "https://gitlab.com/group/project.git", false, false},

		// SSH implicit (git@host:path)
		{"ssh implicit", "git@github.com:org/repo", "", "git@github.com:org/repo.git", false, true},
		{"ssh implicit .git", "git@github.com:org/repo.git", "", "git@github.com:org/repo.git", false, true},
		{"ssh implicit with label", "git@github.com:org/repo#monitoring", "", "git@github.com:org/repo.git", false, true},
		{"ssh implicit with version", "git@github.com:org/repo@v2.0", "", "git@github.com:org/repo.git", false, true},
		{"ssh implicit gitlab", "git@gitlab.com:group/project", "", "git@gitlab.com:group/project.git", false, true},

		// SSH explicit (ssh://git@host/path)
		{"ssh explicit", "ssh://git@github.com/org/repo", "", "ssh://git@github.com/org/repo.git", false, true},
		{"ssh explicit .git", "ssh://git@github.com/org/repo.git", "", "ssh://git@github.com/org/repo.git", false, true},
		{"ssh custom port", "ssh://git@host:2222/org/repo", "", "ssh://git@host:2222/org/repo.git", false, true},
		{"ssh non-git user implicit", "deploy@host:org/repo", "", "deploy@host:org/repo.git", false, true},
		{"ssh non-git user explicit", "ssh://deploy@host/org/repo", "", "ssh://deploy@host/org/repo.git", false, true},

		// git:// protocol (b's custom protocol)
		{"git:// remote", "git://github.com/org/repo:scripts/tool", "", "https://github.com/org/repo.git", false, false},
		{"git:// local relative", "git://../../lok8s", "/home/user/project/.bin", "/home/user/lok8s", true, false},
		{"git:// local relative with label", "git://../../lok8s#local-dev", "/home/user/project/.bin", "/home/user/lok8s", true, false},
		{"git:// local absolute", "git:///home/user/repo:file", "", "/home/user/repo", true, false},

		// Local absolute paths (no stripping of # or @)
		{"local absolute", "/home/user/repo", "", "/home/user/repo", true, false},
		{"local absolute with @", "/home/user/repo@work", "", "/home/user/repo@work", true, false},
		{"local absolute with #", "/home/user/repo#branch", "", "/home/user/repo#branch", true, false},

		// Local relative paths
		{"relative dot", "./subdir/repo", "/home/user/.bin", "/home/user/.bin/subdir/repo", true, false},
		{"relative dotdot", "../my-repo", "/home/user/project/.bin", "/home/user/project/my-repo", true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := ResolveGitURL(tt.ref, tt.configDir)

			// For relative paths, compare with cleaned absolute
			wantURL := tt.wantURL
			if tt.isLocal && !filepath.IsAbs(tt.wantURL) {
				wantURL, _ = filepath.Abs(tt.wantURL)
			}
			// Clean expected paths for comparison
			if tt.isLocal {
				wantURL = filepath.Clean(wantURL)
				r.URL = filepath.Clean(r.URL)
			}

			if r.URL != wantURL {
				t.Errorf("URL = %q, want %q", r.URL, wantURL)
			}
			if r.IsLocal != tt.isLocal {
				t.Errorf("IsLocal = %v, want %v", r.IsLocal, tt.isLocal)
			}
			if r.IsSSH != tt.isSSH {
				t.Errorf("IsSSH = %v, want %v", r.IsSSH, tt.isSSH)
			}
			// SSH and local should never have auth tokens
			if (tt.isSSH || tt.isLocal) && r.AuthToken != "" {
				t.Errorf("AuthToken should be empty for SSH/local, got %q", r.AuthToken)
			}
		})
	}
}

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

// --- SSH refs ---

func TestResolveGitURL_SSH_Implicit(t *testing.T) {
	r := ResolveGitURL("git@github.com:org/repo", "")
	if !r.IsSSH {
		t.Error("expected SSH")
	}
	if r.IsLocal {
		t.Error("should not be local")
	}
	if r.URL != "git@github.com:org/repo.git" {
		t.Errorf("URL = %q", r.URL)
	}
	if r.AuthToken != "" {
		t.Errorf("SSH should have no auth token, got %q", r.AuthToken)
	}
}

func TestResolveGitURL_SSH_ImplicitWithGitSuffix(t *testing.T) {
	r := ResolveGitURL("git@github.com:org/repo.git", "")
	if r.URL != "git@github.com:org/repo.git" {
		t.Errorf("URL = %q, should not double-add .git", r.URL)
	}
}

func TestResolveGitURL_SSH_Explicit(t *testing.T) {
	r := ResolveGitURL("ssh://git@github.com/org/repo", "")
	if !r.IsSSH {
		t.Error("expected SSH")
	}
	if r.URL != "ssh://git@github.com/org/repo.git" {
		t.Errorf("URL = %q", r.URL)
	}
}

func TestResolveGitURL_SSH_CustomPort(t *testing.T) {
	r := ResolveGitURL("ssh://git@custom.host:2222/org/repo", "")
	if !r.IsSSH {
		t.Error("expected SSH")
	}
	if r.URL != "ssh://git@custom.host:2222/org/repo.git" {
		t.Errorf("URL = %q", r.URL)
	}
}

func TestResolveGitURL_SSH_WithLabel(t *testing.T) {
	r := ResolveGitURL("git@github.com:org/repo#monitoring", "")
	if !r.IsSSH {
		t.Error("expected SSH")
	}
	if r.URL != "git@github.com:org/repo.git" {
		t.Errorf("URL = %q, label should be stripped", r.URL)
	}
}

func TestResolveGitURL_SSH_WithVersion(t *testing.T) {
	// git@github.com:org/repo@v2.0 — the last @ is the version, not the SSH user
	r := ResolveGitURL("git@github.com:org/repo@v2.0", "")
	if !r.IsSSH {
		t.Error("expected SSH")
	}
	if r.URL != "git@github.com:org/repo.git" {
		t.Errorf("URL = %q, version should be stripped", r.URL)
	}
}

func TestIsSSHImplicit(t *testing.T) {
	tests := []struct {
		ref  string
		want bool
	}{
		{"git@github.com:org/repo", true},
		{"git@gitlab.com:group/project.git", true},
		{"ssh://git@github.com/org/repo", false}, // explicit, not implicit
		{"github.com/org/repo", false},
		{"https://github.com/org/repo.git", false},
		{"../../local", false},
	}
	for _, tt := range tests {
		t.Run(tt.ref, func(t *testing.T) {
			if got := isSSHImplicit(tt.ref); got != tt.want {
				t.Errorf("isSSHImplicit(%q) = %v, want %v", tt.ref, got, tt.want)
			}
		})
	}
}

func TestIsSSHURL(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{"git@github.com:org/repo.git", true},
		{"ssh://git@github.com/org/repo.git", true},
		{"https://github.com/org/repo.git", false},
		{"/local/repo", false},
	}
	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			if got := IsSSHURL(tt.url); got != tt.want {
				t.Errorf("IsSSHURL(%q) = %v, want %v", tt.url, got, tt.want)
			}
		})
	}
}

// --- RefBase/RefVersion with SSH ---

func TestRefBase_SSH(t *testing.T) {
	got := RefBase("git@github.com:org/repo")
	if got != "git@github.com:org/repo" {
		t.Errorf("RefBase = %q, should preserve SSH ref", got)
	}
}

func TestRefBase_SSH_WithVersion(t *testing.T) {
	got := RefBase("git@github.com:org/repo@v2.0")
	if got != "git@github.com:org/repo" {
		t.Errorf("RefBase = %q, should strip version but keep git@", got)
	}
}

func TestRefVersion_SSH(t *testing.T) {
	got := RefVersion("git@github.com:org/repo@v2.0")
	if got != "v2.0" {
		t.Errorf("RefVersion = %q, want v2.0", got)
	}
}

func TestRefVersion_SSH_NoVersion(t *testing.T) {
	got := RefVersion("git@github.com:org/repo")
	if got != "" {
		t.Errorf("RefVersion = %q, want empty", got)
	}
}

func TestIsSSHUserAt_NonGitUser(t *testing.T) {
	// deploy@host:path — should be SSH user
	if !IsSSHUserAt("deploy@host:org/repo", 6) {
		t.Error("deploy@host:path should be SSH user")
	}
}

func TestIsSSHUserAt_VersionSeparator(t *testing.T) {
	// github.com/org/repo@v2.0 — should NOT be SSH user
	if IsSSHUserAt("github.com/org/repo@v2.0", 20) {
		t.Error("repo@v2.0 should not be SSH user")
	}
}

func TestRefBase_NonGitSSHUser(t *testing.T) {
	got := RefBase("deploy@host:org/repo@v2.0")
	if got != "deploy@host:org/repo" {
		t.Errorf("RefBase = %q, should strip version but keep deploy@", got)
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

// --- Auth token detection ---

func TestDetectAuthToken_GitHub(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "ghp_test123")
	token := detectAuthToken("github.com/org/repo")
	if token != "ghp_test123" {
		t.Errorf("token = %q", token)
	}
}

func TestDetectAuthToken_GitLab(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "glpat-test")
	token := detectAuthToken("gitlab.com/org/repo")
	if token != "glpat-test" {
		t.Errorf("token = %q", token)
	}
}

func TestDetectAuthToken_Gitea(t *testing.T) {
	t.Setenv("GITEA_TOKEN", "giteatok")
	token := detectAuthToken("codeberg.org/org/repo")
	if token != "giteatok" {
		t.Errorf("token = %q", token)
	}
}

func TestDetectAuthToken_NoToken(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GITLAB_TOKEN", "")
	t.Setenv("GITEA_TOKEN", "")
	token := detectAuthToken("github.com/org/repo")
	if token != "" {
		t.Errorf("expected empty token, got %q", token)
	}
}

func TestDetectAuthToken_UnknownHost(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "ghp_test")
	token := detectAuthToken("custom.host.com/org/repo")
	if token != "" {
		t.Errorf("expected empty token for unknown host, got %q", token)
	}
}

func TestDetectAuthToken_SpoofedHost(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "ghp_secret")
	// github.evil.example should NOT match github.com
	token := detectAuthToken("github.evil.example/org/repo")
	if token != "" {
		t.Errorf("spoofed host should not get token, got %q", token)
	}
}

func TestDetectAuthToken_Subdomain(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "ghp_enterprise")
	// enterprise.github.com is a subdomain of github.com — should match
	token := detectAuthToken("enterprise.github.com/org/repo")
	if token != "ghp_enterprise" {
		t.Errorf("subdomain should match, got %q", token)
	}
}

func TestMatchesTrustedHost(t *testing.T) {
	tests := []struct {
		host, domain string
		want         bool
	}{
		{"github.com", "github.com", true},
		{"enterprise.github.com", "github.com", true},
		{"GITHUB.COM", "github.com", true},
		{"github.evil.example", "github.com", false},
		{"notgithub.com", "github.com", false},
		{"gitlab.com", "gitlab.com", true},
		{"codeberg.org", "codeberg.org", true},
	}
	for _, tt := range tests {
		t.Run(tt.host+"→"+tt.domain, func(t *testing.T) {
			if got := matchesTrustedHost(tt.host, tt.domain); got != tt.want {
				t.Errorf("matchesTrustedHost(%q, %q) = %v, want %v", tt.host, tt.domain, got, tt.want)
			}
		})
	}
}

func TestRedactToken(t *testing.T) {
	got := redactToken("git clone https://host: Bearer ghp_secret123 failed", "ghp_secret123")
	if strings.Contains(got, "ghp_secret123") {
		t.Errorf("token should be redacted, got: %q", got)
	}
	if !strings.Contains(got, "***") {
		t.Errorf("expected *** in redacted string, got: %q", got)
	}
}

func TestRedactToken_Empty(t *testing.T) {
	got := redactToken("some error", "")
	if got != "some error" {
		t.Errorf("empty token should not change string, got: %q", got)
	}
}

func TestResolveGitURL_AuthToken(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "ghp_integrated")
	r := ResolveGitURL("github.com/org/repo", "")
	if r.IsLocal {
		t.Error("expected remote")
	}
	// URL should NOT contain the token (no credential leaking)
	if r.URL != "https://github.com/org/repo.git" {
		t.Errorf("URL = %q, should not contain token", r.URL)
	}
	if r.AuthToken != "ghp_integrated" {
		t.Errorf("AuthToken = %q", r.AuthToken)
	}
}

func TestResolveGitURL_LocalNoAuth(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "ghp_shouldnotappear")
	r := ResolveGitURL("/home/user/repo", "")
	if r.URL != "/home/user/repo" {
		t.Errorf("URL = %q", r.URL)
	}
	if r.AuthToken != "" {
		t.Errorf("local repo should not have auth token, got %q", r.AuthToken)
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
	if err := os.WriteFile(filepath.Join(tmpDir, "test.txt"), []byte("hello"), 0644); err != nil {
		t.Fatalf("write test file: %v", err)
	}
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

// --- authArgs ---

func TestAuthArgs_NoToken(t *testing.T) {
	args := authArgs("", "ls-remote", "https://github.com/org/repo.git", "HEAD")
	if args[0] != "git" {
		t.Errorf("args[0] = %q, want 'git'", args[0])
	}
	if args[1] != "ls-remote" {
		t.Errorf("args[1] = %q, want 'ls-remote'", args[1])
	}
	// Should not have -c http.extraHeader
	for _, a := range args {
		if strings.Contains(a, "extraHeader") {
			t.Error("should not have extraHeader without token")
		}
	}
}

func TestAuthArgs_WithToken(t *testing.T) {
	args := authArgs("ghp_secret", "clone", "--bare", "https://github.com/org/repo.git")
	if args[0] != "git" {
		t.Errorf("args[0] = %q", args[0])
	}
	if args[1] != "-c" {
		t.Errorf("args[1] = %q, want '-c'", args[1])
	}
	if !strings.Contains(args[2], "Bearer ghp_secret") {
		t.Errorf("args[2] = %q, should contain Bearer token", args[2])
	}
	if args[3] != "clone" {
		t.Errorf("args[3] = %q, want 'clone'", args[3])
	}
}

// --- ShowFileDir / ListTreeWithModesDir integration ---

func TestShowFileDir_Integration(t *testing.T) {
	tmpDir := t.TempDir()
	initTestRepo(t, tmpDir)

	commit := getHeadCommit(t, tmpDir)

	content, err := ShowFileDir(tmpDir, commit, "test.txt")
	if err != nil {
		t.Fatalf("ShowFileDir error: %v", err)
	}
	if string(content) != "hello" {
		t.Errorf("content = %q, want 'hello'", content)
	}
}

func TestShowFileDir_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	initTestRepo(t, tmpDir)
	commit := getHeadCommit(t, tmpDir)

	_, err := ShowFileDir(tmpDir, commit, "nonexistent.txt")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestListTreeWithModesDir_Integration(t *testing.T) {
	tmpDir := t.TempDir()
	initTestRepo(t, tmpDir)
	commit := getHeadCommit(t, tmpDir)

	entries, err := ListTreeWithModesDir(tmpDir, commit)
	if err != nil {
		t.Fatalf("ListTreeWithModesDir error: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one entry")
	}
	found := false
	for _, e := range entries {
		if e.Path == "test.txt" {
			found = true
			if e.Mode != "100644" {
				t.Errorf("mode = %q, want 100644", e.Mode)
			}
		}
	}
	if !found {
		t.Error("test.txt not found in tree")
	}
}

// --- ResolveLocalRef error case ---

func TestResolveLocalRef_InvalidRepo(t *testing.T) {
	_, err := ResolveLocalRef(t.TempDir(), "")
	if err == nil {
		t.Error("expected error for non-git directory")
	}
}

func TestResolveLocalRef_InvalidVersion(t *testing.T) {
	tmpDir := t.TempDir()
	initTestRepo(t, tmpDir)

	_, err := ResolveLocalRef(tmpDir, "nonexistent-tag-xyz")
	if err == nil {
		t.Error("expected error for invalid version")
	}
}

// --- test helpers ---

func initTestRepo(t *testing.T, dir string) {
	t.Helper()
	for _, cmd := range [][]string{
		{"git", "init", dir},
		{"git", "-C", dir, "config", "user.email", "test@test.com"},
		{"git", "-C", dir, "config", "user.name", "Test"},
	} {
		if out, err := runOutput(cmd...); err != nil {
			t.Fatalf("%v: %v\n%s", cmd, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "test.txt"), []byte("hello"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	for _, cmd := range [][]string{
		{"git", "-C", dir, "add", "-A"},
		{"git", "-C", dir, "commit", "-m", "init", "--no-gpg-sign"},
	} {
		if out, err := runOutput(cmd...); err != nil {
			t.Fatalf("%v: %v\n%s", cmd, err, out)
		}
	}
}

func getHeadCommit(t *testing.T, dir string) string {
	t.Helper()
	commit, err := ResolveLocalRef(dir, "HEAD")
	if err != nil {
		t.Fatalf("getHeadCommit: %v", err)
	}
	return commit
}

func runOutput(args ...string) (string, error) {
	cmd := exec.Command(args[0], args[1:]...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
