package packer

import (
	"context"
	"testing"

	"github.com/fentas/b/pkg/binaries"
	"github.com/fentas/b/pkg/binary"
)

// safeCall runs fn and reports any panic via t.Errorf so that unexpected
// closure regressions are still surfaced.
func safeCall(t *testing.T, label string, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("closure %q panicked: %v", label, r)
		}
	}()
	fn()
}

func TestBinary_packer(t *testing.T) {
	b := Binary(nil)
	if b == nil {
		t.Fatal("Binary(nil) returned nil")
	}
	b = Binary(&binaries.BinaryOptions{Context: context.Background(), Version: "v1.0.0"})
	if b == nil {
		t.Fatal("Binary(opts) returned nil")
	}

	// Synthetic Binary used to invoke the URL/file callbacks. We deliberately
	// do NOT call VersionLocalF because it executes the real binary and parses
	// its output — that's not testable in unit tests, only via integration.
	tmpb := &binary.Binary{
		Name:       b.Name,
		Version:    "v1.0.0",
		GitHubRepo: b.GitHubRepo,
	}
	if b.URLF != nil {
		safeCall(t, "URLF", func() { _, _ = b.URLF(tmpb) })
	}
	if b.GitHubFileF != nil {
		safeCall(t, "GitHubFileF", func() { _, _ = b.GitHubFileF(tmpb) })
	}
	if b.TarFileF != nil {
		safeCall(t, "TarFileF", func() { _, _ = b.TarFileF(tmpb) })
	}
}
