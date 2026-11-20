package gitcache

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// isolateGit makes the git commands of a test read no git config of the
// user or the system, and refuse every network transport. The test runs in
// an empty directory, so git also finds no repository above it.
func isolateGit(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, ".gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_COUNT", "0")
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(home))
	t.Setenv("GIT_ALLOW_PROTOCOL", "file")
	t.Chdir(home)
}

func TestHeaderKey(t *testing.T) {
	tests := []struct {
		remote string
		want   string
	}{
		{"https://github.com/org/repo.git", "http.https://github.com/.extraHeader"},
		{"https://GitHub.com/org/repo.git", "http.https://github.com/.extraHeader"},
		{"https://api.github.com/org/repo.git", "http.https://api.github.com/.extraHeader"},
		{"https://gitlab.com/group/project.git", "http.https://gitlab.com/.extraHeader"},
		{"https://codeberg.org/org/repo.git", "http.https://codeberg.org/.extraHeader"},
		{"https://github.com:8443/org/repo.git", "http.https://github.com:8443/.extraHeader"},
		{"https://github.com/x@y:z.git", "http.https://github.com/.extraHeader"},
		// Not an https URL with a host: no key, so no header.
		{"http://github.com/org/repo.git", ""},
		{"git@github.com:org/repo.git", ""},
		{"ssh://git@github.com/org/repo.git", ""},
		{"https:///org/repo.git", ""},
		{"/srv/repos/repo", ""},
		{"origin", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := headerKey(tt.remote); got != tt.want {
			t.Errorf("headerKey(%q) = %q, want %q", tt.remote, got, tt.want)
		}
	}
}

// A header for a URL that is not https gets no GIT_CONFIG_* entry.
func TestAuthCmd_NoHeaderOffHTTPS(t *testing.T) {
	for _, remote := range []string{"git@github.com:org/repo.git", "http://github.com/org/repo.git", "origin"} {
		ac := authCmd(remote, "Authorization: Bearer "+fakeGH, "ls-remote", "--", remote)
		if len(ac.Env) != 1 || ac.Env[0] != "GIT_TERMINAL_PROMPT=0" {
			t.Errorf("%s: env = %d entries, want only GIT_TERMINAL_PROMPT=0", remote, len(ac.Env))
		}
	}
}

// Every git command that can reach a remote (ls-remote, also the HEAD
// fallback; clone; fetch) runs with GIT_TERMINAL_PROMPT=0, with and without
// a header, so git does not wait for a password on the terminal.
func TestGitCommands_NoTerminalPrompt(t *testing.T) {
	origRun, origOutput := runGit, outputGit
	t.Cleanup(func() { runGit, outputGit = origRun, origOutput })
	for _, hdr := range []string{"", "Authorization: Bearer " + fakeGH} {
		var cmds []AuthCmd
		runGit = func(ac AuthCmd) error {
			cmds = append(cmds, ac)
			return nil
		}
		outputGit = func(ac AuthCmd) (string, error) {
			cmds = append(cmds, ac)
			if ac.Args[len(ac.Args)-1] == "HEAD" {
				return "", nil // no HEAD: ResolveRefAuth tries main and master
			}
			return "c0ffee\trefs/heads/main\n", nil
		}

		const u = "https://github.com/org/repo.git"
		root := t.TempDir()
		if _, err := ResolveRefAuth(u, "", hdr); err != nil {
			t.Fatalf("ResolveRefAuth: %v", err)
		}
		if err := EnsureCloneAuth(root, "github.com/org/repo", u, hdr); err != nil {
			t.Fatalf("EnsureCloneAuth: %v", err)
		}
		if err := FetchURLAuth(root, "github.com/org/repo", u, "c0ffee", hdr); err != nil {
			t.Fatalf("FetchURLAuth: %v", err)
		}
		if len(cmds) != 4 {
			t.Fatalf("header %v: got %d git commands, want 4 (ls-remote HEAD, ls-remote main, clone, fetch)", hdr != "", len(cmds))
		}
		for i, ac := range cmds {
			if !slices.Contains(ac.Env, "GIT_TERMINAL_PROMPT=0") {
				t.Errorf("header %v: command %d (%s) has no GIT_TERMINAL_PROMPT=0", hdr != "", i, gitVerb(ac))
			}
		}
	}
}

// A URL that looks like an option comes after "--", so git reads it as the
// repository.
func TestGitCommands_EndOfOptions(t *testing.T) {
	cmds := recordGit(t)
	const u = "--upload-pack=touch /nonexistent/b-test"
	root := t.TempDir()
	if _, err := ResolveRefAuth(u, "v1", ""); err != nil {
		t.Fatalf("ResolveRefAuth: %v", err)
	}
	if err := EnsureCloneAuth(root, "r", u, ""); err != nil {
		t.Fatalf("EnsureCloneAuth: %v", err)
	}
	if err := FetchURLAuth(root, "r", u, "c0ffee", ""); err != nil {
		t.Fatalf("FetchURLAuth: %v", err)
	}
	if len(*cmds) != 3 {
		t.Fatalf("got %d git commands, want 3", len(*cmds))
	}
	for i, ac := range *cmds {
		dd, at := slices.Index(ac.Args, "--"), slices.Index(ac.Args, u)
		if dd < 0 || at != dd+1 {
			t.Errorf("command %d: %q, want \"--\" just before the URL", i, ac.Args)
		}
	}
}

// gitVerb returns the git subcommand of ac, for test messages.
func gitVerb(ac AuthCmd) string {
	for _, a := range ac.Args {
		switch a {
		case "ls-remote", "clone", "fetch":
			return a
		}
	}
	return strings.Join(ac.Args, " ")
}

// urlMatchHeader asks git which http.extraHeader it uses for the URL u,
// with the env that b gave a git command. git selects http.<url>.* keys for
// a fetch the same way (http.c http_init: url_normalize, then
// urlmatch_config_entry).
func urlMatchHeader(t *testing.T, ac AuthCmd, u string) string {
	t.Helper()
	cmd := exec.Command("git", "config", "--get-urlmatch", "http.extraheader", u)
	cmd.Env = append(os.Environ(), ac.Env...)
	out, err := cmd.Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return "" // no key matches u
	}
	if err != nil {
		t.Fatalf("git config --get-urlmatch %s: %v", u, err)
	}
	return strings.TrimSpace(string(out))
}

