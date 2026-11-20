package cli

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/fentas/b/pkg/env"
	"github.com/fentas/b/pkg/envmatch"
	"github.com/fentas/b/pkg/lock"
	"github.com/fentas/goodies/streams"
)

// The conflict prompt with no answer keeps the local file. Replace is
// the destructive choice, and with no answer no one chose it.

const closedTTY = "read /dev/tty: input/output error"

func promptOptions(stdin io.Reader) (*UpdateOptions, *bytes.Buffer) {
	var errBuf bytes.Buffer
	return &UpdateOptions{
		SharedOptions: &SharedOptions{
			IO: &streams.IO{Out: &bytes.Buffer{}, ErrOut: &errBuf},
		},
		stdinReader: stdin,
	}, &errBuf
}

func TestInteractiveResolver_NoAnswerKeepsTheLocalFile(t *testing.T) {
	saveHooks(t)
	missing := filepath.Join(t.TempDir(), "missing.yaml") // [d]iff cannot read it
	cases := []struct {
		name    string
		stdin   io.Reader
		dest    string
		mergeOK bool
		want    []string // in the output, in this order
	}{
		{"EOF at once", strings.NewReader(""), "d", true,
			[]string{"[d]iff > \n      no answer (end of input): keeping the local file d for this update only\n"}},
		{"EOF after an invalid answer", strings.NewReader("x\n"), "d", true,
			[]string{"Invalid choice. Try r, k, m, or d.", "no answer (end of input): keeping the local file d for this update only\n"}},
		{"EOF after an invalid last answer with no newline", strings.NewReader("x"), "d", true,
			[]string{"Invalid choice. Try r, k, m, or d.", "no answer (end of input): keeping the local file d for this update only\n"}},
		{"EOF after a merge that is not offered", strings.NewReader("m\n"), "d", false,
			[]string{"Merge is not offered for this file. Try r, k, or d.", "no answer (end of input): keeping the local file d for this update only\n"}},
		{"EOF after a diff", strings.NewReader("d\n"), missing, true,
			[]string{"Error reading local file", "no answer (end of input): keeping the local file " + missing + " for this update only\n"}},
		{"a read error (a closed terminal)", iotest.ErrReader(errors.New(closedTTY)), "d", true,
			[]string{"no answer (read error: " + closedTTY + "): keeping the local file d for this update only\n"}},
		// A read error is no answer, also after part of a line: the "r"
		// never got its newline, so it does not count.
		{"a read error after a partial r", io.MultiReader(strings.NewReader("r"), iotest.ErrReader(errors.New(closedTTY))), "d", true,
			[]string{"no answer (read error: " + closedTTY + "): keeping the local file d for this update only\n"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o, errBuf := promptOptions(tc.stdin)
			resolver := o.interactiveConflictResolver("github.com/org/repo", &lock.Lock{})
			if got := resolver("s", tc.dest, tc.mergeOK); got != env.StrategyClient {
				t.Errorf("got %q, want %q (no answer keeps the local file)", got, env.StrategyClient)
			}
			out := errBuf.String()
			rest := out
			for _, want := range tc.want {
				i := strings.Index(rest, want)
				if i < 0 {
					t.Fatalf("output lacks %q (in order):\n%s", want, out)
				}
				rest = rest[i+len(want):]
			}
			if n := strings.Count(out, "no answer"); n != 1 {
				t.Errorf("want one no-answer line, got %d:\n%s", n, out)
			}
		})
	}
}

// A last answer with no newline is an answer: bufio returns it with
// io.EOF (`printf k | b update`). The prompt line is ended first.
func TestInteractiveResolver_LastAnswerWithNoNewline(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  string
	}{
		{"r", env.StrategyReplace},
		{"replace", env.StrategyReplace},
		{"k", env.StrategyClient},
		{"m", env.StrategyMerge},
	} {
		t.Run(tc.input, func(t *testing.T) {
			o, errBuf := promptOptions(strings.NewReader(tc.input))
			resolver := o.interactiveConflictResolver("ref", &lock.Lock{})
			if got := resolver("s", "d", true); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
			out := errBuf.String()
			if !strings.HasSuffix(out, "[d]iff > \n") {
				t.Errorf("the prompt line is not ended:\n%q", out)
			}
			if strings.Contains(out, "no answer") {
				t.Errorf("an answer was taken for no answer:\n%s", out)
			}
		})
	}
}

