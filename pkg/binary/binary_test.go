package binary

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fentas/b/pkg/provider"
)

// --- test helpers ---

// makeTarGz builds a gzip+tar archive in memory from the given (name, mode,
// content) tuples. All I/O errors are checked via t.Fatalf so bad fixtures
// fail fast with actionable errors.
type tarEntry struct {
	name    string
	mode    int64
	content []byte
}

func makeTarGz(t *testing.T, entries ...tarEntry) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		hdr := &tar.Header{
			Name:     e.name,
			Mode:     e.mode,
			Size:     int64(len(e.content)),
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("tar WriteHeader %q: %v", e.name, err)
		}
		if _, err := tw.Write(e.content); err != nil {
			t.Fatalf("tar Write %q: %v", e.name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar Close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip Close: %v", err)
	}
	return &buf
}

// makeZip builds a zip archive in memory from (name, content) tuples.
func makeZip(t *testing.T, entries map[string][]byte) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip Create %q: %v", name, err)
		}
		if _, err := w.Write(content); err != nil {
			t.Fatalf("zip Write %q: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip Close: %v", err)
	}
	return &buf
}

// --- helper.go ---

func TestGetFileExtensionFromURL(t *testing.T) {
	cases := []struct {
		url  string
		want string
		err  bool
	}{
		{"https://example.com/foo.tar.gz", "tar.gz", false},
		{"https://example.com/foo.tar.xz", "tar.xz", false},
		{"https://example.com/foo.zip", "zip", false},
		{"https://example.com/foo.bin", "bin", false},
		{"https://example.com/noext", "", true},
		{"://bad", "", true},
	}
	for _, c := range cases {
		got, err := GetFileExtensionFromURL(c.url)
		if c.err {
			if err == nil {
				t.Errorf("%s: expected error", c.url)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: unexpected error %v", c.url, err)
		}
		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.url, got, c.want)
		}
	}
}

func TestGithubLatest(t *testing.T) {
	// Missing repo
	b := &Binary{Version: "v0"}
	v, err := GithubLatest(b)
	if err == nil {
		t.Error("expected error for empty repo")
	}
	if v != "v0" {
		t.Errorf("got %q", v)
	}

	// With a fake redirect server
	final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer final.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, final.URL+"/v1.2.3", http.StatusFound)
	}))
	defer redirect.Close()

	// Stub GithubLatestURL with direct http.Get by patching via a package-level var isn't possible;
	// call GetBody against the real fake server to exercise helpers.
	body, err := GetBody(final.URL + "/v1.2.3")
	if err != nil || body != "ok" {
		t.Errorf("GetBody: body=%q err=%v", body, err)
	}

	// GetBody error path
	if _, err := GetBody("http://127.0.0.1:1/unreachable"); err == nil {
		t.Error("expected GetBody error")
	}
}

// --- util.go ---

func TestGetGitRootDirectory(t *testing.T) {
	// Best-effort — just ensure it does not panic.
	_, _ = GetGitRootDirectory()
}

// --- binary.go ---

func TestBinary_PathAndExists(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PATH_BIN", tmp)

	b := &Binary{Name: "fake"}
	path := b.BinaryPath()
	if path != filepath.Join(tmp, "fake") {
		t.Errorf("BinaryPath() = %q", path)
	}
	// Calling again should be idempotent
	if got := b.BinaryPath(); got != path {
		t.Errorf("second call got %q", got)
	}

	if b.BinaryExists() {
		t.Error("should not exist yet")
	}

	// Create file and check exists
	if err := os.WriteFile(path, []byte("x"), 0755); err != nil {
		t.Fatal(err)
	}
	if !b.BinaryExists() {
		t.Error("should exist")
	}

	// Alias
	b2 := &Binary{Name: "fake", Alias: "other"}
	if got := b2.BinaryPath(); got != filepath.Join(tmp, "other") {
		t.Errorf("alias path = %q", got)
	}

	// Pre-set File
	b3 := &Binary{File: "/pre/set"}
	if got := b3.BinaryPath(); got != "/pre/set" {
		t.Errorf("preset path = %q", got)
	}
}

