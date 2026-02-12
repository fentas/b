package env

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fentas/b/pkg/lock"
)

func TestWriteFile_CreatesDirectories(t *testing.T) {
	tmpDir := t.TempDir()
	destPath := filepath.Join(tmpDir, "a", "b", "c", "file.txt")

	if err := writeFile(destPath, []byte("content")); err != nil {
		t.Fatalf("writeFile() error = %v", err)
	}

	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(got) != "content" {
		t.Errorf("file content = %q, want %q", got, "content")
	}
}

func TestWriteFile_OverwritesExisting(t *testing.T) {
	tmpDir := t.TempDir()
	destPath := filepath.Join(tmpDir, "file.txt")

	os.WriteFile(destPath, []byte("old"), 0644)

	if err := writeFile(destPath, []byte("new")); err != nil {
		t.Fatalf("writeFile() error = %v", err)
	}

	got, _ := os.ReadFile(destPath)
	if string(got) != "new" {
		t.Errorf("file content = %q, want %q", got, "new")
	}
}

func TestFindLockFile_EmptyFiles(t *testing.T) {
	entry := &lock.EnvEntry{
		Files: []lock.LockFile{},
	}
	if f := findLockFile(entry, "any"); f != nil {
		t.Errorf("expected nil for empty files, got %v", f)
	}
}

func TestSyncMessage_EmptyFiles(t *testing.T) {
	got := syncMessage(nil, 0)
	if got != "0 file(s) synced" {
		t.Errorf("syncMessage(nil) = %q", got)
	}
}

func TestSyncMessage_AllMerged(t *testing.T) {
	files := []lock.LockFile{
		{Status: "merged"},
		{Status: "merged"},
	}
	got := syncMessage(files, 0)
	if got != "2 merged" {
		t.Errorf("syncMessage() = %q, want %q", got, "2 merged")
	}
}

func TestSyncMessage_ReplacedWithLocalChanges(t *testing.T) {
	files := []lock.LockFile{
		{Status: "replaced (local changes overwritten)"},
		{Status: "replaced"},
	}
	got := syncMessage(files, 0)
	if got != "2 file(s) synced" {
		t.Errorf("syncMessage() = %q, want %q", got, "2 file(s) synced")
	}
}

func TestStrategyConstantValues(t *testing.T) {
	strategies := map[string]string{
		"replace": StrategyReplace,
		"client":  StrategyClient,
		"merge":   StrategyMerge,
	}
	for expected, actual := range strategies {
		if actual != expected {
			t.Errorf("Strategy constant %q != %q", actual, expected)
		}
	}
}

func TestFindLockFile_Nil(t *testing.T) {
	if f := findLockFile(nil, "any"); f != nil {
		t.Errorf("expected nil for nil entry, got %v", f)
	}
}

func TestFindLockFile_Found(t *testing.T) {
	entry := &lock.EnvEntry{
		Files: []lock.LockFile{
			{Path: "a.yaml", Dest: "a.yaml", SHA256: "aaa"},
			{Path: "b.yaml", Dest: "b.yaml", SHA256: "bbb"},
		},
	}
	f := findLockFile(entry, "b.yaml")
	if f == nil {
		t.Fatal("expected to find b.yaml")
	}
	if f.SHA256 != "bbb" {
		t.Errorf("SHA256 = %q, want %q", f.SHA256, "bbb")
	}
}

func TestFindLockFile_NotFound(t *testing.T) {
	entry := &lock.EnvEntry{
		Files: []lock.LockFile{
			{Path: "a.yaml", Dest: "a.yaml"},
		},
	}
	if f := findLockFile(entry, "nonexistent"); f != nil {
		t.Errorf("expected nil, got %v", f)
	}
}

func TestSyncMessage_MixedStatuses(t *testing.T) {
	files := []lock.LockFile{
		{Status: "replaced"},
		{Status: "kept"},
		{Status: "merged"},
	}
	got := syncMessage(files, 0)
	if got == "" {
		t.Error("expected non-empty message")
	}
}

func TestSyncMessage_WithConflicts(t *testing.T) {
	files := []lock.LockFile{
		{Status: "replaced"},
		{Status: "conflict"},
	}
	got := syncMessage(files, 1)
	if got == "" {
		t.Error("expected non-empty message")
	}
}

func TestSyncMessage_AllKept(t *testing.T) {
	files := []lock.LockFile{
		{Status: "kept"},
		{Status: "kept"},
	}
	got := syncMessage(files, 0)
	if got != "2 kept" {
		t.Errorf("syncMessage() = %q, want %q", got, "2 kept")
	}
}
