package env

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fentas/b/pkg/envmatch"
	"github.com/fentas/b/pkg/lock"
)

// filterSha returns the sha256 of `filterContent(rawUpstream, selectors)`
// — i.e. the hash of the filtered upstream scope, NOT the full spliced
// target bytes that the post-fix SyncEnv now records in the lock.
//
// Tests use this to simulate the lock state a *pre-fix* sync would have
// left behind (just the filtered upstream sha) so they can exercise
// upgrade/regression scenarios where on-disk content drifts away from
// what the lock claims. With the lock SHA mismatching the on-disk file,
// SyncEnv's localChanged check fires and the merge/splice path runs.
//
// Per copilot review on PR #126 round 4: this helper is intentionally
// computing the OLD lock-format SHA so the test fixtures can simulate
// a pre-fix state. Don't "fix" it to match the new format.
func filterSha(t *testing.T, rawUpstream []byte, selectors []string, sourcePath string) string {
	t.Helper()
	filtered, err := filterContent(rawUpstream, selectors, sourcePath)
	if err != nil {
		t.Fatalf("filterContent: %v", err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(filtered))
}

// Tests for the select + merge interaction fix (issue #122).
//
// The bug: filterContent was only applied to the upstream side of the 3-way
// merge. The base (previous commit) and local (on-disk) sides were passed
// whole to git merge-file, so the text diff saw upstream as a massive
// deletion of everything outside the select scope. That clobbered or
// conflict-marked consumer-owned content (envs, profiles, comments).
//
// The fix: doMerge accepts a filterFn and applies it to all three sides.
// These tests prove the symmetric path works end-to-end against a real local
// bare git repo.

// --- test fixture builder ---

type mergeRepo struct {
	bare       string // bare clone (used as the env ref)
	baseCommit string // commit SHA of the first upstream version (used as lock base)
	sourcePath string // path of the file inside the repo
	baseRaw    []byte // raw upstream contents at baseCommit (for filterSha)
}

// setupMergeRepo creates a local bare repo with two commits of a .bin/b.yaml
// file. The first commit is the "base" (what the consumer last synced); the
// second adds a new binary "tilt". The caller uses baseCommit as the lock
// entry and the bare path as the env ref.
func setupMergeRepo(t *testing.T) mergeRepo {
	t.Helper()
	tmp := t.TempDir()
	work := filepath.Join(tmp, "work")
	bare := filepath.Join(tmp, "bare.git")
	run := func(args ...string) {
		t.Helper()
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	run("git", "init", "-q", work)
	run("git", "-C", work, "config", "user.email", "t@t.com")
	run("git", "-C", work, "config", "user.name", "T")
	run("git", "-C", work, "config", "commit.gpgsign", "false")

	if err := os.MkdirAll(filepath.Join(work, ".bin"), 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// Base version: binaries kubectl and kustomize, plus a profiles section
	// (which the consumer should never see, but which must be ignored by the
	// select filter without tripping the text merger).
	baseContent := `# Upstream b.yaml — managed by platform team
binaries:
  kubectl: {}
  kustomize: {}

profiles:
  core:
    description: core platform tools
    files:
      .bin/b.yaml:
        select:
          - binaries
`
	if err := os.WriteFile(filepath.Join(work, ".bin", "b.yaml"), []byte(baseContent), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	run("git", "-C", work, "add", "-A")
	run("git", "-C", work, "commit", "-m", "v1: base", "--no-gpg-sign")
	baseSHA := strings.TrimSpace(runOutput(t, "git", "-C", work, "rev-parse", "HEAD"))

	// Head version: upstream adds "tilt" to binaries. Consumer should pick
	// this up after a successful merge sync.
	headContent := `# Upstream b.yaml — managed by platform team
binaries:
  kubectl: {}
  kustomize: {}
  tilt: {}

profiles:
  core:
    description: core platform tools
    files:
      .bin/b.yaml:
        select:
          - binaries
`
	if err := os.WriteFile(filepath.Join(work, ".bin", "b.yaml"), []byte(headContent), 0644); err != nil {
		t.Fatalf("WriteFile v2: %v", err)
	}
	run("git", "-C", work, "commit", "-am", "v2: add tilt", "--no-gpg-sign")

	run("git", "clone", "--bare", "-q", work, bare)

	return mergeRepo{
		bare:       bare,
		baseCommit: baseSHA,
		sourcePath: ".bin/b.yaml",
		baseRaw:    []byte(baseContent),
	}
}

func runOutput(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command(args[0], args[1:]...).Output()
	if err != nil {
		t.Fatalf("%v: %v", args, err)
	}
	return string(out)
}

// buildLockEntryFromSha builds a lock entry with a hand-computed SHA — used
// when the test wants to simulate "previous sync stored hash X" rather than
// deriving it from the current file contents.
func buildLockEntryFromSha(commit, sourcePath, sha string) *lock.EnvEntry {
	return &lock.EnvEntry{
		Ref:    "local-repo",
		Commit: commit,
		Files: []lock.LockFile{{
			Path:   sourcePath,
			Dest:   sourcePath,
			SHA256: sha,
		}},
	}
}

// TestSyncEnv_SelectMerge_PreservesConsumerEnvs is the canonical
// reproduction of issue #122's data-loss footgun.
//
// Before the fix, a merge sync with `select: [binaries]` would:
//   - Filter upstream to just the binaries section
//   - Pass the (filtered) upstream, (unfiltered) base, (unfiltered) local
//     to git merge-file
//   - Get back a line-diff that treated "everything outside binaries" as a
//     massive deletion
//   - Write that result to disk, wiping the consumer's envs/profiles
//
// This test asserts that:
//  1. The consumer's out-of-scope `envs:` section survives the sync
//  2. A consumer-added binary inside the scope also survives (via splice)
//  3. An upstream addition is applied to the local file
//
// It does NOT assert zero conflicts — git merge-file is line-based, so
// concurrent adjacent inserts ("local added argsh, upstream added tilt")
// can still produce a spurious conflict marker inside the merged region.
// That's a separate problem (structural merge instead of text merge) that
// is tracked in the #122 follow-up work. The important property for #122
// is DATA PRESERVATION: even with a conflict marker in the binaries
// section, the consumer's envs must still be there for them to resolve.
func TestSyncEnv_SelectMerge_PreservesConsumerEnvs(t *testing.T) {
	repo := setupMergeRepo(t)
	project := t.TempDir()

	// Write the consumer's initial .bin/b.yaml. This is what the previous
	// sync of v1 would have produced, PLUS:
	//   - a consumer-added binary `argsh` inside the selected scope
	//   - an `envs:` block outside the selected scope
	//   - trailing comments and blank lines
	destFile := filepath.Join(project, ".bin", "b.yaml")
	if err := os.MkdirAll(filepath.Dir(destFile), 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	consumerInitial := `# Consumer b.yaml — do not edit binaries manually
binaries:
  kubectl: {}
  kustomize: {}
  argsh: {}

envs:
  github.com/example/extra:
    strategy: replace
    files:
      README.md:
        dest: docs/extra.md
`
	if err := os.WriteFile(destFile, []byte(consumerInitial), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Build a lock entry pinned to the base commit. The recorded hash is
	// what the *previous sync* would have written: filtered v1. The local
	// file on disk differs (consumer added argsh + envs), so SyncEnv's
	// localChanged check fires and the merge path runs.
	lockEntry := buildLockEntryFromSha(
		repo.baseCommit,
		repo.sourcePath,
		filterSha(t, repo.baseRaw, []string{"binaries"}, repo.sourcePath),
	)

	cfg := EnvConfig{
		Ref:      repo.bare,
		Strategy: StrategyMerge,
		Files: map[string]envmatch.GlobConfig{
			// Dest empty → destination mirrors source path under projectRoot.
			repo.sourcePath: {
				Select: []string{"binaries"},
			},
		},
	}

	if _, err := SyncEnv(cfg, project, t.TempDir(), lockEntry); err != nil {
		t.Fatalf("SyncEnv: %v", err)
	}
	// NOTE: we intentionally do not assert `result.Conflicts == 0`. The
	// text-based 3-way merge can produce a spurious conflict when local
	// and upstream both add a new entry in the same region of the
	// binaries map. That's a known limitation documented above.

	after, err := os.ReadFile(destFile)
	if err != nil {
		t.Fatalf("ReadFile after sync: %v", err)
	}
	afterStr := string(after)

	// (1) The new upstream binary `tilt` must be present in the file.
	if !strings.Contains(afterStr, "tilt") {
		t.Errorf("expected 'tilt' in file after sync, got:\n%s", afterStr)
	}

	// (2) The consumer-added `argsh` must survive. Before the fix it was
	// wiped because the whole local file got replaced by filtered upstream.
	if !strings.Contains(afterStr, "argsh") {
		t.Errorf("consumer-added 'argsh' was lost, got:\n%s", afterStr)
	}

	// (3) The consumer's `envs:` section must survive completely. This is
	// the data-loss scenario the issue describes and the core #122 fix.
	if !strings.Contains(afterStr, "github.com/example/extra") {
		t.Errorf("consumer 'envs:' section was lost, got:\n%s", afterStr)
	}
	if !strings.Contains(afterStr, "docs/extra.md") {
		t.Errorf("consumer env dest was lost, got:\n%s", afterStr)
	}
}

// TestSyncEnv_SelectMerge_NoLocalChanges_PreservesEnvs is the regression
// test for a bug copilot caught on PR #126: after a successful sync, the
// consumer's local file matches the previously-recorded lock entry, so
// the next sync sees `localChanged=false` and used to take the
// "no local changes — safe to replace" branch which wrote the *filtered*
// upstream content directly. That re-introduced the data loss the splice
// path was supposed to fix.
//
// The fix is to splice in the no-local-changes branch too. This test
// pins it: consumer has envs:, lock matches local file, sync runs with
// upstream changes inside the binaries scope, envs: must survive.
func TestSyncEnv_SelectMerge_NoLocalChanges_PreservesEnvs(t *testing.T) {
	repo := setupMergeRepo(t)
	project := t.TempDir()

	// Consumer's local file: starts as the v1 binaries (matching base),
	// PLUS an envs: section. This is the "no in-scope local changes,
	// only out-of-scope content" steady state after a previous sync.
	destFile := filepath.Join(project, ".bin", "b.yaml")
	if err := os.MkdirAll(filepath.Dir(destFile), 0755); err != nil {
		t.Fatal(err)
	}
	consumer := `binaries:
  kubectl: {}
  kustomize: {}

envs:
  github.com/keep/me:
    files:
      a.yaml:
        dest: a.yaml
`
	if err := os.WriteFile(destFile, []byte(consumer), 0644); err != nil {
		t.Fatal(err)
	}

	// Lock entry pinned to base. Crucially the recorded SHA matches the
	// CURRENT on-disk file (not the filtered upstream) — that's what
	// "no local changes since previous spliced sync" looks like in
	// practice. With the fix, this case must still preserve envs.
	lockEntry := &lock.EnvEntry{
		Ref:    "local-repo",
		Commit: repo.baseCommit,
		Files: []lock.LockFile{{
			Path: repo.sourcePath,
			Dest: repo.sourcePath,
			SHA256: func() string {
				h, _ := lock.SHA256File(destFile)
				return h
			}(),
		}},
	}

	cfg := EnvConfig{
		Ref: repo.bare,
		// Strategy doesn't really matter — the bug was in the
		// localChanged=false branch, which fires for ALL strategies.
		Strategy: StrategyMerge,
		Files: map[string]envmatch.GlobConfig{
			repo.sourcePath: {Select: []string{"binaries"}},
		},
	}

	if _, err := SyncEnv(cfg, project, t.TempDir(), lockEntry); err != nil {
		t.Fatalf("SyncEnv: %v", err)
	}

	after, err := os.ReadFile(destFile)
	if err != nil {
		t.Fatal(err)
	}
	afterStr := string(after)

	// Upstream change applied (tilt added).
	if !strings.Contains(afterStr, "tilt") {
		t.Errorf("expected 'tilt' from upstream, got:\n%s", afterStr)
	}
	// Consumer envs survived — the regression assertion.
	if !strings.Contains(afterStr, "github.com/keep/me") {
		t.Errorf("REGRESSION: consumer envs wiped on no-local-changes sync, got:\n%s", afterStr)
	}
}

// TestSyncEnv_SelectMerge_FallsBackWhenNoBase — when there is no previous
// commit (first sync with merge strategy), doMerge can't run and SyncEnv
// should fall through cleanly. We exercise this path by passing a lock
// entry with an empty Commit.
func TestSyncEnv_SelectMerge_FallsBackWhenNoBase(t *testing.T) {
	repo := setupMergeRepo(t)
	project := t.TempDir()

	destFile := filepath.Join(project, ".bin", "b.yaml")
	if err := os.MkdirAll(filepath.Dir(destFile), 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	// Consumer has a modified local file — would normally trigger merge.
	if err := os.WriteFile(destFile, []byte("binaries:\n  kubectl: {}\n  argsh: {}\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	// Lock entry with empty commit — no base available.
	lockEntry := &lock.EnvEntry{
		Ref:    "local-repo",
		Commit: "",
		Files: []lock.LockFile{{
			Path:   repo.sourcePath,
			Dest:   repo.sourcePath,
			SHA256: "different-hash-to-force-local-change-detection",
		}},
	}

	cfg := EnvConfig{
		Ref:      repo.bare,
		Strategy: StrategyMerge,
		Files: map[string]envmatch.GlobConfig{
			// Dest empty → destination mirrors source path under projectRoot.
			repo.sourcePath: {
				Select: []string{"binaries"},
			},
		},
	}

	// Must not error out. doMerge returns "no previous commit" which SyncEnv
	// catches and falls back to replace with a status note.
	if _, err := SyncEnv(cfg, project, t.TempDir(), lockEntry); err != nil {
		t.Errorf("SyncEnv: %v", err)
	}
}

// TestSyncEnv_SelectMerge_ValueChangeInsideScope — when the upstream
// bumps a value inside the selected scope (e.g. kubectl version v1.30 →
// v1.31) and the consumer hasn't touched the scoped section, the merge
// should apply cleanly (no conflict) and the consumer's out-of-scope
// content (envs:) must survive.
func TestSyncEnv_SelectMerge_ValueChangeInsideScope(t *testing.T) {
	tmp := t.TempDir()
	work := filepath.Join(tmp, "work")
	bare := filepath.Join(tmp, "bare.git")
	run := func(args ...string) {
		t.Helper()
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	run("git", "init", "-q", work)
	run("git", "-C", work, "config", "user.email", "t@t.com")
	run("git", "-C", work, "config", "user.name", "T")
	run("git", "-C", work, "config", "commit.gpgsign", "false")
	if err := os.MkdirAll(filepath.Join(work, ".bin"), 0755); err != nil {
		t.Fatal(err)
	}
	v1 := `binaries:
  kubectl:
    version: v1.30.0

profiles:
  core: {}
`
	if err := os.WriteFile(filepath.Join(work, ".bin", "b.yaml"), []byte(v1), 0644); err != nil {
		t.Fatal(err)
	}
	run("git", "-C", work, "add", "-A")
	run("git", "-C", work, "commit", "-m", "v1", "--no-gpg-sign")
	baseSHA := strings.TrimSpace(runOutput(t, "git", "-C", work, "rev-parse", "HEAD"))

	v2 := `binaries:
  kubectl:
    version: v1.31.0

profiles:
  core: {}
`
	if err := os.WriteFile(filepath.Join(work, ".bin", "b.yaml"), []byte(v2), 0644); err != nil {
		t.Fatal(err)
	}
	run("git", "-C", work, "commit", "-am", "v2", "--no-gpg-sign")
	run("git", "clone", "--bare", "-q", work, bare)

	project := t.TempDir()
	destFile := filepath.Join(project, ".bin", "b.yaml")
	if err := os.MkdirAll(filepath.Dir(destFile), 0755); err != nil {
		t.Fatal(err)
	}
	// Consumer's local file: same as v1 inside scope, plus an envs block.
	consumer := `binaries:
  kubectl:
    version: v1.30.0

envs:
  github.com/org/repo:
    files:
      README.md:
        dest: README.md
`
	if err := os.WriteFile(destFile, []byte(consumer), 0644); err != nil {
		t.Fatal(err)
	}

	lockEntry := buildLockEntryFromSha(
		baseSHA,
		".bin/b.yaml",
		filterSha(t, []byte(v1), []string{"binaries"}, ".bin/b.yaml"),
	)

	cfg := EnvConfig{
		Ref:      bare,
		Strategy: StrategyMerge,
		Files: map[string]envmatch.GlobConfig{
			".bin/b.yaml": {Select: []string{"binaries"}},
		},
	}

	result, err := SyncEnv(cfg, project, t.TempDir(), lockEntry)
	if err != nil {
		t.Fatalf("SyncEnv: %v", err)
	}
	if result.Conflicts > 0 {
		t.Errorf("expected no conflicts (upstream-only change), got %d", result.Conflicts)
	}

	after, err := os.ReadFile(destFile)
	if err != nil {
		t.Fatal(err)
	}
	afterStr := string(after)
	// Upstream version bump applied.
	if !strings.Contains(afterStr, "v1.31.0") {
		t.Errorf("expected upstream version v1.31.0 applied, got:\n%s", afterStr)
	}
	// Consumer envs survived.
	if !strings.Contains(afterStr, "github.com/org/repo") {
		t.Errorf("consumer envs lost, got:\n%s", afterStr)
	}
}
