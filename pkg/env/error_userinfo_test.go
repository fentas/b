package env

import (
	"strings"
	"testing"

	"github.com/fentas/b/pkg/envmatch"
)

// fakePassword is the password in the refs of these tests. It is not a real
// value.
const fakePassword = "fake-pw-value-for-tests"

// For a ref with a user part, the SyncEnv error shows "***@" in place of
// the user part, in its own text and in the text of gitcache: at the
// version resolve (env.go, "resolving") and, with a forced commit, at the
// clone (env.go, "cloning"). b refuses the URL, so no remote git command
// runs.
func TestSyncEnv_ErrorHidesTheUserPart(t *testing.T) {
	isolateGit(t)
	t.Setenv("GITHUB_TOKEN", "")
	ref := "user:" + fakePassword + "@github.com/org/repo"
	for _, tt := range []struct {
		force, prefix string
	}{
		{"", "resolving ***@github.com/org/repo@"},
		{"c0ffee", "cloning https://***@github.com/org/repo.git"},
	} {
		cfg := EnvConfig{
			Ref:         ref,
			Strategy:    StrategyReplace,
			Files:       map[string]envmatch.GlobConfig{"cfg/a.yaml": {}},
			ForceCommit: tt.force,
		}
		_, err := SyncEnv(cfg, t.TempDir(), t.TempDir(), nil)
		if err == nil {
			t.Fatalf("forced commit %q: SyncEnv passed for a ref with a password", tt.force)
		}
		msg := err.Error()
		if strings.Contains(msg, fakePassword) {
			t.Errorf("forced commit %q: the error shows the password", tt.force)
		}
		if !strings.HasPrefix(msg, tt.prefix) || !strings.Contains(msg, "refusing https://***@github.com/org/repo.git") {
			t.Errorf("forced commit %q: error %q, want the prefix %q and the refused URL with ***@", tt.force, msg, tt.prefix)
		}
	}
}
