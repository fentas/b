package cli

import (
	"testing"

	"github.com/fentas/b/pkg/binary"
	"github.com/fentas/b/pkg/provider"
	"github.com/fentas/goodies/streams"
)

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

func TestIsReleaseProvider(t *testing.T) {
	// GitHub is a release provider
	gh := &provider.GitHub{}
	if !provider.IsReleaseProvider(gh) {
		t.Error("GitHub should be a release provider")
	}
}

// discardWriter implements io.Writer and discards all output.
type discardWriter struct{}

func (d *discardWriter) Write(p []byte) (int, error) {
	return len(p), nil
}
