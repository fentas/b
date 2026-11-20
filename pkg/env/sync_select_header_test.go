package env

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fentas/b/pkg/envmatch"
)

// The header comments above the first key of a consumer's file are
// out-of-scope content, and so is every key that the select does not name:
// a scoped sync must keep them, also when the scoped key is the first key of
// the file, and also for the select form of a profile.

const headerConsumerHeader = `# demo project, managed by b (github.com/fentas/b)
# Run ` + "`b install`" + ` to install binaries; ` + "`b update --envs-only`" + ` to sync
# the framework env.

`

const headerConsumerBinaries = `binaries:
    # Tools that the framework needs
    kubectl: {}
`

const headerConsumerEnvs = `envs:
    example.invalid/owner/repo:
        files:
            .bin/b.yaml: null
`

const headerUpstreamFile = `binaries:
  kubectl:
    groups: [kubeone]
  kubeone:
    groups: [kubeone]
  kind:
    groups: [local]
`

// setupHeaderRepo creates a local bare repo with one commit of .bin/b.yaml.
func setupHeaderRepo(t *testing.T) string {
	t.Helper()
	return setupHeaderRepoFiles(t, map[string]string{".bin/b.yaml": headerUpstreamFile})
}

// setupHeaderRepoFiles creates a local bare repo with one commit of files.
func setupHeaderRepoFiles(t *testing.T, files map[string]string) string {
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
	for name, content := range files {
		path := filepath.Join(work, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	run("git", "-C", work, "add", "-A")
	run("git", "-C", work, "commit", "-q", "-m", "v1", "--no-gpg-sign")
	run("git", "clone", "--bare", "-q", work, bare)
	return bare
}

func TestSyncEnv_ScopedSyncKeepsTheHeaderAndTheOutOfScopeKeys(t *testing.T) {
	selects := []struct {
		name string
		sel  string
	}{
		// The select form of a profile: its filter has dots ([1].groups).
		{"profile hash", "{binaries: from_items(items(binaries)[?[1].groups && contains([1].groups, 'kubeone')])}"},
		// A hash with no dot in it.
		{"plain hash", "{binaries: binaries}"},
		{"dot path", "binaries"},
	}
	layouts := []struct {
		name  string
		local string
	}{
		{"scoped key first", headerConsumerHeader + headerConsumerBinaries + headerConsumerEnvs},
		{"scoped key second", headerConsumerHeader + headerConsumerEnvs + headerConsumerBinaries},
	}
	for _, s := range selects {
		for _, l := range layouts {
			t.Run(s.name+"/"+l.name, func(t *testing.T) {
				bare := setupHeaderRepo(t)
				project := t.TempDir()
				dest := filepath.Join(project, ".bin", "b.yaml")
				if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
					t.Fatalf("MkdirAll: %v", err)
				}
				if err := os.WriteFile(dest, []byte(l.local), 0644); err != nil {
					t.Fatalf("WriteFile: %v", err)
				}

				cfg := EnvConfig{
					Ref:      bare,
					Strategy: StrategyReplace,
					Files: map[string]envmatch.GlobConfig{
						".bin/b.yaml": {Select: []string{s.sel}},
					},
				}
				if _, err := SyncEnv(cfg, project, t.TempDir(), nil); err != nil {
					t.Fatalf("SyncEnv: %v", err)
				}
				got, err := os.ReadFile(dest)
				if err != nil {
					t.Fatalf("ReadFile: %v", err)
				}
				out := string(got)

				if !strings.HasPrefix(out, headerConsumerHeader) {
					t.Errorf("the header above the first key is not kept byte for byte:\n%s", out)
				}
				if !strings.Contains(out, headerConsumerEnvs) {
					t.Errorf("the out-of-scope envs block is not kept byte for byte:\n%s", out)
				}
				if !strings.Contains(out, "kubeone:") {
					t.Errorf("the scoped key was not synced:\n%s", out)
				}
				if strings.Contains(out, "Tools that the framework needs") {
					t.Errorf("the scoped key kept its local body, so it was not replaced:\n%s", out)
				}
			})
		}
	}
}

// A simple dot-path below the top level keeps the legacy write: the splice
// would replace the whole top-level node with the truncated view.
func TestSyncEnv_NestedDotPathKeepsTheLegacyWrite(t *testing.T) {
	bare := setupHeaderRepo(t)
	project := t.TempDir()
	dest := filepath.Join(project, ".bin", "b.yaml")
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	local := headerConsumerHeader + headerConsumerBinaries + headerConsumerEnvs
	if err := os.WriteFile(dest, []byte(local), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg := EnvConfig{
		Ref:      bare,
		Strategy: StrategyReplace,
		Files: map[string]envmatch.GlobConfig{
			".bin/b.yaml": {Select: []string{"binaries.kubeone"}},
		},
	}
	if _, err := SyncEnv(cfg, project, t.TempDir(), nil); err != nil {
		t.Fatalf("SyncEnv: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Contains(string(got), "envs:") {
		t.Errorf("a nested dot-path was spliced; the legacy write must replace the file:\n%s", got)
	}
}

// A complex selector on a JSON file keeps the legacy write: the JSON splice
// takes simple dot-paths only and would refuse the sync.
func TestSyncEnv_ComplexSelectorOnJSONKeepsTheLegacyWrite(t *testing.T) {
	upstream := `{"binaries": {"kubectl": {"groups": ["kubeone"]}, "kind": {"groups": ["local"]}}}` + "\n"
	bare := setupHeaderRepoFiles(t, map[string]string{"cfg/b.json": upstream})
	project := t.TempDir()
	dest := filepath.Join(project, "cfg", "b.json")
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(dest, []byte(`{"binaries": {}, "other": 1}`+"\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg := EnvConfig{
		Ref:      bare,
		Strategy: StrategyReplace,
		Files: map[string]envmatch.GlobConfig{
			"cfg/b.json": {Select: []string{"{binaries: from_items(items(binaries)[?[1].groups && contains([1].groups, 'kubeone')])}"}},
		},
	}
	if _, err := SyncEnv(cfg, project, t.TempDir(), nil); err != nil {
		t.Fatalf("SyncEnv: %v (a complex selector on a JSON file must keep the legacy write)", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(got), "kubectl") || strings.Contains(string(got), "kind") {
		t.Errorf("the filtered content was not written:\n%s", got)
	}
}

// The structural fallback re-encodes the whole file. yaml.v3 keeps a header
// comment on the document node (with a blank line before the first key) or
// on the first key node (without one). The splice replaces only the value
// node, so both forms keep the header.
func TestSpliceYAMLStructural_KeepsTheHeaderAboveTheFirstScopedKey(t *testing.T) {
	merged := []byte("binaries:\n  kubeone: {}\n")
	locals := []struct{ name, local string }{
		{"blank line", "# header one\n# header two\n\nbinaries:\n  kubectl: {}\nenvs: {}\n"},
		{"no blank line", "# header one\n# header two\nbinaries:\n  kubectl: {}\nenvs: {}\n"},
	}
	for _, tc := range locals {
		t.Run(tc.name, func(t *testing.T) {
			out, err := spliceYAMLStructural([]byte(tc.local), merged, map[string]bool{"binaries": true})
			if err != nil {
				t.Fatalf("splice: %v", err)
			}
			s := string(out)
			if !strings.HasPrefix(s, "# header one\n# header two\n") {
				t.Errorf("the header is lost:\n%s", s)
			}
			if !strings.Contains(s, "kubeone") || strings.Contains(s, "kubectl") {
				t.Errorf("the scoped key was not replaced:\n%s", s)
			}
			if !strings.Contains(s, "envs:") {
				t.Errorf("the out-of-scope key is lost:\n%s", s)
			}
		})
	}
}
