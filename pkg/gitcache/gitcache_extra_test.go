package gitcache

import (
	"os"
	"path/filepath"
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
