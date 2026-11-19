package binary

import (
	"archive/zip"
	"bytes"
	"errors"
	"hash/crc32"
	"io"
	"io/fs"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"testing/iotest"

	"github.com/fentas/b/pkg/provider"
)

// archivePath is a way to install a binary from an archive: a preset or a
// release asset, as a tar.gz or a zip.
type archivePath struct {
	name  string
	kind  string // "tar.gz" or "zip"
	asset bool   // a release asset; else a preset
}

var archivePaths = []archivePath{
	{name: "a tar.gz preset", kind: "tar.gz"},
	{name: "a zip preset", kind: "zip"},
	{name: "a tar.gz asset", kind: "tar.gz", asset: true},
	{name: "a zip asset", kind: "zip", asset: true},
}

// oldBinary is the binary before each install of these tests.
const oldBinary = "tool v1\n"

// member is a file in a test archive. mode 0 is a regular file with mode
// 0755.
type member struct {
	name    string
	content string
	mode    fs.FileMode
}

// archiveOf is an archive of kind ("tar.gz" or "zip") that holds the members
// in their order. Two members can have the same name.
func archiveOf(t *testing.T, kind string, members ...member) []byte {
	t.Helper()
	if kind == "tar.gz" {
		var entries []tarEntry
		for _, m := range members {
			mode := int64(0755)
			if m.mode != 0 {
				mode = int64(m.mode.Perm())
			}
			entries = append(entries, tarEntry{name: m.name, mode: mode, content: []byte(m.content)})
		}
		return compressed(t, "tar.gz", tarOf(t, entries...))
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, m := range members {
		fh := &zip.FileHeader{Name: m.name, Method: zip.Store}
		mode := m.mode
		if mode == 0 {
			mode = 0755
		}
		fh.SetMode(mode)
		w, err := zw.CreateHeader(fh)
		if err != nil {
			t.Fatalf("zip CreateHeader %q: %v", m.name, err)
		}
		if _, err := io.WriteString(w, m.content); err != nil {
			t.Fatalf("zip Write %q: %v", m.name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip Close: %v", err)
	}
	return buf.Bytes()
}

// zipWithSize is a zip archive whose one stored member, tool, has the header
// size size but only the bytes of content.
func zipWithSize(t *testing.T, size uint64, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.CreateRaw(&zip.FileHeader{
		Name:               "tool",
		Method:             zip.Store,
		CRC32:              crc32.ChecksumIEEE(content),
		CompressedSize64:   uint64(len(content)),
		UncompressedSize64: size,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// binaryFor is the binary tool in dir, installed from rawURL on path p: an
// archive of kind p.kind, or a raw file when p.kind is "".
func binaryFor(dir, rawURL string, p archivePath) *Binary {
	b := &Binary{Name: "tool", File: filepath.Join(dir, "tool")}
	if p.asset {
		name := "tool"
		if p.kind != "" {
			name += "." + p.kind
		}
		b.AutoDetect = true
		b.ProviderRef = "github.com/org/tool"
		b.ResolvedAsset = &provider.Asset{URL: rawURL, Name: name}
		return b
	}
	b.URL, b.Version = rawURL, "v2"
	b.IsTarGz = p.kind == "tar.gz"
	b.IsTarXz = p.kind == "tar.xz"
	b.IsZip = p.kind == "zip"
	return b
}

// installFrom serves body and installs the binary tool from it on path p,
// over the old binary. It returns the binary after the install and the error
// of the install. No temp file may stay.
func installFrom(t *testing.T, p archivePath, body []byte) (string, error) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	suffix := "/dl/tool"
	if p.kind != "" {
		suffix += "." + p.kind
	}
	dir := t.TempDir()
	b := binaryFor(dir, srv.URL+suffix, p)
	if err := os.WriteFile(b.File, []byte(oldBinary), 0755); err != nil {
		t.Fatal(err)
	}
	err := b.DownloadBinary()
	noTempFiles(t, dir)
	got, rerr := os.ReadFile(b.File)
	if rerr != nil {
		t.Fatal(rerr)
	}
	return string(got), err
}

// wantRefused fails the test unless err is an error with the text want (or
// that contains it, when contains is true) and the binary is the old one.
func wantRefused(t *testing.T, got string, err error, want string, contains bool) {
	t.Helper()
	switch {
	case err == nil:
		t.Fatalf("no error, want %q; the binary is %q", want, got)
	case contains && !strings.Contains(err.Error(), want):
		t.Fatalf("error %q, want one that contains %q", err, want)
	case !contains && err.Error() != want:
		t.Fatalf("error %q, want %q", err, want)
	}
	if got != oldBinary {
		t.Errorf("after the refused install, the binary is %q, want the old binary", got)
	}
}

// wantInstalled fails the test unless the install had no error and the
// binary is want.
func wantInstalled(t *testing.T, got string, err error, want string) {
	t.Helper()
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if got != want {
		t.Errorf("the binary is %q, want %q", got, want)
	}
}

// withLimit sets *limit to n for the test.
func withLimit(t *testing.T, limit *int64, n int64) {
	t.Helper()
	old := *limit
	*limit = n
	t.Cleanup(func() { *limit = old })
}

// TestExtract_ZipSpecialFiles: a zip member that is not a regular file (a
// symlink, a directory or a device) is never the binary, on the preset and
// on the asset path, also when its name is the binary and it holds bytes (a
// symlink holds its target path). A regular file of the same name beside it
// is the one match.
func TestExtract_ZipSpecialFiles(t *testing.T) {
	notFound := map[bool]string{false: "file tool not found", true: "no file found in archive for tool"}
	for _, p := range []archivePath{{name: "a zip preset", kind: "zip"}, {name: "a zip asset", kind: "zip", asset: true}} {
		for _, special := range []struct {
			name string
			mode fs.FileMode
		}{
			{"a symlink", fs.ModeSymlink | 0777},
			{"a directory", fs.ModeDir | 0755},
			{"a device", fs.ModeDevice | 0644},
		} {
			t.Run(p.name+", "+special.name+" is not the binary", func(t *testing.T) {
				body := archiveOf(t, "zip", member{name: "tool", content: "/usr/local/bin/other-tool", mode: special.mode})
				got, err := installFrom(t, p, body)
				wantRefused(t, got, err, notFound[p.asset], false)
			})
			t.Run(p.name+", "+special.name+" beside the regular file", func(t *testing.T) {
				body := archiveOf(t, "zip",
					member{name: "tool", content: "/usr/local/bin/other-tool", mode: special.mode},
					member{name: "tool", content: "tool v2\n"})
				got, err := installFrom(t, p, body)
				wantInstalled(t, got, err, "tool v2\n")
			})
		}
	}
}

// TestExtract_UnsafeNames: an archive that holds a regular file with an
// absolute name or a ".." element is refused on every path, before the file
// can be selected and whether it comes before or after the binary. The error
// names the file, not the URL.
func TestExtract_UnsafeNames(t *testing.T) {
	for _, p := range archivePaths {
		for _, name := range []string{"/tool", "../tool", "bin/../../tool", `..\tool`, "/etc/passwd"} {
			for _, order := range []string{"before", "after"} {
				t.Run(p.name+", "+name+" "+order+" the binary", func(t *testing.T) {
					members := []member{{name: "tool", content: "tool v2\n"}, {name: name, content: "other\n"}}
					if order == "before" {
						members[0], members[1] = members[1], members[0]
					}
					got, err := installFrom(t, p, archiveOf(t, p.kind, members...))
					wantRefused(t, got, err, "refusing tool."+p.kind+": the file name "+strconv.Quote(name)+` is absolute, names a drive or has a ".." element`, false)
				})
			}
		}
		t.Run(p.name+", a name with a .. element that stays inside", func(t *testing.T) {
			// bin/../tool is tool after path.Clean: not unsafe. The zip preset
			// path compares the whole name, so it does not match there.
			got, err := installFrom(t, p, archiveOf(t, p.kind, member{name: "bin/../tool", content: "tool v2\n"}, member{name: "tool", content: "tool v2\n"}))
			if p.kind == "zip" && !p.asset {
				wantInstalled(t, got, err, "tool v2\n")
				return
			}
			wantRefused(t, got, err, `refusing tool.`+p.kind+`: 2 files in it match the binary tool: "bin/../tool", "tool"`, false)
		})
	}
}

// TestExtract_Ambiguous: an archive in which more than one file matches the
// binary is refused on every path, and the error names the files. A single
// match installs, and on the asset paths it wins over a larger file.
func TestExtract_Ambiguous(t *testing.T) {
	for _, p := range archivePaths {
		archive := "tool." + p.kind
		t.Run(p.name+", two dirs with the same name", func(t *testing.T) {
			got, err := installFrom(t, p, archiveOf(t, p.kind,
				member{name: "a/tool", content: "tool a\n"}, member{name: "b/tool", content: "tool b\n"}))
			if p.kind == "zip" && !p.asset {
				// The zip preset path compares the whole name.
				wantRefused(t, got, err, "file tool not found", false)
				return
			}
			wantRefused(t, got, err, "refusing "+archive+`: 2 files in it match the binary tool: "a/tool", "b/tool"`, false)
		})
		t.Run(p.name+", a duplicate entry", func(t *testing.T) {
			got, err := installFrom(t, p, archiveOf(t, p.kind,
				member{name: "tool", content: "tool v2\n"}, member{name: "tool", content: "tool v3\n"}))
			wantRefused(t, got, err, "refusing "+archive+`: 2 files in it match the binary tool: "tool", "tool"`, false)
		})
		t.Run(p.name+", a duplicate entry after other files", func(t *testing.T) {
			got, err := installFrom(t, p, archiveOf(t, p.kind,
				member{name: "tool", content: "tool v2\n"}, member{name: "README", content: "docs\n", mode: 0644},
				member{name: "tool", content: "tool v3\n"}))
			wantRefused(t, got, err, "refusing "+archive+`: 2 files in it match the binary tool: "tool", "tool"`, false)
		})
		t.Run(p.name+", a single match", func(t *testing.T) {
			got, err := installFrom(t, p, archiveOf(t, p.kind,
				member{name: "README", content: "docs\n", mode: 0644}, member{name: "tool", content: "tool v2\n"},
				member{name: "bin/helper", content: "a larger helper binary\n"}))
			wantInstalled(t, got, err, "tool v2\n")
		})
	}
}

// TestExtract_FallbackTie: on the asset paths, an archive with no file named
// like the binary installs its single largest candidate. Two largest
// candidates of the same size refuse the archive; a tie below the largest
// does not count.
func TestExtract_FallbackTie(t *testing.T) {
	for _, p := range archivePaths {
		if !p.asset {
			continue
		}
		archive := "tool." + p.kind
		for _, tc := range []struct {
			name    string
			members []member
			err     string // "" when the binary installs
			want    string
		}{
			{name: "a tie of the largest", members: []member{{name: "a", content: "1234"}, {name: "b", content: "5678"}},
				err: "refusing " + archive + `: no file in it is named tool, and 2 files tie as the largest: "a", "b"`},
			{name: "a tie of the largest after a smaller one", members: []member{{name: "s", content: "1"}, {name: "a", content: "1234"}, {name: "b", content: "5678"}},
				err: "refusing " + archive + `: no file in it is named tool, and 2 files tie as the largest: "a", "b"`},
			{name: "a tie below the largest, before it", members: []member{{name: "a", content: "12"}, {name: "b", content: "34"}, {name: "c", content: "largest"}},
				want: "largest"},
			{name: "a tie below the largest, after it", members: []member{{name: "c", content: "largest"}, {name: "a", content: "12"}, {name: "b", content: "34"}},
				want: "largest"},
			{name: "the single largest", members: []member{{name: "a", content: "12"}, {name: "c", content: "largest"}},
				want: "largest"},
		} {
			t.Run(p.name+", "+tc.name, func(t *testing.T) {
				got, err := installFrom(t, p, archiveOf(t, p.kind, tc.members...))
				if tc.err != "" {
					wantRefused(t, got, err, tc.err, false)
					return
				}
				wantInstalled(t, got, err, tc.want)
			})
		}
	}
}

// TestExtract_SizeLimit: a member, or a raw download, whose size is above
// maxBinarySize is refused by that size before the copy starts, on every
// path. (The copy itself stops one byte after the limit:
// TestWriteFile_SizeLimit.)
func TestExtract_SizeLimit(t *testing.T) {
	withLimit(t, &maxBinarySize, 4)
	for _, p := range append([]archivePath{
		{name: "a raw preset"},
		{name: "a raw asset", asset: true},
	}, archivePaths...) {
		t.Run(p.name+", a size above the limit", func(t *testing.T) {
			var body []byte
			want := "is too large: Content-Length is 8 bytes, and the limit for a binary is 4 bytes"
			if p.kind == "" {
				body = []byte("tool v2\n")
			} else {
				body = archiveOf(t, p.kind, member{name: "tool", content: "tool v2\n"})
				want = "extraction of \"tool\" from tool." + p.kind + " is too large: its size in the archive is 8 bytes, and the limit for a binary is 4 bytes"
			}
			got, err := installFrom(t, p, body)
			wantRefused(t, got, err, want, true)
		})
		t.Run(p.name+", a size at the limit", func(t *testing.T) {
			body := []byte("tool")
			if p.kind != "" {
				body = archiveOf(t, p.kind, member{name: "tool", content: "tool"})
			}
			got, err := installFrom(t, p, body)
			wantInstalled(t, got, err, "tool")
		})
	}
}

// TestExtract_HeaderSizeIsCheckedFirst: a member whose header size is above
// maxBinarySize is refused by the header before b reads one byte of it. The
// archive holds only 8 bytes of the member, so a read would fail with
// io.ErrUnexpectedEOF instead. (The tar paths read the whole archive before
// they select a member: there, a header size above the unpack limit refuses
// the archive first, TestExtract_TarHeaderSizeIsCheckedFirst.)
func TestExtract_HeaderSizeIsCheckedFirst(t *testing.T) {
	const size = 1 << 40
	limit := strconv.FormatInt(maxBinarySize, 10)
	for _, tc := range []struct {
		p    archivePath
		body func(t *testing.T) []byte
		size string
	}{
		{archivePath{name: "a zip preset", kind: "zip"}, func(t *testing.T) []byte { return zipWithSize(t, size, []byte("tool v2\n")) }, strconv.Itoa(size)},
		{archivePath{name: "a zip asset", kind: "zip", asset: true}, func(t *testing.T) []byte { return zipWithSize(t, size, []byte("tool v2\n")) }, strconv.Itoa(size)},
		{archivePath{name: "a zip preset, a size that does not fit an int64", kind: "zip"}, func(t *testing.T) []byte {
			return zipWithSize(t, math.MaxUint64, []byte("tool v2\n"))
		}, strconv.FormatUint(math.MaxUint64, 10)},
		{archivePath{name: "a zip asset, a size that does not fit an int64", kind: "zip", asset: true}, func(t *testing.T) []byte {
			return zipWithSize(t, math.MaxUint64, []byte("tool v2\n"))
		}, strconv.FormatUint(math.MaxUint64, 10)},
	} {
		t.Run(tc.p.name, func(t *testing.T) {
			got, err := installFrom(t, tc.p, tc.body(t))
			wantRefused(t, got, err, "extraction of \"tool\" from tool."+tc.p.kind+" is too large: its size in the archive is "+tc.size+" bytes, and the limit for a binary is "+limit+" bytes", false)
		})
	}
}

// countingReader counts the bytes that were read from r.
type countingReader struct {
	r io.Reader
	n int
}

func (c *countingReader) Read(p []byte) (int, error) {
	k, err := c.r.Read(p)
	c.n += k
	return k, err
}

// TestWriteFile_SizeLimit: without a size (-1), the copy reads at most one
// byte more than maxBinarySize, and a binary above the limit is refused; the
// old binary stays. No real path reaches this: fetch.Download gives the size
// of the spooled body, and the archive readers stop a member at its header
// size, which is checked before the copy.
func TestWriteFile_SizeLimit(t *testing.T) {
	withLimit(t, &maxBinarySize, 16)
	for _, tc := range []struct {
		name  string
		write func(b *Binary, r io.Reader) error
		what  string
	}{
		{"a raw download", func(b *Binary, r io.Reader) error { return b.writeFile(r, downloadOf(b.File), -1) }, "download of "},
		{"a member", func(b *Binary, r io.Reader) error { return b.writeMember(r, "tool", "tool.tar.gz", -1) }, `extraction of "tool" from tool.tar.gz`},
	} {
		for _, n := range []int{16, 17, 64} {
			t.Run(tc.name+" of "+strconv.Itoa(n)+" bytes", func(t *testing.T) {
				dir := t.TempDir()
				b := &Binary{Name: "tool", File: filepath.Join(dir, "tool")}
				if err := os.WriteFile(b.File, []byte(oldBinary), 0755); err != nil {
					t.Fatal(err)
				}
				r := &countingReader{r: bytes.NewReader(bytes.Repeat([]byte("x"), n))}
				err := tc.write(b, r)
				noTempFiles(t, dir)
				got, rerr := os.ReadFile(b.File)
				if rerr != nil {
					t.Fatal(rerr)
				}
				if r.n > 17 {
					t.Errorf("the copy read %d bytes, want at most 17", r.n)
				}
				if n <= 16 {
					wantInstalled(t, string(got), err, strings.Repeat("x", n))
					return
				}
				wantRefused(t, string(got), err, "is too large: it has more than 16 bytes, the limit for a binary", true)
				if !strings.HasPrefix(err.Error(), tc.what) {
					t.Errorf("error %q does not start with %q", err, tc.what)
				}
			})
		}
	}
}

// TestDownload_SizeLimit: a download with more bytes than maxDownloadSize is
// refused while it is read, or at once when its Content-Length says so, and
// it is not retried. This holds for every download: a raw binary and an
// archive.
func TestDownload_SizeLimit(t *testing.T) {
	withLimit(t, &maxDownloadSize, 16)
	paths := []archivePath{{name: "a raw preset"}, {name: "a raw asset", asset: true}, {name: "a tar.gz preset", kind: "tar.gz"}}
	for _, p := range paths {
		// The label of each of these downloads is tool: the name of the binary
		// for a preset, the asset name for an asset.
		want := "refusing the download of tool: it is larger than 16 bytes, the limit for a download"
		for _, tc := range []struct {
			name  string
			serve func(w http.ResponseWriter)
		}{
			{name: "a body above the limit", serve: func(w http.ResponseWriter) {
				w.Header().Set("Content-Length", "32")
				_, _ = w.Write(bytes.Repeat([]byte("x"), 32))
			}},
			{name: "a body above the limit without a Content-Length", serve: func(w http.ResponseWriter) {
				for range 4 {
					_, _ = w.Write(bytes.Repeat([]byte("x"), 8))
					w.(http.Flusher).Flush()
				}
			}},
			{name: "a Content-Length above the limit", serve: func(w http.ResponseWriter) {
				// The body is short: only the header can refuse it at once.
				w.Header().Set("Content-Length", strconv.Itoa(1<<40))
				_, _ = w.Write([]byte("tool v2\n"))
			}},
		} {
			t.Run(p.name+", "+tc.name, func(t *testing.T) {
				var tries int
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					tries++
					tc.serve(w)
				}))
				defer srv.Close()
				dir := t.TempDir()
				suffix := "/dl/tool"
				if p.kind != "" {
					suffix += "." + p.kind
				}
				b := binaryFor(dir, srv.URL+suffix, p)
				if err := os.WriteFile(b.File, []byte(oldBinary), 0755); err != nil {
					t.Fatal(err)
				}
				err := b.DownloadBinary()
				got, rerr := os.ReadFile(b.File)
				if rerr != nil {
					t.Fatal(rerr)
				}
				wantRefused(t, string(got), err, want, false)
				if tries != 1 {
					t.Errorf("%d tries, want 1: the limit is not retried", tries)
				}
			})
		}
		if p.kind != "" {
			continue // an archive of 16 bytes holds no member
		}
		t.Run(p.name+", a body at the limit", func(t *testing.T) {
			body := []byte("tool v2 at 16 b\n")
			got, err := installFrom(t, p, body)
			wantInstalled(t, got, err, string(body))
		})
	}
}

// TestLimitedBody: a download body is refused once it has more bytes than
// maxDownloadSize, also when the reader returns the last bytes together
// with io.EOF, and at once, before a read, when its Content-Length is above
// the limit. A body at the limit reads to its end.
func TestLimitedBody(t *testing.T) {
	withLimit(t, &maxDownloadSize, 16)
	const want = "refusing the download of tool: it is larger than 16 bytes, the limit for a download"
	for _, tc := range []struct {
		name          string
		n             int   // the body has n bytes
		contentLength int64 // -1: none
		err           bool
	}{
		{name: "a body at the limit", n: 16, contentLength: -1},
		{name: "a body above the limit", n: 17, contentLength: -1, err: true},
		{name: "a body at the limit with its Content-Length", n: 16, contentLength: 16},
		{name: "a Content-Length above the limit", n: 8, contentLength: 1 << 40, err: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// DataErrReader returns the last bytes together with io.EOF.
			r := &countingReader{r: iotest.DataErrReader(bytes.NewReader(bytes.Repeat([]byte("x"), tc.n)))}
			resp := &http.Response{Body: io.NopCloser(r), ContentLength: tc.contentLength}
			data, err := io.ReadAll(newLimitedBody(resp, "tool"))
			if !tc.err {
				if err != nil || len(data) != tc.n {
					t.Fatalf("read %d bytes, %v; want %d bytes, no error", len(data), err, tc.n)
				}
				return
			}
			if err == nil || err.Error() != want {
				t.Fatalf("error %v, want %q", err, want)
			}
			if tc.contentLength > maxDownloadSize && r.n != 0 {
				t.Errorf("read %d bytes of a body whose Content-Length is above the limit, want 0", r.n)
			}
		})
	}
}

// TestOpenZip_SizeLimit: the zip paths refuse a zip archive larger than
// maxDownloadSize. (On the real paths, the download limit refuses a larger
// body first: TestDownload_SizeLimit.)
func TestOpenZip_SizeLimit(t *testing.T) {
	body := archiveOf(t, "zip", member{name: "tool", content: "tool v2\n"})
	b := &Binary{Name: "tool", File: filepath.Join(t.TempDir(), "tool")}
	zipPaths := map[string]func(archiveFile, string) error{
		"extractSingleFileFromZip": b.extractSingleFileFromZip,
		"extractFromZipAuto":       b.extractFromZipAuto,
	}
	withLimit(t, &maxDownloadSize, int64(len(body)-1))
	want := "refusing tool.zip: the zip archive is larger than " + strconv.Itoa(len(body)-1) + " bytes, the limit for a download"
	for name, extract := range zipPaths {
		if err := extract(bytes.NewReader(body), "tool.zip"); err == nil || err.Error() != want {
			t.Errorf("%s: error %v, want %q", name, err, want)
		}
	}
	withLimit(t, &maxDownloadSize, int64(len(body)))
	for name, extract := range zipPaths {
		if err := extract(bytes.NewReader(body), "tool.zip"); err != nil {
			t.Errorf("%s: a zip archive at the limit: %v", name, err)
		}
	}
}

// secretURL is rawURL (http://host/path) with user info, a token in the
// query and a fragment. Each secret part holds "sekrit".
func secretURL(t *testing.T, rawURL string) string {
	t.Helper()
	host, path, ok := strings.Cut(strings.TrimPrefix(rawURL, "http://"), "/")
	if !ok {
		t.Fatalf("no path in %q", rawURL)
	}
	return "http://u-sekrit-name:p-sekrit-pass@" + host + "/" + path + "?access_token=q-sekrit-token#f-sekrit-frag"
}

// TestDownload_ErrorsHideCredentials: when a download fails, the error names
// the host and the path of the URL, but not its user info, query or
// fragment: on the preset and the asset path, for a 401, a 403, a 403 of a
// rate limit and a transport error. A transport error still wraps its cause.
func TestDownload_ErrorsHideCredentials(t *testing.T) {
	for _, p := range []archivePath{{name: "a tar.gz preset", kind: "tar.gz"}, {name: "a tar.gz asset", kind: "tar.gz", asset: true}} {
		for _, tc := range []struct {
			name   string
			status int
			header string
			want   string
		}{
			{"a 401", http.StatusUnauthorized, "", "HTTP 401 for %s: access denied"},
			{"a 403", http.StatusForbidden, "", "HTTP 403 for %s: access denied"},
			{"a 403 of a rate limit", http.StatusForbidden, "0", "HTTP 403 for %s: rate limit exhausted"},
		} {
			t.Run(p.name+", "+tc.name, func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					if tc.header != "" {
						w.Header().Set("X-RateLimit-Remaining", tc.header)
					}
					w.WriteHeader(tc.status)
				}))
				defer srv.Close()
				plain := srv.URL + "/dl/tool.tar.gz"
				err := binaryFor(t.TempDir(), secretURL(t, plain), p).DownloadBinary()
				want := strings.Replace(tc.want, "%s", plain, 1)
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("error %v, want one that contains %q", err, want)
				}
				if strings.Contains(err.Error(), "sekrit") {
					t.Errorf("error %q holds a part of the user info, the query or the fragment", err)
				}
			})
		}
		t.Run(p.name+", a transport error", func(t *testing.T) {
			l, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			plain := "http://" + l.Addr().String() + "/dl/tool.tar.gz"
			if err := l.Close(); err != nil {
				t.Fatal(err)
			}
			err = binaryFor(t.TempDir(), secretURL(t, plain), p).DownloadBinary()
			if err == nil || !strings.Contains(err.Error(), plain) {
				t.Fatalf("error %v, want one that names %q", err, plain)
			}
			if strings.Contains(err.Error(), "sekrit") {
				t.Errorf("error %q holds a part of the user info, the query or the fragment", err)
			}
			if !errors.Is(err, syscall.ECONNREFUSED) {
				t.Errorf("error %q does not wrap ECONNREFUSED", err)
			}
		})
	}
}

// TestGetBody_ErrorHidesCredentials: GetBody names the URL of a failed
// request without its user info, query and fragment.
func TestGetBody_ErrorHidesCredentials(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	plain := srv.URL + "/stable.txt"
	_, err := GetBody(secretURL(t, plain))
	if want := "HTTP 401 for " + plain; err == nil || err.Error() != want {
		t.Fatalf("error %v, want %q", err, want)
	}
}
