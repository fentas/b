package cli

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
		// CRLF endings: the separator line is `=======\r` after
		// scanning, which a naive bytes.Equal would miss. The
		// scanner trims the trailing \r before comparing.
		{"full 2-way CRLF", "<<<<<<< local\r\nours\r\n=======\r\ntheirs\r\n>>>>>>> upstream\r\n", true},
		// Very long line with no newline must not panic or fall
		// back. Build a 200KB line of `x` followed by a real
		// conflict region; the scanner state machine should ignore
		// the long line entirely (no marker prefix matches) and
		// still detect the conflict that comes after.
		{"long line then conflict", strings.Repeat("x", 200*1024) + "\n<<<<<<< local\nours\n=======\ntheirs\n>>>>>>> upstream\n", true},
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