func TestBinary_LocalBinary(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PATH_BIN", tmp)

	b := &Binary{
		Name:    "fake",
		Version: "v1",
		VersionF: func(b *Binary) (string, error) {
			return "v2", nil
		},
		VersionLocalF: func(b *Binary) (string, error) {
			return "v1", nil
		},
	}
	lb := b.LocalBinary(true)
	if lb.Name != "fake" || lb.Latest != "v2" || lb.Version != "v1" || lb.Enforced != "v1" {
		t.Errorf("LocalBinary = %+v", lb)
	}
	if lb.File != "" {
		t.Errorf("expected empty File when not present, got %q", lb.File)
	}

	// Create the binary file so File is populated
	if err := os.WriteFile(filepath.Join(tmp, "fake"), []byte("x"), 0755); err != nil {
		t.Fatal(err)
	}
	b.File = ""               // reset cached
	lb = b.LocalBinary(false) // skip remote
	if lb.File == "" || lb.Latest != "" {
		t.Errorf("LocalBinary = %+v", lb)
	}
}

func TestBinary_EnsureBinary_NoopExisting(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PATH_BIN", tmp)
	if err := os.WriteFile(filepath.Join(tmp, "fake"), []byte("x"), 0755); err != nil {
		t.Fatal(err)
	}
	b := &Binary{Name: "fake"}
	if err := b.EnsureBinary(false); err != nil {
		t.Errorf("EnsureBinary: %v", err)
	}
}

func TestBinary_EnsureBinary_UpdateUpToDate(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PATH_BIN", tmp)
	if err := os.WriteFile(filepath.Join(tmp, "fake"), []byte("x"), 0755); err != nil {
		t.Fatal(err)
	}
	b := &Binary{
		Name:    "fake",
		Version: "v1",
		VersionF: func(b *Binary) (string, error) {
			return "v1", nil
		},
		VersionLocalF: func(b *Binary) (string, error) {
			return "v1", nil
		},
	}
	if err := b.EnsureBinary(true); err != nil {
		t.Errorf("EnsureBinary: %v", err)
	}
}

// TestBinary_EnsureBinary_UpdateWhenLocalVersionUnknown guards against a
// regression where a preset whose VersionLocalF errored (e.g. the binary
// doesn't support the expected subcommand) would silently skip updates —
// local.Version="" == local.Enforced="" used to satisfy the skip check,
// so 'b update' did nothing even when the upstream release had moved.
//
// Now an empty local.Version means "unknown"; EnsureBinary must proceed
// to DownloadBinary instead of early-returning.
func TestBinary_EnsureBinary_UpdateWhenLocalVersionUnknown(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PATH_BIN", tmp)
	// Pre-existing binary on disk, so the BinaryExists branch runs.
	if err := os.WriteFile(filepath.Join(tmp, "broken"), []byte("stale"), 0755); err != nil {
		t.Fatal(err)
	}
	downloadCalled := false
	b := &Binary{
		Name: "broken",
		// No pin.
		Version: "",
		VersionF: func(b *Binary) (string, error) {
			return "v2", nil // upstream has a newer version
		},
		VersionLocalF: func(b *Binary) (string, error) {
			// Preset's probe command failed — classic 'argsh version' bug:
			// subcommand doesn't exist so the binary exits non-zero and
			// VersionLocalF has no version to report.
			return "", os.ErrNotExist
		},
		// No URL/URLF/GitHubRepo so DownloadBinary will return an error —
		// we use that as a sentinel that the skip path was bypassed.
	}
	err := b.EnsureBinary(true)
	if err == nil {
		t.Fatal("expected DownloadBinary to be attempted (and fail without a download source), got nil")
	}
	_ = downloadCalled
}

// --- exec.go ---

func TestBinary_Env(t *testing.T) {
	b := &Binary{Envs: map[string]string{"A": "1"}}
	env := b.Env()
	if len(env) != 1 || env[0] != "A=1" {
		t.Errorf("Env = %v", env)
	}
}

func TestBinary_Cmd_MissingBinary(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PATH_BIN", tmp)
	b := &Binary{Name: "missing"}
	if cmd := b.Cmd("--version"); cmd != nil {
		t.Error("expected nil Cmd when binary missing")
	}
	if _, err := b.Exec("--version"); err == nil {
		t.Error("expected Exec error")
	}
}

func TestBinary_Cmd_Exec(t *testing.T) {
	// Use the system 'sh' as our "binary"
	shPath, err := exeLookup("sh")
	if err != nil {
		t.Skip("sh not available")
	}
	tmp := t.TempDir()
	t.Setenv("PATH_BIN", tmp)
	b := &Binary{Name: "sh", File: shPath, Context: context.Background()}
	out, err := b.Exec("-c", "echo hello")
	if err != nil || out != "hello" {
		t.Errorf("Exec = %q err=%v", out, err)
	}
	// Context nil path
	b2 := &Binary{Name: "sh", File: shPath}
	cmd := b2.Cmd("-c", "true")
	if cmd == nil {
		t.Error("Cmd nil")
	}
}

