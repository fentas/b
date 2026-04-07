package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fentas/b/pkg/lock"
)

// --- request.go ---

func TestRequestOptions(t *testing.T) {
	shared := NewSharedOptions(mkIO(), nil)
	o := &RequestOptions{SharedOptions: shared}

	if err := o.Complete([]string{}); err == nil {
		t.Error("expected error for empty args")
	}
	if err := o.Complete([]string{"foo"}); err != nil {
		t.Error(err)
	}
	if o.BinaryName != "foo" {
		t.Errorf("name = %q", o.BinaryName)
	}
	if err := o.Validate(); err != nil {
		t.Error(err)
	}
	o.BinaryName = ""
	if err := o.Validate(); err == nil {
		t.Error("expected error")
	}

	o.BinaryName = "jq"
	url := o.createIssueURL()
	if !strings.Contains(url, "Binary+Request") && !strings.Contains(url, "Binary%20Request") {
		t.Errorf("url = %q", url)
	}

	// NOTE: we intentionally do NOT call openURL/Run because they spawn
	// xdg-open and launch a real browser. createIssueURL is enough.
}

func TestNewRequestCmd(t *testing.T) {
	c := NewRequestCmd(NewSharedOptions(mkIO(), nil))
	c.SetArgs([]string{"--help"})
	if err := c.Execute(); err != nil {
		t.Errorf("Execute: %v", err)
	}
}

// --- search.go ---

func TestSearchOptions(t *testing.T) {
	io := mkIO()
	shared := NewSharedOptions(io, mkBinaries())
	o := &SearchOptions{SharedOptions: shared}

	if err := o.Complete([]string{"jq"}); err != nil {
		t.Error(err)
	}
	if o.Query != "jq" {
		t.Errorf("q = %q", o.Query)
	}
	if err := o.Validate(); err != nil {
		t.Error(err)
	}
	// Run goes through IO.Print which encodes to YAML by default — should succeed.
	if err := o.Run(); err != nil {
		t.Errorf("Run jq: %v", err)
	}

	// Empty query
	o.Query = ""
	if err := o.Run(); err != nil {
		t.Errorf("Run empty: %v", err)
	}

	// Query that triggers no-results hint
	o.Query = "nonexistent"
	if err := o.Run(); err != nil {
		t.Errorf("Run no-results: %v", err)
	}

	// Query shaped like a provider ref
	o.Query = "github.com/org/repo"
	if err := o.Run(); err != nil {
		t.Errorf("Run provider-ref: %v", err)
	}
}

// --- list.go ---

func TestListOptions_NoConfig(t *testing.T) {
	shared := NewSharedOptions(mkIO(), nil)
	o := &ListOptions{SharedOptions: shared}
	if err := o.Complete(nil); err != nil {
		t.Errorf("Complete: %v", err)
	}
	if err := o.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
	if err := o.Run(); err != nil {
		t.Error(err)
	}
}

func TestListOptions_WithConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH_BIN", filepath.Join(dir, ".bin"))
	shared := NewSharedOptions(mkIO(), mkBinaries())
	cfgYaml := filepath.Join(dir, "b.yaml")
	mustWrite(t, cfgYaml, []byte("binaries:\n  jq: {}\n"))
	shared.ConfigPath = cfgYaml
	if err := shared.LoadConfig(); err != nil {
		t.Fatal(err)
	}
	o := &ListOptions{SharedOptions: shared}
	if err := o.Run(); err != nil {
		t.Errorf("%v", err)
	}
}

func TestSearchCmd_Execute(t *testing.T) {
	c := NewSearchCmd(NewSharedOptions(mkIO(), mkBinaries()))
	c.SetArgs([]string{"jq"})
	if err := c.Execute(); err != nil {
		t.Errorf("Execute: %v", err)
	}
}

func TestListCmd_Execute(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH_BIN", filepath.Join(dir, ".bin"))
	c := NewListCmd(NewSharedOptions(mkIO(), mkBinaries()))
	c.SetArgs([]string{})
	if err := c.Execute(); err != nil {
		t.Errorf("Execute: %v", err)
	}
}

func TestInitCmd_Execute(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH_BIN", filepath.Join(dir, ".bin"))
	t.Chdir(dir)
	c := NewInitCmd(NewSharedOptions(mkIO(), nil))
	c.SetArgs([]string{})
	if err := c.Execute(); err != nil {
		t.Errorf("Execute: %v", err)
	}
}

