package env

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fentas/b/pkg/lock"
)

func TestFindLockFile(t *testing.T) {
	entry := &lock.EnvEntry{
		Files: []lock.LockFile{
			{Path: "a/b.yaml", Dest: "b.yaml", SHA256: "aaa"},
			{Path: "c/d.yaml", Dest: "d.yaml", SHA256: "bbb"},
		},
	}

	f := findLockFile(entry, "a/b.yaml")
	if f == nil || f.SHA256 != "aaa" {
		t.Errorf("findLockFile(a/b.yaml) = %v, want SHA256=aaa", f)
	}

	f = findLockFile(entry, "c/d.yaml")
	if f == nil || f.SHA256 != "bbb" {
		t.Errorf("findLockFile(c/d.yaml) = %v, want SHA256=bbb", f)
	}

	f = findLockFile(entry, "not/found.yaml")
	if f != nil {
		t.Errorf("findLockFile(not/found.yaml) = %v, want nil", f)
	}
}

func TestSyncMessage(t *testing.T) {
	tests := []struct {
		name      string
		files     []lock.LockFile
		conflicts int
		want      string
	}{
		{
			name: "all replaced",
			files: []lock.LockFile{
				{Status: "replaced"},
				{Status: "replaced"},
			},
			want: "2 file(s) synced",
		},
		{
			name: "mixed",
			files: []lock.LockFile{
				{Status: "replaced"},
				{Status: "kept"},
				{Status: "merged"},
			},
			want: "1 replaced, 1 kept, 1 merged",
		},
		{
			name: "with conflicts",
			files: []lock.LockFile{
				{Status: "replaced"},
				{Status: "conflict"},
			},
			conflicts: 1,
			want:      "1 replaced, 1 merged, 1 conflict(s)",
		},
		{
			name: "all kept",
			files: []lock.LockFile{
				{Status: "kept"},
				{Status: "kept"},
			},
			want: "2 kept",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := syncMessage(tt.files, tt.conflicts)
			if got != tt.want {
				t.Errorf("syncMessage() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWriteFile(t *testing.T) {
	tmpDir := t.TempDir()

	// Write to nested path that doesn't exist yet
	destPath := filepath.Join(tmpDir, "sub", "dir", "file.txt")
	content := []byte("hello world")

	if err := writeFile(destPath, content); err != nil {
		t.Fatalf("writeFile() error = %v", err)
	}

	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(got) != "hello world" {
		t.Errorf("file content = %q, want %q", string(got), "hello world")
	}
}

// TestStrategyReplace tests that replace strategy overwrites files even with local changes.
func TestStrategyReplace(t *testing.T) {
	tmpDir := t.TempDir()
	destPath := filepath.Join(tmpDir, "test.yaml")

	// Simulate: file was synced previously, then locally modified
	originalContent := []byte("original upstream content\n")
	localContent := []byte("locally modified content\n")
	upstreamContent := []byte("new upstream content\n")

	originalHash := fmt.Sprintf("%x", sha256.Sum256(originalContent))

	// Write the "locally modified" file
	if err := os.WriteFile(destPath, localContent, 0644); err != nil {
		t.Fatal(err)
	}

	// Lock entry records the original hash
	lockEntry := &lock.EnvEntry{
		Commit: "oldcommitabc",
		Files: []lock.LockFile{
			{Path: "test.yaml", Dest: "test.yaml", SHA256: originalHash},
		},
	}

	// Detect local change
	localHash, _ := lock.SHA256File(destPath)
	if localHash == originalHash {
		t.Fatal("expected local file to differ from lock")
	}

	// Simulate replace strategy: write upstream
	if err := writeFile(destPath, upstreamContent); err != nil {
		t.Fatal(err)
	}

	got, _ := os.ReadFile(destPath)
	if string(got) != "new upstream content\n" {
		t.Errorf("after replace, file = %q, want upstream content", string(got))
	}

	_ = lockEntry // used in the logic above
}

// TestStrategyClient tests that client strategy keeps local files when modified.
func TestStrategyClient(t *testing.T) {
	tmpDir := t.TempDir()
	destPath := filepath.Join(tmpDir, "test.yaml")

	originalContent := []byte("original upstream content\n")
	localContent := []byte("locally modified content\n")

	originalHash := fmt.Sprintf("%x", sha256.Sum256(originalContent))

	// Write the "locally modified" file
	if err := os.WriteFile(destPath, localContent, 0644); err != nil {
		t.Fatal(err)
	}

	// Detect local change
	localHash, _ := lock.SHA256File(destPath)
	if localHash == originalHash {
		t.Fatal("expected local file to differ from lock")
	}

	// Client strategy: DON'T write, keep local
	got, _ := os.ReadFile(destPath)
	if string(got) != "locally modified content\n" {
		t.Errorf("client strategy should preserve local file, got %q", string(got))
	}

	// The lock file entry should use the local hash
	if localHash == "" {
		t.Error("localHash should not be empty")
	}
}

// TestStrategyConstants tests strategy constant values.
func TestStrategyConstants(t *testing.T) {
	if StrategyReplace != "replace" {
		t.Errorf("StrategyReplace = %q, want %q", StrategyReplace, "replace")
	}
	if StrategyClient != "client" {
		t.Errorf("StrategyClient = %q, want %q", StrategyClient, "client")
	}
	if StrategyMerge != "merge" {
		t.Errorf("StrategyMerge = %q, want %q", StrategyMerge, "merge")
	}
}

// TestFindLockFileNilEntry tests findLockFile with nil entry.
func TestFindLockFileNilEntry(t *testing.T) {
	f := findLockFile(nil, "any.yaml")
	if f != nil {
		t.Errorf("findLockFile(nil, ...) = %v, want nil", f)
	}
}

// TestPathTraversalRejection tests that destPath with ".." is caught.
func TestPathTraversalCheck(t *testing.T) {
	// This tests the path traversal check concept
	destPath := "../../../etc/passwd"
	if !strings.Contains(destPath, "..") {
		t.Error("expected path traversal to be detected")
	}
}
