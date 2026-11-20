package gitcache

import (
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// schemeText is the fixed text of errScheme.
const schemeText = "refusing a git ref with a scheme that b does not fetch: use https://, ssh://, git:// or no scheme"

// boundaryErrors returns the errors of ls-remote, clone and fetch when they
// get url directly. Call recordGit first, so that no git command runs.
func boundaryErrors(t *testing.T, url string) map[string]error {
	t.Helper()
	root := t.TempDir()
	const ref = "github.com/org/repo"
	_, lsErr := ResolveRefAuth(url, "v1", "")
	return map[string]error{
		"ls-remote": lsErr,
		"clone":     EnsureCloneAuth(root, ref, url, ""),
		"fetch":     FetchURLAuth(root, ref, url, "c0ffee", ""),
	}
}

// The remote git commands (ls-remote, clone, fetch) let an https, ssh://,
// scp-like or local URL pass to git. They refuse every other transport and
// more than one @ in the authority of each form with a fixed text, before
// git runs: http:// and file:// given to FetchURLAuth directly, SSH:// in
// upper case, a remote helper ("<transport>::<address>"), and ssh:// or
// scp-like URLs with a second @. No error shows the password.
func TestRemoteCommands_Transports(t *testing.T) {
	passed := []string{
		"https://github.com/org/repo.git",
		"HTTPS://github.com/org/repo.git",
		"ssh://git@github.com/org/repo.git",
		"ssh://github.com:2222/org/repo.git",
		"ssh://git@[::1]/org/repo.git",
		"git@github.com:org/repo.git",
		"git@[::1]:org/repo.git",
		"github.com:org/repo.git",
		"/srv/repos/repo.git",
		"./rel/repo",
		"../rel/repo",
		"rel/dir:with-colon",
		"repo",
	}
	refused := []struct{ url, want string }{
		{"http://github.com/org/repo.git", schemeText},
		{"file:///srv/repos/repo.git", schemeText},
		{"file://localhost/srv/repos/repo.git", schemeText},
		{"ftp://host.example/x.git", schemeText},
		{"SSH://git@github.com/org/repo.git", schemeText},
		{"Ssh://git@github.com/org/repo.git", schemeText},
		{"git://github.com/org/repo.git", schemeText},
		{"git+ssh://git@github.com/org/repo.git", schemeText},
		{"file::/srv/repos/repo.git", schemeText},
		{"ext::sh -c true", schemeText},
		{"https://http://github.com/org/repo.git", schemeText},
		{"ssh://http://github.com/org/repo.git", schemeText},
		{"ssh://git@x@github.com/org/repo.git", refusedText},
		{"ssh://git@" + refPassword + "@github.com/org/repo.git", refusedText},
		{"ssh://git:p@" + refPassword + "@github.com/org/repo.git", refusedText},
		{"git@x@github.com:org/repo.git", refusedText},
		{"git@" + refPassword + "@github.com:org/repo.git", refusedText},
		{"https://a@" + refPassword + "@github.com/org/repo.git", refusedText},
		{refusedURL, refusedText},
	}
	for _, url := range passed {
		cmds := recordGit(t)
		for op, err := range boundaryErrors(t, url) {
			if err != nil {
				t.Errorf("%s %s: %v", url, op, err)
			}
		}
		if len(*cmds) != 3 {
			t.Errorf("%s: %d git commands ran, want 3", url, len(*cmds))
		}
		for i, ac := range *cmds {
			if !slices.Contains(ac.Args, url) {
				t.Errorf("%s: command %d does not get the URL: %q", url, i, ac.Args)
			}
		}
	}
	for i, tt := range refused {
		cmds := recordGit(t)
		for op, err := range boundaryErrors(t, tt.url) {
			if err == nil || err.Error() != tt.want {
				t.Errorf("URL %d %s: error %q, want %q", i, op,
					strings.ReplaceAll(fmt.Sprint(err), refPassword, "<password>"), tt.want)
			}
			if err != nil && formatsHold(err, refPassword) {
				t.Errorf("URL %d %s: the error shows the password", i, op)
			}
		}
		if len(*cmds) != 0 {
			t.Errorf("URL %d: %d git commands ran", i, len(*cmds))
		}
	}
}

// FetchAuth (deprecated) gives the URL that a clone stored to the transport
// check of FetchURLAuth. A stored http://, file:// or ssh:// URL with a
// second @ is refused with the fixed text before git fetches, and no error
// shows the password. A stored https, ssh://, scp-like or local URL passes.
func TestFetchAuth_StoredOriginTransports(t *testing.T) {
	isolateGit(t)
	setProviderEnv(t, "")
	root := t.TempDir()
	for i, tt := range []struct{ origin, want string }{
		{"http://github.com/org/repo.git", schemeText},
		{"file:///srv/repos/repo.git", schemeText},
		{"ssh://git@x@github.com/org/repo.git", refusedText},
		{"ssh://git@" + refPassword + "@github.com/org/repo.git", refusedText},
		{"git@" + refPassword + "@github.com:org/repo.git", refusedText},
		{"https://github.com/org/repo.git", ""},
		{"ssh://git@github.com/org/repo.git", ""},
		{"git@github.com:org/repo.git", ""},
		{"/srv/repos/repo.git", ""},
	} {
		ref := fmt.Sprintf("host.example/org/repo-%d", i)
		dir := CacheDir(root, ref)
		if out, err := exec.Command("git", "init", "-q", "--bare", dir).CombinedOutput(); err != nil {
			t.Fatalf("git init: %v\n%s", err, out)
		}
		if out, err := exec.Command("git", "-C", dir, "config", "remote.origin.url", tt.origin).CombinedOutput(); err != nil {
			t.Fatalf("set origin: %v\n%s", err, out)
		}
		cmds := recordGit(t)
		err := FetchAuth(root, ref, "c0ffee", "")
		if tt.want == "" {
			if err != nil || len(*cmds) != 1 || !slices.Contains((*cmds)[0].Args, tt.origin) {
				t.Errorf("origin %d: error %v, %d commands, want one fetch from the stored URL", i, err, len(*cmds))
			}
			continue
		}
		if err == nil || err.Error() != tt.want {
			t.Errorf("origin %d: error %q, want %q", i,
				strings.ReplaceAll(fmt.Sprint(err), refPassword, "<password>"), tt.want)
		}
		if err != nil && formatsHold(err, refPassword) {
			t.Errorf("origin %d: the error shows the password", i)
		}
		if len(*cmds) != 0 {
			t.Errorf("origin %d: %d git commands ran", i, len(*cmds))
		}
	}
}
