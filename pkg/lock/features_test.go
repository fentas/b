package lock

import (
	"testing"
)

// --- Feature 4: RemoveEnv ---

func TestRemoveEnv_Found(t *testing.T) {
	lk := &Lock{
		Envs: []EnvEntry{
			{Ref: "github.com/org/infra", Label: ""},
			{Ref: "github.com/org/infra", Label: "monitoring"},
			{Ref: "github.com/org/other", Label: ""},
		},
	}

	if !lk.RemoveEnv("github.com/org/infra", "monitoring") {
		t.Error("RemoveEnv should return true when found")
	}
	if len(lk.Envs) != 2 {
		t.Fatalf("expected 2 envs after remove, got %d", len(lk.Envs))
	}
	if lk.FindEnv("github.com/org/infra", "monitoring") != nil {
		t.Error("removed env should not be findable")
	}
}

func TestRemoveEnv_NotFound(t *testing.T) {
	lk := &Lock{
		Envs: []EnvEntry{
			{Ref: "github.com/org/infra", Label: ""},
		},
	}

	if lk.RemoveEnv("github.com/org/missing", "") {
		t.Error("RemoveEnv should return false when not found")
	}
	if len(lk.Envs) != 1 {
		t.Errorf("expected 1 env unchanged, got %d", len(lk.Envs))
	}
}

func TestRemoveEnv_Empty(t *testing.T) {
	lk := &Lock{}
	if lk.RemoveEnv("any", "") {
		t.Error("RemoveEnv on empty lock should return false")
	}
}

func TestRemoveEnv_ByLabel(t *testing.T) {
	lk := &Lock{
		Envs: []EnvEntry{
			{Ref: "github.com/org/infra", Label: "base"},
			{Ref: "github.com/org/infra", Label: "monitoring"},
		},
	}

	lk.RemoveEnv("github.com/org/infra", "base")
	if len(lk.Envs) != 1 {
		t.Fatalf("expected 1 env, got %d", len(lk.Envs))
	}
	if lk.Envs[0].Label != "monitoring" {
		t.Errorf("remaining env label = %q, want %q", lk.Envs[0].Label, "monitoring")
	}
}

func TestRemoveEnv_PersistsAfterWrite(t *testing.T) {
	dir := t.TempDir()
	lk := &Lock{
		Envs: []EnvEntry{
			{Ref: "github.com/org/a", Commit: "aaa"},
			{Ref: "github.com/org/b", Commit: "bbb"},
		},
	}

	lk.RemoveEnv("github.com/org/a", "")
	if err := WriteLock(dir, lk, "v1.0.0"); err != nil {
		t.Fatal(err)
	}

	lk2, err := ReadLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(lk2.Envs) != 1 {
		t.Fatalf("expected 1 env after read, got %d", len(lk2.Envs))
	}
	if lk2.Envs[0].Ref != "github.com/org/b" {
		t.Errorf("remaining ref = %q, want github.com/org/b", lk2.Envs[0].Ref)
	}
}
