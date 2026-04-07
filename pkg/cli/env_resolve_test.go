package cli

import (
	"strings"
	"testing"
)

// TestResolveConflictMarkers_Diff3KeepOurs handles the diff3-format
// markers that `git merge-file --diff3` (and therefore b's merge
// path) writes today.
func TestResolveConflictMarkers_Diff3KeepOurs(t *testing.T) {
	in := []byte(`before
<<<<<<< local
ours-line
||||||| base
base-line
=======
theirs-line
>>>>>>> upstream
after
`)
	out, n, err := resolveConflictMarkers(in, true)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("want 1 region, got %d", n)
	}
	s := string(out)
	if !strings.Contains(s, "ours-line") {
		t.Errorf("ours line missing: %s", s)
	}
	if strings.Contains(s, "theirs-line") || strings.Contains(s, "base-line") {
		t.Errorf("non-ours content survived: %s", s)
	}
	if strings.Contains(s, "<<<<<<<") || strings.Contains(s, ">>>>>>>") {
		t.Errorf("markers survived: %s", s)
	}
}

func TestResolveConflictMarkers_Diff3KeepTheirs(t *testing.T) {
	in := []byte("a\n<<<<<<< local\nours\n||||||| base\nbase\n=======\ntheirs\n>>>>>>> upstream\nz\n")
	out, n, err := resolveConflictMarkers(in, false)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("want 1, got %d", n)
	}
	s := string(out)
	if !strings.Contains(s, "theirs") || strings.Contains(s, "ours") || strings.Contains(s, "base") {
		t.Errorf("wrong content kept: %s", s)
	}
}

// TestResolveConflictMarkers_TwoWayForm — bare 2-way markers without
// the `|||||||` base section. Hand-edited files often look like this,
// and the parser must accept them.
func TestResolveConflictMarkers_TwoWayForm(t *testing.T) {
	in := []byte("<<<<<<< local\nours\n=======\ntheirs\n>>>>>>> upstream\n")
	out, n, err := resolveConflictMarkers(in, true)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("want 1, got %d", n)
	}
	if !strings.Contains(string(out), "ours") {
		t.Errorf("ours missing: %s", out)
	}
}

// TestResolveConflictMarkers_MultipleRegions resolves several regions
// in one pass.
func TestResolveConflictMarkers_MultipleRegions(t *testing.T) {
	in := []byte(`a
<<<<<<< local
ours1
=======
theirs1
>>>>>>> upstream
b
<<<<<<< local
ours2
=======
theirs2
>>>>>>> upstream
c
`)
	out, n, err := resolveConflictMarkers(in, false)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("want 2 regions, got %d", n)
	}
	s := string(out)
	if !strings.Contains(s, "theirs1") || !strings.Contains(s, "theirs2") {
		t.Errorf("missing theirs content: %s", s)
	}
}

// TestResolveConflictMarkers_Unterminated returns an error rather
// than producing garbage when the closing marker is missing.
func TestResolveConflictMarkers_Unterminated(t *testing.T) {
	in := []byte("<<<<<<< local\nstuff\n=======\nmore\n")
	_, _, err := resolveConflictMarkers(in, true)
	if err == nil {
		t.Error("expected error for unterminated region")
	}
}

// TestHasConflictMarkers requires all three signature pieces so a
// markdown rule like `=======` alone doesn't trip the detector.
func TestHasConflictMarkers(t *testing.T) {
	if hasConflictMarkers([]byte("=======\n")) {
		t.Error("=======-only should not be a conflict")
	}
	if !hasConflictMarkers([]byte("<<<<<<< local\nx\n=======\ny\n>>>>>>> upstream\n")) {
		t.Error("full conflict not detected")
	}
}
