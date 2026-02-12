package gitcache

import (
	"strings"
	"testing"
)

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

func TestMerge3Way_CleanMerge(t *testing.T) {
	// Changes must be far enough apart for git merge-file to not treat them as overlapping.
	// git merge-file uses a 3-line context, so changes need 3+ unchanged lines between them.
	base := []byte("line one\nline two\nline three\nline four\nline five\nline six\nline seven\n")
	local := []byte("LOCAL CHANGE\nline two\nline three\nline four\nline five\nline six\nline seven\n")
	upstream := []byte("line one\nline two\nline three\nline four\nline five\nline six\nUPSTREAM CHANGE\n")

	result, hasConflict, err := Merge3Way(local, base, upstream)
	if err != nil {
		t.Fatalf("Merge3Way() error = %v", err)
	}
	if hasConflict {
		t.Errorf("Merge3Way() hasConflict = true, want false\nresult:\n%s", result)
	}

	got := string(result)
	if !strings.Contains(got, "LOCAL CHANGE") {
		t.Errorf("merge result missing local change: %s", got)
	}
	if !strings.Contains(got, "UPSTREAM CHANGE") {
		t.Errorf("merge result missing upstream change: %s", got)
	}
}

func TestMerge3Way_Conflict(t *testing.T) {
	// Both modify the same line
	base := []byte("original line\n")
	local := []byte("local version\n")
	upstream := []byte("upstream version\n")

	result, hasConflict, err := Merge3Way(local, base, upstream)
	if err != nil {
		t.Fatalf("Merge3Way() error = %v", err)
	}
	if !hasConflict {
		t.Error("Merge3Way() hasConflict = false, want true")
	}

	// Should have conflict markers
	got := string(result)
	if !strings.Contains(got, "<<<<<<<") || !strings.Contains(got, ">>>>>>>") {
		t.Errorf("conflict result missing markers: %s", got)
	}
}

func TestMerge3Way_NoChanges(t *testing.T) {
	// All three are the same
	content := []byte("same content\n")

	result, hasConflict, err := Merge3Way(content, content, content)
	if err != nil {
		t.Fatalf("Merge3Way() error = %v", err)
	}
	if hasConflict {
		t.Error("Merge3Way() hasConflict = true, want false")
	}
	if string(result) != "same content\n" {
		t.Errorf("result = %q, want %q", string(result), "same content\n")
	}
}

func TestDiffNoIndex(t *testing.T) {
	a := []byte("hello\nworld\n")
	b := []byte("hello\nplanet\n")

	diff, err := DiffNoIndex(a, b, "a", "b")
	if err != nil {
		t.Fatalf("DiffNoIndex() error = %v", err)
	}

	if !strings.Contains(diff, "world") || !strings.Contains(diff, "planet") {
		t.Errorf("diff should show changes:\n%s", diff)
	}
}

func TestDiffNoIndex_Identical(t *testing.T) {
	content := []byte("same\n")

	diff, err := DiffNoIndex(content, content, "a", "b")
	if err != nil {
		t.Fatalf("DiffNoIndex() error = %v", err)
	}

	if diff != "" {
		t.Errorf("expected empty diff for identical content, got: %s", diff)
	}
}
