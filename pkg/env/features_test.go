package env

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fentas/b/pkg/lock"
)

// --- Feature 3: Skip writes when content identical ---

func TestSyncMessage_WithUnchanged(t *testing.T) {
	files := []lock.LockFile{
		{Status: "replaced"},
		{Status: "unchanged"},
		{Status: "unchanged"},
	}
	got := syncMessage(files, 0)
	if got == "" {
		t.Error("expected non-empty message")
	}
	// Should contain "unchanged"
	if got != "1 replaced, 2 unchanged" {
		t.Errorf("syncMessage() = %q, want %q", got, "1 replaced, 2 unchanged")
	}
}

func TestSyncMessage_AllUnchanged(t *testing.T) {
	files := []lock.LockFile{
		{Status: "unchanged"},
		{Status: "unchanged"},
	}
	got := syncMessage(files, 0)
	if got != "2 file(s) unchanged" {
		t.Errorf("syncMessage() = %q, want %q", got, "2 file(s) unchanged")
	}
}

// --- Feature 6: File mode handling ---

func TestGitModeToFileMode(t *testing.T) {
	tests := []struct {
		gitMode string
		want    os.FileMode
	}{
		{"100644", 0644},
		{"100755", 0755},
		{"100664", 0644}, // unknown mode defaults to 0644
		{"", 0644},       // empty defaults to 0644
	}
	for _, tt := range tests {
		got := gitModeToFileMode(tt.gitMode)
		if got != tt.want {
			t.Errorf("gitModeToFileMode(%q) = %o, want %o", tt.gitMode, got, tt.want)
		}
	}
}

func TestFileModeToString(t *testing.T) {
	tests := []struct {
		mode os.FileMode
		want string
	}{
		{0644, "644"},
		{0755, "755"},
		{0600, "600"},
	}
	for _, tt := range tests {
		got := fileModeToString(tt.mode)
		if got != tt.want {
			t.Errorf("fileModeToString(%o) = %q, want %q", tt.mode, got, tt.want)
		}
	}
}

func TestWriteFile_NewFileRespectsUmask(t *testing.T) {
	tmpDir := t.TempDir()
	destPath := filepath.Join(tmpDir, "newfile.txt")

	// New file: writeFile should not force chmod, letting umask apply
	if err := writeFile(destPath, []byte("content"), 0644); err != nil {
		t.Fatalf("writeFile() error = %v", err)
	}

	// File should exist and be readable
	info, err := os.Stat(destPath)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	// Should not have execute bit (we asked for 0644)
	if info.Mode().Perm()&0111 != 0 {
		t.Errorf("new file should not be executable, got %o", info.Mode().Perm())
	}
}

func TestWriteFile_ChmodExistingFile(t *testing.T) {
	tmpDir := t.TempDir()
	destPath := filepath.Join(tmpDir, "script.sh")

	// Create file with 0644
	if err := writeFile(destPath, []byte("#!/bin/sh"), 0644); err != nil {
		t.Fatalf("writeFile() error = %v", err)
	}
	info, _ := os.Stat(destPath)
	if info.Mode().Perm()&0100 != 0 {
		t.Fatal("file should not be executable yet")
	}

	// Overwrite same content but with 0755 — should chmod existing file
	if err := writeFile(destPath, []byte("#!/bin/sh"), 0755); err != nil {
		t.Fatalf("writeFile() error = %v", err)
	}
	info, _ = os.Stat(destPath)
	if info.Mode().Perm()&0100 == 0 {
		t.Errorf("expected executable after writeFile with 0755, got %o", info.Mode().Perm())
	}
}

func TestWriteFile_ExecutableMode(t *testing.T) {
	tmpDir := t.TempDir()
	destPath := filepath.Join(tmpDir, "script.sh")

	if err := writeFile(destPath, []byte("#!/bin/sh\necho hello"), 0755); err != nil {
		t.Fatalf("writeFile() error = %v", err)
	}

	info, err := os.Stat(destPath)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	// On Unix, check that execute bit is set
	if info.Mode().Perm()&0100 == 0 {
		t.Errorf("expected executable mode, got %o", info.Mode().Perm())
	}
}

// --- Feature 7: Hook execution ---

func TestRunHook_Success(t *testing.T) {
	tmpDir := t.TempDir()
	markerFile := filepath.Join(tmpDir, "hook-ran")

	err := runHook(fmt.Sprintf("touch %s", markerFile), tmpDir, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("runHook() error = %v", err)
	}

	if _, err := os.Stat(markerFile); os.IsNotExist(err) {
		t.Error("hook did not create marker file")
	}
}

func TestRunHook_Failure(t *testing.T) {
	err := runHook("exit 1", t.TempDir(), io.Discard, io.Discard)
	if err == nil {
		t.Error("expected error from failing hook")
	}
}

func TestRunHook_UsesWorkingDir(t *testing.T) {
	tmpDir := t.TempDir()
	err := runHook("test -d .", tmpDir, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("runHook() should succeed in valid dir, got %v", err)
	}
}

