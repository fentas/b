package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fentas/b/pkg/lock"
	"github.com/fentas/goodies/streams"
)

// b install writes the b.lock rows of each env that it syncs: the env of an
// SCP-style argument (ref:/glob dest) and the env of an env key of b.yaml,
// also when one run has both. The env key syncs after the SCP-style argument
// and reads b.lock from disk, so the rows of the SCP-style argument must be
// on disk first. An env key that fails gets its ✗ line and the run fails
// with the summary, but the row of the SCP-style argument stays.
func TestInstall_SCPAndEnvKeyKeepTheirLockRows(t *testing.T) {
	scp := setupLocalRepo(t)
	key := setupLocalRepo(t)
	missing := filepath.Join(t.TempDir(), "missing.git")
	scpArgs := []string{scp + ":/manifests/*.yaml", "from-scp"}

	for _, tc := range []struct {
		name     string
		args     []string
		wantRows map[string]string // ref -> the dest of its files
		wantErr  string            // "" for no error
	}{
		{"SCP-style argument only", scpArgs,
			map[string]string{scp: "from-scp"}, ""},
		{"env key only", []string{key},
			map[string]string{key: "from-key"}, ""},
		{"SCP-style argument and env key", append(append([]string{}, scpArgs...), key),
			map[string]string{scp: "from-scp", key: "from-key"}, ""},
		{"SCP-style argument and a failing env key", append(append([]string{}, scpArgs...), missing),
			map[string]string{scp: "from-scp"}, "1 env(s) failed: " + missing},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			project := t.TempDir()
			t.Setenv("PATH_BIN", filepath.Join(project, ".bin"))
			t.Setenv("PATH_BASE", project)
			cfg := filepath.Join(project, "b.yaml")
			mustWrite(t, cfg, []byte("envs:\n"+
				"  \""+key+"\":\n    files:\n      \"manifests/*.yaml\": {dest: from-key}\n"+
				"  \""+missing+"\":\n    files:\n      \"manifests/*.yaml\": {dest: from-missing}\n"))

			var out, errOut bytes.Buffer
			shared := NewSharedOptions(&streams.IO{In: strings.NewReader(""), Out: &out, ErrOut: &errOut}, mkBinaries())
			shared.ConfigPath = cfg
			if err := shared.LoadConfig(); err != nil {
				t.Fatal(err)
			}
			o := &InstallOptions{SharedOptions: shared}
			if err := o.Complete(tc.args); err != nil {
				t.Fatalf("Complete: %v", err)
			}
			err := o.Run()
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("Run: %v\nerror output:\n%s", err, errOut.String())
			case tc.wantErr != "" && (err == nil || err.Error() != tc.wantErr):
				t.Fatalf("Run error = %v, want %q", err, tc.wantErr)
			}
			// One ✗ line, for the failing env key, and none in the other cases.
			failed := 0
			if tc.wantErr != "" {
				failed = 1
			}
			if got := strings.Count(errOut.String(), "✗"); got != failed || failed == 1 && !strings.Contains(errOut.String(), missing) {
				t.Errorf("error output:\n%s\nwant %d ✗ line(s), for %s", errOut.String(), failed, missing)
			}

			lk, err := lock.ReadLock(project)
			if err != nil {
				t.Fatal(err)
			}
			if len(lk.Envs) != len(tc.wantRows) {
				t.Errorf("b.lock has %d env rows, want %d: %+v", len(lk.Envs), len(tc.wantRows), lk.Envs)
			}
			for ref, dest := range tc.wantRows {
				e := lk.FindEnv(ref, "")
				if e == nil {
					t.Errorf("b.lock has no row for %s", ref)
					continue
				}
				if e.Commit == "" || len(e.Files) != 2 {
					t.Errorf("b.lock row for %s: commit %q, %d files, want a commit and 2 files", ref, e.Commit, len(e.Files))
				}
				for _, f := range e.Files {
					if !strings.HasPrefix(f.Dest, dest+"/") {
						t.Errorf("b.lock row for %s has the file %s, want it under %s/", ref, f.Dest, dest)
					}
				}
			}
		})
	}
}
