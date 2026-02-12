package provider

import "testing"

func TestMatchAsset(t *testing.T) {
	assets := []Asset{
		{Name: "tool_Linux_x86_64.tar.gz", URL: "https://example.com/linux.tar.gz", Size: 1000},
		{Name: "tool_Darwin_arm64.tar.gz", URL: "https://example.com/darwin-arm.tar.gz", Size: 1000},
		{Name: "tool_Darwin_x86_64.tar.gz", URL: "https://example.com/darwin-amd.tar.gz", Size: 1000},
		{Name: "tool_Windows_x86_64.zip", URL: "https://example.com/windows.zip", Size: 1000},
		{Name: "checksums.txt", URL: "https://example.com/checksums.txt", Size: 100},
		{Name: "tool_Linux_x86_64.tar.gz.sha256", URL: "https://example.com/sha256", Size: 64},
	}

	// This test assumes GOOS=linux GOARCH=amd64
	if a, err := MatchAsset(assets, "tool"); err != nil {
		t.Fatalf("MatchAsset() error: %v", err)
	} else if a.Name != "tool_Linux_x86_64.tar.gz" {
		t.Errorf("MatchAsset() = %q, want %q", a.Name, "tool_Linux_x86_64.tar.gz")
	}
}

func TestMatchAssetNoMatch(t *testing.T) {
	assets := []Asset{
		{Name: "tool_Darwin_arm64.tar.gz", URL: "https://example.com/d.tar.gz", Size: 1000},
	}
	// On linux/amd64, this should not match
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
		{"charm_linux_amd64", "arm", false}, // "arm" inside "charm" should not match
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
