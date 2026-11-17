package cli

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fentas/b/pkg/binary"
	"github.com/fentas/b/pkg/fetch"
	"github.com/fentas/b/pkg/lock"
	"github.com/fentas/goodies/streams"
)

// runB runs `b <args>` through the root command, with presets as the
// presets and the b.yaml of the fixture. It returns what b wrote to stderr
// and the error of the command.
func (f *pinsFixture) runB(t *testing.T, presets []*binary.Binary, args ...string) (string, error) {
	t.Helper()
	var errOut bytes.Buffer
	// The commands install the binaries in parallel, and each goroutine
	// writes to IO.ErrOut; a bytes.Buffer does not take concurrent writes.
	io := &streams.IO{
		In:     strings.NewReader(""),
		Out:    &syncWriter{w: &bytes.Buffer{}},
		ErrOut: &syncWriter{w: &errOut},
	}
	root := NewRootCmd(presets, io, "test", "")
	root.SetArgs(append([]string{"--config", f.config}, args...))
	err := root.Execute()
	return errOut.String(), err
}

// fastRetries makes the download retries of this test short.
func fastRetries(t *testing.T) {
	t.Helper()
	old := fetch.DefaultPolicy
	fetch.DefaultPolicy = fetch.Policy{Attempts: 2, BaseDelay: time.Millisecond, MaxDelay: 10 * time.Millisecond, MaxWait: time.Second}
	t.Cleanup(func() { fetch.DefaultPolicy = old })
}

// tempFiles are the temp files of downloads in dir.
func tempFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var tmp []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			tmp = append(tmp, e.Name())
		}
	}
	return tmp
}

// toolTarGz is a tar.gz with the executable file tool and content.
func toolTarGz(t *testing.T, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "tool", Mode: 0755, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestInstallAndUpdate_FailedDownloadKeepsTheBinary: a download that fails
// after the server sent a part of the body (an empty body, a connection
// that closes halfway, a tar.gz that ends halfway, a zip with a bad
// checksum) leaves the old binary as it was, in `b install --force` and in
// `b update --force`, and leaves no temp file. b install returns the error.
// A later `b install` without a version keeps that binary, and b.lock keeps
// its version: it must never record an incomplete file.
func TestInstallAndUpdate_FailedDownloadKeepsTheBinary(t *testing.T) {
	// A tar.gz of 64 KiB that gzip cannot make smaller, cut in the middle
	// of the file.
	big := make([]byte, 64<<10)
	rand.New(rand.NewSource(1)).Read(big)
	cutTarGz := toolTarGz(t, big)
	cutTarGz = cutTarGz[:len(cutTarGz)/2]

	// A zip whose stored content does not match its CRC-32: the zip
	// reader returns the error only after the last byte.
	newTool := []byte("tool v2.0.0\n")
	var zipBuf bytes.Buffer
	zw := zip.NewWriter(&zipBuf)
	w, err := zw.CreateHeader(&zip.FileHeader{Name: "tool", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(newTool); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	badZip := zipBuf.Bytes()
	badZip[bytes.Index(badZip, newTool)] ^= 0xff

	serve := func(body []byte) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			_, _ = w.Write(body)
		}
	}
	for _, tc := range []struct {
		name    string
		archive string // "tar.gz" or "zip"; "" for a raw file
		serve   http.HandlerFunc
	}{
		{name: "an empty body", serve: serve(nil)},
		{name: "a connection that closes halfway", serve: func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Length", "1000")
			_, _ = w.Write(newTool)
			w.(http.Flusher).Flush()
			panic(http.ErrAbortHandler)
		}},
		{name: "a tar.gz that ends halfway", archive: "tar.gz", serve: serve(cutTarGz)},
		{name: "a zip with a bad checksum", archive: "zip", serve: serve(badZip)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPinsFixture(t)
			fastRetries(t)
			mustWrite(t, f.config, []byte("binaries:\n  tool: {}\n"))
			f.present(t, "v1.0.0", true)
			var hits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				tc.serve(w, r)
			}))
			defer srv.Close()
			presets := func() []*binary.Binary {
				return []*binary.Binary{{
					Name:       "tool",
					GitHubRepo: "test/tool",
					Context:    context.Background(),
					URLF:       func(b *binary.Binary) (string, error) { return srv.URL + "/" + b.Version, nil },
					VersionF:   func(*binary.Binary) (string, error) { return "v2.0.0", nil },
					IsTarGz:    tc.archive == "tar.gz",
					IsZip:      tc.archive == "zip",
				}}
			}
			kept := func(cmd string) {
				t.Helper()
				if got := f.file(t); got != "tool v1.0.0\n" {
					t.Errorf("after %s, .bin/tool is %q, want the old v1.0.0 binary", cmd, got)
				}
				if e, describes := f.entry(t); e.Version != "v1.0.0" || !describes {
					t.Errorf("after %s, b.lock says %q and describes the file = %v, want v1.0.0 and true", cmd, e.Version, describes)
				}
				if tmp := tempFiles(t, f.binDir); len(tmp) > 0 {
					t.Errorf("after %s, temp files in .bin: %v", cmd, tmp)
				}
			}

			if _, err := f.runB(t, presets(), "install", "--force", "tool@v2.0.0"); err == nil || !strings.Contains(err.Error(), "download failed") {
				t.Errorf("b install --force: error %v, want the error of the download", err)
			}
			kept("b install --force")

			// b update shows a failed download in its progress output.
			_, _ = f.runB(t, presets(), "update", "--force", "tool")
			kept("b update --force")
			if hits.Load() == 0 {
				t.Fatal("no download: the test checks nothing")
			}

			hits.Store(0)
			if _, err := f.runB(t, presets(), "install", "tool"); err != nil {
				t.Errorf("b install: %v", err)
			}
			if n := hits.Load(); n != 0 {
				t.Errorf("b install without a version: %d downloads, want 0", n)
			}
			kept("b install")
		})
	}
}

