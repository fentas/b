package gitcache

import (
	"strings"
	"testing"
)

// An @ after the path of an explicit SSH ref is a version, as it is for an
// https ref. B_GIT_SSH_AS_HTTPS changes the URL only.
func TestExplicitSSHRef_Version(t *testing.T) {
	tests := []struct {
		ref, base, version string
		urlOff, urlOn      string
	}{
		{"ssh://git@github.com/org/repo@v1", "ssh://git@github.com/org/repo", "v1",
			"ssh://git@github.com/org/repo.git", "https://github.com/org/repo.git"},
		{"ssh://git@github.com/org/repo.git@v1", "ssh://git@github.com/org/repo.git", "v1",
			"ssh://git@github.com/org/repo.git", "https://github.com/org/repo.git"},
		{"ssh://git@github.com/org/repo@feature/x", "ssh://git@github.com/org/repo", "feature/x",
			"ssh://git@github.com/org/repo.git", "https://github.com/org/repo.git"},
		{"ssh://git@github.com/org/repo#infra@v1", "ssh://git@github.com/org/repo", "v1",
			"ssh://git@github.com/org/repo.git", "https://github.com/org/repo.git"},
		{"ssh://git@host.example:2222/org/repo@v1", "ssh://git@host.example:2222/org/repo", "v1",
			"ssh://git@host.example:2222/org/repo.git", "ssh://git@host.example:2222/org/repo.git"},
		{"ssh://git@github.com/org/repo", "ssh://git@github.com/org/repo", "",
			"ssh://git@github.com/org/repo.git", "https://github.com/org/repo.git"},
		// A ":" after the @: not a git ref name, so the @ is in the path.
		{"ssh://git@github.com/x@y:z", "ssh://git@github.com/x@y:z", "",
			"ssh://git@github.com/x@y:z.git", "https://github.com/x@y:z.git"},
		{"git@github.com:org/repo@v1", "git@github.com:org/repo", "v1",
			"git@github.com:org/repo.git", "https://github.com/org/repo.git"},
	}
	for _, tt := range tests {
		if got := RefBase(tt.ref); got != tt.base {
			t.Errorf("RefBase(%q) = %q, want %q", tt.ref, got, tt.base)
		}
		if got := RefVersion(tt.ref); got != tt.version {
			t.Errorf("RefVersion(%q) = %q, want %q", tt.ref, got, tt.version)
		}
		for _, knob := range []struct{ value, want string }{{"", tt.urlOff}, {"1", tt.urlOn}} {
			t.Setenv(SSHAsHTTPSEnv, knob.value)
			t.Setenv("GITHUB_TOKEN", "")
			if got := ResolveGitURL(tt.ref, "").URL; got != knob.want {
				t.Errorf("%s=%q: ResolveGitURL(%q) = %q, want %q", SSHAsHTTPSEnv, knob.value, tt.ref, got, knob.want)
			}
		}
	}
}

// An @ in front of a host is a user part, not a version: the ref keeps it,
// and the https URL with that user part is refused.
func TestIsSSHUserAt_Forms(t *testing.T) {
	tests := []struct {
		ref  string
		want bool // true: the last @ is not a version
	}{
		{"git@github.com:org/repo", true},
		{"git@github.com:x@y:z", true},
		{"ssh://git@github.com/org/repo", true},
		{"ssh://git@github.com/x@y:z", true},
		{"https://user:pw@github.com/org/repo", true},
		{"user:pw@github.com/org/repo", true},
		{"user:pw@github.com:443/org/repo", true},
		{"tok@github.com/org/repo", true},
		{"github.com/org/repo@v2.0", false},
		{"git@github.com:org/repo@v1", false},
		{"git@github.com:repo@v1", false},
		{"deploy@host:repo@release/1.0", false}, // an @ in an scp-style path
		{"ssh://git@github.com/org/repo@v1", false},
		{"https://github.com/org/repo@v1", false},
		{"kubectl@v1.30", false},
	}
	for _, tt := range tests {
		if got := IsSSHUserAt(tt.ref, strings.LastIndex(tt.ref, "@")); got != tt.want {
			t.Errorf("IsSSHUserAt(%q, last @) = %v, want %v", tt.ref, got, tt.want)
		}
	}
}