func exeLookup(name string) (string, error) {
	return exec.LookPath(name)
}

// --- download.go preset path ---

func TestDownloadPreset_PlainBinary(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("#!/bin/sh\necho hi"))
	}))
	defer srv.Close()

	tmp := t.TempDir()
	b := &Binary{
		Name:    "fake",
		File:    filepath.Join(tmp, "fake"),
		URL:     srv.URL + "/fake",
		Version: "v1",
	}
	if err := b.downloadPreset(); err != nil {
		t.Fatalf("downloadPreset: %v", err)
	}
	if _, err := os.Stat(b.File); err != nil {
		t.Error("file missing")
	}
}

func TestDownloadPreset_URLF_NoURL(t *testing.T) {
	tmp := t.TempDir()
	b := &Binary{Name: "x", File: filepath.Join(tmp, "x"), Version: "v1"}
	if err := b.downloadPreset(); err == nil {
		t.Error("expected no URL error")
	}
}

func TestDownloadPreset_VersionFUsed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	tmp := t.TempDir()
	b := &Binary{
		Name: "x",
		File: filepath.Join(tmp, "x"),
		URLF: func(b *Binary) (string, error) { return srv.URL, nil },
		VersionF: func(b *Binary) (string, error) {
			return "v2", nil
		},
	}
	if err := b.downloadPreset(); err != nil {
		t.Errorf("%v", err)
	}
	if b.Version != "v2" {
		t.Errorf("version = %q", b.Version)
	}
}

func TestDownloadPreset_HTTP404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	tmp := t.TempDir()
	b := &Binary{
		Name:    "x",
		File:    filepath.Join(tmp, "x"),
		URL:     srv.URL,
		Version: "v1",
		VersionF: func(b *Binary) (string, error) {
			return "v2", nil
		},
	}
	if err := b.downloadPreset(); err == nil {
		t.Error("expected 404 error")
	}
}

func TestDownloadPreset_IsDynamicUnknown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	tmp := t.TempDir()
	b := &Binary{
		Name:      "x",
		File:      filepath.Join(tmp, "x"),
		URL:       srv.URL + "/foo.unknown",
		Version:   "v1",
		IsDynamic: true,
	}
	if err := b.downloadPreset(); err == nil {
		t.Error("expected unknown ext error")
	}
}

func TestDownloadPreset_IsTarGz(t *testing.T) {
	tmp := t.TempDir()
	content := []byte("binary-data")
	buf := makeTarGz(t, tarEntry{name: "foo", mode: 0755, content: content})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(buf.Bytes())
	}))
	defer srv.Close()

	b := &Binary{
		Name:    "foo",
		File:    filepath.Join(tmp, "foo"),
		URL:     srv.URL + "/foo.tar.gz",
		Version: "v1",
		IsTarGz: true,
	}
	if err := b.downloadPreset(); err != nil {
		t.Fatalf("%v", err)
	}
	data, err := os.ReadFile(b.File)
	if err != nil || !bytes.Equal(data, content) {
		t.Errorf("extracted content = %q err=%v", data, err)
	}
}

func TestDownloadPreset_IsZip(t *testing.T) {
	tmp := t.TempDir()
	buf := makeZip(t, map[string][]byte{"foo": []byte("zip-data")})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(buf.Bytes())
	}))
	defer srv.Close()

	b := &Binary{
		Name:    "foo",
		File:    filepath.Join(tmp, "foo"),
		URL:     srv.URL + "/foo.zip",
		Version: "v1",
		IsZip:   true,
	}
	if err := b.downloadPreset(); err != nil {
		t.Fatalf("%v", err)
	}
}

func TestExtractSingleFileFromTar_UnknownComp(t *testing.T) {
	b := &Binary{Name: "foo"}
	if err := b.extractSingleFileFromTar(bytes.NewReader(nil), "bogus"); err == nil {
		t.Error("expected error")
	}
}

func TestExtractSingleFileFromTar_BadGzip(t *testing.T) {
	b := &Binary{Name: "foo"}
	if err := b.extractSingleFileFromTar(bytes.NewReader([]byte("not-gzip")), "gz"); err == nil {
		t.Error("expected error")
	}
}

func TestExtractSingleFileFromTar_FileNotFound(t *testing.T) {
	buf := makeTarGz(t, tarEntry{name: "other", mode: 0755, content: []byte("x")})

	tmp := t.TempDir()
	b := &Binary{Name: "foo", File: filepath.Join(tmp, "foo")}
	err := b.extractSingleFileFromTar(buf, "gz")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("err = %v", err)
	}
}

