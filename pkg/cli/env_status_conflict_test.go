package cli

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestHasConflictMarkers covers the local helper used by env status
// to flag conflicted files. The check is line-anchored so a markdown
// rule (`=======`) or stray `<<<<<<<` inside a string literal does
// not trigger a false positive.
func TestHasConflictMarkers_StatusHelper(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"empty", "", false},
		{"plain text", "hello world\n", false},
		{"only equals", "=======\n", false},
		{"only opener", "<<<<<<< local\nstuff\n", false},
		{"markdown rule", "title\n=======\nbody\n", false},
		{"stray opener inside string", `value: "<<<<<<<"` + "\n", false},
		{"full diff3", "<<<<<<< local\nours\n||||||| base\nbase\n=======\ntheirs\n>>>>>>> upstream\n", true},
		{"full 2-way", "<<<<<<< local\nours\n=======\ntheirs\n>>>>>>> upstream\n", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hasConflictMarkers([]byte(c.in)); got != c.want {
				t.Errorf("hasConflictMarkers(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

// TestHashAndScanConflicts verifies the bundled streaming helper
// produces the same hex digest as crypto/sha256 over the same bytes
// AND correctly detects markers in a single read pass.
func TestHashAndScanConflicts(t *testing.T) {
	tmp := t.TempDir()
	cases := []struct {
		name        string
		body        string
		wantMarkers bool
	}{
		{"clean", "hello\nworld\n", false},
		{
			"conflicted",
			"line1\n<<<<<<< local\nours\n=======\ntheirs\n>>>>>>> upstream\nline7\n",
			true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(tmp, c.name+".txt")
			if err := os.WriteFile(path, []byte(c.body), 0644); err != nil {
				t.Fatal(err)
			}
			gotHash, gotMarkers, err := hashAndScanConflicts(path)
			if err != nil {
				t.Fatal(err)
			}
			wantHash := fmt.Sprintf("%x", sha256.Sum256([]byte(c.body)))
			if gotHash != wantHash {
				t.Errorf("hash = %s, want %s", gotHash, wantHash)
			}
			if gotMarkers != c.wantMarkers {
				t.Errorf("markers = %v, want %v", gotMarkers, c.wantMarkers)
			}
		})
	}
}
