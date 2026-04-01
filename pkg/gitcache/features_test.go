package gitcache

import (
	"testing"
)

// --- Feature 6: TreeEntry type ---

func TestTreeEntry_Struct(t *testing.T) {
	entry := TreeEntry{
		Path: "manifests/deploy.yaml",
		Mode: "100644",
	}
	if entry.Path != "manifests/deploy.yaml" {
		t.Errorf("Path = %q", entry.Path)
	}
	if entry.Mode != "100644" {
		t.Errorf("Mode = %q", entry.Mode)
	}
}

func TestTreeEntry_ExecutableMode(t *testing.T) {
	entry := TreeEntry{
		Path: "scripts/deploy.sh",
		Mode: "100755",
	}
	if entry.Mode != "100755" {
		t.Errorf("Mode = %q, want 100755", entry.Mode)
	}
}
