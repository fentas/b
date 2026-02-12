package provider

import "testing"

func TestGitHubMatch(t *testing.T) {
	g := &GitHub{}
	tests := []struct {
		ref  string
		want bool
	}{
		{"github.com/derailed/k9s", true},
		{"github.com/derailed/k9s@v0.32.0", true},
		{"github.com/org/repo", true},
		{"gitlab.com/org/repo", false},
		{"derailed/k9s", true},              // bare owner/repo
		{"kubectl", false},                  // just a name
		{"go://github.com/org/repo", false}, // protocol prefix
	}

	for _, tt := range tests {
		got := g.Match(tt.ref)
		if got != tt.want {
			t.Errorf("GitHub.Match(%q) = %v, want %v", tt.ref, got, tt.want)
		}
	}
}

func TestGitHubOwnerRepo(t *testing.T) {
	tests := []struct {
		ref       string
		wantOwner string
		wantRepo  string
	}{
		{"github.com/derailed/k9s", "derailed", "k9s"},
		{"github.com/derailed/k9s@v0.32.0", "derailed", "k9s"},
		{"derailed/k9s", "derailed", "k9s"},
	}

	for _, tt := range tests {
		owner, repo := githubOwnerRepo(tt.ref)
		if owner != tt.wantOwner || repo != tt.wantRepo {
			t.Errorf("githubOwnerRepo(%q) = (%q, %q), want (%q, %q)",
				tt.ref, owner, repo, tt.wantOwner, tt.wantRepo)
		}
	}
}
