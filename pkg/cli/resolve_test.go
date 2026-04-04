package cli

import (
	"fmt"
	"runtime"
	"strings"
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

func TestResolveAmbiguousAssets_SkipsAlreadyResolved(t *testing.T) {
	existing := &provider.Asset{Name: "already-set", URL: "http://example.com"}
	b := &binary.Binary{
		Name:          "test",
		AutoDetect:    true,
		ProviderRef:   "nonexistent.invalid/org/repo",
		ResolvedAsset: existing,
	}
	out := &streams.IO{Out: &discardWriter{}, ErrOut: &discardWriter{}}
	resolveAmbiguousAssets([]*binary.Binary{b}, true, out)

	// Already resolved — should be skipped entirely
	if b.ResolvedAsset != existing {
		t.Error("already-resolved binary should be skipped")
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

// --- Core resolution path tests with fake provider ---

// fakeProvider is a test provider that returns deterministic releases.
type fakeProvider struct {
	assets []provider.Asset
}

func (f *fakeProvider) Name() string                                       { return "fake" }
func (f *fakeProvider) Match(ref string) bool                              { return strings.HasPrefix(ref, "fake://") }
func (f *fakeProvider) LatestVersion(ref string) (string, error)           { return "v1.0.0", nil }
func (f *fakeProvider) FetchRelease(ref, version string) (*provider.Release, error) {
	return &provider.Release{Version: version, Assets: f.assets}, nil
}

func init() {
	// Register fake provider for tests — uses "fake://" prefix so no collision.
	// Asset name uses runtime OS/arch for platform-independent matching.
	assetName := fmt.Sprintf("tool-%s-%s", runtime.GOOS, runtime.GOARCH)
	provider.Register(&fakeProvider{
		assets: []provider.Asset{
			{Name: assetName, URL: "https://example.com/" + assetName, Size: 1024},
		},
	})
}

func TestResolveAmbiguousAssets_NonAmbiguous_SetsResolvedAsset(t *testing.T) {
	b := &binary.Binary{
		Name:        "tool",
		AutoDetect:  true,
		ProviderRef: "fake://org/tool",
		Version:     "v1.0.0",
	}
	out := &streams.IO{Out: &discardWriter{}, ErrOut: &discardWriter{}}
	resolveAmbiguousAssets([]*binary.Binary{b}, true, out)

	if b.ResolvedAsset == nil {
		t.Fatal("expected ResolvedAsset to be set for non-ambiguous match")
	}
	wantName := fmt.Sprintf("tool-%s-%s", runtime.GOOS, runtime.GOARCH)
	if b.ResolvedAsset.Name != wantName {
		t.Errorf("ResolvedAsset.Name = %q, want %q", b.ResolvedAsset.Name, wantName)
	}
}

// fakeEmptyProvider returns a release with no assets.
type fakeEmptyProvider struct{}

func (f *fakeEmptyProvider) Name() string                                       { return "fakeempty" }
func (f *fakeEmptyProvider) Match(ref string) bool                              { return strings.HasPrefix(ref, "fakeempty://") }
func (f *fakeEmptyProvider) LatestVersion(ref string) (string, error)           { return "v1.0.0", nil }
func (f *fakeEmptyProvider) FetchRelease(ref, version string) (*provider.Release, error) {
	return &provider.Release{Version: version, Assets: []provider.Asset{}}, nil
}

func init() {
	provider.Register(&fakeEmptyProvider{})
}

func TestResolveAmbiguousAssets_NoMatchingAssets_NilResolvedAsset(t *testing.T) {
	b := &binary.Binary{
		Name:        "tool",
		AutoDetect:  true,
		ProviderRef: "fakeempty://org/tool",
		Version:     "v1.0.0",
	}
	out := &streams.IO{Out: &discardWriter{}, ErrOut: &discardWriter{}}
	resolveAmbiguousAssets([]*binary.Binary{b}, true, out)

	if b.ResolvedAsset != nil {
		t.Errorf("expected nil ResolvedAsset when no assets match, got %q", b.ResolvedAsset.Name)
	}
}

// discardWriter implements io.Writer and discards all output.
type discardWriter struct{}

func (d *discardWriter) Write(p []byte) (int, error) {
	return len(p), nil
}