// With a token set, real git gets the header key scoped to github.com: git
// itself selects the header for the github.com URL, and selects no header
// for a URL of another host. git compares the key with the URL after its
// url.<base>.insteadOf rewrite (remote.c add_url_alias; transport.c
// transport_get; remote-curl.c http_init), so a rewrite of the user to
// another host does not get the token. The rewrite here goes to a local bare
// repo, so no git command leaves the machine.
func TestAuthHeader_RealGitScopedToHost(t *testing.T) {
	isolateGit(t)
	bare, commit := setupBareRepo(t)
	t.Setenv(SSHAsHTTPSEnv, "1")
	t.Setenv("GITHUB_TOKEN", fakeGH)
	const ghURL = "https://github.com/org/repo.git"
	// The insteadOf rewrite is in the (test) global git config: b sets its
	// own GIT_CONFIG_COUNT for the header, which replaces env config.
	if out, err := exec.Command("git", "config", "--global", "url."+bare+".insteadOf", ghURL).CombinedOutput(); err != nil {
		t.Fatalf("set insteadOf: %v\n%s", err, out)
	}

	var cmds []AuthCmd
	origRun, origOutput := runGit, outputGit
	t.Cleanup(func() { runGit, outputGit = origRun, origOutput })
	runGit = func(ac AuthCmd) error {
		cmds = append(cmds, ac)
		return origRun(ac)
	}
	outputGit = func(ac AuthCmd) (string, error) {
		cmds = append(cmds, ac)
		return origOutput(ac)
	}

	const ref = "git@github.com:org/repo"
	r := ResolveGitURL(ref, "")
	if r.URL != ghURL || r.AuthHeader != "Authorization: Bearer "+fakeGH {
		t.Fatalf("ResolveGitURL: URL=%q header set=%v", r.URL, r.AuthHeader != "")
	}
	root := t.TempDir()
	got, err := ResolveRefAuth(r.URL, "", r.AuthHeader)
	if err != nil || got != commit {
		t.Fatalf("ResolveRefAuth = %q, %v, want %q", got, err, commit)
	}
	if err := EnsureCloneAuth(root, ref, r.URL, r.AuthHeader); err != nil {
		t.Fatalf("EnsureCloneAuth: %v", err)
	}
	if err := FetchURLAuth(root, ref, r.URL, commit, r.AuthHeader); err != nil {
		t.Fatalf("FetchURLAuth: %v", err)
	}
	if !HasCommit(root, ref, commit) {
		t.Fatal("the cache has no commit after the fetch")
	}
	if len(cmds) != 3 {
		t.Fatalf("got %d git commands, want 3", len(cmds))
	}

	for i, ac := range cmds {
		if h := urlMatchHeader(t, ac, ghURL); h != r.AuthHeader {
			t.Errorf("command %d (%s): git selects no GitHub header for %s", i, gitVerb(ac), ghURL)
		}
		for _, other := range []string{
			"https://git.example.org/org/repo.git", // a url.<base>.insteadOf rewrite to another host
			"https://github.com.example.org/org/repo.git",
			"https://gitlab.com/org/repo.git",
			"http://github.com/org/repo.git",
		} {
			if h := urlMatchHeader(t, ac, other); h != "" {
				t.Errorf("command %d (%s): git selects the header for %s", i, gitVerb(ac), other)
			}
		}
	}
}
