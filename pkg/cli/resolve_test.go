package cli

import (
	"testing"

	"github.com/fentas/b/pkg/binary"
	"github.com/fentas/b/pkg/provider"
	"github.com/fentas/goodies/streams"
)

// --- resolveAmbiguousAssets ---

func TestResolveAmbiguousAssets_NonAutoDetect_Skipped(t *testing.T) {
	b := &binary.Binary{Name: "jq", AutoDetect: false}
	out := &streams.IO{Out: &discardWriter{}, ErrOut: &discardWriter{}}
	resolveAmbiguousAssets([]*binary.Binary{b}, true, out)

	if b.ResolvedAsset != nil {
		t.Error("non-auto-detect binary should not have ResolvedAsset set")
	}
}

func TestResolveAmbiguousAssets_NoProvider_Skipped(t *testing.T) {
	b := &binary.Binary{
		Name:        "unknown",
		AutoDetect:  true,
		ProviderRef: "nonexistent.invalid/org/repo",
	}
	out := &streams.IO{Out: &discardWriter{}, ErrOut: &discardWriter{}}
	resolveAmbiguousAssets([]*binary.Binary{b}, true, out)

	if b.ResolvedAsset != nil {
		t.Error("unknown provider should not set ResolvedAsset")
	}
}

func TestResolveAmbiguousAssets_EmptyList(t *testing.T) {
	out := &streams.IO{Out: &discardWriter{}, ErrOut: &discardWriter{}}
	// Should not panic on empty list
	resolveAmbiguousAssets(nil, true, out)
	resolveAmbiguousAssets([]*binary.Binary{}, true, out)
}

func TestResolveAmbiguousAssets_PreservedExistingResolvedAsset(t *testing.T) {
	existing := &provider.Asset{Name: "already-set", URL: "http://example.com"}
	b := &binary.Binary{
		Name:          "test",
		AutoDetect:    true,
		ProviderRef:   "nonexistent.invalid/org/repo",
		ResolvedAsset: existing,
	}
	out := &streams.IO{Out: &discardWriter{}, ErrOut: &discardWriter{}}
	resolveAmbiguousAssets([]*binary.Binary{b}, true, out)

	// Should not overwrite — provider detection will fail, but existing should stay
	if b.ResolvedAsset != existing {
		t.Error("existing ResolvedAsset should not be overwritten on provider error")
	}
}

// --- IsReleaseProvider ---

func TestIsReleaseProvider_GitHub(t *testing.T) {
	if !provider.IsReleaseProvider(&provider.GitHub{}) {
		t.Error("GitHub should be a release provider")
	}
}

func TestIsReleaseProvider_GoInstall(t *testing.T) {
	if provider.IsReleaseProvider(&provider.GoInstall{}) {
		t.Error("GoInstall should NOT be a release provider")
	}
}

func TestIsReleaseProvider_Docker(t *testing.T) {
	if provider.IsReleaseProvider(&provider.Docker{}) {
		t.Error("Docker should NOT be a release provider")
	}
}

func TestIsReleaseProvider_Nil(t *testing.T) {
	if provider.IsReleaseProvider(nil) {
		t.Error("nil should NOT be a release provider")
	}
}

func TestIsReleaseProvider_Git(t *testing.T) {
	if provider.IsReleaseProvider(&provider.Git{}) {
		t.Error("Git should NOT be a release provider")
	}
}

// --- firstLine ---

func TestFirstLine_SingleLine(t *testing.T) {
	got := firstLine("hello world")
	if got != "hello world" {
		t.Errorf("firstLine = %q, want %q", got, "hello world")
	}
}

func TestFirstLine_MultiLine(t *testing.T) {
	got := firstLine("first line\nsecond line\nthird")
	if got != "first line" {
		t.Errorf("firstLine = %q, want %q", got, "first line")
	}
}

func TestFirstLine_CRLF(t *testing.T) {
	got := firstLine("first line\r\nsecond line")
	if got != "first line" {
		t.Errorf("firstLine = %q, want %q (should strip \\r)", got, "first line")
	}
}

func TestFirstLine_TrailingCR(t *testing.T) {
	got := firstLine("only line\r")
	if got != "only line" {
		t.Errorf("firstLine = %q, want %q", got, "only line")
	}
}

func TestFirstLine_Empty(t *testing.T) {
	got := firstLine("")
	if got != "" {
		t.Errorf("firstLine = %q, want empty", got)
	}
}

func TestFirstLine_OnlyNewline(t *testing.T) {
	got := firstLine("\n")
	if got != "" {
		t.Errorf("firstLine = %q, want empty", got)
	}
}

func TestFirstLine_GitErrorFormat(t *testing.T) {
	// Realistic git error with multi-line stderr
	got := firstLine("git ls-remote https://github.com/org/repo.git HEAD: exit status 128\nfatal: unable to access")
	if got != "git ls-remote https://github.com/org/repo.git HEAD: exit status 128" {
		t.Errorf("firstLine = %q", got)
	}
}

// discardWriter implements io.Writer and discards all output.
type discardWriter struct{}

func (d *discardWriter) Write(p []byte) (int, error) {
	return len(p), nil
}
