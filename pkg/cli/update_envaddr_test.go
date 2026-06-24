package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fentas/b/pkg/binary"
	"github.com/fentas/b/pkg/env"
	"github.com/fentas/b/pkg/lock"
	"github.com/fentas/b/pkg/state"
	"github.com/fentas/goodies/streams"
)

// envAddrOpts builds an UpdateOptions whose config carries the given env keys
// and an empty preset set, ready for Complete() resolver tests.
func envAddrOpts(t *testing.T, envKeys ...string) (*UpdateOptions, *bytes.Buffer) {
	t.Helper()
	t.Setenv("PATH_BIN", t.TempDir())
	errBuf := &bytes.Buffer{}
	io := &streams.IO{Out: &bytes.Buffer{}, ErrOut: errBuf}
	shared := NewSharedOptions(io, nil)
	envs := make(state.EnvList, 0, len(envKeys))
	for _, k := range envKeys {
		envs = append(envs, &state.EnvEntry{Key: k})
	}
	shared.Config = &state.State{Envs: envs}
	return &UpdateOptions{SharedOptions: shared}, errBuf
}

// --- issue #166: env addressing ---

// The exact SSH key `b env status` prints must round-trip back to `b update`.
// Its '@' would be split by the binary parser, so it has to be recognized as
// an env (via the '#' marker) before that split happens.
func TestResolveArg_SSHEnvKey_RoundTrips(t *testing.T) {
	o, _ := envAddrOpts(t, "git@github.com:acme/framework#main")

	if err := o.Complete([]string{"git@github.com:acme/framework#main"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(o.specifiedBinaries) != 0 {
		t.Errorf("must not resolve to a binary, got %d", len(o.specifiedBinaries))
	}
	if len(o.specifiedEnvRefs) != 1 || o.specifiedEnvRefs[0] != "git@github.com:acme/framework#main" {
		t.Errorf("env refs = %v, want [git@github.com:acme/framework#main]", o.specifiedEnvRefs)
	}
}

// A label-less SSH env (no '#') still round-trips bare, via the env-exact
// membership match that runs before binary resolution.
func TestResolveArg_LabelLessSSHEnv_Bare(t *testing.T) {
	o, _ := envAddrOpts(t, "git@github.com:acme/framework")

	if err := o.Complete([]string{"git@github.com:acme/framework"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(o.specifiedEnvRefs) != 1 || o.specifiedEnvRefs[0] != "git@github.com:acme/framework" {
		t.Errorf("env refs = %v, want the SSH key", o.specifiedEnvRefs)
	}
}

// A trailing '#' forces env resolution for a key that is otherwise also a
// valid binary provider ref (the dual-namespace escape hatch).
func TestResolveArg_TrailingHashForcesEnv(t *testing.T) {
	o, _ := envAddrOpts(t, "github.com/org/infra")

	if err := o.Complete([]string{"github.com/org/infra#"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(o.specifiedBinaries) != 0 {
		t.Errorf("trailing '#' must force env, got %d binaries", len(o.specifiedBinaries))
	}
	if len(o.specifiedEnvRefs) != 1 || o.specifiedEnvRefs[0] != "github.com/org/infra" {
		t.Errorf("env refs = %v, want [github.com/org/infra]", o.specifiedEnvRefs)
	}
}

// An env-marked arg ('#') that matches no configured env is an error — the
// user was explicit, so we don't silently fall through to binary space.
func TestResolveArg_EnvMarkerNoMatch_Errors(t *testing.T) {
	o, _ := envAddrOpts(t, "github.com/org/infra")

	err := o.Complete([]string{"github.com/org/nope#main"})
	if err == nil || !strings.Contains(err.Error(), "unknown env") {
		t.Errorf("expected unknown env error, got: %v", err)
	}
}

// A short handle ('#'-marked) resolves to an env by repo basename or org/repo
// tail — even an SSH-keyed env with a label. Closes issue #166's "short handle"
// gap (Gemini review #1).
func TestResolveArg_ShortHandle(t *testing.T) {
	cases := []struct{ name, arg, want string }{
		{"basename", "lok8s#", "git@github.com:kernpilot/lok8s#main"},
		{"orgRepoTail", "kernpilot/lok8s#", "git@github.com:kernpilot/lok8s#main"},
		{"labelledHttps", "github.com/org/infra#", "github.com/org/infra#prod"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o, _ := envAddrOpts(t, "git@github.com:kernpilot/lok8s#main", "github.com/org/infra#prod")
			if err := o.Complete([]string{tc.arg}); err != nil {
				t.Fatalf("Complete(%q): %v", tc.arg, err)
			}
			if len(o.specifiedBinaries) != 0 {
				t.Errorf("%q must not resolve to a binary", tc.arg)
			}
			if len(o.specifiedEnvRefs) != 1 || o.specifiedEnvRefs[0] != tc.want {
				t.Errorf("%q → env refs %v, want [%s]", tc.arg, o.specifiedEnvRefs, tc.want)
			}
		})
	}
}

// An ambiguous short handle (matching two envs) errors with the candidates,
// rather than silently picking one.
func TestResolveArg_ShortHandle_Ambiguous(t *testing.T) {
	o, _ := envAddrOpts(t, "github.com/org/infra#a", "gitlab.com/org/infra#b")

	err := o.Complete([]string{"infra#"})
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected ambiguity error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "github.com/org/infra#a") ||
		!strings.Contains(err.Error(), "gitlab.com/org/infra#b") {
		t.Errorf("ambiguity error should list both candidates, got: %v", err)
	}
}

// A short handle whose org segment is wrong must NOT match — suffix matching
// compares whole segments, so `wrongorg/lok8s#` cannot reach kernpilot/lok8s.
func TestResolveArg_ShortHandle_WrongOrg(t *testing.T) {
	o, _ := envAddrOpts(t, "git@github.com:kernpilot/lok8s#main")

	err := o.Complete([]string{"wrongorg/lok8s#"})
	if err == nil || !strings.Contains(err.Error(), "unknown env") {
		t.Fatalf("wrong org should not match, want unknown env, got: %v", err)
	}
}

// Short-handle matching is case-insensitive.
func TestResolveArg_ShortHandle_CaseInsensitive(t *testing.T) {
	o, _ := envAddrOpts(t, "git@github.com:kernpilot/lok8s#main")

	if err := o.Complete([]string{"LOK8S#"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(o.specifiedEnvRefs) != 1 || o.specifiedEnvRefs[0] != "git@github.com:kernpilot/lok8s#main" {
		t.Errorf("case-insensitive handle → %v, want the SSH key", o.specifiedEnvRefs)
	}
}

// --plan-json with binaries configured but no envs must still emit a valid
// (empty) JSON array — binaries never appear in the plan (Copilot round-2).
func TestRunAll_PlanJSON_BinariesOnlyConfig_EmitsEmptyArray(t *testing.T) {
	t.Setenv("PATH_BIN", t.TempDir())
	out := &bytes.Buffer{}
	io := &streams.IO{Out: out, ErrOut: &bytes.Buffer{}}
	shared := NewSharedOptions(io, nil)
	shared.Config = &state.State{
		Binaries: state.BinaryList{
			&binary.LocalBinary{Name: "github.com/derailed/k9s", IsProviderRef: true},
		},
	}
	binariesRan := false
	o := &UpdateOptions{
		SharedOptions:   shared,
		PlanJSON:        true,
		updateBinariesF: func([]*binary.Binary) error { binariesRan = true; return nil },
	}

	if err := o.runAll(); err != nil {
		t.Fatalf("runAll: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != "[]" {
		t.Errorf("plan-json with binaries but no envs should emit '[]', got: %q", got)
	}
	if binariesRan {
		t.Error("--plan-json must not run binary updates (would corrupt the JSON)")
	}
}

// When two envs share an org/repo tail across hosts, the env hint must NOT
// suggest the ambiguous `tail#` short handle (it wouldn't resolve) — only the
// exact key.
func TestEnvUpdateHint_OmitsAmbiguousShortHandle(t *testing.T) {
	o, _ := envAddrOpts(t, "github.com/org/infra#a", "gitlab.com/org/infra#b")

	hint := o.envUpdateHint("github.com/org/infra#a")
	if !strings.Contains(hint, "github.com/org/infra#a") {
		t.Errorf("hint should cite the exact key, got: %q", hint)
	}
	if strings.Contains(hint, "or short") {
		t.Errorf("hint must not suggest the ambiguous short handle, got: %q", hint)
	}

	// A unique tail still gets the short-handle suggestion.
	o2, _ := envAddrOpts(t, "git@github.com:kernpilot/lok8s#main")
	if h := o2.envUpdateHint("git@github.com:kernpilot/lok8s#main"); !strings.Contains(h, "kernpilot/lok8s#") {
		t.Errorf("unique tail should suggest the short handle, got: %q", h)
	}
}

// A bare short name (no '#') must NOT match an env — it stays in binary space
// so plain `b update <name>` can never shadow a binary.
func TestResolveArg_BareShortName_NotEnv(t *testing.T) {
	o, _ := envAddrOpts(t, "git@github.com:kernpilot/lok8s#main")

	// 'lok8s' is not a preset/provider binary, so this should fail as a binary
	// lookup — crucially NOT silently resolve to the env.
	err := o.Complete([]string{"lok8s"})
	if err == nil || !strings.Contains(err.Error(), "unknown binary or env") {
		t.Fatalf("bare name should fall to binary space, got: %v", err)
	}
	if len(o.specifiedEnvRefs) != 0 {
		t.Errorf("bare name must not resolve to an env, got %v", o.specifiedEnvRefs)
	}
}

// The documented `b update github.com/org/infra` (no '#') must still resolve to
// the env when it is configured as one — not get hijacked into binary space by
// the github provider convention.
func TestResolveArg_HttpsEnvKey_NotHijackedByBinary(t *testing.T) {
	o, _ := envAddrOpts(t, "github.com/org/infra")

	if err := o.Complete([]string{"github.com/org/infra"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(o.specifiedBinaries) != 0 {
		t.Errorf("env key must not resolve as a binary, got %d", len(o.specifiedBinaries))
	}
	if len(o.specifiedEnvRefs) != 1 {
		t.Errorf("expected 1 env ref, got %d", len(o.specifiedEnvRefs))
	}
}

// Typing the https path of an SSH-keyed env resolves to an ad-hoc binary (a
// legitimate operation) but prints a heads-up pointing at the matching env.
func TestResolveArg_HttpsPathForSSHEnv_HintsEnv(t *testing.T) {
	o, errBuf := envAddrOpts(t, "git@github.com:acme/framework#main")

	if err := o.Complete([]string{"github.com/acme/framework"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(o.specifiedBinaries) != 1 {
		t.Fatalf("expected ad-hoc binary resolution, got %d", len(o.specifiedBinaries))
	}
	note := errBuf.String()
	if !strings.Contains(note, "git@github.com:acme/framework#main") {
		t.Errorf("note should cite the exact env key, got: %q", note)
	}
	// The hint must offer a WORKING form — a short handle — not "append '#'" to
	// the https arg, which would not match the SSH key (Copilot round-2).
	if !strings.Contains(note, "acme/framework#") {
		t.Errorf("note should suggest the working short handle, got: %q", note)
	}
}

// When several configured envs share the same repo tail, the ad-hoc-binary note
// must list all candidates rather than arbitrarily naming the first, and must
// not suggest an ambiguous short handle (Copilot round-3).
func TestResolveArg_HttpsPathForSSHEnv_HintsAllOnAmbiguousTail(t *testing.T) {
	o, errBuf := envAddrOpts(t, "git@github.com:org/infra#a", "gitlab.com/org/infra#b")

	if err := o.Complete([]string{"github.com/org/infra"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(o.specifiedBinaries) != 1 {
		t.Fatalf("expected ad-hoc binary resolution, got %d", len(o.specifiedBinaries))
	}
	note := errBuf.String()
	if !strings.Contains(note, "git@github.com:org/infra#a") || !strings.Contains(note, "gitlab.com/org/infra#b") {
		t.Errorf("note should list both candidate envs, got: %q", note)
	}
	if strings.Contains(note, "or short") {
		t.Errorf("note must not suggest an ambiguous short handle, got: %q", note)
	}
}

// A '#' inside a docker:// in-container path is a path character, not an env
// label — the ref must stay in binary space.
func TestResolveArg_DockerPathHash_StaysBinary(t *testing.T) {
	o, _ := envAddrOpts(t) // no envs

	if err := o.Complete([]string{"docker://alpine@3.19:/opt/we#rd/tool"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(o.specifiedEnvRefs) != 0 {
		t.Errorf("docker ref must not resolve as env, got %d", len(o.specifiedEnvRefs))
	}
	if len(o.specifiedBinaries) != 1 {
		t.Errorf("expected 1 binary, got %d", len(o.specifiedBinaries))
	}
}

// repoTail folds https and SSH forms of the same repo to one comparable tail.
func TestRepoTail(t *testing.T) {
	cases := map[string]string{
		"github.com/acme/framework":     "acme/framework",
		"git@github.com:acme/framework": "acme/framework",
		"github.com/acme/framework.git": "acme/framework",
		"ssh://git@host/acme/framework": "acme/framework",
		"single":                        "",
	}
	for in, want := range cases {
		if got := repoTail(in); got != want {
			t.Errorf("repoTail(%q) = %q, want %q", in, got, want)
		}
	}
}

// --- scope flags / --group implies env-only ---

func TestUpdateValidate_ScopeFlags(t *testing.T) {
	cases := []struct {
		name    string
		o       UpdateOptions
		wantErr string
	}{
		{"both", UpdateOptions{EnvsOnly: true, BinariesOnly: true}, "mutually exclusive"},
		{"binsAndGroup", UpdateOptions{BinariesOnly: true, Group: "dev"}, "cannot be combined"},
		{"binsAndPlanJSON", UpdateOptions{BinariesOnly: true, PlanJSON: true}, "no effect with --binaries-only"},
		{"scopeWithArgs", UpdateOptions{EnvsOnly: true, specifiedArgs: []string{"jq"}}, "no arguments"},
		{"groupWithArgs", UpdateOptions{Group: "dev", specifiedArgs: []string{"github.com/org/infra"}}, "no arguments"},
		{"envsOnlyOK", UpdateOptions{EnvsOnly: true}, ""},
		{"groupOK", UpdateOptions{Group: "dev"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.o.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Errorf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("Validate() = %v, want error containing %q", err, tc.wantErr)
			}
		})
	}
}

// scopeHarness wires the binary + env update hooks and returns flags to inspect
// which namespaces a runAll() invocation touched.
type scopeHarness struct {
	o           *UpdateOptions
	binariesRan *bool
	envsSynced  *[]string
}

func newScopeHarness(t *testing.T, o *UpdateOptions) scopeHarness {
	t.Helper()
	saveHooks(t)
	tmpDir := t.TempDir()
	if err := lock.WriteLock(tmpDir, &lock.Lock{}, "v1.0"); err != nil {
		t.Fatal(err)
	}

	binariesRan := false
	synced := []string{}
	syncEnvFunc = func(cfg env.EnvConfig, projectRoot, cacheRoot string, lockEntry *lock.EnvEntry) (*env.SyncResult, error) {
		synced = append(synced, cfg.Ref)
		return &env.SyncResult{
			Ref:     cfg.Ref,
			Commit:  "abc",
			Files:   []lock.LockFile{{Path: "a.yaml", Dest: "a.yaml", Status: "replaced"}},
			Message: "1 file(s) synced",
		}, nil
	}

	o.updateBinariesF = func(bins []*binary.Binary) error { binariesRan = true; return nil }
	o.Yes = true
	o.loadedConfigPath = filepath.Join(tmpDir, "b.yaml")
	o.bVersion = "v1.0"
	return scopeHarness{o: o, binariesRan: &binariesRan, envsSynced: &synced}
}

func scopeConfig() *state.State {
	return &state.State{
		Binaries: state.BinaryList{
			&binary.LocalBinary{Name: "github.com/derailed/k9s", IsProviderRef: true},
		},
		Envs: state.EnvList{
			{Key: "github.com/org/dev-config", Group: "dev"},
			{Key: "github.com/org/shared"},
		},
	}
}

func TestRunAll_EnvsOnly_SkipsBinaries(t *testing.T) {
	t.Setenv("PATH_BIN", t.TempDir())
	io := &streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}}
	shared := NewSharedOptions(io, nil)
	shared.Config = scopeConfig()
	h := newScopeHarness(t, &UpdateOptions{SharedOptions: shared, EnvsOnly: true})

	if err := h.o.runAll(); err != nil {
		t.Fatalf("runAll: %v", err)
	}
	if *h.binariesRan {
		t.Error("--envs-only must not run binaries")
	}
	if len(*h.envsSynced) != 2 {
		t.Errorf("expected both envs synced, got %v", *h.envsSynced)
	}
}

func TestRunAll_BinariesOnly_SkipsEnvs(t *testing.T) {
	t.Setenv("PATH_BIN", t.TempDir())
	io := &streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}}
	shared := NewSharedOptions(io, nil)
	shared.Config = scopeConfig()
	h := newScopeHarness(t, &UpdateOptions{SharedOptions: shared, BinariesOnly: true})

	if err := h.o.runAll(); err != nil {
		t.Fatalf("runAll: %v", err)
	}
	if !*h.binariesRan {
		t.Error("--binaries-only should run binaries")
	}
	if len(*h.envsSynced) != 0 {
		t.Errorf("--binaries-only must not sync envs, got %v", *h.envsSynced)
	}
}

// --group is env-only, so it must skip binaries (issue #166 claim 4) and only
// sync envs in that group.
func TestRunAll_GroupImpliesEnvsOnly(t *testing.T) {
	t.Setenv("PATH_BIN", t.TempDir())
	io := &streams.IO{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}}
	shared := NewSharedOptions(io, nil)
	shared.Config = scopeConfig()
	h := newScopeHarness(t, &UpdateOptions{SharedOptions: shared, Group: "dev"})

	if err := h.o.runAll(); err != nil {
		t.Fatalf("runAll: %v", err)
	}
	if *h.binariesRan {
		t.Error("--group must not run binaries (group is env-only)")
	}
	if len(*h.envsSynced) != 1 || (*h.envsSynced)[0] != "github.com/org/dev-config" {
		t.Errorf("expected only the dev env synced, got %v", *h.envsSynced)
	}
}