// TestInstall_FailedReleaseAssetKeepsTheBinary: a release asset of a provider
// ref with an empty body leaves the old binary and its b.lock row as they
// were.
func TestInstall_FailedReleaseAssetKeepsTheBinary(t *testing.T) {
	f := newPinsFixture(t)
	f.serveRefs(t)
	mustWrite(t, f.config, []byte("binaries:\n  fakerel://a/tool:\n    version: v1.0.0\n"))
	hash := f.binFile(t, "tool", "tool a/v1.0.0\n")
	f.writeLock(t, lock.BinEntry{Name: "tool", Version: "v1.0.0", SHA256: hash, Source: "fakerel://a/tool", Provider: "fakerel"})
	f.empty["a/v2.0.0"] = true

	if _, err := f.runB(t, f.presets(), "install", "--force", "fakerel://a/tool@v2.0.0"); err == nil || !strings.Contains(err.Error(), "wrote 0 bytes") {
		t.Errorf("b install --force: error %v, want the error of the empty download", err)
	}
	if n := f.hits.Load(); n == 0 {
		t.Fatal("no download: the test checks nothing")
	}
	f.checkRow(t, f.readLock(t), "tool", "tool a/v1.0.0\n", "v1.0.0")
	if tmp := tempFiles(t, f.binDir); len(tmp) > 0 {
		t.Errorf("temp files in .bin: %v", tmp)
	}
}

// TestInstall_DownloadReplacesTheBinaryInOneStep: `b install --force` puts a
// new file in place of the old binary, so a program that runs the old binary
// never reads a part of the new one. A hard link to the old file keeps the
// old content.
func TestInstall_DownloadReplacesTheBinaryInOneStep(t *testing.T) {
	f := newPinsFixture(t)
	f.present(t, "v1.0.0", true)
	link := filepath.Join(f.dir, "old-tool")
	if err := os.Link(filepath.Join(f.binDir, "tool"), link); err != nil {
		t.Skipf("no hard links here: %v", err)
	}
	if _, err := f.runB(t, f.presets(), "install", "--force", "tool@v2.0.0"); err != nil {
		t.Fatalf("b install --force: %v", err)
	}
	if got := f.file(t); got != "tool v2.0.0\n" {
		t.Errorf(".bin/tool is %q, want the v2.0.0 binary", got)
	}
	if got, err := os.ReadFile(link); err != nil || string(got) != "tool v1.0.0\n" {
		t.Errorf("a hard link to the old binary is %q (%v), want the v1.0.0 binary: the install wrote into it", got, err)
	}
	fi, err := os.Stat(filepath.Join(f.binDir, "tool"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0755 {
		t.Errorf(".bin/tool has mode %v, want 0755", fi.Mode().Perm())
	}
	if tmp := tempFiles(t, f.binDir); len(tmp) > 0 {
		t.Errorf("temp files in .bin: %v", tmp)
	}
}

// TestInstallAdd_FailedDownloadWritesNoConfig: `b install --add` with a
// download that fails returns the error and leaves b.yaml as it was, also
// when another binary of the same command installs (here: other, whose
// entry --add would move to v2.0.0). b.yaml must not name a binary that is
// not installed. The binary that installed is in b.lock.
func TestInstallAdd_FailedDownloadWritesNoConfig(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		ok   string // a binary that installs, "" for none
	}{
		{name: "a preset", args: []string{"tool@v3.0.0"}},
		{name: "a provider ref", args: []string{"fakerel://a/tool@v3.0.0"}},
		{name: "a preset beside one that installs", args: []string{"tool@v3.0.0", "other@v2.0.0"}, ok: "other"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPinsFixture(t)
			f.serveRefs(t)
			f.missing["v3.0.0"] = true
			f.missing["a/v3.0.0"] = true
			config := "binaries:\n  other:\n    version: v1.0.0\n"
			mustWrite(t, f.config, []byte(config))

			_, err := f.runB(t, f.presets(), append([]string{"install", "--add"}, tc.args...)...)
			if err == nil || !strings.Contains(err.Error(), "download failed") || !strings.Contains(err.Error(), "--add did not change b.yaml") {
				t.Errorf("b install --add: error %v, want the error of the download", err)
			}
			if got, err := os.ReadFile(f.config); err != nil || string(got) != config {
				t.Errorf("b.yaml is %q (%v), want it as it was", got, err)
			}
			if _, err := os.Stat(filepath.Join(f.binDir, "tool")); !os.IsNotExist(err) {
				t.Errorf(".bin/tool: %v, want no file", err)
			}
			if tc.ok != "" {
				f.checkRow(t, f.readLock(t), tc.ok, tc.ok+" v2.0.0\n", "v2.0.0")
			}
		})
	}
}
