package gitcache

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultCacheRoot(t *testing.T) {
	root := DefaultCacheRoot()
	if root == "" {
		t.Fatal("DefaultCacheRoot() returned empty string")
	}

	home, _ := os.UserHomeDir()
	expected := filepath.Join(home, ".cache", "b", "repos")
	if root != expected {
		t.Errorf("DefaultCacheRoot() = %q, want %q", root, expected)
	}
}

func TestGitURL_ProtocolPrefixes(t *testing.T) {
	tests := []struct {
		ref  string
		want string
	}{
		// Already tested in gitcache_test.go but adding edge cases
		{"codeberg.org/user/app", "https://codeberg.org/user/app.git"},
		{"codeberg.org/user/app@v1.0", "https://codeberg.org/user/app.git"},
		{"codeberg.org/user/app#label", "https://codeberg.org/user/app.git"},
	}
	for _, tt := range tests {
		got := GitURL(tt.ref)
		if got != tt.want {
			t.Errorf("GitURL(%q) = %q, want %q", tt.ref, got, tt.want)
		}
	}
}

func TestCacheDir_Deterministic(t *testing.T) {
	root := "/tmp/test-cache"
	ref := "github.com/org/test-repo"

	// Same inputs produce same output
	d1 := CacheDir(root, ref)
	d2 := CacheDir(root, ref)
	if d1 != d2 {
		t.Errorf("CacheDir not deterministic: %q != %q", d1, d2)
	}

	// Different root produces different path
	d3 := CacheDir("/other/root", ref)
	if d1 == d3 {
		t.Error("different roots should produce different paths")
	}
}

func TestRefBase_EdgeCases(t *testing.T) {
	tests := []struct {
		ref  string
		want string
	}{
		{"simple", "simple"},
		{"a/b/c#label@version", "a/b/c"},
		{"a@v1", "a"},
	}
	for _, tt := range tests {
		got := RefBase(tt.ref)
		if got != tt.want {
			t.Errorf("RefBase(%q) = %q, want %q", tt.ref, got, tt.want)
		}
	}
}

func TestRepoPath(t *testing.T) {
	tests := []struct {
		ref  string
		want []string
	}{
		{"github.com/org/repo", []string{"github.com", "org", "repo"}},
		{"git@github.com:org/repo", []string{"org", "repo"}},
		{"git@github.com:org/repo.git#main", []string{"org", "repo"}},
		{"github.com/org/repo@v2.0", []string{"github.com", "org", "repo"}},
		{"ssh://git@host/org/repo", []string{"org", "repo"}},
		// host:port must not be mistaken for an scp "host:path" separator.
		{"ssh://git@host:2222/org/repo", []string{"org", "repo"}},
		{"https://github.com:443/org/repo", []string{"github.com:443", "org", "repo"}},
		// .git must be stripped even with a trailing slash.
		{"github.com/org/repo.git/", []string{"github.com", "org", "repo"}},
		{"single", []string{"single"}},
		{"/abs/local/path", nil},
		{"./rel", nil},
	}
	for _, tt := range tests {
		got := RepoPath(tt.ref)
		if len(got) != len(tt.want) {
			t.Errorf("RepoPath(%q) = %v, want %v", tt.ref, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("RepoPath(%q) = %v, want %v", tt.ref, got, tt.want)
				break
			}
		}
	}
}

func TestHasCommit(t *testing.T) {
	tmp := t.TempDir()
	work := filepath.Join(tmp, "work")
	bare := filepath.Join(tmp, "bare.git")
	cacheRoot := filepath.Join(tmp, "cache")
	run := func(args ...string) {
		t.Helper()
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	run("git", "init", "-q", "-b", "main", work)
	run("git", "-C", work, "config", "user.email", "t@t.com")
	run("git", "-C", work, "config", "user.name", "T")
	if err := os.WriteFile(filepath.Join(work, "f"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	run("git", "-C", work, "add", "-A")
	run("git", "-C", work, "commit", "-q", "-m", "c", "--no-gpg-sign")
	run("git", "clone", "--bare", "-q", work, bare)

	// Cache dir doesn't exist yet → false, no error.
	if HasCommit(cacheRoot, "r", "0000000000000000000000000000000000000000") {
		t.Error("HasCommit on missing cache dir should be false")
	}
	if err := EnsureClone(cacheRoot, "r", bare); err != nil {
		t.Fatal(err)
	}
	commit, err := ResolveRef(bare, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := Fetch(cacheRoot, "r", commit); err != nil {
		t.Fatal(err)
	}
	if !HasCommit(cacheRoot, "r", commit) {
		t.Error("HasCommit should be true for a fetched commit")
	}
	if HasCommit(cacheRoot, "r", "1111111111111111111111111111111111111111") {
		t.Error("HasCommit should be false for an unknown sha")
	}
}

func TestBlobOIDAndTreeOIDs(t *testing.T) {
	tmp := t.TempDir()
	work := filepath.Join(tmp, "work")
	run := func(args ...string) string {
		t.Helper()
		out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("git", "init", "-q", "-b", "main", work)
	run("git", "-C", work, "config", "user.email", "t@t.com")
	run("git", "-C", work, "config", "user.name", "T")
	content := []byte("hello blob\n")
	if err := os.WriteFile(filepath.Join(work, "f.txt"), content, 0644); err != nil {
		t.Fatal(err)
	}
	run("git", "-C", work, "add", "-A")
	run("git", "-C", work, "commit", "-q", "-m", "c", "--no-gpg-sign")

	// BlobOID must equal what git itself computes.
	gitOID := run("git", "-C", work, "hash-object", filepath.Join(work, "f.txt"))
	if got := BlobOID(content, len(gitOID)); got != gitOID {
		t.Errorf("BlobOID = %s, want %s (git hash-object)", got, gitOID)
	}

	// ListTreeWithModesDir must surface type + OID per entry.
	commit := run("git", "-C", work, "rev-parse", "HEAD")
	entries, err := ListTreeWithModesDir(work, commit)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	if entries[0].Type != "blob" || entries[0].OID != gitOID {
		t.Errorf("entry = %+v, want type=blob oid=%s", entries[0], gitOID)
	}
}

func TestRefLabel_EdgeCases(t *testing.T) {
	tests := []struct {
		ref  string
		want string
	}{
		{"no-label", ""},
		{"repo#", ""},
		{"repo#label", "label"},
	}
	for _, tt := range tests {
		got := RefLabel(tt.ref)
		if got != tt.want {
			t.Errorf("RefLabel(%q) = %q, want %q", tt.ref, got, tt.want)
		}
	}
}

func TestRefVersion_EdgeCases(t *testing.T) {
	tests := []struct {
		ref  string
		want string
	}{
		{"no-version", ""},
		{"repo@", ""},
		{"repo@v1.0", "v1.0"},
	}
	for _, tt := range tests {
		got := RefVersion(tt.ref)
		if got != tt.want {
			t.Errorf("RefVersion(%q) = %q, want %q", tt.ref, got, tt.want)
		}
	}
}
