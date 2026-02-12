package provider

import (
	"runtime"
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

	a, err := MatchAsset(assets, "tool")
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
	_, err := MatchAsset(assets, "tool")
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
