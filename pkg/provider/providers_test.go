package provider

import "testing"

// TestDockerMatch tests Docker provider matching.
func TestDockerMatch(t *testing.T) {
	d := &Docker{}
	tests := []struct {
		ref  string
		want bool
	}{
		{"docker://hashicorp/terraform", true},
		{"docker://alpine", true},
		{"github.com/org/repo", false},
		{"kubectl", false},
	}
	for _, tt := range tests {
		if got := d.Match(tt.ref); got != tt.want {
			t.Errorf("Docker.Match(%q) = %v, want %v", tt.ref, got, tt.want)
		}
	}
}

func TestDockerName(t *testing.T) {
	d := &Docker{}
	if d.Name() != "docker" {
		t.Errorf("Docker.Name() = %q", d.Name())
	}
}

func TestDockerLatestVersion(t *testing.T) {
	d := &Docker{}
	v, err := d.LatestVersion("docker://alpine")
	if err != nil {
		t.Fatalf("LatestVersion() error = %v", err)
	}
	if v != "latest" {
		t.Errorf("LatestVersion() = %q, want %q", v, "latest")
	}
}

func TestDockerFetchRelease(t *testing.T) {
	d := &Docker{}
	_, err := d.FetchRelease("docker://alpine", "latest")
	if err == nil {
		t.Error("expected error from Docker.FetchRelease")
	}
}

func TestDockerImage(t *testing.T) {
	tests := []struct {
		ref  string
		want string
	}{
		{"docker://hashicorp/terraform", "hashicorp/terraform"},
		{"docker://alpine", "alpine"},
		{"docker://hashicorp/terraform@v1.0", "hashicorp/terraform"},
	}
	for _, tt := range tests {
		got := dockerImage(tt.ref)
		if got != tt.want {
			t.Errorf("dockerImage(%q) = %q, want %q", tt.ref, got, tt.want)
		}
	}
}

// TestGiteaMatch tests Gitea provider matching.
func TestGiteaMatch(t *testing.T) {
	g := &Gitea{}
	tests := []struct {
		ref  string
		want bool
	}{
		{"codeberg.org/user/app", true},
		{"codeberg.org/user/app@v1.0", true},
		{"gitea.com/user/app", true},
		{"github.com/org/repo", false},
		{"kubectl", false},
	}
	for _, tt := range tests {
		if got := g.Match(tt.ref); got != tt.want {
			t.Errorf("Gitea.Match(%q) = %v, want %v", tt.ref, got, tt.want)
		}
	}
}

func TestGiteaName(t *testing.T) {
	g := &Gitea{}
	if g.Name() != "gitea" {
		t.Errorf("Gitea.Name() = %q", g.Name())
	}
}

func TestGiteaParts(t *testing.T) {
	tests := []struct {
		ref       string
		wantHost  string
		wantOwner string
		wantRepo  string
	}{
		{"codeberg.org/user/app", "codeberg.org", "user", "app"},
		{"codeberg.org/user/app@v1.0", "codeberg.org", "user", "app"},
		{"gitea.com/org/tool", "gitea.com", "org", "tool"},
		{"github.com/org/repo", "", "", ""}, // not a Gitea host
	}
	for _, tt := range tests {
		host, owner, repo := giteaParts(tt.ref)
		if host != tt.wantHost || owner != tt.wantOwner || repo != tt.wantRepo {
			t.Errorf("giteaParts(%q) = (%q, %q, %q), want (%q, %q, %q)",
				tt.ref, host, owner, repo, tt.wantHost, tt.wantOwner, tt.wantRepo)
		}
	}
}

// TestGitLabMatch tests GitLab provider matching.
func TestGitLabMatch(t *testing.T) {
	g := &GitLab{}
	tests := []struct {
		ref  string
		want bool
	}{
		{"gitlab.com/org/tool", true},
		{"gitlab.com/org/tool@v1.0", true},
		{"gitlab.com/group/subgroup/project", true},
		{"github.com/org/repo", false},
		{"kubectl", false},
	}
	for _, tt := range tests {
		if got := g.Match(tt.ref); got != tt.want {
			t.Errorf("GitLab.Match(%q) = %v, want %v", tt.ref, got, tt.want)
		}
	}
}

func TestGitLabName(t *testing.T) {
	g := &GitLab{}
	if g.Name() != "gitlab" {
		t.Errorf("GitLab.Name() = %q", g.Name())
	}
}