func TestVersionCmd_Execute(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH_BIN", filepath.Join(dir, ".bin"))
	c := NewVersionCmd(NewSharedOptions(mkIO(), mkBinaries()))
	c.SetArgs([]string{"--local"})
	if err := c.Execute(); err != nil {
		t.Errorf("Execute: %v", err)
	}
}

func TestNewListCmd(t *testing.T) {
	c := NewListCmd(NewSharedOptions(mkIO(), nil))
	c.SetArgs([]string{"--help"})
	if err := c.Execute(); err != nil {
		t.Errorf("Execute: %v", err)
	}
}

// --- verify.go ---

func TestVerifyRun_WithEntries(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, ".bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	t.Setenv("PATH_BIN", binDir)

	// Real binary that matches
	goodContent := []byte("#!/bin/sh\necho hi")
	if err := os.WriteFile(filepath.Join(binDir, "jq"), goodContent, 0755); err != nil {
		t.Fatalf("WriteFile jq: %v", err)
	}
	goodHash, err := lock.SHA256File(filepath.Join(binDir, "jq"))
	if err != nil {
		t.Fatalf("SHA256File jq: %v", err)
	}

	// Env file: matching
	envPath := filepath.Join(dir, "configs", "a.yaml")
	if err := os.MkdirAll(filepath.Dir(envPath), 0755); err != nil {
		t.Fatalf("MkdirAll configs: %v", err)
	}
	if err := os.WriteFile(envPath, []byte("data"), 0644); err != nil {
		t.Fatalf("WriteFile envPath: %v", err)
	}
	envHash, err := lock.SHA256File(envPath)
	if err != nil {
		t.Fatalf("SHA256File envPath: %v", err)
	}

	lk := &lock.Lock{
		Version: 1,
		Binaries: []lock.BinEntry{
			{Name: "jq", Version: "v1", SHA256: goodHash},
			{Name: "missing-bin", Version: "v1", SHA256: "abc"},
			{Name: "kubectl", Version: "v1", SHA256: "wrong"}, // exists but mismatch
		},
		Envs: []lock.EnvEntry{
			{
				Ref: "/r", Files: []lock.LockFile{
					{Path: "a.yaml", Dest: "configs/a.yaml", SHA256: envHash, Mode: "100644"},
					{Path: "b.yaml", Dest: "missing/b.yaml", SHA256: "abc"},
					{Path: "c.yaml", Dest: "configs/a.yaml", SHA256: "wrong"}, // mismatch
				},
			},
		},
	}
	if err := lock.WriteLock(dir, lk, "test"); err != nil {
		t.Fatalf("WriteLock: %v", err)
	}

	// Create kubectl with different content
	if err := os.WriteFile(filepath.Join(binDir, "kubectl"), []byte("different"), 0755); err != nil {
		t.Fatalf("WriteFile kubectl: %v", err)
	}

	shared := NewSharedOptions(mkIO(), nil)
	shared.ConfigPath = filepath.Join(dir, "b.yaml")
	o := &VerifyOptions{SharedOptions: shared}
	// Lock has intentional mismatches/missing files — Run should report failures.
	if err := o.Run(); err == nil {
		t.Error("expected verify to return an error for mismatched/missing entries")
	}
}

func TestVerifyRun_NoLock(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH_BIN", filepath.Join(dir, ".bin"))
	shared := NewSharedOptions(mkIO(), nil)
	shared.ConfigPath = filepath.Join(dir, "b.yaml")
	o := &VerifyOptions{SharedOptions: shared}
	if err := o.Run(); err != nil {
		t.Errorf("Run: %v", err)
	}
}

func TestNewVerifyCmd(t *testing.T) {
	c := NewVerifyCmd(NewSharedOptions(mkIO(), nil))
	c.SetArgs([]string{"--help"})
	if err := c.Execute(); err != nil {
		t.Errorf("Execute: %v", err)
	}
}

// --- version ---

func TestVersionOptions_Run_Local(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, ".bin")
	mustMkdir(t, binDir)
	mustWriteExec(t, filepath.Join(binDir, "jq"), []byte("#!/bin/sh\n"))
	t.Setenv("PATH_BIN", binDir)

	shared := NewSharedOptions(mkIO(), mkBinaries())
	o := &VersionOptions{SharedOptions: shared, Local: true}
	if err := o.Complete(nil); err != nil {
		t.Errorf("Complete nil: %v", err)
	}
	if err := o.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
	if err := o.Run(); err != nil {
		t.Errorf("Run all: %v", err)
	}

	// With specific args
	if err := o.Complete([]string{"jq"}); err != nil {
		t.Errorf("Complete jq: %v", err)
	}
	if err := o.Run(); err != nil {
		t.Errorf("Run jq: %v", err)
	}

	// Unknown binary
	if err := o.Complete([]string{"totally-unknown"}); err == nil {
		t.Error("expected error")
	}
}

