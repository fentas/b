package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFormatSize(t *testing.T) {
	tests := []struct {
		bytes int64
		want  string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
		{1610612736, "1.5 GB"},
	}

	for _, tt := range tests {
		got := formatSize(tt.bytes)
		if got != tt.want {
			t.Errorf("formatSize(%d) = %q, want %q", tt.bytes, got, tt.want)
		}
	}
}

func TestDirSize(t *testing.T) {
	tmpDir := t.TempDir()

	// Create some files
	if err := os.WriteFile(filepath.Join(tmpDir, "a.txt"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	subDir := filepath.Join(tmpDir, "sub")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "b.txt"), []byte("world!"), 0644); err != nil {
		t.Fatal(err)
	}

	size, err := dirSize(tmpDir)
	if err != nil {
		t.Fatalf("dirSize() error = %v", err)
	}
	// "hello" (5) + "world!" (6) = 11
	if size != 11 {
		t.Errorf("dirSize() = %d, want 11", size)
	}
}

func TestDirSize_NotExist(t *testing.T) {
	_, err := dirSize("/nonexistent/path/abc123")
	if err == nil {
		t.Error("dirSize() on nonexistent path should return error")
	}
}