func TestGitLabProjectPath(t *testing.T) {
	tests := []struct {
		ref  string
		want string
	}{
		{"gitlab.com/org/tool", "org/tool"},
		{"gitlab.com/org/tool@v1.0", "org/tool"},
		{"gitlab.com/group/subgroup/project", "group/subgroup/project"},
	}
	for _, tt := range tests {
		got := gitlabProjectPath(tt.ref)
		if got != tt.want {
			t.Errorf("gitlabProjectPath(%q) = %q, want %q", tt.ref, got, tt.want)
		}
	}
}

// TestGoInstallMatch tests GoInstall provider matching.
func TestGoInstallMatch(t *testing.T) {
	g := &GoInstall{}
	tests := []struct {
		ref  string
		want bool
	}{
		{"go://github.com/org/tool", true},
		{"go://github.com/org/tool@v1.0", true},
		{"github.com/org/repo", false},
		{"kubectl", false},
	}
	for _, tt := range tests {
		if got := g.Match(tt.ref); got != tt.want {
			t.Errorf("GoInstall.Match(%q) = %v, want %v", tt.ref, got, tt.want)
		}
	}
}

func TestGoInstallName(t *testing.T) {
	g := &GoInstall{}
	if g.Name() != "go" {
		t.Errorf("GoInstall.Name() = %q", g.Name())
	}
}

func TestGoInstallLatestVersion(t *testing.T) {
	g := &GoInstall{}
	v, err := g.LatestVersion("go://github.com/org/tool")
	if err != nil {
		t.Fatalf("LatestVersion() error = %v", err)
	}
	if v != "latest" {
		t.Errorf("LatestVersion() = %q, want %q", v, "latest")
	}
}

func TestGoInstallFetchRelease(t *testing.T) {
	g := &GoInstall{}
	_, err := g.FetchRelease("go://github.com/org/tool", "latest")
	if err == nil {
		t.Error("expected error from GoInstall.FetchRelease")
	}
}

func TestGoModule(t *testing.T) {
	tests := []struct {
		ref  string
		want string
	}{
		{"go://github.com/jrhouston/tfk8s", "github.com/jrhouston/tfk8s"},
		{"go://github.com/jrhouston/tfk8s@v0.1.8", "github.com/jrhouston/tfk8s"},
	}
	for _, tt := range tests {
		got := goModule(tt.ref)
		if got != tt.want {
			t.Errorf("goModule(%q) = %q, want %q", tt.ref, got, tt.want)
		}
	}
}

// TestDetect tests the Detect function with known provider refs.
func TestDetect(t *testing.T) {
	tests := []struct {
		ref      string
		wantName string
		wantErr  bool
	}{
		{"github.com/derailed/k9s", "github", false},
		{"gitlab.com/org/tool", "gitlab", false},
		{"codeberg.org/user/app", "gitea", false},
		{"go://github.com/org/tool", "go", false},
		{"docker://hashicorp/terraform", "docker", false},
		{"kubectl", "", true}, // no provider matches
	}
	for _, tt := range tests {
		p, err := Detect(tt.ref)
		if (err != nil) != tt.wantErr {
			t.Errorf("Detect(%q) error = %v, wantErr %v", tt.ref, err, tt.wantErr)
			continue
		}
		if !tt.wantErr && p.Name() != tt.wantName {
			t.Errorf("Detect(%q).Name() = %q, want %q", tt.ref, p.Name(), tt.wantName)
		}
	}
}

// TestRegister verifies providers are registered.
func TestRegister(t *testing.T) {
	if len(registry) == 0 {
		t.Error("registry should have registered providers")
	}
}

func TestGitHubName(t *testing.T) {
	g := &GitHub{}
	if g.Name() != "github" {
		t.Errorf("GitHub.Name() = %q", g.Name())
	}
}

