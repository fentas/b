package gitcache

import (
	"crypto/sha256"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGH is the GITHUB_TOKEN value in these tests. It is not a real value.
const fakeGH = "fake-gh-value-for-tests"

// fakeGL is the GITLAB_TOKEN value in these tests. It is not a real value.
const fakeGL = "fake-gl-value-for-tests"

func setProviderEnv(t *testing.T, knob string) {
	t.Helper()
	t.Setenv(SSHAsHTTPSEnv, knob)
	t.Setenv("GITHUB_TOKEN", fakeGH)
	t.Setenv("GITLAB_TOKEN", fakeGL)
	t.Setenv("GITEA_TOKEN", "")
}

func TestResolveGitURL_SSHAsHTTPS(t *testing.T) {
	const ghHTTPS = "https://github.com/org/repo.git"
	tests := []struct {
		name    string
		ref     string
		wantOn  string // URL with the knob on
		sshOn   bool   // IsSSH with the knob on
		wantOff string // URL with the knob off
		hdrOn   bool   // the GitHub header is set with the knob on
	}{
		{"ssh implicit", "git@github.com:org/repo", ghHTTPS, false, "git@github.com:org/repo.git", true},
		{"ssh implicit .git", "git@github.com:org/repo.git", ghHTTPS, false, "git@github.com:org/repo.git", true},
		{"ssh implicit label+version", "git@github.com:org/repo#mon@v1.2", ghHTTPS, false, "git@github.com:org/repo.git", true},
		{"ssh implicit host case", "git@GitHub.com:org/repo", ghHTTPS, false, "git@GitHub.com:org/repo.git", true},
		{"ssh explicit", "ssh://git@github.com/org/repo", ghHTTPS, false, "ssh://git@github.com/org/repo.git", true},
		{"ssh explicit .git", "ssh://git@github.com/org/repo.git", ghHTTPS, false, "ssh://git@github.com/org/repo.git", true},
		// The path holds "@" and ":": the https ref must not read as SSH again.
		{"ssh implicit @ and : in path", "git@github.com:x@y:z", "https://github.com/x@y:z.git", false, "git@github.com:x@y:z.git", true},
		{"ssh explicit @ and : in path", "ssh://git@github.com/x@y:z", "https://github.com/x@y:z.git", false, "ssh://git@github.com/x@y:z.git", true},
		{"https", "github.com/org/repo", ghHTTPS, false, ghHTTPS, true},
		{"git:// remote", "git://github.com/org/repo:file", ghHTTPS, false, ghHTTPS, true},
		{"local path", "/srv/repos/repo", "/srv/repos/repo", false, "/srv/repos/repo", false},
		{"ssh gitlab", "git@gitlab.com:group/project", "git@gitlab.com:group/project.git", true, "git@gitlab.com:group/project.git", false},
		{"ssh explicit other host", "ssh://git@example.org/org/repo", "ssh://git@example.org/org/repo.git", true, "ssh://git@example.org/org/repo.git", false},
		{"ssh github port", "ssh://git@github.com:22/org/repo", "ssh://git@github.com:22/org/repo.git", true, "ssh://git@github.com:22/org/repo.git", false},
		{"ssh github other user", "deploy@github.com:org/repo", "deploy@github.com:org/repo.git", true, "deploy@github.com:org/repo.git", false},
		{"ssh github subdomain", "git@ssh.github.com:org/repo", "git@ssh.github.com:org/repo.git", true, "git@ssh.github.com:org/repo.git", false},
	}

	for _, tt := range tests {
		t.Run(tt.name+"/on", func(t *testing.T) {
			setProviderEnv(t, "1")
			r := ResolveGitURL(tt.ref, "")
			if r.URL != tt.wantOn || r.IsSSH != tt.sshOn {
				t.Errorf("URL=%q IsSSH=%v, want %q %v", r.URL, r.IsSSH, tt.wantOn, tt.sshOn)
			}
			if got := r.AuthHeader == "Authorization: Bearer "+fakeGH; got != tt.hdrOn {
				t.Errorf("GitHub header set = %v, want %v", got, tt.hdrOn)
			}
			if tt.sshOn && r.AuthHeader != "" {
				t.Error("an SSH ref must not get a header")
			}
		})
		t.Run(tt.name+"/off", func(t *testing.T) {
			setProviderEnv(t, "")
			r := ResolveGitURL(tt.ref, "")
			if r.URL != tt.wantOff {
				t.Errorf("URL=%q, want %q", r.URL, tt.wantOff)
			}
			if r.IsSSH && r.AuthHeader != "" {
				t.Error("an SSH ref must not get a header")
			}
		})
	}
}

// Only the exact value "1" turns the knob on.
func TestResolveGitURL_SSHAsHTTPS_OnlyExactOne(t *testing.T) {
	for _, v := range []string{"", "0", "true", "yes", "on", " 1", "1 ", "11"} {
		t.Run(fmt.Sprintf("%q", v), func(t *testing.T) {
			setProviderEnv(t, v)
			r := ResolveGitURL("git@github.com:org/repo", "")
			if r.URL != "git@github.com:org/repo.git" || !r.IsSSH || r.AuthHeader != "" {
				t.Errorf("value %q: URL=%q IsSSH=%v header set=%v, want the SSH ref unchanged",
					v, r.URL, r.IsSSH, r.AuthHeader != "")
			}
		})
	}
}

// recordGit replaces runGit and outputGit with a recorder: no git runs and
// nothing goes to the network. ls-remote returns one fake commit.
func recordGit(t *testing.T) *[]AuthCmd {
	t.Helper()
	var cmds []AuthCmd
	origRun, origOutput := runGit, outputGit
	t.Cleanup(func() { runGit, outputGit = origRun, origOutput })
	runGit = func(ac AuthCmd) error {
		cmds = append(cmds, ac)
		return nil
	}
	outputGit = func(ac AuthCmd) (string, error) {
		cmds = append(cmds, ac)
		return "c0ffee\trefs/tags/v1\n", nil
	}
	return &cmds
}

// headerEnv is the env that b gives git for header on host: the header
// key is scoped to that host.
func headerEnv(host, header string) []string {
	return []string{
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=http.https://" + host + "/.extraHeader",
		"GIT_CONFIG_VALUE_0=" + header,
	}
}

// The ls-remote, clone and fetch command lines carry the https URL and the
// header (in env, not argv) for a github.com SSH ref with the knob on, and
// for a plain https ref. The header key is scoped to the host of the URL;
// there is no unscoped http.extraHeader. With the knob off, and for an SSH
// ref of another host, they carry the SSH URL and no header. Each command
// line ends its options with "--" before the URL.
func TestGitCommands_SSHAsHTTPS(t *testing.T) {
	ghEnv := headerEnv("github.com", "Authorization: Bearer "+fakeGH)
	glEnv := headerEnv("gitlab.com", "PRIVATE-TOKEN: "+fakeGL)
	noHdr := []string{"GIT_TERMINAL_PROMPT=0"}
	tests := []struct {
		name    string
		knob    string
		ref     string
		wantURL string
		wantEnv []string
	}{
		{"github on", "1", "git@github.com:org/repo#mon", "https://github.com/org/repo.git", ghEnv},
		{"github explicit on", "1", "ssh://git@github.com/org/repo", "https://github.com/org/repo.git", ghEnv},
		{"github off", "", "git@github.com:org/repo#mon", "git@github.com:org/repo.git", noHdr},
		{"gitlab on", "1", "git@gitlab.com:group/project", "git@gitlab.com:group/project.git", noHdr},
		{"gitlab off", "", "git@gitlab.com:group/project", "git@gitlab.com:group/project.git", noHdr},
		{"https github", "", "github.com/org/repo", "https://github.com/org/repo.git", ghEnv},
		{"https gitlab", "", "gitlab.com/group/project", "https://gitlab.com/group/project.git", glEnv},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setProviderEnv(t, tt.knob)
			cmds := recordGit(t)
			root := t.TempDir()
			r := ResolveGitURL(tt.ref, "")
			base := RefBase(tt.ref)

			if _, err := ResolveRefAuth(r.URL, "v1", r.AuthHeader); err != nil {
				t.Fatalf("ResolveRefAuth: %v", err)
			}
			if err := EnsureCloneAuth(root, base, r.URL, r.AuthHeader); err != nil {
				t.Fatalf("EnsureCloneAuth: %v", err)
			}
			if err := FetchURLAuth(root, base, r.URL, "c0ffee", r.AuthHeader); err != nil {
				t.Fatalf("FetchURLAuth: %v", err)
			}

			dir := CacheDir(root, base)
			want := [][]string{
				{"git", "ls-remote", "--", tt.wantURL, "v1"},
				{"git", "clone", "--bare", "--depth", "1", "--", tt.wantURL, dir},
				{"git", "-C", dir, "fetch", "--depth", "1", "--", tt.wantURL, "c0ffee"},
			}
			if len(*cmds) != len(want) {
				t.Fatalf("got %d git commands, want %d", len(*cmds), len(want))
			}
			for i, ac := range *cmds {
				if strings.Join(ac.Args, " ") != strings.Join(want[i], " ") {
					t.Errorf("command %d: %q, want %q", i, ac.Args, want[i])
				}
				for _, a := range ac.Args {
					if strings.Contains(a, fakeGH) || strings.Contains(a, fakeGL) {
						t.Errorf("command %d: a provider value is in argv", i)
					}
				}
				if strings.Join(ac.Env, "\n") != strings.Join(tt.wantEnv, "\n") {
					t.Errorf("command %d: env is not the wanted env (%d entries, want %d)", i, len(ac.Env), len(tt.wantEnv))
				}
				for _, e := range ac.Env {
					if strings.HasSuffix(e, "=http.extraHeader") {
						t.Errorf("command %d: env sets the unscoped key: %s", i, e)
					}
				}
			}
		})
	}
}

