package lock

import (
	"os"
	"path/filepath"
	"strings"
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

// TestLock_DigestRoundTrips covers the 'digest' field added for
// docker:// / oci:// binaries — it must round-trip through write+read
// so 'b update' can compare the stored digest against a freshly
// resolved one, and must be omitted from JSON when empty so older
// entries stay small.
func TestLock_DigestRoundTrips(t *testing.T) {
	dir := t.TempDir()

	lk := &Lock{
		Binaries: []BinEntry{
			{
				Name:     "docker",
				Version:  "cli",
				SHA256:   "sha-of-binary",
				Source:   "oci://docker:/usr/local/bin/docker",
				Provider: "oci",
				Digest:   "sha256:deadbeefcafebabe",
			},
			// Legacy-shape entry without a digest (e.g. github provider).
			{Name: "fzf", Version: "v0.61.1", SHA256: "abc", Source: "github.com/junegunn/fzf", Provider: "github"},
		},
	}
	if err := WriteLock(dir, lk, "v5.0.0"); err != nil {
		t.Fatalf("WriteLock: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, lockFileName))
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if !strings.Contains(s, `"digest": "sha256:deadbeefcafebabe"`) {
		t.Errorf("expected digest to be written, got:\n%s", s)
	}
	// Non-digest entries must NOT carry an empty "digest" field.
	if strings.Contains(s, `"digest": ""`) {
		t.Errorf("empty digest should be omitted via omitempty, got:\n%s", s)
	}

	lk2, err := ReadLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	docker := lk2.FindBinary("docker")
	if docker == nil {
		t.Fatal("docker binary missing from lock")
	}
	if docker.Digest != "sha256:deadbeefcafebabe" {
		t.Errorf("digest = %q, want %q", docker.Digest, "sha256:deadbeefcafebabe")
	}
	fzf := lk2.FindBinary("fzf")
	if fzf == nil || fzf.Digest != "" {
		t.Errorf("fzf should round-trip with empty digest, got %+v", fzf)
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

func TestFindEnv(t *testing.T) {
	lk := &Lock{
		Envs: []EnvEntry{
			{Ref: "github.com/org/infra", Label: ""},
			{Ref: "github.com/org/infra", Label: "monitoring"},
		},
	}

	if e := lk.FindEnv("github.com/org/infra", ""); e == nil {
		t.Error("expected to find org/infra (no label)")
	}
	if e := lk.FindEnv("github.com/org/infra", "monitoring"); e == nil {
		t.Error("expected to find org/infra#monitoring")
	}
	if e := lk.FindEnv("github.com/org/other", ""); e != nil {
		t.Error("expected nil for missing env")
	}
}

func TestUpsertEnv(t *testing.T) {
	lk := &Lock{
		Envs: []EnvEntry{
			{Ref: "github.com/org/infra", Commit: "old"},
		},
	}

	// Update existing
	lk.UpsertEnv(EnvEntry{Ref: "github.com/org/infra", Commit: "new"})
	if len(lk.Envs) != 1 {
		t.Fatalf("expected 1 env after upsert, got %d", len(lk.Envs))
	}
	if lk.Envs[0].Commit != "new" {
		t.Errorf("commit = %q, want %q", lk.Envs[0].Commit, "new")
	}

	// Add new
	lk.UpsertEnv(EnvEntry{Ref: "github.com/org/other", Commit: "abc"})
	if len(lk.Envs) != 2 {
		t.Fatalf("expected 2 envs, got %d", len(lk.Envs))
	}
}

func TestReadWriteLockWithEnvs(t *testing.T) {
	dir := t.TempDir()

	lk := &Lock{
		Envs: []EnvEntry{
			{
				Ref:     "github.com/org/infra",
				Version: "v2.1.0",
				Commit:  "abc123",
				Files: []LockFile{
					{Path: "manifests/deploy.yaml", Dest: "/hetzner/deploy.yaml", SHA256: "sha1", Mode: "644"},
				},
			},
		},
	}
	if err := WriteLock(dir, lk, "v5.0.0"); err != nil {
		t.Fatalf("WriteLock: %v", err)
	}

	lk2, err := ReadLock(dir)
	if err != nil {
		t.Fatalf("ReadLock: %v", err)
	}
	if len(lk2.Envs) != 1 {
		t.Fatalf("got %d envs, want 1", len(lk2.Envs))
	}
	if lk2.Envs[0].Commit != "abc123" {
		t.Errorf("commit = %q, want %q", lk2.Envs[0].Commit, "abc123")
	}
	if len(lk2.Envs[0].Files) != 1 {
		t.Fatalf("got %d files, want 1", len(lk2.Envs[0].Files))
	}
	if lk2.Envs[0].Files[0].Dest != "/hetzner/deploy.yaml" {
		t.Errorf("dest = %q, want %q", lk2.Envs[0].Files[0].Dest, "/hetzner/deploy.yaml")
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