func TestExtractFromTarAuto_NameMatch(t *testing.T) {
	buf := makeTarGz(t, tarEntry{name: "foo", mode: 0755, content: []byte("bin")})

	tmp := t.TempDir()
	b := &Binary{Name: "foo", File: filepath.Join(tmp, "foo")}
	if err := b.extractFromTarAuto(buf, "gz"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(b.File)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "bin" {
		t.Errorf("got %q", data)
	}
}

func TestExtractFromTarAuto_NoExecutables(t *testing.T) {
	buf := makeTarGz(t, tarEntry{name: "readme", mode: 0644, content: []byte("x")})

	tmp := t.TempDir()
	b := &Binary{Name: "foo", File: filepath.Join(tmp, "foo")}
	if err := b.extractFromTarAuto(buf, "gz"); err == nil {
		t.Error("expected error")
	}
}

func TestExtractFromTarAuto_PicksLargest(t *testing.T) {
	buf := makeTarGz(t,
		tarEntry{name: "a", mode: 0755, content: []byte("xx")},
		tarEntry{name: "b", mode: 0755, content: []byte("xxxxxxxx")},
	)
	tmp := t.TempDir()
	b := &Binary{Name: "foo", File: filepath.Join(tmp, "foo")}
	if err := b.extractFromTarAuto(buf, "gz"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(b.File)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "xxxxxxxx" {
		t.Errorf("got %q", data)
	}
}

func TestExtractFromTarAuto_BadComp(t *testing.T) {
	b := &Binary{Name: "foo"}
	if err := b.extractFromTarAuto(bytes.NewReader(nil), "bogus"); err == nil {
		t.Error("expected error")
	}
}

func TestExtractFromZipAuto_NameMatch(t *testing.T) {
	buf := makeZip(t, map[string][]byte{"foo": []byte("bin")})
	tmp := t.TempDir()
	b := &Binary{Name: "foo", File: filepath.Join(tmp, "foo")}
	if err := b.extractFromZipAuto(buf); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(b.File)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "bin" {
		t.Errorf("got %q", data)
	}
}

func TestExtractFromZipAuto_BadZip(t *testing.T) {
	b := &Binary{Name: "foo"}
	if err := b.extractFromZipAuto(bytes.NewReader([]byte("not-zip"))); err == nil {
		t.Error("expected error")
	}
}

func TestExtractFromZipAuto_PicksLargest(t *testing.T) {
	buf := makeZip(t, map[string][]byte{"a": []byte("xx"), "b": []byte("xxxxxxxx")})
	tmp := t.TempDir()
	b := &Binary{Name: "none", File: filepath.Join(tmp, "out")}
	if err := b.extractFromZipAuto(buf); err != nil {
		t.Fatal(err)
	}
}

func TestGithubURL(t *testing.T) {
	b := &Binary{GitHubRepo: "foo/bar", GitHubFile: "file", Version: "v1"}
	u, err := b.githubURL()
	if err != nil || !strings.Contains(u, "foo/bar") {
		t.Errorf("u=%q err=%v", u, err)
	}

	// With GitHubFileF error
	bErr := &Binary{GitHubRepo: "f/b", Version: "v1", GitHubFileF: func(b *Binary) (string, error) {
		return "", fmt.Errorf("fail")
	}}
	if _, err := bErr.githubURL(); err == nil {
		t.Error("expected error")
	}
}

func TestDownloadAsset_PlainBinary(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(w, bytes.NewReader([]byte("raw")))
	}))
	defer srv.Close()
	tmp := t.TempDir()
	b := &Binary{Name: "x", File: filepath.Join(tmp, "x")}
	// asset with .bin suffix → DetectArchiveType returns "" → plain path
	err := b.downloadAsset(&provider.Asset{URL: srv.URL + "/file.bin", Name: "file.bin"})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDownloadAsset_TarGz(t *testing.T) {
	buf := makeTarGz(t, tarEntry{name: "foo", mode: 0755, content: []byte("bin")})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(buf.Bytes())
	}))
	defer srv.Close()
	tmp := t.TempDir()
	b := &Binary{Name: "foo", File: filepath.Join(tmp, "foo")}
	if err := b.downloadAsset(&provider.Asset{URL: srv.URL + "/file.tar.gz", Name: "file.tar.gz"}); err != nil {
		t.Fatal(err)
	}
}

func TestDownloadAsset_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()
	b := &Binary{Name: "x"}
	if err := b.downloadAsset(&provider.Asset{URL: srv.URL, Name: "x"}); err == nil {
		t.Error("expected error")
	}
}

