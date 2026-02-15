package provider

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGitMatch(t *testing.T) {
	g := &Git{}

	tests := []struct {
		ref  string
		want bool
	}{
		{"git:///home/user/repo:.scripts/lo", true},
		{"git://github.com/org/repo:scripts/tool.sh", true},
		{"git://github.com/org/repo:scripts/tool.sh@v1.0", true},
		{"github.com/org/repo", false},
		{"go://github.com/org/repo", false},
		{"docker://alpine/helm", false},
		{"", false},
	}

	for _, tt := range tests {
		got := g.Match(tt.ref)
		if got != tt.want {
			t.Errorf("Git.Match(%q) = %v, want %v", tt.ref, got, tt.want)
		}
	}
}

func TestGitName(t *testing.T) {
	g := &Git{}
	if got := g.Name(); got != "git" {
		t.Errorf("Git.Name() = %q, want %q", got, "git")
	}
}

func TestParseGitRef(t *testing.T) {
	tests := []struct {
		ref      string
		wantRepo string
		wantFile string
		wantErr  bool
	}{
		{
			ref:      "git:///home/user/repo:.scripts/lo",
			wantRepo: "/home/user/repo",
			wantFile: ".scripts/lo",
		},
		{
			ref:      "git://github.com/org/repo:scripts/tool.sh",
			wantRepo: "github.com/org/repo",
			wantFile: "scripts/tool.sh",
		},
		{
			ref:      "git://github.com/org/repo:scripts/tool.sh@v1.0",
			wantRepo: "github.com/org/repo",
			wantFile: "scripts/tool.sh",
		},
		{
			ref:      "git:///tmp/my-repo:bin/app",
			wantRepo: "/tmp/my-repo",
			wantFile: "bin/app",
		},
		{
			ref:     "git://no-colon-here",
			wantErr: true,
		},
		{
			ref:     "git://",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		repo, filePath, err := parseGitRef(tt.ref)
		if tt.wantErr {
			if err == nil {
				t.Errorf("parseGitRef(%q) expected error, got repo=%q file=%q", tt.ref, repo, filePath)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseGitRef(%q) unexpected error: %v", tt.ref, err)
			continue
		}
		if repo != tt.wantRepo {
			t.Errorf("parseGitRef(%q) repo = %q, want %q", tt.ref, repo, tt.wantRepo)
		}
		if filePath != tt.wantFile {
			t.Errorf("parseGitRef(%q) filePath = %q, want %q", tt.ref, filePath, tt.wantFile)
		}
	}
}

func TestIsLocalRepo(t *testing.T) {
	tests := []struct {
		repo string
		want bool
	}{
		{"/home/user/repo", true},
		{"/tmp/repo", true},
		{"github.com/org/repo", false},
		{"gitlab.com/org/repo", false},
	}

	for _, tt := range tests {
		got := isLocalRepo(tt.repo)
		if got != tt.want {
			t.Errorf("isLocalRepo(%q) = %v, want %v", tt.repo, got, tt.want)
		}
	}
}

func TestGitBinaryName(t *testing.T) {
	tests := []struct {
		ref  string
		want string
	}{
		{"git:///home/user/repo:.scripts/lo", "lo"},
		{"git://github.com/org/repo:scripts/tool.sh", "tool.sh"},
		{"git://github.com/org/repo:bin/my-app@v1.0", "my-app"},
		{"git:///tmp/repo:single-file", "single-file"},
	}

	for _, tt := range tests {
		got := BinaryName(tt.ref)
		if got != tt.want {
			t.Errorf("BinaryName(%q) = %q, want %q", tt.ref, got, tt.want)
		}
	}
}

// initTestRepo creates a temporary git repo and returns a run helper.
func initTestRepo(t *testing.T) (string, func(...string) string) {
	t.Helper()

	repoDir := t.TempDir()

	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = repoDir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@test.com",
			"GIT_CONFIG_NOSYSTEM=1",
			"HOME="+repoDir,
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("command %v failed: %v\n%s", args, err, out)
		}
		return string(out)
	}

	run("git", "init")
	run("git", "checkout", "-b", "main")
	// Ensure no GPG signing in test repos
	run("git", "config", "commit.gpgsign", "false")

	return repoDir, run
}

