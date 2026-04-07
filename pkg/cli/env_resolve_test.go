package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fentas/goodies/streams"

	"github.com/fentas/b/pkg/lock"
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

// TestEnvResolveRun_RewritesFileAndUpdatesLock is the end-to-end
// test the round-2 reviewer asked for. It writes a fake lockfile
// pointing at a temp project file containing diff3 conflict
// markers, runs `env resolve --theirs`, and checks that:
//   - the file on disk no longer contains the markers
//   - the lock entry's SHA was updated to match the new on-disk hash
func TestEnvResolveRun_RewritesFileAndUpdatesLock(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PATH_BASE", tmp)
	conflicted := []byte("a\n<<<<<<< local\nour\n=======\nther\n>>>>>>> upstream\nz\n")
	destRel := "configs/a.yaml"
	destAbs := filepath.Join(tmp, destRel)
	if err := os.MkdirAll(filepath.Dir(destAbs), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destAbs, conflicted, 0644); err != nil {
		t.Fatal(err)
	}

	lk := &lock.Lock{
		Version: 1,
		Envs: []lock.EnvEntry{{
			Ref: "github.com/org/infra",
			Files: []lock.LockFile{
				{Path: "configs/a.yaml", Dest: destRel, SHA256: "stale-sha"},
			},
		}},
	}
	if err := lock.WriteLock(tmp, lk, "test"); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	o := &EnvResolveOptions{
		SharedOptions: &SharedOptions{
			IO:               &streams.IO{Out: &out, ErrOut: &bytes.Buffer{}},
			loadedConfigPath: filepath.Join(tmp, "b.yaml"),
		},
		Theirs: true,
	}
	if err := o.Run(nil); err != nil {
		t.Fatalf("Run: %v", err)
	}

	got, err := os.ReadFile(destAbs)
	if err != nil {
		t.Fatal(err)
	}
	if hasConflictMarkers(got) {
		t.Errorf("conflict markers not stripped:\n%s", got)
	}
	if !strings.Contains(string(got), "ther") {
		t.Errorf("upstream side missing:\n%s", got)
	}

	lk2, err := lock.ReadLock(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if lk2.Envs[0].Files[0].SHA256 == "stale-sha" {
		t.Errorf("lock SHA was not updated")
	}
}

// TestEnvResolveRun_PathTraversalRejected verifies that a malicious
// lock entry pointing outside the project root is rejected before
// any file I/O happens.
func TestEnvResolveRun_PathTraversalRejected(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PATH_BASE", tmp)
	lk := &lock.Lock{
		Version: 1,
		Envs: []lock.EnvEntry{{
			Ref: "github.com/org/infra",
			Files: []lock.LockFile{
				{Path: "evil", Dest: "../../etc/passwd", SHA256: "x"},
			},
		}},
	}
	if err := lock.WriteLock(tmp, lk, "test"); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	o := &EnvResolveOptions{
		SharedOptions: &SharedOptions{
			IO:               &streams.IO{Out: &out, ErrOut: &bytes.Buffer{}},
			loadedConfigPath: filepath.Join(tmp, "b.yaml"),
		},
	}
	err := o.Run(nil)
	if err == nil {
		t.Fatal("expected path-traversal error")
	}
	if !strings.Contains(err.Error(), "path traversal") {
		t.Errorf("error should mention path traversal, got: %v", err)
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
