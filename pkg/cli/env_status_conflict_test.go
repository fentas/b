package cli

import "testing"

// TestHasConflictMarkers covers the local helper used by env status to
// flag conflicted files. We need all three of <<<<<<< / ======= /
// >>>>>>> for a positive match so partial markdown rules don't trigger.
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
