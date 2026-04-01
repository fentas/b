package gitcache

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// --- Feature 6: TreeEntry type ---

func TestTreeEntry_Struct(t *testing.T) {
	entry := TreeEntry{
		Path: "manifests/deploy.yaml",
		Mode: "100644",
	}
	if entry.Path != "manifests/deploy.yaml" {
		t.Errorf("Path = %q", entry.Path)
	}
	if entry.Mode != "100644" {
		t.Errorf("Mode = %q", entry.Mode)
	}
}

func TestTreeEntry_ExecutableMode(t *testing.T) {
	entry := TreeEntry{
		Path: "scripts/deploy.sh",
		Mode: "100755",
	}
	if entry.Mode != "100755" {
		t.Errorf("Mode = %q, want 100755", entry.Mode)
	}
}

// --- Feature 6: ListTreeWithModes integration ---

func TestListTreeWithModes_Integration(t *testing.T) {
	// Create a temporary bare git repo with a normal file and an executable
	tmpDir := t.TempDir()
	workDir := filepath.Join(tmpDir, "work")
	bareDir := filepath.Join(tmpDir, "bare")

	// Init work repo
	for _, args := range [][]string{
		{"git", "init", workDir},
		{"git", "-C", workDir, "config", "user.email", "test@test.com"},
		{"git", "-C", workDir, "config", "user.name", "Test"},
	} {
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%v failed: %v\n%s", args, err, out)
		}
	}

	// Create a normal file
	os.WriteFile(filepath.Join(workDir, "config.yaml"), []byte("key: value"), 0644)

	// Create an executable file
	scriptPath := filepath.Join(workDir, "deploy.sh")
	os.WriteFile(scriptPath, []byte("#!/bin/sh\necho deploy"), 0755)

	// Git needs the executable bit tracked
	for _, args := range [][]string{
		{"git", "-C", workDir, "add", "-A"},
		{"git", "-C", workDir, "update-index", "--chmod=+x", "deploy.sh"},
		{"git", "-C", workDir, "commit", "-m", "init", "--no-gpg-sign"},
	} {
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%v failed: %v\n%s", args, err, out)
		}
	}

	// Clone to bare repo (simulating the cache)
	if out, err := exec.Command("git", "clone", "--bare", workDir, bareDir).CombinedOutput(); err != nil {
		t.Fatalf("bare clone failed: %v\n%s", err, out)
	}

	// Get HEAD commit
	commitOut, err := exec.Command("git", "-C", bareDir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse failed: %v", err)
	}
	commit := string(commitOut[:len(commitOut)-1]) // strip newline

	// Use a fake cache root that maps ref → bareDir
	// We need CacheDir(root, ref) == bareDir, so compute the root
	// Instead, just call ListTreeWithModes directly on the bare dir
	// by using a root/ref combo that resolves to bareDir
	ref := "test-repo"
	root := filepath.Dir(bareDir)
	// Override: rename bare dir to match CacheDir hash
	cacheDir := CacheDir(root, ref)
	os.Rename(bareDir, cacheDir)

	entries, err := ListTreeWithModes(root, ref, commit)
	if err != nil {
		t.Fatalf("ListTreeWithModes error: %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	modes := make(map[string]string)
	for _, e := range entries {
		modes[e.Path] = e.Mode
	}

	if modes["config.yaml"] != "100644" {
		t.Errorf("config.yaml mode = %q, want 100644", modes["config.yaml"])
	}
	if modes["deploy.sh"] != "100755" {
		t.Errorf("deploy.sh mode = %q, want 100755", modes["deploy.sh"])
	}
}
