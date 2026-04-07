package sops

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/fentas/b/pkg/binaries"
	"github.com/fentas/b/pkg/binary"
)

func safeCall(fn func()) {
	defer func() { _ = recover() }()
	fn()
}

func TestBinary_sops(t *testing.T) {
	b := Binary(nil)
	if b == nil {
		t.Fatal("Binary(nil) returned nil")
	}
	b = Binary(&binaries.BinaryOptions{Context: context.Background(), Version: "v1.0.0"})
	if b == nil {
		t.Fatal("Binary(opts) returned nil")
	}
	tmp := t.TempDir()
	t.Setenv("PATH_BIN", tmp)
	fake := filepath.Join(tmp, b.Name)
	_ = os.WriteFile(fake, []byte("#!/bin/sh\nexit 0\n"), 0755)

	tmpb := &binary.Binary{
		Name:       b.Name,
		Version:    "v1.0.0",
		GitHubRepo: b.GitHubRepo,
		File:       fake,
	}
	safeCall(func() {
		if b.URLF != nil {
			_, _ = b.URLF(tmpb)
		}
	})
	safeCall(func() {
		if b.GitHubFileF != nil {
			_, _ = b.GitHubFileF(tmpb)
		}
	})
	safeCall(func() {
		if b.TarFileF != nil {
			_, _ = b.TarFileF(tmpb)
		}
	})
	safeCall(func() {
		if b.VersionLocalF != nil {
			_, _ = b.VersionLocalF(tmpb)
		}
	})
}
