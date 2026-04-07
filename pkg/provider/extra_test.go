package provider

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestIsReleaseProvider(t *testing.T) {
	if IsReleaseProvider(nil) {
		t.Error("nil should be false")
	}
	if IsReleaseProvider(&GoInstall{}) {
		t.Error("go should be false")
	}
	if IsReleaseProvider(&Docker{}) {
		t.Error("docker should be false")
	}
	if IsReleaseProvider(&Git{}) {
		t.Error("git should be false")
	}
	if !IsReleaseProvider(&GitHub{}) {
		t.Error("github should be true")
	}
}

func TestDetectContainerRuntime(t *testing.T) {
	// Save PATH and clear so nothing is found
	oldPath := os.Getenv("PATH")
	defer os.Setenv("PATH", oldPath)
	os.Setenv("PATH", "/nonexistent")
	if _, err := detectContainerRuntime(); err == nil {
		t.Error("expected error when no runtime available")
	}

	// With the current PATH — may or may not find one, just ensure it doesn't panic
	os.Setenv("PATH", oldPath)
	_, _ = detectContainerRuntime()
}

func TestGit_LatestVersion_Local(t *testing.T) {
	// Create a local repo, commit something
	tmp := t.TempDir()
	run := func(args ...string) {
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	run("git", "init", "-q", tmp)
	run("git", "-C", tmp, "config", "user.email", "t@t.com")
	run("git", "-C", tmp, "config", "user.name", "T")
	run("git", "-C", tmp, "config", "commit.gpgsign", "false")
	_ = os.WriteFile(filepath.Join(tmp, "script.sh"), []byte("#!/bin/sh\necho hi"), 0755)
	run("git", "-C", tmp, "add", "-A")
	run("git", "-C", tmp, "commit", "-m", "init", "--no-gpg-sign")

	g := &Git{}
	ref := "git://" + tmp + ":script.sh"
	sha, err := g.LatestVersion(ref)
	if err != nil {
		t.Fatalf("LatestVersion: %v", err)
	}
	if len(sha) < 7 {
		t.Errorf("sha = %q", sha)
	}

	// FetchRelease should error
	if _, err := g.FetchRelease(ref, ""); err == nil {
		t.Error("FetchRelease should error")
	}

	// Install local
	dest := t.TempDir()
	out, err := g.Install(ref, "", dest)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if _, err := os.Stat(out); err != nil {
		t.Errorf("binary missing: %v", err)
	}
}

func TestGit_ParseErrors(t *testing.T) {
	if _, _, err := parseGitRef("git://"); err == nil {
		t.Error("expected empty ref error")
	}
	if _, _, err := parseGitRef("git:///abs/no-colon"); err == nil {
		t.Error("expected missing separator")
	}
	if _, _, err := parseGitRef("git://github.com/org/repo"); err == nil {
		t.Error("expected missing filepath")
	}
	// Relative
	if _, _, err := parseGitRef("git://./repo:file"); err != nil {
		t.Errorf("relative: %v", err)
	}
	// Absolute
	if r, f, err := parseGitRef("git:///x/y:file"); err != nil || r != "/x/y" || f != "file" {
		t.Errorf("got r=%q f=%q err=%v", r, f, err)
	}
	// Remote
	if r, _, err := parseGitRef("git://host.com/org/repo:path"); err != nil || r != "host.com/org/repo" {
		t.Errorf("got r=%q err=%v", r, err)
	}
	// With version suffix
	if r, _, err := parseGitRef("git:///x/y:file@v1"); err != nil || r != "/x/y" {
		t.Errorf("got r=%q err=%v", r, err)
	}
}

func TestGoInstall_Install_MissingGo(t *testing.T) {
	oldPath := os.Getenv("PATH")
	defer os.Setenv("PATH", oldPath)
	os.Setenv("PATH", "/nonexistent")
	g := &GoInstall{}
	if _, err := g.Install("go://example.com/foo", "latest", t.TempDir()); err == nil {
		t.Error("expected no-go error")
	}
}

func TestGoInstall_Metadata(t *testing.T) {
	g := &GoInstall{}
	if g.Name() != "go" {
		t.Errorf("name = %q", g.Name())
	}
	if !g.Match("go://example.com/foo") {
		t.Error("should match go://")
	}
	if g.Match("github.com/x/y") {
		t.Error("should not match")
	}
	v, err := g.LatestVersion("go://x")
	if err != nil || v != "latest" {
		t.Errorf("v=%q err=%v", v, err)
	}
	if _, err := g.FetchRelease("", ""); err == nil {
		t.Error("should error")
	}
	if goModule("go://example.com/foo@v1") != "example.com/foo" {
		t.Errorf("goModule wrong")
	}
}

func TestDocker_Metadata(t *testing.T) {
	d := &Docker{}
	if d.Name() != "docker" {
		t.Errorf("name = %q", d.Name())
	}
	if !d.Match("docker://hashicorp/terraform") {
		t.Error("should match")
	}
	v, _ := d.LatestVersion("docker://x")
	if v != "latest" {
		t.Errorf("v = %q", v)
	}
	if _, err := d.FetchRelease("", ""); err == nil {
		t.Error("should error")
	}
	if dockerImage("docker://hashicorp/terraform:1.0") != "hashicorp/terraform" {
		t.Errorf("dockerImage wrong")
	}
}

func TestGiteaSetAuth(t *testing.T) {
	r, _ := http.NewRequest("GET", "http://x", nil)
	os.Unsetenv("GITEA_TOKEN")
	giteaSetAuth(r)
	if r.Header.Get("Authorization") != "" {
		t.Error("expected empty without env")
	}
	t.Setenv("GITEA_TOKEN", "secret")
	giteaSetAuth(r)
	if r.Header.Get("Authorization") != "token secret" {
		t.Errorf("header = %q", r.Header.Get("Authorization"))
	}
}

// rewriteTransport rewrites outgoing request URLs to a local test server.
type rewriteTransport struct {
	server string
	inner  http.RoundTripper
}

func (rt *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	newURL := rt.server + req.URL.Path
	if req.URL.RawQuery != "" {
		newURL += "?" + req.URL.RawQuery
	}
	newReq, err := http.NewRequest(req.Method, newURL, req.Body)
	if err != nil {
		return nil, err
	}
	newReq.Header = req.Header
	return rt.inner.RoundTrip(newReq)
}

func withFakeAPI(t *testing.T, handler http.Handler) func() {
	t.Helper()
	srv := httptest.NewServer(handler)
	oldTransport := http.DefaultTransport
	oldClient := http.DefaultClient
	rewriter := &rewriteTransport{server: srv.URL, inner: oldTransport}
	http.DefaultTransport = rewriter
	http.DefaultClient = &http.Client{Transport: rewriter}
	return func() {
		http.DefaultTransport = oldTransport
		http.DefaultClient = oldClient
		srv.Close()
	}
}

func TestGitHub_FetchRelease_Mocked(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/fentas/b/releases/tags/v1.0.0", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v1.0.0","assets":[{"name":"b-linux","browser_download_url":"http://x/b-linux","size":100}]}`))
	})
	cleanup := withFakeAPI(t, mux)
	defer cleanup()

	g := &GitHub{}
	rel, err := g.FetchRelease("github.com/fentas/b", "v1.0.0")
	if err != nil {
		t.Fatalf("FetchRelease: %v", err)
	}
	if rel.Version != "v1.0.0" || len(rel.Assets) != 1 {
		t.Errorf("got %+v", rel)
	}
}

func TestGitHub_LatestVersion_Mocked(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/fentas/b/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/fentas/b/releases/tag/v9.9.9", http.StatusFound)
	})
	mux.HandleFunc("/fentas/b/releases/tag/v9.9.9", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	cleanup := withFakeAPI(t, mux)
	defer cleanup()
	g := &GitHub{}
	v, err := g.LatestVersion("github.com/fentas/b")
	if err != nil {
		t.Fatalf("%v", err)
	}
	if v != "v9.9.9" {
		t.Errorf("v=%q", v)
	}
}

func TestGitHub_LatestVersion_NotFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/x/y/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	cleanup := withFakeAPI(t, mux)
	defer cleanup()
	g := &GitHub{}
	if _, err := g.LatestVersion("github.com/x/y"); err == nil {
		t.Error("expected not-found error")
	}
}

func TestGitHub_FetchRelease_EmptyVersion(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/x/y/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/x/y/releases/tag/v7", http.StatusFound)
	})
	mux.HandleFunc("/x/y/releases/tag/v7", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/repos/x/y/releases/tags/v7", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v7","assets":[]}`))
	})
	cleanup := withFakeAPI(t, mux)
	defer cleanup()
	g := &GitHub{}
	rel, err := g.FetchRelease("github.com/x/y", "")
	if err != nil || rel.Version != "v7" {
		t.Errorf("rel=%+v err=%v", rel, err)
	}
}

