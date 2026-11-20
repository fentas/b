package env

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fentas/b/pkg/envmatch"
)

// mergeStub is a ConflictFunc that always returns merge and records the
// mergeOK value of each call.
type mergeStub struct {
	calls   int
	mergeOK []bool
}

func (s *mergeStub) resolve(_, _ string, mergeOK bool) string {
	s.calls++
	s.mergeOK = append(s.mergeOK, mergeOK)
	return StrategyMerge
}

// TestSyncEnv_ResolverMergeRefusedForDottedSelector: under strategy
// replace, the conflict resolver (the TTY prompt) can return merge. For a
// selector that the merge gate refuses, SyncEnv must stop with the gate's
// error, wrapped with the cause, and leave the file as it was. Before the fix, the merge ran and
// the splice replaced the whole top-level node with the truncated
// selected view, so `database.port` and the other siblings were lost.
func TestSyncEnv_ResolverMergeRefusedForDottedSelector(t *testing.T) {
	cases := []struct {
		name     string
		path     string
		upstream string
		local    string
		sel      string
	}{
		{
			name:     "nested path on YAML",
			path:     "config/app.yaml",
			upstream: "# app config\ndatabase:\n  host: db.internal\n  port: 5432\ncache:\n  ttl: 60\n",
			local:    "# app config\ndatabase:\n  host: db.local\n  port: 6543\n  user: app\ncache:\n  ttl: 60\nextra: kept\n",
			sel:      "database.host",
		},
		{
			name:     "nested path on JSON",
			path:     "config/app.json",
			upstream: "{\"database\": {\"host\": \"db.internal\", \"port\": 5432}}\n",
			local:    "{\"database\": {\"host\": \"db.local\", \"port\": 6543}, \"extra\": \"kept\"}\n",
			sel:      "database.host",
		},
		{
			name:     "filter in a hash on YAML",
			path:     "config/app.yaml",
			upstream: "database:\n  host: db.internal\n  port: 5432\ncache:\n  ttl: 60\n",
			local:    "database:\n  host: db.local\n  port: 6543\ncache:\n  ttl: 30\nextra: kept\n",
			sel:      "{database: database, cache: cache.ttl}",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bare := setupHeaderRepoFiles(t, map[string]string{tc.path: tc.upstream})
			commit := strings.TrimSpace(runOutput(t, "git", "-C", bare, "rev-parse", "HEAD"))
			files := map[string]envmatch.GlobConfig{tc.path: {Select: []string{tc.sel}}}

			// The error that the strategy gate gives for this file.
			_, gateErr := SyncEnv(EnvConfig{Ref: bare, Strategy: StrategyMerge, Files: files},
				t.TempDir(), t.TempDir(), nil)
			if gateErr == nil {
				t.Fatal("strategy: merge accepted the selector; the case does not test the gate")
			}

			project := t.TempDir()
			destFile := filepath.Join(project, filepath.FromSlash(tc.path))
			if err := os.MkdirAll(filepath.Dir(destFile), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(destFile, []byte(tc.local), 0644); err != nil {
				t.Fatal(err)
			}
			// A lock hash that differs from the file: the file has local
			// changes, so SyncEnv asks the resolver.
			lockEntry := buildLockEntryFromSha(commit, tc.path, "stale")

			stub := &mergeStub{}
			cfg := EnvConfig{
				Ref:             bare,
				Strategy:        StrategyReplace,
				Files:           files,
				ResolveConflict: stub.resolve,
			}
			res, err := SyncEnv(cfg, project, t.TempDir(), lockEntry)
			after, readErr := os.ReadFile(destFile)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if !bytes.Equal(after, []byte(tc.local)) {
				t.Errorf("the file changed:\n%s\nwant it unchanged:\n%s", after, tc.local)
			}
			if stub.calls != 1 || stub.mergeOK[0] {
				t.Errorf("resolver calls = %d, mergeOK = %v; want one call with mergeOK false", stub.calls, stub.mergeOK)
			}
			if err == nil {
				t.Fatalf("SyncEnv accepted a merge for %q, result files: %+v", tc.sel, res.Files)
			}
			// The env uses replace, so the error names the prompt as the
			// cause and wraps the strategy gate's error (%w: errors.Is
			// and errors.Unwrap reach it).
			const cause = "the conflict prompt chose merge: "
			if !strings.HasPrefix(err.Error(), cause) {
				t.Errorf("error = %q, want the prefix %q", err, cause)
			}
			if inner := errors.Unwrap(err); inner == nil || inner.Error() != gateErr.Error() {
				t.Errorf("errors.Unwrap(error) = %v, want the strategy gate's error %q", inner, gateErr)
			}
		})
	}
}

// TestSyncEnv_ResolverMergeTopLevelSelectorMerges: a top-level selector
// keeps the merge choice. The resolver is told that merge is allowed, and
// a merge that it returns runs the three-way merge: the consumer's edit
// inside the scope stays (a replace would overwrite it), the upstream
// change arrives, and the content outside the scope stays.
func TestSyncEnv_ResolverMergeTopLevelSelectorMerges(t *testing.T) {
	repo := setupMergeRepo(t)
	project := t.TempDir()
	destFile := filepath.Join(project, ".bin", "b.yaml")
	if err := os.MkdirAll(filepath.Dir(destFile), 0755); err != nil {
		t.Fatal(err)
	}
	local := `# Consumer b.yaml
binaries:
  kubectl:
    version: v1.30.0
  kustomize: {}

envs:
  github.com/example/extra:
    strategy: replace
`
	if err := os.WriteFile(destFile, []byte(local), 0644); err != nil {
		t.Fatal(err)
	}
	lockEntry := buildLockEntryFromSha(
		repo.baseCommit,
		repo.sourcePath,
		filterSha(t, repo.baseRaw, []string{"binaries"}, repo.sourcePath),
	)

	stub := &mergeStub{}
	cfg := EnvConfig{
		Ref:             repo.bare,
		Strategy:        StrategyReplace,
		Files:           map[string]envmatch.GlobConfig{repo.sourcePath: {Select: []string{"binaries"}}},
		ResolveConflict: stub.resolve,
	}
	res, err := SyncEnv(cfg, project, t.TempDir(), lockEntry)
	if err != nil {
		t.Fatalf("SyncEnv: %v", err)
	}
	if stub.calls != 1 || !stub.mergeOK[0] {
		t.Errorf("resolver calls = %d, mergeOK = %v; want one call with mergeOK true", stub.calls, stub.mergeOK)
	}
	if len(res.Files) != 1 || res.Files[0].Status != "merged" {
		t.Errorf("result files = %+v, want one file with status merged", res.Files)
	}
	after, err := os.ReadFile(destFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"v1.30.0", "tilt", "github.com/example/extra", "# Consumer b.yaml"} {
		if !strings.Contains(string(after), want) {
			t.Errorf("file after the merge has no %q:\n%s", want, after)
		}
	}
}