// After the end of input, each later file is kept too: the reader stays
// at EOF, and every prompt says so.
func TestInteractiveResolver_NoAnswerForEachFile(t *testing.T) {
	o, errBuf := promptOptions(strings.NewReader(""))
	resolver := o.interactiveConflictResolver("ref", &lock.Lock{})
	for _, dest := range []string{"a.yaml", "b.yaml"} {
		if got := resolver("s", dest, true); got != env.StrategyClient {
			t.Errorf("%s: got %q, want %q", dest, got, env.StrategyClient)
		}
		if want := "no answer (end of input): keeping the local file " + dest + " for this update only\n"; !strings.Contains(errBuf.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, errBuf.String())
		}
	}
}

// The prompt with no answer leaves the file on disk as it was, through a
// real SyncEnv: a local bare repo, a project copy with local changes, and
// a lock hash that differs from the copy, so SyncEnv asks the prompt. The
// "r" case is the control: the same setup with an answer replaces the
// file.
func TestConflictPrompt_NoAnswerLeavesTheFileOnDisk(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	// No user or system git config reaches the git commands of the test
	// and of SyncEnv.
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	const (
		path     = "config/app.yaml"
		upstream = "a: 1\n"
		local    = "a: 2\nlocal: true\n"
	)
	bare, commit := conflictPromptRepo(t, path, upstream)
	for _, tc := range []struct {
		name       string
		stdin      io.Reader
		wantFile   string
		wantStatus string
	}{
		{"EOF at once", strings.NewReader(""), local, "kept"},
		{"EOF after an invalid answer", strings.NewReader("x\n"), local, "kept"},
		{"a read error", iotest.ErrReader(errors.New(closedTTY)), local, "kept"},
		{"control: r with no newline", strings.NewReader("r"), upstream, "replaced (local changes overwritten)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project := t.TempDir()
			dest := filepath.Join(project, filepath.FromSlash(path))
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(dest, []byte(local), 0o644); err != nil {
				t.Fatal(err)
			}
			lockEntry := &lock.EnvEntry{
				Ref:    "local-repo",
				Commit: commit,
				Files:  []lock.LockFile{{Path: path, Dest: path, SHA256: "stale"}},
			}
			o, errBuf := promptOptions(tc.stdin)
			cfg := env.EnvConfig{
				Ref:             bare,
				Strategy:        env.StrategyReplace,
				Files:           map[string]envmatch.GlobConfig{path: {}},
				ResolveConflict: o.interactiveConflictResolver(bare, &lock.Lock{}),
				Stdout:          &bytes.Buffer{},
				Stderr:          &bytes.Buffer{},
			}
			res, err := env.SyncEnv(cfg, project, t.TempDir(), lockEntry)
			if err != nil {
				t.Fatalf("SyncEnv: %v", err)
			}
			if !strings.Contains(errBuf.String(), dest+" has local changes.") {
				t.Fatalf("the prompt did not run:\n%s", errBuf.String())
			}
			got, err := os.ReadFile(dest)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.wantFile {
				t.Errorf("file on disk = %q, want %q", got, tc.wantFile)
			}
			if len(res.Files) != 1 || res.Files[0].Status != tc.wantStatus {
				t.Errorf("result files = %+v, want one file with status %q", res.Files, tc.wantStatus)
			}
		})
	}
}

// conflictPromptRepo makes a local bare repo with one commit of path and
// returns the repo and the commit.
func conflictPromptRepo(t *testing.T, path, content string) (bare, commit string) {
	t.Helper()
	tmp := t.TempDir()
	work := filepath.Join(tmp, "work")
	bare = filepath.Join(tmp, "bare.git")
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", work)
	git("-C", work, "config", "user.email", "t@t.com")
	git("-C", work, "config", "user.name", "T")
	file := filepath.Join(work, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	git("-C", work, "add", "-A")
	git("-C", work, "commit", "-q", "--no-gpg-sign", "-m", "v1")
	git("clone", "--bare", "-q", work, bare)
	return bare, git("-C", bare, "rev-parse", "HEAD")
}
