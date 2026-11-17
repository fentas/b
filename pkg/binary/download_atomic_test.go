package binary

import (
	"archive/zip"
	"bytes"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/fentas/b/pkg/provider"
)

// randomBytes are n bytes that gzip cannot make much smaller, so that a cut
// archive is cut in the middle of the file.
func randomBytes(n int) []byte {
	data := make([]byte, n)
	rand.New(rand.NewSource(1)).Read(data)
	return data
}

// cutTarGz is a tar.gz with the executable file name, cut in the middle of
// that file: a download that ended halfway.
func cutTarGz(t *testing.T, name string) []byte {
	t.Helper()
	data := makeTarGz(t, tarEntry{name: name, mode: 0755, content: randomBytes(64 << 10)}).Bytes()
	return data[:len(data)/2]
}

// badChecksumZip is a zip with the file name, whose content does not match
// its CRC-32: the zip reader returns an error only after the last byte.
func badChecksumZip(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()
	i := bytes.Index(data, content)
	if i < 0 {
		t.Fatal("the stored content is not in the zip")
	}
	data[i] ^= 0xff
	return data
}

// noTempFiles fails the test when a temp file of a download is in dir.
func noTempFiles(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("temp file %s is still in %s", e.Name(), dir)
		}
	}
}

// TestDownload_ReplacesTheBinaryInOneStep: each path that writes a binary
// (a preset as a raw file, a tar.gz or a zip; a release asset as a raw file,
// a tar.gz or a zip) replaces it in one step. A download that fails (an
// empty body, a connection that closes halfway, a cut archive, a zip with a
// bad checksum) leaves the old binary as it was, and no temp file. A
// download that works puts a new file in place: a hard link to the old file
// keeps the old content.
func TestDownload_ReplacesTheBinaryInOneStep(t *testing.T) {
	newTool := []byte("tool v2\n")
	cutConnection := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "1000")
		_, _ = w.Write(newTool)
		w.(http.Flusher).Flush()
		panic(http.ErrAbortHandler)
	}
	serve := func(body []byte) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			_, _ = w.Write(body)
		}
	}
	for _, tc := range []struct {
		name    string
		asset   string // the name of the release asset; "" for a preset
		archive string // "tar.gz" or "zip" for a preset; "" for a raw file
		good    []byte
		bad     http.HandlerFunc
	}{
		{name: "a raw preset with an empty body", good: newTool, bad: serve(nil)},
		{name: "a raw preset whose connection closes halfway", good: newTool, bad: cutConnection},
		{name: "a tar.gz preset cut halfway", archive: "tar.gz", good: makeTarGz(t, tarEntry{name: "tool", mode: 0755, content: newTool}).Bytes(), bad: serve(cutTarGz(t, "tool"))},
		{name: "a zip preset with a bad checksum", archive: "zip", good: makeZip(t, map[string][]byte{"tool": newTool}).Bytes(), bad: serve(badChecksumZip(t, "tool", newTool))},
		{name: "a raw asset with an empty body", asset: "tool", good: newTool, bad: serve(nil)},
		{name: "a raw asset whose connection closes halfway", asset: "tool", good: newTool, bad: cutConnection},
		{name: "a tar.gz asset cut halfway", asset: "tool.tar.gz", good: makeTarGz(t, tarEntry{name: "tool", mode: 0755, content: newTool}).Bytes(), bad: serve(cutTarGz(t, "tool"))},
		{name: "a zip asset with a bad checksum", asset: "tool.zip", good: makeZip(t, map[string][]byte{"tool": newTool}).Bytes(), bad: serve(badChecksumZip(t, "tool", newTool))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			file := filepath.Join(dir, "tool")
			if err := os.WriteFile(file, []byte("tool v1\n"), 0755); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(t.TempDir(), "link")
			if err := os.Link(file, link); err != nil {
				t.Skipf("no hard links here: %v", err)
			}
			var handler atomic.Value // http.HandlerFunc
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				handler.Load().(http.HandlerFunc)(w, r)
			}))
			defer srv.Close()
			download := func() error {
				b := &Binary{Name: "tool", File: file}
				if tc.asset != "" {
					b.AutoDetect = true
					b.ProviderRef = "github.com/org/tool"
					b.ResolvedAsset = &provider.Asset{URL: srv.URL + "/" + tc.asset, Name: tc.asset}
				} else {
					b.URL, b.Version = srv.URL+"/tool", "v2"
					b.IsTarGz = tc.archive == "tar.gz"
					b.IsZip = tc.archive == "zip"
				}
				return b.DownloadBinary()
			}

			handler.Store(tc.bad)
			if err := download(); err == nil {
				t.Fatal("the failed download: no error")
			}
			if got, err := os.ReadFile(file); err != nil || string(got) != "tool v1\n" {
				t.Errorf("after the failed download, the binary is %q (%v), want the old binary", got, err)
			}
			noTempFiles(t, dir)

			handler.Store(serve(tc.good))
			if err := download(); err != nil {
				t.Fatalf("download: %v", err)
			}
			if got, err := os.ReadFile(file); err != nil || !bytes.Equal(got, newTool) {
				t.Errorf("the binary is %q (%v), want %q", got, err, newTool)
			}
			if got, err := os.ReadFile(link); err != nil || string(got) != "tool v1\n" {
				t.Errorf("a hard link to the old binary is %q (%v), want the old binary: the download wrote into it", got, err)
			}
			fi, err := os.Stat(file)
			if err != nil {
				t.Fatal(err)
			}
			if fi.Mode().Perm() != 0755 {
				t.Errorf("mode %v, want 0755", fi.Mode().Perm())
			}
			noTempFiles(t, dir)
		})
	}
}