func TestGitHub_FetchRelease_RateLimit(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/x/y/releases/tags/v1", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})
	cleanup := withFakeAPI(t, mux)
	defer cleanup()
	g := &GitHub{}
	if _, err := g.FetchRelease("github.com/x/y", "v1"); err == nil {
		t.Error("expected rate-limit error")
	}
}

func TestGitLab_FetchRelease_EmptyVersion(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/x/y/releases", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"tag_name":"v8"}]`))
	})
	mux.HandleFunc("/api/v4/projects/x/y/releases/v8", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v8","assets":{}}`))
	})
	cleanup := withFakeAPI(t, mux)
	defer cleanup()
	g := &GitLab{}
	rel, err := g.FetchRelease("gitlab.com/x/y", "")
	if err != nil || rel.Version != "v8" {
		t.Errorf("%+v %v", rel, err)
	}
}

func TestGitea_FetchRelease_EmptyVersion(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/repos/a/b/releases", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"tag_name":"v2"}]`))
	})
	mux.HandleFunc("/api/v1/repos/a/b/releases/tags/v2", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v2","assets":[]}`))
	})
	cleanup := withFakeAPI(t, mux)
	defer cleanup()
	g := &Gitea{}
	rel, err := g.FetchRelease("codeberg.org/a/b", "")
	if err != nil || rel.Version != "v2" {
		t.Errorf("%+v %v", rel, err)
	}
}