// The cache directory name is the SHA-256 of the ref as written, with the
// knob on and off, so an existing cache is found again after a flip.
func TestCacheDir_SSHAsHTTPS_KeepsRefKey(t *testing.T) {
	root := t.TempDir()
	for _, ref := range []string{"git@github.com:org/repo", "ssh://git@github.com/org/repo.git"} {
		want := filepath.Join(root, fmt.Sprintf("%x", sha256.Sum256([]byte(ref))))
		setProviderEnv(t, "")
		off := CacheDir(root, ref)
		setProviderEnv(t, "1")
		on := CacheDir(root, ref)
		if off != want || on != want {
			t.Errorf("%s: CacheDir off=%q on=%q, want %q for both", ref, off, on, want)
		}
	}
}

// A cache cloned over SSH stores the SSH URL as origin. FetchURL fetches
// from the URL it gets and keeps the stored origin. GIT_ALLOW_PROTOCOL=file
// (isolateGit) makes git refuse every network transport.
func TestFetchURL_IgnoresStoredOrigin(t *testing.T) {
	isolateGit(t)
	bare, commit := setupBareRepo(t)
	root := t.TempDir()
	ref := "git@github.com:org/repo"
	const sshOrigin = "git@github.com:org/repo.git"

	if err := EnsureClone(root, ref, bare); err != nil {
		t.Fatalf("EnsureClone: %v", err)
	}
	dir := CacheDir(root, ref)
	if out, err := exec.Command("git", "-C", dir, "config", "remote.origin.url", sshOrigin).CombinedOutput(); err != nil {
		t.Fatalf("set origin: %v\n%s", err, out)
	}

	if err := FetchURL(root, ref, bare, commit); err != nil {
		t.Fatalf("FetchURL from the given URL: %v", err)
	}
	err := Fetch(root, ref, commit)
	if err == nil {
		t.Fatal("Fetch from the stored SSH origin passed; the protocol fence does not hold")
	}
	if !strings.Contains(err.Error(), "transport 'ssh' not allowed") {
		t.Errorf("Fetch did not fail on the SSH transport: %v", err)
	}
	out, err := exec.Command("git", "-C", dir, "config", "remote.origin.url").Output()
	if err != nil {
		t.Fatalf("read origin: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != sshOrigin {
		t.Errorf("stored origin = %q after FetchURL, want %q", got, sshOrigin)
	}
}
