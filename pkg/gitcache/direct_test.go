package gitcache

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func setupBareRepo(t *testing.T) (bareDir, commit string) {
	t.Helper()
	tmp := t.TempDir()
	work := filepath.Join(tmp, "work")
	bare := filepath.Join(tmp, "bare.git")
	run := func(args ...string) {
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	run("git", "init", "-q", work)
	run("git", "-C", work, "config", "user.email", "t@t.com")
	run("git", "-C", work, "config", "user.name", "T")
	run("git", "-C", work, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(work, "a.txt"), []byte("hello"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	run("git", "-C", work, "add", "-A")
	run("git", "-C", work, "commit", "-m", "init", "--no-gpg-sign")
	run("git", "clone", "--bare", "-q", work, bare)
	out, err := exec.Command("git", "-C", bare, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return bare, string(out[:len(out)-1])
}

func TestGitcache_EnsureCloneFetchResolve(t *testing.T) {
	bare, commit := setupBareRepo(t)
	root := t.TempDir()
	ref := bare

	if err := EnsureClone(root, ref, bare); err != nil {
		t.Fatalf("EnsureClone: %v", err)
	}
	// Second call is a no-op
	if err := EnsureClone(root, ref, bare); err != nil {
		t.Fatal(err)
	}
	if err := Fetch(root, ref, commit); err != nil {
		t.Errorf("Fetch: %v", err)
	}
	// ResolveRef via ls-remote
	sha, err := ResolveRef(bare, "")
	if err != nil {
		t.Errorf("ResolveRef: %v", err)
	}
	if sha != commit {
		t.Errorf("sha=%q want %q", sha, commit)
	}
	// ListTree & ShowFile
	paths, err := ListTree(root, ref, commit)
	if err != nil || len(paths) == 0 {
		t.Errorf("ListTree: %v paths=%v", err, paths)
	}
	data, err := ShowFile(root, ref, commit, "a.txt")
	if err != nil || string(data) != "hello" {
		t.Errorf("ShowFile: %q err=%v", data, err)
	}
}

func TestGitcache_ResolveRef_NotFound(t *testing.T) {
	if _, err := ResolveRef("/nonexistent/repo", "HEAD"); err == nil {
		t.Error("expected error")
	}
}