func TestIsArchive(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"tool.tar.gz", true},
		{"tool.tgz", true},
		{"tool.tar.xz", true},
		{"tool.txz", true},
		{"tool.tar.bz2", true},
		{"tool.zip", true},
		{"tool.exe", false},
		{"tool", false},
		{"tool.sha256", false},
	}
	for _, tt := range tests {
		got := isArchive(tt.name)
		if got != tt.want {
			t.Errorf("isArchive(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestContainsWord_EdgeCases(t *testing.T) {
	tests := []struct {
		name, word string
		want       bool
	}{
		{"linux", "linux", true},
		{"", "linux", false},
		{"linux-amd64", "linux", true},
		{"linux_amd64", "linux", true},
		{"tool.linux.amd64", "linux", true},
		{"mylinuxtool", "linux", false},
		{"amd64", "amd64", true},
		{"tool-arm64-linux", "arm", false},
		{"tool-arm-linux", "arm", true},
	}
	for _, tt := range tests {
		got := containsWord(tt.name, tt.word)
		if got != tt.want {
			t.Errorf("containsWord(%q, %q) = %v, want %v", tt.name, tt.word, got, tt.want)
		}
	}
}

func TestShouldIgnore_Extra(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"tool.sha512", true},
		{"tool.sha512sum", true},
		{"tool.asc", true},
		{"tool.pem", true},
		{"tool.sbom", true},
		{"tool.spdx", true},
		{"tool.rpm", true},
		{"tool.msi", true},
		{"tool.pkg", true},
		{"tool.apk", true},
		{"tool.json", true},
		{"tool.tar.gz", false},
		{"tool-linux-amd64", false},
	}
	for _, tt := range tests {
		got := shouldIgnore(tt.name)
		if got != tt.want {
			t.Errorf("shouldIgnore(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestMatchAsset_PrefersTarGz(t *testing.T) {
	assets := []Asset{
		{Name: "tool_linux_amd64.zip", URL: "https://example.com/zip", Size: 1000},
		{Name: "tool_linux_amd64.tar.gz", URL: "https://example.com/tgz", Size: 1000},
	}
	a, err := MatchAsset(assets, "tool")
	if err != nil {
		t.Fatalf("MatchAsset() error: %v", err)
	}
	if a.Name != "tool_linux_amd64.tar.gz" {
		t.Errorf("MatchAsset() = %q, want tar.gz preferred", a.Name)
	}
}

func TestMatchAsset_PrefersArchive(t *testing.T) {
	assets := []Asset{
		{Name: "tool_linux_amd64", URL: "https://example.com/bin", Size: 1000},
		{Name: "tool_linux_amd64.tar.gz", URL: "https://example.com/tgz", Size: 1000},
	}
	a, err := MatchAsset(assets, "tool")
	if err != nil {
		t.Fatalf("MatchAsset() error: %v", err)
	}
	if a.Name != "tool_linux_amd64.tar.gz" {
		t.Errorf("MatchAsset() = %q, want archive preferred", a.Name)
	}
}

func TestMatchAsset_EmptyAssets(t *testing.T) {
	_, err := MatchAsset(nil, "tool")
	if err == nil {
		t.Error("expected error for empty assets")
	}
}

func TestDockerImage_EdgeCases(t *testing.T) {
	tests := []struct {
		ref  string
		want string
	}{
		{"docker://library/alpine", "library/alpine"},
		{"docker://org/image:latest", "org/image"},
		{"docker://single", "single"},
	}
	for _, tt := range tests {
		got := dockerImage(tt.ref)
		if got != tt.want {
			t.Errorf("dockerImage(%q) = %q, want %q", tt.ref, got, tt.want)
		}
	}
}

func TestGiteaParts_EdgeCases(t *testing.T) {
	tests := []struct {
		ref       string
		wantHost  string
		wantOwner string
		wantRepo  string
	}{
		{"codeberg.org/user/app@v2.0", "codeberg.org", "user", "app"},
		{"notgitea.com/user/app", "", "", ""},
		{"codeberg.org/user", "", "", ""},
	}
	for _, tt := range tests {
		host, owner, repo := giteaParts(tt.ref)
		if host != tt.wantHost || owner != tt.wantOwner || repo != tt.wantRepo {
			t.Errorf("giteaParts(%q) = (%q, %q, %q), want (%q, %q, %q)",
				tt.ref, host, owner, repo, tt.wantHost, tt.wantOwner, tt.wantRepo)
		}
	}
}

func TestGitLabProjectPath_EdgeCases(t *testing.T) {
	tests := []struct {
		ref  string
		want string
	}{
		{"gitlab.com/a/b/c/d", "a/b/c/d"},
		{"gitlab.com/simple/project@v1.0", "simple/project"},
	}
	for _, tt := range tests {
		got := gitlabProjectPath(tt.ref)
		if got != tt.want {
			t.Errorf("gitlabProjectPath(%q) = %q, want %q", tt.ref, got, tt.want)
		}
	}
}
