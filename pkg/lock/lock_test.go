package lock

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadWriteLock(t *testing.T) {
	dir := t.TempDir()

	// Write
	lk := &Lock{
		Binaries: []BinEntry{
			{Name: "fzf", Version: "v0.61.1", SHA256: "abc123", Source: "github.com/junegunn/fzf", Provider: "github"},
		},
	}
	if err := WriteLock(dir, lk, "v5.0.0"); err != nil {
		t.Fatalf("WriteLock: %v", err)
	}

	// Read
	lk2, err := ReadLock(dir)
	if err != nil {
		t.Fatalf("ReadLock: %v", err)
	}
	if lk2.Version != 1 {
		t.Errorf("version = %d, want 1", lk2.Version)
	}
	if len(lk2.Binaries) != 1 {
		t.Fatalf("got %d binaries, want 1", len(lk2.Binaries))
	}
	if lk2.Binaries[0].Name != "fzf" {
		t.Errorf("name = %q, want %q", lk2.Binaries[0].Name, "fzf")
	}
	if lk2.Binaries[0].SHA256 != "abc123" {
		t.Errorf("sha256 = %q, want %q", lk2.Binaries[0].SHA256, "abc123")
	}
}

func TestReadLockMissing(t *testing.T) {
	dir := t.TempDir()
	lk, err := ReadLock(dir)
	if err != nil {
		t.Fatalf("ReadLock on missing file: %v", err)
	}
	if lk.Version != 1 {
		t.Errorf("version = %d, want 1", lk.Version)
	}
	if len(lk.Binaries) != 0 {
		t.Errorf("got %d binaries, want 0", len(lk.Binaries))
	}
}

func TestUpsertBinary(t *testing.T) {
	lk := &Lock{
		Binaries: []BinEntry{
			{Name: "fzf", Version: "v0.61.0", SHA256: "old"},
		},
	}

	// Update existing
	lk.UpsertBinary(BinEntry{Name: "fzf", Version: "v0.61.1", SHA256: "new"})
	if len(lk.Binaries) != 1 {
		t.Fatalf("expected 1 binary after upsert, got %d", len(lk.Binaries))
	}
	if lk.Binaries[0].SHA256 != "new" {
		t.Errorf("sha256 = %q, want %q", lk.Binaries[0].SHA256, "new")
	}

	// Add new
	lk.UpsertBinary(BinEntry{Name: "bat", Version: "v0.24.0", SHA256: "bat123"})
	if len(lk.Binaries) != 2 {
		t.Fatalf("expected 2 binaries, got %d", len(lk.Binaries))
	}
}

func TestFindBinary(t *testing.T) {
	lk := &Lock{
		Binaries: []BinEntry{
			{Name: "fzf"},
			{Name: "bat"},
		},
	}

	if e := lk.FindBinary("fzf"); e == nil {
		t.Error("expected to find fzf")
	}
	if e := lk.FindBinary("missing"); e != nil {
		t.Error("expected nil for missing binary")
	}
}

func TestSHA256File(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test")
	if err := os.WriteFile(path, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	hash, err := SHA256File(path)
	if err != nil {
		t.Fatalf("SHA256File: %v", err)
	}
	// sha256("hello") = 2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824
	want := "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if hash != want {
		t.Errorf("SHA256File = %q, want %q", hash, want)
	}
}
