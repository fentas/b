package envmatch

import "testing"

func TestGlobPrefix(t *testing.T) {
	tests := []struct {
		glob string
		want string
	}{
		{"manifests/hetzner/**", "manifests/hetzner/"},
		{"manifests/base/**", "manifests/base/"},
		{"configs/ingress.yaml", "configs/ingress.yaml"},
		{"**/*.yaml", ""},
		{"*.yaml", ""},
		{"a/b/c/*.txt", "a/b/c/"},
	}

	for _, tt := range tests {
		got := globPrefix(tt.glob)
		if got != tt.want {
			t.Errorf("globPrefix(%q) = %q, want %q", tt.glob, got, tt.want)
		}
	}
}

func TestMatchGlob(t *testing.T) {
	tests := []struct {
		pattern string
		path    string
		want    bool
	}{
		{"manifests/hetzner/**", "manifests/hetzner/deployment.yaml", true},
		{"manifests/hetzner/**", "manifests/hetzner/sub/svc.yaml", true},
		{"manifests/hetzner/**", "manifests/base/deployment.yaml", false},
		{"manifests/base/**", "manifests/base/deployment.yaml", true},
		{"**/*.yaml", "configs/ingress.yaml", true},
		{"**/*.yaml", "a/b/c/deep.yaml", true},
		{"**/*.yaml", "readme.md", false},
		{"configs/ingress.yaml", "configs/ingress.yaml", true},
		{"configs/ingress.yaml", "configs/other.yaml", false},
		{"*.txt", "readme.txt", true},
		{"*.txt", "dir/readme.txt", false},
	}

	for _, tt := range tests {
		got := matchGlob(tt.pattern, tt.path)
		if got != tt.want {
			t.Errorf("matchGlob(%q, %q) = %v, want %v", tt.pattern, tt.path, got, tt.want)
		}
	}
}

func TestComputeDest(t *testing.T) {
	tests := []struct {
		source string
		prefix string
		dest   string
		want   string
	}{
		// With dest: strip prefix, prepend dest
		{"manifests/hetzner/deployment.yaml", "manifests/hetzner/", "/hetzner", "/hetzner/deployment.yaml"},
		{"manifests/hetzner/sub/svc.yaml", "manifests/hetzner/", "/hetzner", "/hetzner/sub/svc.yaml"},
		// Without dest: preserve original path
		{"manifests/base/deployment.yaml", "manifests/base/", "", "manifests/base/deployment.yaml"},
		// Literal path with dest
		{"configs/ingress.yaml", "configs/ingress.yaml", "/config", "/config/ingress.yaml"},
		// Dest with trailing slash stripped
		{"manifests/hetzner/deploy.yaml", "manifests/hetzner/", "/hetzner/", "/hetzner/deploy.yaml"},
	}

	for _, tt := range tests {
		got := computeDest(tt.source, tt.prefix, tt.dest)
		if got != tt.want {
			t.Errorf("computeDest(%q, %q, %q) = %q, want %q", tt.source, tt.prefix, tt.dest, got, tt.want)
		}
	}
}

func TestMatchGlobs(t *testing.T) {
	tree := []string{
		"manifests/base/deployment.yaml",
		"manifests/base/service.yaml",
		"manifests/hetzner/deployment.yaml",
		"manifests/hetzner/values.yaml",
		"configs/ingress.yaml",
		"configs/ingress.yaml.bak",
		"README.md",
		"tests/test.go",
	}

	globs := map[string]GlobConfig{
		"manifests/hetzner/**": {Dest: "/hetzner"},
		"manifests/base/**":    {},
		"configs/ingress.yaml": {Dest: "/config"},
	}

	globalIgnore := []string{"*.md", "tests/**"}

	result := MatchGlobs(tree, globs, globalIgnore)

	// Should match: 2 hetzner + 2 base + 1 ingress = 5
	if len(result) != 5 {
		t.Fatalf("MatchGlobs: got %d files, want 5", len(result))
		for _, m := range result {
			t.Logf("  %s → %s (glob: %s)", m.SourcePath, m.DestPath, m.GlobKey)
		}
	}

	// Check specific dest paths
	destMap := make(map[string]string)
	for _, m := range result {
		destMap[m.SourcePath] = m.DestPath
	}

	// hetzner files should be under /hetzner/
	if d, ok := destMap["manifests/hetzner/deployment.yaml"]; !ok || d != "/hetzner/deployment.yaml" {
		t.Errorf("hetzner/deployment.yaml dest = %q, want /hetzner/deployment.yaml", d)
	}

	// base files should preserve path
	if d, ok := destMap["manifests/base/deployment.yaml"]; !ok || d != "manifests/base/deployment.yaml" {
		t.Errorf("base/deployment.yaml dest = %q, want manifests/base/deployment.yaml", d)
	}

	// ingress should go to /config/
	if d, ok := destMap["configs/ingress.yaml"]; !ok || d != "/config/ingress.yaml" {
		t.Errorf("configs/ingress.yaml dest = %q, want /config/ingress.yaml", d)
	}
}

func TestMatchGlobsWithPerGlobIgnore(t *testing.T) {
	tree := []string{
		"configs/ingress.yaml",
		"configs/ingress.yaml.bak",
		"configs/secret.yaml",
	}

	globs := map[string]GlobConfig{
		"configs/**": {Dest: "/config", Ignore: []string{"*.bak"}},
	}

	result := MatchGlobs(tree, globs, nil)
	if len(result) != 2 {
		t.Fatalf("got %d files, want 2 (bak should be ignored)", len(result))
	}
}

func TestIsIgnored(t *testing.T) {
	tests := []struct {
		path   string
		global []string
		local  []string
		want   bool
	}{
		{"README.md", []string{"*.md"}, nil, true},
		{"docs/README.md", []string{"*.md"}, nil, true},
		{"main.go", []string{"*.md"}, nil, false},
		{"test.bak", nil, []string{"*.bak"}, true},
		{"tests/foo.go", []string{"tests/**"}, nil, true},
	}

	for _, tt := range tests {
		got := isIgnored(tt.path, tt.global, tt.local)
		if got != tt.want {
			t.Errorf("isIgnored(%q, %v, %v) = %v, want %v", tt.path, tt.global, tt.local, got, tt.want)
		}
	}
}
