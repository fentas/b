package cli

import (
	"fmt"
	"os"
	"path/filepath"
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

func (f *fakeProvider) Name() string                             { return "fake" }
func (f *fakeProvider) Match(ref string) bool                    { return strings.HasPrefix(ref, "fake://") }
func (f *fakeProvider) LatestVersion(ref string) (string, error) { return "v1.0.0", nil }
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

func (f *fakeEmptyProvider) Name() string                             { return "fakeempty" }
func (f *fakeEmptyProvider) Match(ref string) bool                    { return strings.HasPrefix(ref, "fakeempty://") }
func (f *fakeEmptyProvider) LatestVersion(ref string) (string, error) { return "v1.0.0", nil }
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

// fakeTiedProvider returns a release with two assets that tie on score.
type fakeTiedProvider struct{}

func (f *fakeTiedProvider) Name() string                             { return "faketied" }
func (f *fakeTiedProvider) Match(ref string) bool                    { return strings.HasPrefix(ref, "faketied://") }
func (f *fakeTiedProvider) LatestVersion(ref string) (string, error) { return "v1.0.0", nil }
func (f *fakeTiedProvider) FetchRelease(ref, version string) (*provider.Release, error) {
	// Two assets with identical OS/arch naming — they will tie in scoring
	name1 := fmt.Sprintf("tool-%s-%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	name2 := fmt.Sprintf("tool-%s-%s.zip", runtime.GOOS, runtime.GOARCH)
	return &provider.Release{
		Version: version,
		Assets: []provider.Asset{
			{Name: name1, URL: "https://example.com/" + name1, Size: 1024},
			{Name: name2, URL: "https://example.com/" + name2, Size: 2048},
		},
	}, nil
}

func init() {
	provider.Register(&fakeTiedProvider{})
}

func TestResolveAmbiguousAssets_TiedScore_QuietPicksFirst(t *testing.T) {
	b := &binary.Binary{
		Name:        "tool",
		AutoDetect:  true,
		ProviderRef: "faketied://org/tool",
		Version:     "v1.0.0",
	}
	out := &streams.IO{Out: &discardWriter{}, ErrOut: &discardWriter{}}
	// quiet=true → auto-picks first (highest score) without prompting
	resolveAmbiguousAssets([]*binary.Binary{b}, true, out)

	if b.ResolvedAsset == nil {
		t.Fatal("expected ResolvedAsset to be set for tied score (quiet auto-pick)")
	}
	// Should pick one of the two tied assets
	wantPrefix := fmt.Sprintf("tool-%s-%s", runtime.GOOS, runtime.GOARCH)
	if !strings.HasPrefix(b.ResolvedAsset.Name, wantPrefix) {
		t.Errorf("ResolvedAsset.Name = %q, want prefix %q", b.ResolvedAsset.Name, wantPrefix)
	}
	// Quiet/non-TTY auto-picks must NOT persist — user never saw the choice.
	if b.AssetFilter != "" {
		t.Errorf("quiet auto-pick should not set AssetFilter, got %q", b.AssetFilter)
	}
}

// fakeTrueTieProvider returns two assets that actually tie on score: both
// have the same OS/arch, neither is an archive, neither contains the repo
// name — both score 10.
type fakeTrueTieProvider struct{}

func (f *fakeTrueTieProvider) Name() string { return "faketruetie" }
func (f *fakeTrueTieProvider) Match(ref string) bool {
	return strings.HasPrefix(ref, "faketruetie://")
}
func (f *fakeTrueTieProvider) LatestVersion(ref string) (string, error) { return "v1.0.0", nil }
func (f *fakeTrueTieProvider) FetchRelease(ref, version string) (*provider.Release, error) {
	name1 := fmt.Sprintf("pick-me-%s-%s", runtime.GOOS, runtime.GOARCH)
	name2 := fmt.Sprintf("or-me-%s-%s", runtime.GOOS, runtime.GOARCH)
	return &provider.Release{
		Version: version,
		Assets: []provider.Asset{
			{Name: name1, URL: "https://example.com/" + name1, Size: 1024},
			{Name: name2, URL: "https://example.com/" + name2, Size: 2048},
		},
	}, nil
}

func init() {
	provider.Register(&fakeTrueTieProvider{})
}

// TestResolveAmbiguousAssets_QuietAutoPick_DoesNotPersist ensures that a
// quiet/non-TTY auto-pick of a genuinely tied ambiguity does NOT set
// AssetFilter — the user never saw the choice so we won't pin it.
func TestResolveAmbiguousAssets_QuietAutoPick_DoesNotPersist(t *testing.T) {
	b := &binary.Binary{
		Name:        "tool",
		AutoDetect:  true,
		ProviderRef: "faketruetie://org/unique",
		Version:     "v1.0.0",
	}
	out := &streams.IO{Out: &discardWriter{}, ErrOut: &discardWriter{}}
	resolveAmbiguousAssets([]*binary.Binary{b}, true, out) // quiet=true

	if b.ResolvedAsset == nil {
		t.Fatal("expected ResolvedAsset to be set (auto-pick)")
	}
	if b.AssetFilter != "" {
		t.Errorf("quiet auto-pick of tied candidates must not persist, got %q", b.AssetFilter)
	}
}

// TestResolveAmbiguousAssets_Interactive_PersistsChoice verifies that when
// the interactive picker is actually used (TTY + not quiet + user picks),
// the chosen asset name is stored on AssetFilter so 'b install --add' can
// persist it to b.yaml and 'b update' keeps using the same asset.
func TestResolveAmbiguousAssets_Interactive_PersistsChoice(t *testing.T) {
	// Force the TTY check to true and stdin to a known choice.
	origTTY := isTTYFunc
	isTTYFunc = func() bool { return true }
	defer func() { isTTYFunc = origTTY }()

	origStdin := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdin = r
	defer func() { os.Stdin = origStdin; r.Close() }()
	// Pick choice "1" (first listed).
	go func() {
		fmt.Fprintln(w, "1")
		w.Close()
	}()

	b := &binary.Binary{
		// Name must not appear in assets so scoring doesn't award repo-name bonus
		// asymmetrically.
		Name:        "tool",
		AutoDetect:  true,
		ProviderRef: "faketruetie://org/unique",
		Version:     "v1.0.0",
	}
	out := &streams.IO{Out: &discardWriter{}, ErrOut: &discardWriter{}}
	// quiet=false → interactive prompt is used
	resolveAmbiguousAssets([]*binary.Binary{b}, false, out)

	if b.ResolvedAsset == nil {
		t.Fatal("expected ResolvedAsset to be set after interactive pick")
	}
	if b.AssetFilter == "" {
		t.Error("interactive pick should persist to AssetFilter for 'b install --add'")
	}
	// AssetFilter may have glob metachars escaped; the correct invariant is
	// that it matches the chosen asset's filename literally.
	if m, err := filepath.Match(b.AssetFilter, b.ResolvedAsset.Name); err != nil || !m {
		t.Errorf("AssetFilter=%q must match ResolvedAsset.Name=%q (matched=%v err=%v)",
			b.AssetFilter, b.ResolvedAsset.Name, m, err)
	}
}

// TestResolveAmbiguousAssets_Interactive_EOFDoesNotPersist verifies that
// when the user closes stdin without picking (EOF), the default pick is
// used for this run but NOT persisted to AssetFilter.
func TestResolveAmbiguousAssets_Interactive_EOFDoesNotPersist(t *testing.T) {
	origTTY := isTTYFunc
	isTTYFunc = func() bool { return true }
	defer func() { isTTYFunc = origTTY }()

	origStdin := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdin = r
	defer func() { os.Stdin = origStdin; r.Close() }()
	w.Close() // EOF immediately — no input

	b := &binary.Binary{
		Name:        "tool",
		AutoDetect:  true,
		ProviderRef: "faketruetie://org/unique",
		Version:     "v1.0.0",
	}
	out := &streams.IO{Out: &discardWriter{}, ErrOut: &discardWriter{}}
	resolveAmbiguousAssets([]*binary.Binary{b}, false, out)

	if b.ResolvedAsset == nil {
		t.Fatal("expected ResolvedAsset to be set to default on EOF")
	}
	if b.AssetFilter != "" {
		t.Errorf("EOF must not persist: AssetFilter = %q", b.AssetFilter)
	}
}

// TestResolveAmbiguousAssets_Interactive_InvalidChoiceDoesNotPersist covers
// the case where the user types a non-numeric or out-of-range value — the
// default is used but not persisted.
func TestResolveAmbiguousAssets_Interactive_InvalidChoiceDoesNotPersist(t *testing.T) {
	origTTY := isTTYFunc
	isTTYFunc = func() bool { return true }
	defer func() { isTTYFunc = origTTY }()

	origStdin := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdin = r
	defer func() { os.Stdin = origStdin; r.Close() }()
	go func() {
		fmt.Fprintln(w, "nope")
		w.Close()
	}()

	b := &binary.Binary{
		Name:        "tool",
		AutoDetect:  true,
		ProviderRef: "faketruetie://org/unique",
		Version:     "v1.0.0",
	}
	out := &streams.IO{Out: &discardWriter{}, ErrOut: &discardWriter{}}
	resolveAmbiguousAssets([]*binary.Binary{b}, false, out)

	if b.ResolvedAsset == nil {
		t.Fatal("expected ResolvedAsset to be set to default on invalid input")
	}
	if b.AssetFilter != "" {
		t.Errorf("invalid input must not persist: AssetFilter = %q", b.AssetFilter)
	}
}

// TestEscapeAssetGlob verifies that glob metacharacters in asset filenames
// are escaped so filepath.Match treats the persisted AssetFilter as a
// literal name on subsequent runs.
func TestEscapeAssetGlob(t *testing.T) {
	tests := []string{
		"argsh",          // no metachars
		"argsh-so-linux", // dashes fine
		"foo*bar.tar.gz", // star
		"what?.zip",      // question mark
		"a[tag]b",        // brackets
		"mix*of?[all]",   // mix
	}
	for _, in := range tests {
		pattern := escapeAssetGlob(in)
		matched, err := filepath.Match(pattern, in)
		if err != nil {
			t.Errorf("filepath.Match(%q, %q) error: %v", pattern, in, err)
		}
		if !matched {
			t.Errorf("escaped pattern %q must match its original %q", pattern, in)
		}
		// And it must not match an altered name (sanity check).
		if in != "x" {
			if m, _ := filepath.Match(pattern, "x"+in); m {
				t.Errorf("pattern %q unexpectedly matched %q", pattern, "x"+in)
			}
		}
	}
}

// discardWriter implements io.Writer and discards all output.
type discardWriter struct{}

func (d *discardWriter) Write(p []byte) (int, error) {
	return len(p), nil
}