// --- install.go ---

func TestNewInstallCmd_Help(t *testing.T) {
	c := NewInstallCmd(NewSharedOptions(mkIO(), nil))
	c.SetArgs([]string{"--help"})
	if err := c.Execute(); err != nil {
		t.Errorf("Execute: %v", err)
	}
}

// --- env match with local repo ---

func setupLocalRepo(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	work := filepath.Join(tmp, "repo")
	bare := filepath.Join(tmp, "bare.git")
	run := func(args ...string) {
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	run("git", "init", "-q", work)
	run("git", "-C", work, "config", "user.email", "t@t.com")
	run("git", "-C", work, "config", "user.name", "T")
	run("git", "-C", work, "config", "commit.gpgsign", "false")
	mustMkdir(t, filepath.Join(work, "manifests"))
	mustWrite(t, filepath.Join(work, "manifests", "a.yaml"), []byte("a: 1\n"))
	mustWrite(t, filepath.Join(work, "manifests", "b.yaml"), []byte("b: 2\n"))
	run("git", "-C", work, "add", "-A")
	run("git", "-C", work, "commit", "-m", "init", "--no-gpg-sign")
	run("git", "clone", "--bare", "-q", work, bare)
	return bare
}

func TestEnvMatchOptions_Run_Local(t *testing.T) {
	bare := setupLocalRepo(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	shared := NewSharedOptions(mkIO(), nil)
	o := &EnvMatchOptions{SharedOptions: shared}
	if err := o.Run([]string{bare, "manifests/*.yaml"}); err != nil {
		t.Errorf("Run: %v", err)
	}
	// Run again with dest, no match
	if err := o.Run([]string{bare, "no/such/*", "dest"}); err != nil {
		t.Errorf("Run no-match: %v", err)
	}
	// Run with dest for matched files
	if err := o.Run([]string{bare, "manifests/*.yaml", "out"}); err != nil {
		t.Errorf("Run with dest: %v", err)
	}
}

func setupLocalRepoWithBYaml(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	work := filepath.Join(tmp, "repo")
	bare := filepath.Join(tmp, "bare.git")
	run := func(args ...string) {
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	run("git", "init", "-q", work)
	run("git", "-C", work, "config", "user.email", "t@t.com")
	run("git", "-C", work, "config", "user.name", "T")
	run("git", "-C", work, "config", "commit.gpgsign", "false")
	mustMkdir(t, filepath.Join(work, "manifests"))
	mustWrite(t, filepath.Join(work, "manifests", "a.yaml"), []byte("a: 1\n"))
	mustWrite(t, filepath.Join(work, "b.yaml"), []byte(`profiles:
  base:
    description: Base profile
    files:
      "manifests/*.yaml": {dest: "manifests"}
`))
	run("git", "-C", work, "add", "-A")
	run("git", "-C", work, "commit", "-m", "init", "--no-gpg-sign")
	run("git", "clone", "--bare", "-q", work, bare)
	return bare
}

func TestEnvAddOptions_Run_MissingLabel(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	shared := NewSharedOptions(mkIO(), nil)
	o := &EnvAddOptions{SharedOptions: shared}
	// Remote-looking ref without #label
	if err := o.Run("github.com/org/repo"); err == nil {
		t.Error("expected missing-label error")
	}
}

func TestEnvAddOptions_Run_Interactive_NotTTY(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	shared := NewSharedOptions(mkIO(), nil)
	o := &EnvAddOptions{SharedOptions: shared, Interactive: true}
	// Interactive mode requires a TTY — non-TTY should error
	if err := o.Run("github.com/org/repo"); err == nil {
		t.Error("expected TTY error")
	}
}

func TestEnvAddOptions_FetchUpstreamAndFindProfile(t *testing.T) {
	bare := setupLocalRepoWithBYaml(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	shared := NewSharedOptions(mkIO(), nil)
	o := &EnvAddOptions{SharedOptions: shared}
	up, err := o.fetchUpstream(bare, "", bare)
	if err != nil {
		t.Fatalf("fetchUpstream: %v", err)
	}
	if len(up.Profiles) == 0 {
		t.Fatal("expected profiles")
	}
	// findProfile: hit
	if _, err := o.findProfile("base", bare, up); err != nil {
		t.Errorf("findProfile hit: %v", err)
	}
	// findProfile: miss (available list shown)
	if _, err := o.findProfile("missing", bare, up); err == nil {
		t.Error("expected error")
	}
	// findProfile: no profiles at all
	empty := up
	empty.Profiles = nil
	if _, err := o.findProfile("x", bare, empty); err == nil {
		t.Error("expected error")
	}
}

func TestEnvProfilesOptions_Run_WithBYaml(t *testing.T) {
	bare := setupLocalRepoWithBYaml(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	shared := NewSharedOptions(mkIO(), nil)
	o := &EnvProfilesOptions{SharedOptions: shared}
	if err := o.Run(bare); err != nil {
		t.Errorf("%v", err)
	}
}

func TestEnvProfilesOptions_Run_Local(t *testing.T) {
	bare := setupLocalRepo(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	shared := NewSharedOptions(mkIO(), nil)
	o := &EnvProfilesOptions{SharedOptions: shared}
	// No upstream b.yaml — Run prints "No profiles found" but should not error.
	if err := o.Run(bare); err != nil {
		t.Errorf("Run: %v", err)
	}
}

// --- install.Run with existing binary ---

func TestInstallOptions_Run_NoBinariesNoEnvs(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH_BIN", filepath.Join(dir, ".bin"))
	t.Setenv("PATH_BASE", dir)
	cfgPath := filepath.Join(dir, "b.yaml")
	mustWrite(t, cfgPath, []byte("binaries: {}\n"))
	shared := NewSharedOptions(mkIO(), mkBinaries())
	shared.ConfigPath = cfgPath
	if err := shared.LoadConfig(); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	o := &InstallOptions{SharedOptions: shared}
	if err := o.Complete(nil); err != nil {
		t.Fatal(err)
	}
	if err := o.Run(); err != nil {
		t.Errorf("%v", err)
	}
}

func TestInstallOptions_AddEnvToConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH_BIN", filepath.Join(dir, ".bin"))
	cfgYaml := filepath.Join(dir, "b.yaml")
	mustWrite(t, cfgYaml, []byte("envs: {}\n"))
	shared := NewSharedOptions(mkIO(), nil)
	shared.ConfigPath = cfgYaml
	if err := shared.LoadConfig(); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	o := &InstallOptions{SharedOptions: shared}
	ei := envInstall{
		ref:     "github.com/org/repo",
		version: "v1",
		glob:    "manifests/**",
		dest:    "out",
	}
	if err := o.addEnvToConfig(ei); err != nil {
		t.Errorf("%v", err)
	}
	// Second call should update the existing entry
	if err := o.addEnvToConfig(ei); err != nil {
		t.Errorf("%v", err)
	}
}

// --- discoverUpstreamConfig ---

func TestInstallOptions_DiscoverUpstreamConfig(t *testing.T) {
	bare := setupLocalRepoWithBYaml(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	shared := NewSharedOptions(mkIO(), nil)
	o := &InstallOptions{SharedOptions: shared}
	out := o.discoverUpstreamConfig(bare)
	if out == "" {
		t.Error("expected non-empty hint")
	}

	// Unknown ref → empty
	if o.discoverUpstreamConfig("/nonexistent/repo/path") != "" {
		t.Error("expected empty for unknown ref")
	}
}

// --- install syncConfigEnvs via config ---

func TestInstallOptions_SyncConfigEnvs(t *testing.T) {
	bare := setupLocalRepo(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	project := t.TempDir()
	t.Setenv("PATH_BIN", filepath.Join(project, ".bin"))
	t.Setenv("PATH_BASE", project)

	cfgYaml := filepath.Join(project, "b.yaml")
	yaml := `envs:
  "` + bare + `":
    files:
      "manifests/*.yaml": {dest: "out"}
`
	mustWrite(t, cfgYaml, []byte(yaml))
	shared := NewSharedOptions(mkIO(), mkBinaries())
	shared.ConfigPath = cfgYaml
	if err := shared.LoadConfig(); err != nil {
		t.Fatal(err)
	}
	o := &InstallOptions{SharedOptions: shared}
	if err := o.Complete(nil); err != nil {
		t.Fatal(err)
	}
	if err := o.Run(); err != nil {
		t.Errorf("Run: %v", err)
	}
	// Also test syncConfigEnvs with specific refs
	if err := o.syncConfigEnvs([]string{bare}); err != nil {
		t.Errorf("syncConfigEnvs: %v", err)
	}
	// syncConfigEnvs with filter that doesn't match
	if err := o.syncConfigEnvs([]string{"nonexistent"}); err != nil {
		t.Errorf("syncConfigEnvs filter: %v", err)
	}
	// With no config at all
	o2 := &InstallOptions{SharedOptions: NewSharedOptions(mkIO(), nil)}
	if err := o2.syncConfigEnvs(nil); err != nil {
		t.Errorf("no-config: %v", err)
	}
}

// --- install Run with local env config ---

func TestInstallOptions_RunEnvInstalls_Local(t *testing.T) {
	bare := setupLocalRepo(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	project := t.TempDir()
	t.Setenv("PATH_BIN", filepath.Join(project, ".bin"))
	t.Setenv("PATH_BASE", project)

	shared := NewSharedOptions(mkIO(), nil)
	shared.ConfigPath = filepath.Join(project, "b.yaml")
	o := &InstallOptions{SharedOptions: shared}
	// Directly set envInstalls for the local path
	o.envInstalls = []envInstall{{
		ref:  bare,
		glob: "manifests/*.yaml",
		dest: "out",
	}}
	if err := o.runEnvInstalls(); err != nil {
		t.Errorf("runEnvInstalls: %v", err)
	}
}

// --- env subcommands help ---

func TestNewEnvCmd_Subcommands(t *testing.T) {
	c := NewEnvCmd(NewSharedOptions(mkIO(), nil))
	for _, sub := range []string{"status", "remove", "match", "profiles", "add"} {
		c.SetArgs([]string{sub, "--help"})
		if err := c.Execute(); err != nil {
			t.Errorf("Execute: %v", err)
		}
	}
}

// --- cli.go CmdBinaryOptions Complete / Validate / Run ---

func TestCmdBinaryOptions_Complete_UnknownArg(t *testing.T) {
	opts := &CmdBinaryOptions{
		IO:       mkIO(),
		Binaries: mkBinaries(),
	}
	c := NewCmdBinary(opts)
	if err := opts.Complete(c, []string{"unknown-binary"}); err == nil {
		t.Error("expected error")
	}
}

func TestCmdBinaryOptions_Complete_AllWithArgs(t *testing.T) {
	opts := &CmdBinaryOptions{
		IO:       mkIO(),
		Binaries: mkBinaries(),
	}
	c := NewCmdBinary(opts)
	opts.all = true
	if err := opts.Complete(c, []string{"jq"}); err == nil {
		t.Error("expected error")
	}
}

func TestCmdBinaryOptions_Complete_Known(t *testing.T) {
	opts := &CmdBinaryOptions{
		IO:       mkIO(),
		Binaries: mkBinaries(),
	}
	c := NewCmdBinary(opts)
	if err := opts.Complete(c, []string{"jq"}); err != nil {
		t.Errorf("%v", err)
	}
}

func TestCmdBinaryOptions_Validate(t *testing.T) {
	opts := &CmdBinaryOptions{IO: mkIO(), Binaries: mkBinaries()}
	c := NewCmdBinary(opts)
	if err := opts.Validate(c); err == nil {
		t.Error("expected error for no flags")
	}
}

func TestCmdBinaryOptions_Run_CheckMode(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH_BIN", filepath.Join(dir, ".bin"))
	opts := &CmdBinaryOptions{IO: mkIO(), Binaries: mkBinaries()}
	c := NewCmdBinary(opts)
	opts.check = true
	// No ensure set; check mode with 0 locals is a no-op and should succeed.
	if err := opts.Complete(c, nil); err != nil {
		t.Errorf("Complete: %v", err)
	}
	if err := opts.Run(); err != nil {
		t.Errorf("Run: %v", err)
	}
}

func TestCmdBinaryOptions_Run_Available(t *testing.T) {
	opts := &CmdBinaryOptions{IO: mkIO(), Binaries: mkBinaries()}
	_ = NewCmdBinary(opts)
	opts.available = true
	if err := opts.Run(); err != nil {
		t.Error(err)
	}
}

// --- helper.go installBinaries / lookupLocals (indirectly) ---

func TestCmdBinaryOptions_RunLookupLocals(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PATH_BIN", tmp)
	opts := &CmdBinaryOptions{IO: mkIO(), Binaries: mkBinaries()}
	_ = NewCmdBinary(opts)
	// With no flags set, it will call lookupLocals
	if err := opts.Run(); err != nil {
		t.Errorf("%v", err)
	}
}

// --- init error paths ---

func TestInit_GitignoreSkip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH_BIN", filepath.Join(dir, ".bin"))
	t.Chdir(dir)

	// Pre-create .bin/.gitignore so the create path takes the skip branch
	mustMkdir(t, filepath.Join(dir, ".bin"))
	mustWrite(t, filepath.Join(dir, ".bin", ".gitignore"), []byte("existing"))
	mustWrite(t, filepath.Join(dir, ".envrc"), []byte("existing"))

	shared := NewSharedOptions(mkIO(), nil)
	o := &InitOptions{SharedOptions: shared}
	if err := o.Run(); err != nil {
		t.Errorf("%v", err)
	}
}

// --- shared.go LoadConfig path ---

func TestSharedOptions_LoadConfig_Missing(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("PATH_BIN", filepath.Join(dir, ".bin"))
	so := NewSharedOptions(mkIO(), nil)
	_ = so.LoadConfig() // may error if no config — that's fine
}

func TestSharedOptions_LoadConfig_WithPath(t *testing.T) {
	dir := t.TempDir()
	yaml := filepath.Join(dir, "b.yaml")
	mustWrite(t, yaml, []byte("binaries:\n  jq: {}\n"))
	so := NewSharedOptions(mkIO(), nil)
	so.ConfigPath = yaml
	if err := so.LoadConfig(); err != nil {
		t.Errorf("%v", err)
	}
}

// --- cache runClean ---

func TestCacheOptions_RunClean_Empty(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	o := &CacheOptions{SharedOptions: NewSharedOptions(mkIO(), nil)}
	if err := o.runClean(); err != nil {
		t.Errorf("%v", err)
	}
}

func TestCacheOptions_RunClean_NonEmpty(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	mustMkdir(t, filepath.Join(home, ".cache", "b", "repos", "x"))
	mustWrite(t, filepath.Join(home, ".cache", "b", "repos", "x", "y"), []byte("hello"))
	o := &CacheOptions{SharedOptions: NewSharedOptions(mkIO(), nil)}
	if err := o.runClean(); err != nil {
		t.Errorf("%v", err)
	}
}

// --- install Complete ---

func TestInstallOptions_Complete_NoArgs_NoConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH_BIN", filepath.Join(dir, ".bin"))
	shared := NewSharedOptions(mkIO(), mkBinaries())
	o := &InstallOptions{SharedOptions: shared}
	if err := o.Complete(nil); err == nil {
		t.Error("expected no-config error")
	}
}

func TestInstallOptions_Complete_KnownBinary(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH_BIN", filepath.Join(dir, ".bin"))
	shared := NewSharedOptions(mkIO(), mkBinaries())
	o := &InstallOptions{SharedOptions: shared}
	if err := o.Complete([]string{"jq"}); err != nil {
		t.Errorf("%v", err)
	}
	if err := o.Validate(); err != nil {
		t.Error(err)
	}
}

func TestInstallOptions_Complete_UnknownBinary(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH_BIN", filepath.Join(dir, ".bin"))
	shared := NewSharedOptions(mkIO(), mkBinaries())
	o := &InstallOptions{SharedOptions: shared}
	if err := o.Complete([]string{"totally-unknown-binary-xyz"}); err == nil {
		t.Error("expected unknown-binary error")
	}
}

// --- root.go Execute ---

func TestExecute_Help(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH_BIN", filepath.Join(dir, ".bin"))
	t.Chdir(dir)
	// Swap os.Args so cobra processes a safe command
	oldArgs := os.Args
	os.Args = []string{"b", "--help"}
	defer func() { os.Args = oldArgs }()
	if err := Execute(mkBinaries(), mkIO(), "dev", ""); err != nil {
		t.Errorf("Execute: %v", err)
	}
}

func TestSharedOptions_GetBinary(t *testing.T) {
	so := NewSharedOptions(mkIO(), mkBinaries())
	if _, ok := so.GetBinary("jq"); !ok {
		t.Error("expected jq")
	}
	if _, ok := so.GetBinary("unknown"); ok {
		t.Error("should not match")
	}
}
