package gitcache

import (
	"path/filepath"
	"strings"
	"testing"
)

// These ref forms resolve the same way with and without the checks for an
// explicit scheme and for a user part. Each row gives the URL, the
// transport, whether a provider token is chosen and the version. The other
// forms are in TestResolveGitURL_AllVariants, TestExplicitSSHRef_Version and
// TestResolveGitURL_SSHAsHTTPS.
func TestResolveGitURL_KeptForms(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "FAKE-TOKEN-x")
	t.Setenv("GITLAB_TOKEN", "")
	t.Setenv("GITEA_TOKEN", "")
	const cfg = "/cfg"
	tests := []struct {
		ref, sshAsHTTPS, url string
		local, ssh, token    bool
		version              string
	}{
		{"host.example:8443/org/repo", "", "https://host.example:8443/org/repo.git", false, false, false, ""},
		{"host.example:443/org/repo", "", "https://host.example:443/org/repo.git", false, false, false, ""},
		{"github.com/org/repo#lbl@v1", "", "https://github.com/org/repo.git", false, false, true, "v1"},
		{"github.com/org/repo@v1", "", "https://github.com/org/repo.git", false, false, true, "v1"},
		{".", "", "/cfg", true, false, false, ""},
		{"..", "", "/", true, false, false, ""},
		{"/srv/repos/repo", "", "/srv/repos/repo", true, false, false, ""},
		{"git://./rel", "", "/cfg/rel", true, false, false, ""},
		{"git://github.com/org/repo", "", "https://github.com/org/repo.git", false, false, true, ""},
		// In an scp-style ref, an @ after the ":" is in the path: the last
		// @ is a version, also when the version has a "/".
		{"deploy@host.example:repo@release/1.0", "", "deploy@host.example:repo.git", false, true, false, "release/1.0"},
		{"git@host.example:org/repo@v1", "", "git@host.example:org/repo.git", false, true, false, "v1"},
		{"ssh://git@github.com/org/repo", "", "ssh://git@github.com/org/repo.git", false, true, false, ""},
		{"git@github.com:org/repo", "1", "https://github.com/org/repo.git", false, false, true, ""},
	}
	for _, tt := range tests {
		t.Setenv(SSHAsHTTPSEnv, tt.sshAsHTTPS)
		r := ResolveGitURL(tt.ref, cfg)
		got := r.URL
		if tt.local {
			got = filepath.ToSlash(filepath.Clean(got))
		}
		if got != tt.url || r.IsLocal != tt.local || r.IsSSH != tt.ssh || (r.AuthToken != "") != tt.token {
			t.Errorf("%s=%q: ResolveGitURL(%q) = URL %q, local %v, ssh %v, token set %v; want %q, %v, %v, %v",
				SSHAsHTTPSEnv, tt.sshAsHTTPS, tt.ref, r.URL, r.IsLocal, r.IsSSH, r.AuthToken != "",
				tt.url, tt.local, tt.ssh, tt.token)
		}
		if err := CheckRef(tt.ref); err != nil {
			t.Errorf("CheckRef(%q) = %v, want nil", tt.ref, err)
		}
		if !tt.local {
			if got := RefVersion(tt.ref); got != tt.version {
				t.Errorf("RefVersion(%q) = %q, want %q", tt.ref, got, tt.version)
			}
		}
	}
}

// An https ref and a remote ref without a scheme that end in ".git" get the
// URL, the token and the header of the ref without ".git": the URL ends in
// ".git" once. Only one ".git" goes, so "repo.git.git" keeps its name.
func TestResolveGitURL_OneGitSuffix(t *testing.T) {
	setProviderEnv(t, "")
	for _, tt := range []struct{ ref, plain string }{
		{"github.com/org/repo.git", "github.com/org/repo"},
		{"https://github.com/org/repo.git", "https://github.com/org/repo"},
		{"HTTPS://github.com/org/repo.git@v1", "HTTPS://github.com/org/repo@v1"},
		{"gitlab.com/group/project.git#lbl", "gitlab.com/group/project#lbl"},
		{"git://github.com/org/repo.git:tool.sh", "git://github.com/org/repo:tool.sh"},
		{"host.example/org/repo.git", "host.example/org/repo"},
	} {
		got, plain := ResolveGitURL(tt.ref, ""), ResolveGitURL(tt.plain, "")
		if got != plain || strings.HasSuffix(got.URL, ".git.git") {
			t.Errorf("ResolveGitURL(%q) = URL %q, token set %v; want the result of %q: URL %q, token set %v",
				tt.ref, got.URL, got.AuthToken != "", tt.plain, plain.URL, plain.AuthToken != "")
		}
	}
	for _, ref := range []string{"github.com/org/repo.git.git", "https://github.com/org/repo.git.git"} {
		if got := ResolveGitURL(ref, ""); got.URL != "https://github.com/org/repo.git.git" || got.AuthToken != fakeGH {
			t.Errorf("ResolveGitURL(%q) = URL %q, token set %v; want repo.git.git and the token", ref, got.URL, got.AuthToken != "")
		}
	}
}
