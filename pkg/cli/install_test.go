package cli

import "testing"

func TestParseSCPArg(t *testing.T) {
	tests := []struct {
		arg          string
		remaining    []string
		wantOk       bool
		wantRef      string
		wantVer      string
		wantGlob     string
		wantDest     string
		wantConsumed int
	}{
		{
			arg:          "github.com/org/infra:/manifests/hetzner/**",
			remaining:    []string{"/hetzner"},
			wantOk:       true,
			wantRef:      "github.com/org/infra",
			wantGlob:     "manifests/hetzner/**",
			wantDest:     "/hetzner",
			wantConsumed: 1,
		},
		{
			arg:          "github.com/org/infra@v2.0:/manifests/base/**",
			remaining:    []string{"."},
			wantOk:       true,
			wantRef:      "github.com/org/infra",
			wantVer:      "v2.0",
			wantGlob:     "manifests/base/**",
			wantDest:     ".",
			wantConsumed: 1,
		},
		{
			arg:          "github.com/org/infra:/**",
			remaining:    nil,
			wantOk:       true,
			wantRef:      "github.com/org/infra",
			wantGlob:     "**",
			wantDest:     "",
			wantConsumed: 0,
		},
		{
			// Without leading slash
			arg:          "github.com/org/infra:manifests/**",
			remaining:    []string{"out"},
			wantOk:       true,
			wantRef:      "github.com/org/infra",
			wantGlob:     "manifests/**",
			wantDest:     "out",
			wantConsumed: 1,
		},
		{
			// No colon — not SCP
			arg:    "github.com/org/repo",
			wantOk: false,
		},
		{
			// Protocol prefix (go://) — not SCP
			arg:    "go://github.com/org/repo",
			wantOk: false,
		},
		{
			// Just a name — not SCP
			arg:    "kubectl",
			wantOk: false,
		},
	}

	for _, tt := range tests {
		ei, consumed, ok := parseSCPArg(tt.arg, tt.remaining)
		if ok != tt.wantOk {
			t.Errorf("parseSCPArg(%q) ok = %v, want %v", tt.arg, ok, tt.wantOk)
			continue
		}
		if !ok {
			continue
		}
		if ei.ref != tt.wantRef {
			t.Errorf("parseSCPArg(%q) ref = %q, want %q", tt.arg, ei.ref, tt.wantRef)
		}
		if ei.version != tt.wantVer {
			t.Errorf("parseSCPArg(%q) version = %q, want %q", tt.arg, ei.version, tt.wantVer)
		}
		if ei.glob != tt.wantGlob {
			t.Errorf("parseSCPArg(%q) glob = %q, want %q", tt.arg, ei.glob, tt.wantGlob)
		}
		if ei.dest != tt.wantDest {
			t.Errorf("parseSCPArg(%q) dest = %q, want %q", tt.arg, ei.dest, tt.wantDest)
		}
		if consumed != tt.wantConsumed {
			t.Errorf("parseSCPArg(%q) consumed = %d, want %d", tt.arg, consumed, tt.wantConsumed)
		}
	}
}

func TestShortCommit(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", "(new)"},
		{"abc1234567890", "abc1234"},
		{"short", "short"},
	}

	for _, tt := range tests {
		got := shortCommit(tt.input)
		if got != tt.want {
			t.Errorf("shortCommit(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