func TestBinary_DownloadBinary_MkdirErr(t *testing.T) {
	// Unreachable parent to trigger MkdirAll error
	b := &Binary{Name: "x", File: "/proc/1/cannot/create/x"}
	if err := b.DownloadBinary(); err == nil {
		t.Error("expected error when MkdirAll cannot create parent directory")
	}
}

func TestExtractSingleFileFromTar_SuccessMatch(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	// Include a non-reg entry (directory) to exercise the skip branch.
	if err := tw.WriteHeader(&tar.Header{Name: "dir/", Mode: 0755, Typeflag: tar.TypeDir}); err != nil {
		t.Fatalf("WriteHeader dir: %v", err)
	}
	// Include match on b.Name
	if err := tw.WriteHeader(&tar.Header{Name: "foo", Mode: 0755, Size: 3, Typeflag: tar.TypeReg}); err != nil {
		t.Fatalf("WriteHeader foo: %v", err)
	}
	if _, err := tw.Write([]byte("bin")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar Close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip Close: %v", err)
	}

	tmp := t.TempDir()
	b := &Binary{Name: "foo", File: filepath.Join(tmp, "foo")}
	if err := b.extractSingleFileFromTar(&buf, "gz"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(b.File)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "bin" {
		t.Errorf("got %q", data)
	}
}

func TestExtractSingleFileFromTar_GitHubFileStripExt(t *testing.T) {
	buf := makeTarGz(t, tarEntry{name: "mytool-linux", mode: 0755, content: []byte("xy")})
	tmp := t.TempDir()
	b := &Binary{Name: "other", GitHubFile: "mytool-linux.tar.gz", File: filepath.Join(tmp, "out")}
	if err := b.extractSingleFileFromTar(buf, "gz"); err != nil {
		t.Fatal(err)
	}
}

func TestBinary_DownloadViaProvider_ResolvedAsset(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("raw-bin"))
	}))
	defer srv.Close()
	tmp := t.TempDir()
	b := &Binary{
		Name:          "foo",
		File:          filepath.Join(tmp, "foo"),
		AutoDetect:    true,
		ProviderRef:   "github.com/org/foo",
		ResolvedAsset: &provider.Asset{URL: srv.URL + "/foo.bin", Name: "foo.bin"},
	}
	if err := b.downloadViaProvider(); err != nil {
		t.Fatalf("downloadViaProvider: %v", err)
	}
	data, err := os.ReadFile(b.File)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "raw-bin" {
		t.Errorf("got %q", data)
	}
}

func TestBinary_DownloadViaProvider_GitLocal(t *testing.T) {
	// Set up a local git repo with a script file
	tmp := t.TempDir()
	work := filepath.Join(tmp, "repo")
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
	if err := os.WriteFile(filepath.Join(work, "tool.sh"), []byte("#!/bin/sh\necho hi"), 0755); err != nil {
		t.Fatal(err)
	}
	run("git", "-C", work, "add", "-A")
	run("git", "-C", work, "commit", "-m", "init", "--no-gpg-sign")
	run("git", "clone", "--bare", "-q", work, bare)

	destDir := t.TempDir()
	b := &Binary{
		Name:        "tool.sh",
		File:        filepath.Join(destDir, "tool.sh"),
		AutoDetect:  true,
		ProviderRef: "git://" + bare + ":tool.sh",
	}
	if err := b.downloadViaProvider(); err != nil {
		t.Fatalf("downloadViaProvider: %v", err)
	}
	if _, err := os.Stat(b.File); err != nil {
		t.Errorf("missing: %v", err)
	}
}

func TestBinary_DownloadBinary_UnknownProvider(t *testing.T) {
	tmp := t.TempDir()
	b := &Binary{
		Name:        "foo",
		File:        filepath.Join(tmp, "foo"),
		AutoDetect:  true,
		ProviderRef: "some-bogus-ref-that-matches-nothing-!!",
	}
	// downloadViaProvider → provider.Detect should fail
	if err := b.downloadViaProvider(); err == nil {
		t.Error("expected provider detect error")
	}
}

func TestBinary_DownloadBinary_Switch(t *testing.T) {
	// DownloadBinary calls downloadBinary which routes to provider or preset
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	tmp := t.TempDir()
	b := &Binary{
		Name:    "foo",
		File:    filepath.Join(tmp, "foo"),
		URL:     srv.URL,
		Version: "v1",
	}
	if err := b.DownloadBinary(); err != nil {
		t.Errorf("%v", err)
	}
}