func TestGithubLatest_HelperMocked(t *testing.T) {
	// Used elsewhere — just exercise the GithubOwnerRepo edge cases.
	o, r := githubOwnerRepo("github.com/fentas/b@v1")
	if o != "fentas" || r != "b" {
		t.Errorf("o=%q r=%q", o, r)
	}
}

func TestGitHub_FetchRelease_NotFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/org/repo/releases/tags/v1", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	cleanup := withFakeAPI(t, mux)
	defer cleanup()
	g := &GitHub{}
	if _, err := g.FetchRelease("github.com/org/repo", "v1"); err == nil {
		t.Error("expected not-found")
	}
}

func TestGitLab_FetchRelease_Mocked(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/org/repo/releases/v1", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v1","assets":{"links":[{"name":"f","direct_asset_url":"http://x/f"}]}}`))
	})
	cleanup := withFakeAPI(t, mux)
	defer cleanup()
	g := &GitLab{}
	rel, err := g.FetchRelease("gitlab.com/org/repo", "v1")
	if err != nil {
		t.Fatalf("%v", err)
	}
	if rel.Version != "v1" || len(rel.Assets) != 1 {
		t.Errorf("got %+v", rel)
	}
}

func TestGitLab_LatestVersion_Mocked(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/org/repo/releases", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"tag_name":"v2"}]`))
	})
	cleanup := withFakeAPI(t, mux)
	defer cleanup()
	g := &GitLab{}
	v, err := g.LatestVersion("gitlab.com/org/repo")
	if err != nil {
		t.Fatalf("%v", err)
	}
	if v != "v2" {
		t.Errorf("v=%q", v)
	}
}

func TestGitea_LatestVersion_Mocked(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/repos/foo/bar/releases", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"tag_name":"v3"}]`))
	})
	cleanup := withFakeAPI(t, mux)
	defer cleanup()
	g := &Gitea{}
	v, err := g.LatestVersion("codeberg.org/foo/bar")
	if err != nil {
		t.Fatalf("%v", err)
	}
	if v != "v3" {
		t.Errorf("v=%q", v)
	}
}

func TestGitea_FetchRelease_Mocked(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/repos/foo/bar/releases/tags/v3", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v3","assets":[{"name":"a","browser_download_url":"http://x/a"}]}`))
	})
	cleanup := withFakeAPI(t, mux)
	defer cleanup()
	g := &Gitea{}
	rel, err := g.FetchRelease("codeberg.org/foo/bar", "v3")
	if err != nil {
		t.Fatalf("%v", err)
	}
	if rel.Version != "v3" || len(rel.Assets) != 1 {
		t.Errorf("%+v", rel)
	}
}

func TestDocker_Install_NoRuntime(t *testing.T) {
	oldPath := os.Getenv("PATH")
	defer os.Setenv("PATH", oldPath)
	os.Setenv("PATH", "/nonexistent")
	d := &Docker{}
	if _, err := d.Install("docker://x/y", "latest", t.TempDir(), nil); err == nil {
		t.Error("expected no-runtime error")
	}
}
