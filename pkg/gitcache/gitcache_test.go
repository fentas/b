package gitcache

import "testing"

func TestCacheDir(t *testing.T) {
	root := "/tmp/cache"
	dir := CacheDir(root, "github.com/org/repo")
	if dir == "" {
		t.Fatal("CacheDir returned empty string")
	}
	// Same ref should produce same dir
	dir2 := CacheDir(root, "github.com/org/repo")
	if dir != dir2 {
		t.Errorf("CacheDir not deterministic: %q != %q", dir, dir2)
	}
	// Different ref should produce different dir
	dir3 := CacheDir(root, "github.com/other/repo")
	if dir == dir3 {
		t.Errorf("CacheDir collision: %q == %q", dir, dir3)
	}
}

func TestGitURL(t *testing.T) {
	tests := []struct {
		ref  string
		want string
	}{
		{"github.com/org/repo", "https://github.com/org/repo.git"},
		{"github.com/org/repo@v2.0", "https://github.com/org/repo.git"},
		{"github.com/org/repo#monitoring", "https://github.com/org/repo.git"},
		{"github.com/org/repo#label@v1.0", "https://github.com/org/repo.git"},
		{"gitlab.com/group/project", "https://gitlab.com/group/project.git"},
	}

	for _, tt := range tests {
		got := GitURL(tt.ref)
		if got != tt.want {
			t.Errorf("GitURL(%q) = %q, want %q", tt.ref, got, tt.want)
		}
	}
}

func TestRefBase(t *testing.T) {
	tests := []struct {
		ref  string
		want string
	}{
		{"github.com/org/repo", "github.com/org/repo"},
		{"github.com/org/repo@v2.0", "github.com/org/repo"},
		{"github.com/org/repo#monitoring", "github.com/org/repo"},
		{"github.com/org/repo#label@v1.0", "github.com/org/repo"},
	}

	for _, tt := range tests {
		got := RefBase(tt.ref)
		if got != tt.want {
			t.Errorf("RefBase(%q) = %q, want %q", tt.ref, got, tt.want)
		}
	}
}

func TestRefLabel(t *testing.T) {
	tests := []struct {
		ref  string
		want string
	}{
		{"github.com/org/repo", ""},
		{"github.com/org/repo#monitoring", "monitoring"},
		{"github.com/org/repo#label@v1.0", "label"},
		{"github.com/org/repo@v2.0", ""},
	}

	for _, tt := range tests {
		got := RefLabel(tt.ref)
		if got != tt.want {
			t.Errorf("RefLabel(%q) = %q, want %q", tt.ref, got, tt.want)
		}
	}
}

func TestRefVersion(t *testing.T) {
	tests := []struct {
		ref  string
		want string
	}{
		{"github.com/org/repo", ""},
		{"github.com/org/repo@v2.0", "v2.0"},
		{"github.com/org/repo#label@v1.0", "v1.0"},
	}

	for _, tt := range tests {
		got := RefVersion(tt.ref)
		if got != tt.want {
			t.Errorf("RefVersion(%q) = %q, want %q", tt.ref, got, tt.want)
		}
	}
}
