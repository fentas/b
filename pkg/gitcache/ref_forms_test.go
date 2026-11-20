package gitcache

import (
	"path/filepath"
	"testing"
)

// These ref forms resolve the same way with and without the checks for an
// explicit scheme and for a user part. Each row gives the URL, the
// transport and whether a provider token is chosen. The other forms are in
// TestResolveGitURL_AllVariants, TestExplicitSSHRef_Version and
// TestResolveGitURL_SSHAsHTTPS.
func TestResolveGitURL_KeptForms(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "FAKE-TOKEN-x")
	t.Setenv("GITLAB_TOKEN", "")
	t.Setenv("GITEA_TOKEN", "")
	const cfg = "/cfg"
	tests := []struct {
		ref, sshAsHTTPS, url string
		local, ssh, token    bool
	}{
		{"host.example:8443/org/repo", "", "https://host.example:8443/org/repo.git", false, false, false},
		{"github.com/org/repo#lbl@v1", "", "https://github.com/org/repo.git", false, false, true},
		{".", "", "/cfg", true, false, false},
		{"..", "", "/", true, false, false},
		{"git://./rel", "", "/cfg/rel", true, false, false},
		{"git://github.com/org/repo", "", "https://github.com/org/repo.git", false, false, true},
		// In an scp-style ref, an @ after the ":" is in the path: the last
		// @ is a version, also when the version has a "/".
		{"deploy@host.example:repo@release/1.0", "", "deploy@host.example:repo.git", false, true, false},
		// An scp-style ref with two @ before the ":" is an SSH ref with the
		// user name "git@x". This form has no password field. It stays SSH
		// with B_GIT_SSH_AS_HTTPS=1: the host is not github.com.
		{"git@x@github.com:org/repo", "", "git@x@github.com:org/repo.git", false, true, false},
		{"git@x@github.com:org/repo", "1", "git@x@github.com:org/repo.git", false, true, false},
		{"git@x@github.com:org/repo@v1", "1", "git@x@github.com:org/repo.git", false, true, false},
		{"ssh://git@x@github.com/org/repo", "", "ssh://git@x@github.com/org/repo.git", false, true, false},
		{"ssh://git@x@github.com/org/repo", "1", "ssh://git@x@github.com/org/repo.git", false, true, false},
		{"git@github.com:org/repo", "1", "https://github.com/org/repo.git", false, false, true},
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
	}
}