// TestGitInstallLocal creates a temporary git repo and tests Install on it.
func TestGitInstallLocal(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repoDir, run := initTestRepo(t)

	// Create a script file
	scriptDir := filepath.Join(repoDir, ".scripts")
	if err := os.MkdirAll(scriptDir, 0755); err != nil {
		t.Fatal(err)
	}
	scriptContent := []byte("#!/bin/bash\necho hello\n")
	if err := os.WriteFile(filepath.Join(scriptDir, "lo"), scriptContent, 0755); err != nil {
		t.Fatal(err)
	}

	run("git", "add", "-A")
	run("git", "commit", "-m", "initial")

	// Test Install
	g := &Git{}
	destDir := t.TempDir()
	ref := "git://" + repoDir + ":.scripts/lo"

	path, err := g.Install(ref, "", destDir)
	if err != nil {
		t.Fatalf("Git.Install() error: %v", err)
	}

	if filepath.Base(path) != "lo" {
		t.Errorf("installed binary name = %q, want %q", filepath.Base(path), "lo")
	}

	// Verify file content
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading installed binary: %v", err)
	}
	if string(data) != string(scriptContent) {
		t.Errorf("installed content = %q, want %q", string(data), string(scriptContent))
	}

	// Verify executable bit
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0111 == 0 {
		t.Error("installed file is not executable")
	}
}

// TestGitLatestVersionLocal tests LatestVersion on a local repo.
func TestGitLatestVersionLocal(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repoDir, run := initTestRepo(t)

	if err := os.WriteFile(filepath.Join(repoDir, "file.txt"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	run("git", "add", "-A")
	run("git", "commit", "-m", "initial")

	g := &Git{}
	ref := "git://" + repoDir + ":file.txt"

	version, err := g.LatestVersion(ref)
	if err != nil {
		t.Fatalf("Git.LatestVersion() error: %v", err)
	}

	// Should be a 40-char hex SHA
	if len(version) != 40 {
		t.Errorf("LatestVersion returned %q, expected 40-char SHA", version)
	}
}

// TestGitInstallLocalWithVersion tests Install with a specific tag/revision.
func TestGitInstallLocalWithVersion(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repoDir, run := initTestRepo(t)

	// First commit with v1 content
	if err := os.WriteFile(filepath.Join(repoDir, "tool"), []byte("v1-content"), 0755); err != nil {
		t.Fatal(err)
	}
	run("git", "add", "-A")
	run("git", "commit", "-m", "v1")
	run("git", "tag", "v1.0")

	// Second commit with v2 content
	if err := os.WriteFile(filepath.Join(repoDir, "tool"), []byte("v2-content"), 0755); err != nil {
		t.Fatal(err)
	}
	run("git", "add", "-A")
	run("git", "commit", "-m", "v2")

	// Install at v1.0 tag
	g := &Git{}
	destDir := t.TempDir()
	ref := "git://" + repoDir + ":tool"

	path, err := g.Install(ref, "v1.0", destDir)
	if err != nil {
		t.Fatalf("Git.Install() at v1.0 error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "v1-content" {
		t.Errorf("content at v1.0 = %q, want %q", string(data), "v1-content")
	}

	// Install at HEAD (default)
	destDir2 := t.TempDir()
	path2, err := g.Install(ref, "", destDir2)
	if err != nil {
		t.Fatalf("Git.Install() at HEAD error: %v", err)
	}

	data2, err := os.ReadFile(path2)
	if err != nil {
		t.Fatal(err)
	}
	if string(data2) != "v2-content" {
		t.Errorf("content at HEAD = %q, want %q", string(data2), "v2-content")
	}
}
