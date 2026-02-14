package provider

import (
	"runtime"
	"strings"
	"testing"
)

func TestMatchAsset(t *testing.T) {
	assets := []Asset{
		{Name: "tool_Linux_x86_64.tar.gz", URL: "https://example.com/linux.tar.gz", Size: 1000},
		{Name: "tool_Darwin_arm64.tar.gz", URL: "https://example.com/darwin-arm.tar.gz", Size: 1000},
		{Name: "tool_Darwin_x86_64.tar.gz", URL: "https://example.com/darwin-amd.tar.gz", Size: 1000},
		{Name: "tool_Windows_x86_64.zip", URL: "https://example.com/windows.zip", Size: 1000},
		{Name: "checksums.txt", URL: "https://example.com/checksums.txt", Size: 100},
		{Name: "tool_Linux_x86_64.tar.gz.sha256", URL: "https://example.com/sha256", Size: 64},
	}

	expected := map[string]map[string]string{
		"linux":   {"amd64": "tool_Linux_x86_64.tar.gz"},
		"darwin":  {"arm64": "tool_Darwin_arm64.tar.gz", "amd64": "tool_Darwin_x86_64.tar.gz"},
		"windows": {"amd64": "tool_Windows_x86_64.zip"},
	}

	archMap, ok := expected[runtime.GOOS]
	if !ok {
		t.Skipf("no test assets for GOOS=%s", runtime.GOOS)
	}
	want, ok := archMap[runtime.GOARCH]
	if !ok {
		t.Skipf("no test asset for %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	a, err := MatchAsset(assets, "tool", "")
	if err != nil {
		t.Fatalf("MatchAsset() error: %v", err)
	}
	if a.Name != want {
		t.Errorf("MatchAsset() = %q, want %q", a.Name, want)
	}
}

func TestMatchAssetNoMatch(t *testing.T) {
	assets := []Asset{
		{Name: "tool_Darwin_arm64.tar.gz", URL: "https://example.com/d.tar.gz", Size: 1000},
	}
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		t.Skip("test only valid when GOOS/GOARCH != darwin/arm64")
	}
	_, err := MatchAsset(assets, "tool", "")
	if err == nil {
		t.Error("expected no match error")
	}
}

func TestDetectArchiveType(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"foo.tar.gz", "tar.gz"},
		{"foo.tgz", "tar.gz"},
		{"foo.tar.xz", "tar.xz"},
		{"foo.txz", "tar.xz"},
		{"foo.zip", "zip"},
		{"foo.tar.bz2", "tar.bz2"},
		{"foo", ""},
		{"foo.exe", ""},
	}

	for _, tt := range tests {
		got := DetectArchiveType(tt.name)
		if got != tt.want {
			t.Errorf("DetectArchiveType(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestContainsWord(t *testing.T) {
	tests := []struct {
		name, word string
		want       bool
	}{
		{"k9s_linux_amd64.tar.gz", "linux", true},
		{"k9s_linux_amd64.tar.gz", "amd64", true},
		{"charm_linux_amd64", "arm", false},  // "arm" inside "charm" should not match
		{"charm_arm64_linux", "arm64", true}, // "arm" inside "charm" but "arm64" standalone later
		{"tool-arm64-linux", "arm64", true},
		{"tool_x86_64", "x86_64", true},
	}

	for _, tt := range tests {
		got := containsWord(tt.name, tt.word)
		if got != tt.want {
			t.Errorf("containsWord(%q, %q) = %v, want %v", tt.name, tt.word, got, tt.want)
		}
	}
}

// TestMatchAssetWithFilter tests that the asset filter glob narrows results.
func TestMatchAssetWithFilter(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("test requires linux/amd64")
	}

	// Simulate argsh release assets
	assets := []Asset{
		{Name: "argsh", URL: "https://example.com/argsh", Size: 24000},
		{Name: "argsh-linux-amd64.so", URL: "https://example.com/so", Size: 481000},
		{Name: "argsh-linux-arm64.so", URL: "https://example.com/so-arm", Size: 434000},
		{Name: "argsh-so-linux-amd64", URL: "https://example.com/standalone", Size: 667000},
		{Name: "argsh-so-linux-arm64", URL: "https://example.com/standalone-arm", Size: 605000},
		{Name: "minifier-linux-amd64", URL: "https://example.com/minifier", Size: 1810000},
		{Name: "minifier-linux-arm64", URL: "https://example.com/minifier-arm", Size: 1630000},
		{Name: "shdoc-linux-amd64", URL: "https://example.com/shdoc", Size: 1890000},
		{Name: "shdoc-linux-arm64", URL: "https://example.com/shdoc-arm", Size: 1760000},
		{Name: "sha256sum.txt", URL: "https://example.com/sha", Size: 762},
	}

	// Without filter: ambiguous (multiple match with same score)
	candidates := MatchAssets(assets, "argsh", "")
	if len(candidates) < 2 {
		t.Fatalf("expected multiple candidates without filter, got %d", len(candidates))
	}

	// With filter: narrows to argsh-so-*
	a, err := MatchAsset(assets, "argsh", "argsh-so-*")
	if err != nil {
		t.Fatalf("MatchAsset() with filter error: %v", err)
	}
	if a.Name != "argsh-so-linux-amd64" {
		t.Errorf("MatchAsset() with filter = %q, want %q", a.Name, "argsh-so-linux-amd64")
	}

	// Filter for .so variant
	a, err = MatchAsset(assets, "argsh", "argsh-linux-*.so")
	if err != nil {
		t.Fatalf("MatchAsset() with .so filter error: %v", err)
	}
	if a.Name != "argsh-linux-amd64.so" {
		t.Errorf("MatchAsset() with .so filter = %q, want %q", a.Name, "argsh-linux-amd64.so")
	}

	// Filter for minifier
	a, err = MatchAsset(assets, "argsh", "minifier-*")
	if err != nil {
		t.Fatalf("MatchAsset() with minifier filter error: %v", err)
	}
	if a.Name != "minifier-linux-amd64" {
		t.Errorf("MatchAsset() with minifier filter = %q, want %q", a.Name, "minifier-linux-amd64")
	}

	// Filter that matches nothing
	_, err = MatchAsset(assets, "argsh", "nonexistent-*")
	if err == nil {
		t.Error("expected error for filter matching nothing")
	}
}

// TestMatchAssets_SortedByScore tests that MatchAssets returns sorted results.
func TestMatchAssets_SortedByScore(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("test requires linux/amd64")
	}

	assets := []Asset{
		{Name: "tool-linux-amd64", URL: "https://example.com/bin", Size: 1000},
		{Name: "tool-linux-amd64.tar.gz", URL: "https://example.com/tgz", Size: 2000},
	}

	candidates := MatchAssets(assets, "tool", "")
	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(candidates))
	}
	// tar.gz should score higher (archive bonus + tar.gz bonus)
	if candidates[0].Asset.Name != "tool-linux-amd64.tar.gz" {
		t.Errorf("expected tar.gz first, got %q", candidates[0].Asset.Name)
	}
	if candidates[0].Score <= candidates[1].Score {
		t.Errorf("first candidate score (%d) should be > second (%d)", candidates[0].Score, candidates[1].Score)
	}
}