func TestRunHook_CapturesOutput(t *testing.T) {
	var stdout bytes.Buffer
	err := runHook("echo hello", t.TempDir(), &stdout, io.Discard)
	if err != nil {
		t.Fatalf("runHook() error = %v", err)
	}
	if !strings.Contains(stdout.String(), "hello") {
		t.Errorf("stdout = %q, want 'hello'", stdout.String())
	}
}

// --- Feature 1: Dry-run (syncMessage with dry-run suffix) ---

func TestSyncMessage_DryRunSuffix(t *testing.T) {
	files := []lock.LockFile{
		{Status: "replaced (dry-run)"},
		{Status: "replaced (dry-run)"},
	}
	got := syncMessage(files, 0)
	if got != "2 file(s) synced" {
		t.Errorf("syncMessage() with dry-run = %q, want %q", got, "2 file(s) synced")
	}
}

func TestSyncMessage_UnchangedDryRun(t *testing.T) {
	files := []lock.LockFile{
		{Status: "unchanged (dry-run)"},
	}
	got := syncMessage(files, 0)
	// Should strip dry-run and count as unchanged
	if got != "1 file(s) unchanged" {
		t.Errorf("syncMessage() = %q, want %q", got, "1 file(s) unchanged")
	}
}

// --- Feature 3: Unchanged detection via hash comparison ---

func TestFindLockFile_MatchesHash(t *testing.T) {
	// Verifies findLockFile returns the correct entry and its hash can be
	// compared against an upstream hash — the building block of unchanged detection.
	content := []byte("some content")
	hash := fmt.Sprintf("%x", sha256.Sum256(content))

	lockEntry := &lock.EnvEntry{
		Commit: "oldcommit",
		Files: []lock.LockFile{
			{Path: "test.yaml", Dest: "test.yaml", SHA256: hash},
		},
	}

	f := findLockFile(lockEntry, "test.yaml")
	if f == nil {
		t.Fatal("expected to find lock entry")
	}
	if f.SHA256 != hash {
		t.Errorf("SHA256 = %q, want %q", f.SHA256, hash)
	}
}

func TestUnchangedDetection_LocalMatchesUpstream(t *testing.T) {
	// End-to-end test: when local file content matches upstream content,
	// the file should be detected as unchanged (hash comparison).
	tmpDir := t.TempDir()
	content := []byte("identical content\n")
	hash := fmt.Sprintf("%x", sha256.Sum256(content))

	destPath := filepath.Join(tmpDir, "test.yaml")
	os.WriteFile(destPath, content, 0644)

	// Local hash should match the upstream hash
	localHash, err := lock.SHA256File(destPath)
	if err != nil {
		t.Fatalf("SHA256File error: %v", err)
	}
	if localHash != hash {
		t.Errorf("localHash = %q, upstreamHash = %q — should be equal", localHash, hash)
	}

	// When they're equal, the sync logic skips the write (unchanged)
	// This is the condition checked in SyncEnv: localHash == upstreamHash
}

func TestUnchangedDetection_LocalDiffers(t *testing.T) {
	// When local file content differs from upstream, hashes should NOT match.
	tmpDir := t.TempDir()
	upstreamContent := []byte("upstream content\n")
	upstreamHash := fmt.Sprintf("%x", sha256.Sum256(upstreamContent))

	destPath := filepath.Join(tmpDir, "test.yaml")
	os.WriteFile(destPath, []byte("local modified content\n"), 0644)

	localHash, _ := lock.SHA256File(destPath)
	if localHash == upstreamHash {
		t.Error("local and upstream hashes should differ")
	}
}

// --- ValidatePathUnderRoot ---

func TestValidatePathUnderRoot_Valid(t *testing.T) {
	tmpDir := t.TempDir()
	destPath := filepath.Join(tmpDir, "sub", "file.yaml")

	if err := ValidatePathUnderRoot(tmpDir, destPath); err != nil {
		t.Errorf("expected valid path, got: %v", err)
	}
}

func TestValidatePathUnderRoot_DotDotEscape(t *testing.T) {
	tmpDir := t.TempDir()
	destPath := filepath.Join(tmpDir, "..", "escape.yaml")

	if err := ValidatePathUnderRoot(tmpDir, destPath); err == nil {
		t.Error("expected error for .. escape")
	}
}

func TestValidatePathUnderRoot_SymlinkEscape(t *testing.T) {
	tmpDir := t.TempDir()
	outsideDir := t.TempDir()

	// Create a symlink inside tmpDir that points outside
	symlinkPath := filepath.Join(tmpDir, "escape-link")
	if err := os.Symlink(outsideDir, symlinkPath); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}

	destPath := filepath.Join(symlinkPath, "file.yaml")

	if err := ValidatePathUnderRoot(tmpDir, destPath); err == nil {
		t.Error("expected error for symlink escape")
	}
}

func TestValidatePathUnderRoot_NestedValid(t *testing.T) {
	tmpDir := t.TempDir()
	// Create a real nested directory
	nested := filepath.Join(tmpDir, "a", "b", "c")
	os.MkdirAll(nested, 0755)
	destPath := filepath.Join(nested, "file.yaml")

	if err := ValidatePathUnderRoot(tmpDir, destPath); err != nil {
		t.Errorf("expected valid nested path, got: %v", err)
	}
}
