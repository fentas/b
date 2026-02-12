package provider

import "testing"

func TestParseRef(t *testing.T) {
	tests := []struct {
		input       string
		wantBase    string
		wantVersion string
	}{
		{"github.com/derailed/k9s", "github.com/derailed/k9s", ""},
		{"github.com/derailed/k9s@v0.32.0", "github.com/derailed/k9s", "v0.32.0"},
		{"go://github.com/jrhouston/tfk8s@v0.1.8", "go://github.com/jrhouston/tfk8s", "v0.1.8"},
		{"docker://hashicorp/terraform", "docker://hashicorp/terraform", ""},
		{"gitlab.com/org/tool@v1.0", "gitlab.com/org/tool", "v1.0"},
	}

	for _, tt := range tests {
		base, version := ParseRef(tt.input)
		if base != tt.wantBase || version != tt.wantVersion {
			t.Errorf("ParseRef(%q) = (%q, %q), want (%q, %q)",
				tt.input, base, version, tt.wantBase, tt.wantVersion)
		}
	}
}

func TestIsProviderRef(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"kubectl", false},
		{"jq", false},
		{"github.com/derailed/k9s", true},
		{"gitlab.com/org/tool", true},
		{"go://github.com/jrhouston/tfk8s", true},
		{"docker://hashicorp/terraform", true},
		{"derailed/k9s", true}, // bare owner/repo
	}

	for _, tt := range tests {
		got := IsProviderRef(tt.input)
		if got != tt.want {
			t.Errorf("IsProviderRef(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestBinaryName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"github.com/derailed/k9s", "k9s"},
		{"github.com/junegunn/fzf@v0.61.0", "fzf"},
		{"go://github.com/jrhouston/tfk8s@v0.1.8", "tfk8s"},
		{"docker://hashicorp/terraform", "terraform"},
		{"gitlab.com/org/my-tool", "my-tool"},
		{"codeberg.org/user/app", "app"},
	}

	for _, tt := range tests {
		got := BinaryName(tt.input)
		if got != tt.want {
			t.Errorf("BinaryName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