// TestMatchAssetFilterErrorMessage tests error message includes filter.
func TestMatchAssetFilterErrorMessage(t *testing.T) {
	_, err := MatchAsset(nil, "tool", "custom-*")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "custom-*") {
		t.Errorf("error should contain filter pattern, got: %s", err.Error())
	}
}

// TestMatchAssetInvalidGlobPattern tests that a malformed glob pattern
// returns an error instead of silently matching nothing.
func TestMatchAssetInvalidGlobPattern(t *testing.T) {
	assets := []Asset{
		{Name: "tool-linux-amd64", URL: "https://example.com/bin", Size: 1000},
	}

	// "[invalid" is a malformed glob (unclosed bracket)
	candidates := MatchAssets(assets, "tool", "[invalid")
	if len(candidates) != 0 {
		t.Errorf("expected 0 candidates for invalid glob, got %d", len(candidates))
	}

	_, err := MatchAsset(assets, "tool", "[invalid")
	if err == nil {
		t.Fatal("expected error for invalid glob pattern")
	}
}

func TestShouldIgnore(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"tool.tar.gz", false},
		{"checksums.sha256", true},
		{"tool.sig", true},
		{"README.md", true},
		{"tool.deb", true},
		{"tool-linux-amd64", false},
	}

	for _, tt := range tests {
		got := shouldIgnore(tt.name)
		if got != tt.want {
			t.Errorf("shouldIgnore(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}
