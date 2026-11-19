package binary

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/flate"
	"compress/gzip"
	"errors"
	"hash/crc32"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/fentas/b/pkg/provider"
	"github.com/ulikunitz/xz"
)

// tarOf is a tar archive (not compressed) of the entries.
func tarOf(t *testing.T, entries ...tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		if err := tw.WriteHeader(&tar.Header{Name: e.name, Mode: e.mode, Size: int64(len(e.content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatalf("tar WriteHeader %q: %v", e.name, err)
		}
		if _, err := tw.Write(e.content); err != nil {
			t.Fatalf("tar Write %q: %v", e.name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar Close: %v", err)
	}
	return buf.Bytes()
}

// shortTar is a tar archive whose one entry has a header size of size bytes
// but only the bytes of content, and then the archive ends: a member that
// is shorter than its header says.
func shortTar(t *testing.T, name string, size int64, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0755, Size: size, Typeflag: tar.TypeReg}); err != nil {
		t.Fatalf("tar WriteHeader %q: %v", name, err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatalf("tar Write %q: %v", name, err)
	}
	// No Close: it would refuse the missing bytes. The header and the bytes
	// are in buf already.
	return buf.Bytes()
}

// compressed is data compressed as kind ("tar.gz" or "tar.xz").
func compressed(t *testing.T, kind string, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	var w io.WriteCloser
	switch kind {
	case "tar.gz":
		w = gzip.NewWriter(&buf)
	case "tar.xz":
		xw, err := xz.NewWriter(&buf)
		if err != nil {
			t.Fatalf("xz NewWriter: %v", err)
		}
		w = xw
	default:
		t.Fatalf("no compression for %q", kind)
	}
	if _, err := w.Write(data); err != nil {
		t.Fatalf("%s Write: %v", kind, err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("%s Close: %v", kind, err)
	}
	return buf.Bytes()
}

// shortZip is a zip archive whose one deflated member has a header size
// of 5 bytes more than its content: the member is shorter than its header
// says.
func shortZip(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var deflated bytes.Buffer
	fw, err := flate.NewWriter(&deflated, flate.DefaultCompression)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := fw.Close(); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.CreateRaw(&zip.FileHeader{
		Name:               name,
		Method:             zip.Deflate,
		CRC32:              crc32.ChecksumIEEE(content),
		CompressedSize64:   uint64(deflated.Len()),
		UncompressedSize64: uint64(len(content) + 5),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(deflated.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// unknownMethodZip is a zip archive whose one member uses a compression
// method that the zip reader does not know: the member cannot be opened.
func unknownMethodZip(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.CreateRaw(&zip.FileHeader{
		Name:               name,
		Method:             99,
		CRC32:              crc32.ChecksumIEEE(content),
		CompressedSize64:   uint64(len(content)),
		UncompressedSize64: uint64(len(content)),
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

// TestExtract_ChecksTheMember: each path that extracts a binary from an
// archive (a preset and a release asset, each as a tar.gz, a tar.xz and a
// zip) checks the member before it replaces the binary. An empty member, a
// member shorter than its header says, an archive with a broken header, a
// broken tar and a zip member that cannot be opened are refused: the old
// binary stays, no temp file stays, and the error names the archive (and the
// member, when there is one). A good member installs.
//
// Only the empty member reaches the byte count of verifyWritten. The archive
// reader of the standard library refuses the other cases first (their names
// say "the reader refuses it"); they prove that the error of the reader
// reaches the caller and that the old binary stays. TestWriteMember and
// TestWriteFile_SizeLimit test the count itself.
func TestExtract_ChecksTheMember(t *testing.T) {
	good := []byte("tool v2\n")
	archive := func(t *testing.T, kind string, content []byte) []byte {
		if kind == "zip" {
			return makeZip(t, map[string][]byte{"tool": content}).Bytes()
		}
		return compressed(t, kind, tarOf(t, tarEntry{name: "tool", mode: 0755, content: content}))
	}
	short := func(t *testing.T, kind string) []byte {
		if kind == "zip" {
			return shortZip(t, "tool", good)
		}
		return compressed(t, kind, shortTar(t, "tool", 64, good))
	}
	brokenHeader := map[string]struct {
		errHas string
		errIs  error // nil: the xz package exports no such error
	}{
		"tar.gz": {"reading the gzip header of tool.tar.gz: ", gzip.ErrHeader},
		"tar.xz": {"reading the xz header of tool.tar.xz: ", nil},
		"zip":    {"reading the zip directory of tool.zip: ", zip.ErrFormat},
	}
	for _, path := range []struct {
		name  string
		kind  string // "tar.gz", "tar.xz" or "zip"
		asset bool   // a release asset; else a preset
	}{
		{name: "a tar.gz preset", kind: "tar.gz"},
		{name: "a tar.xz preset", kind: "tar.xz"},
		{name: "a zip preset", kind: "zip"},
		{name: "a tar.gz asset", kind: "tar.gz", asset: true},
		{name: "a tar.xz asset", kind: "tar.xz", asset: true},
		{name: "a zip asset", kind: "zip", asset: true},
	} {
		member := "extraction of \"tool\" from tool." + path.kind
		// The tar paths read the whole archive before they select a member,
		// so there the tar reader refuses a cut member.
		shortErr := member + ": "
		if path.kind != "zip" {
			shortErr = "reading the tar archive tool." + path.kind + ": "
		}
		type extractCase struct {
			name   string
			body   func(t *testing.T) []byte
			errHas string // "" for a good member
			errIs  error  // the cause that the error wraps, if any
		}
		cases := []extractCase{
			{name: "an empty member", body: func(t *testing.T) []byte { return archive(t, path.kind, nil) },
				errHas: member + " wrote 0 bytes: the file in the archive is empty"},
			{name: "a member shorter than its header, the reader refuses it", body: func(t *testing.T) []byte { return short(t, path.kind) },
				errHas: shortErr, errIs: io.ErrUnexpectedEOF},
			{name: "a broken header, the reader refuses it", body: func(*testing.T) []byte { return []byte("not an archive") },
				errHas: brokenHeader[path.kind].errHas, errIs: brokenHeader[path.kind].errIs},
			{name: "a good member", body: func(t *testing.T) []byte { return archive(t, path.kind, good) }},
		}
		if path.kind == "zip" {
			cases = append(cases,
				extractCase{name: "a member it cannot open, the reader refuses it", body: func(t *testing.T) []byte { return unknownMethodZip(t, "tool", good) },
					errHas: "opening \"tool\" in tool.zip: ", errIs: zip.ErrAlgorithm})
		} else {
			cases = append(cases,
				extractCase{name: "a broken tar, the reader refuses it", body: func(t *testing.T) []byte { return compressed(t, path.kind, []byte("not a tar archive")) },
					errHas: "reading the tar archive tool." + path.kind + ": "})
		}
		for _, tc := range cases {
			t.Run(path.name+", "+tc.name, func(t *testing.T) {
				body := tc.body(t)
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Length", strconv.Itoa(len(body)))
					_, _ = w.Write(body)
				}))
				defer srv.Close()
				dir := t.TempDir()
				file := filepath.Join(dir, "tool")
				if err := os.WriteFile(file, []byte("tool v1\n"), 0755); err != nil {
					t.Fatal(err)
				}
				b := &Binary{Name: "tool", File: file}
				if path.asset {
					b.AutoDetect = true
					b.ProviderRef = "github.com/org/tool"
					b.ResolvedAsset = &provider.Asset{URL: srv.URL + "/tool." + path.kind, Name: "tool." + path.kind}
				} else {
					b.URL, b.Version = srv.URL+"/tool."+path.kind, "v2"
					b.IsTarGz = path.kind == "tar.gz"
					b.IsTarXz = path.kind == "tar.xz"
					b.IsZip = path.kind == "zip"
				}

				err := b.DownloadBinary()
				noTempFiles(t, dir)
				got, rerr := os.ReadFile(file)
				if rerr != nil {
					t.Fatal(rerr)
				}
				if tc.errHas == "" {
					if err != nil {
						t.Fatalf("download: %v", err)
					}
					if !bytes.Equal(got, good) {
						t.Errorf("the binary is %q, want %q", got, good)
					}
					return
				}
				if err == nil || !strings.Contains(err.Error(), tc.errHas) {
					t.Fatalf("error %v, want one that contains %q", err, tc.errHas)
				}
				if tc.errIs != nil && !errors.Is(err, tc.errIs) {
					t.Errorf("error %v does not wrap %v", err, tc.errIs)
				}
				if string(got) != "tool v1\n" {
					t.Errorf("after the refused extraction, the binary is %q, want the old binary", got)
				}
			})
		}
	}
}

// TestVerifyWritten: the count check of a member and of a download. A
// member or a download with no byte is refused, and a byte count that
// differs from the size of the source is refused. With no size (-1), only
// the empty case is refused. The download errors are the ones that b
// printed before members had a check.
func TestVerifyWritten(t *testing.T) {
	member := memberOf("dir/tool", "tool.tar.gz")
	download := downloadOf("/bin/tool")
	for _, tc := range []struct {
		name          string
		src           writeSource
		written, want int64
		err           string // "" for no error
	}{
		{name: "a member of its size", src: member, written: 10, want: 10},
		{name: "a member without a size", src: member, written: 5, want: -1},
		{name: "an empty member", src: member, written: 0, want: 0,
			err: "extraction of \"dir/tool\" from tool.tar.gz wrote 0 bytes: the file in the archive is empty; the archive is damaged or does not hold the binary"},
		{name: "a member shorter than its size", src: member, written: 5, want: 10,
			err: "extraction of \"dir/tool\" from tool.tar.gz is incomplete: wrote 5 bytes, its size in the archive is 10; the archive is damaged"},
		{name: "a member longer than its size", src: member, written: 12, want: 10,
			err: "extraction of \"dir/tool\" from tool.tar.gz is incomplete: wrote 12 bytes, its size in the archive is 10; the archive is damaged"},
		{name: "a download of its Content-Length", src: download, written: 7, want: 7},
		{name: "an empty download", src: download, written: 0, want: -1,
			err: "download of /bin/tool wrote 0 bytes: the server sent an empty body; retry the download"},
		{name: "a short download", src: download, written: 5, want: 7,
			err: "download of /bin/tool is incomplete: wrote 5 bytes, Content-Length is 7; retry the download"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := verifyWritten(tc.src, tc.written, tc.want)
			switch {
			case tc.err == "" && err != nil:
				t.Errorf("error %v, want none", err)
			case tc.err != "" && (err == nil || err.Error() != tc.err):
				t.Errorf("error %v, want %q", err, tc.err)
			}
		})
	}
}

// TestWriteMember: writeMember counts the bytes where it writes them. It
// refuses a member whose byte count differs from its size in the archive,
// also when the reader ends without an error, and it keeps the old binary.
// (The archive readers of the standard library refuse a short member first,
// so only a direct call reaches this count.) A read error names the member
// and the archive.
func TestWriteMember(t *testing.T) {
	const member = "extraction of \"dir/tool\" from tool.tar.gz"
	for _, tc := range []struct {
		name    string
		content string
		size    int64
		readErr error  // the reader fails with it after content
		err     string // "" for no error
	}{
		{name: "a member of its size", content: "tool v2\n", size: 8},
		{name: "a member without a size", content: "tool v2\n", size: -1},
		{name: "an empty member", content: "", size: 0,
			err: member + " wrote 0 bytes: the file in the archive is empty; the archive is damaged or does not hold the binary"},
		{name: "a member shorter than its size", content: "tool", size: 8,
			err: member + " is incomplete: wrote 4 bytes, its size in the archive is 8; the archive is damaged"},
		{name: "a member longer than its size", content: "tool v2\nmore", size: 8,
			err: member + " is incomplete: wrote 12 bytes, its size in the archive is 8; the archive is damaged"},
		{name: "a read error", content: "tool", size: 8, readErr: io.ErrUnexpectedEOF,
			err: member + ": unexpected EOF"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			file := filepath.Join(dir, "tool")
			if err := os.WriteFile(file, []byte("tool v1\n"), 0755); err != nil {
				t.Fatal(err)
			}
			var r io.Reader = strings.NewReader(tc.content)
			if tc.readErr != nil {
				r = io.MultiReader(r, iotest.ErrReader(tc.readErr))
			}
			b := &Binary{Name: "tool", File: file}

			err := b.writeMember(r, "dir/tool", "tool.tar.gz", tc.size)
			noTempFiles(t, dir)
			got, rerr := os.ReadFile(file)
			if rerr != nil {
				t.Fatal(rerr)
			}
			if tc.err == "" {
				if err != nil {
					t.Fatalf("writeMember: %v", err)
				}
				if string(got) != tc.content {
					t.Errorf("the binary is %q, want %q", got, tc.content)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Fatalf("error %v, want one that contains %q", err, tc.err)
			}
			if tc.readErr != nil && !errors.Is(err, tc.readErr) {
				t.Errorf("error %v does not wrap %v", err, tc.readErr)
			}
			if string(got) != "tool v1\n" {
				t.Errorf("after the refused member, the binary is %q, want the old binary", got)
			}
		})
	}
}

// atOnly is a zip archive that can be read only at offsets: its Read fails.
type atOnly struct{ *bytes.Reader }

func (atOnly) Read([]byte) (int, error) {
	return 0, errors.New("the zip archive was read as a stream")
}

// failingAt is a zip archive whose ReadAt fails.
type failingAt struct{ *bytes.Reader }

func (failingAt) ReadAt([]byte, int64) (int, error) { return 0, io.ErrUnexpectedEOF }

// TestExtractZip_ReadsAtOffsets: the zip paths read the archive at its
// offsets (ReadAt) from the temp file of the download, never as a stream
// into memory. A read error names the archive. (The download layer refuses
// a cut body before the zip paths read it, so only a direct call reaches
// this error.)
func TestExtractZip_ReadsAtOffsets(t *testing.T) {
	body := makeZip(t, map[string][]byte{"tool": []byte("tool v2\n")}).Bytes()
	for _, preset := range []bool{true, false} {
		b := &Binary{Name: "tool", File: filepath.Join(t.TempDir(), "tool")}
		if err := b.extractZip(atOnly{bytes.NewReader(body)}, "tool.zip", preset); err != nil {
			t.Fatalf("preset %v: %v", preset, err)
		}
		if got, err := os.ReadFile(b.File); err != nil || string(got) != "tool v2\n" {
			t.Errorf("preset %v: the binary is %q (%v), want %q", preset, got, err, "tool v2\n")
		}
		err := b.extractZip(failingAt{bytes.NewReader(body)}, "tool.zip", preset)
		const want = "reading the zip directory of tool.zip: unexpected EOF"
		if err == nil || err.Error() != want || !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Errorf("preset %v: error %v, want %q that wraps io.ErrUnexpectedEOF", preset, err, want)
		}
	}
}

// TestArchiveName: the name of a preset archive in errors is the last
// element of the URL path, without the query. Without a path element, it
// is the URL without its user info, query and fragment.
func TestArchiveName(t *testing.T) {
	for in, want := range map[string]string{
		"https://example.com/dl/v1/tool_1.0_linux_amd64.tar.gz?raw=1": "tool_1.0_linux_amd64.tar.gz",
		"https://user:pass@example.com/dl/tool.zip":                   "tool.zip",
		"https://example.com/":                                        "https://example.com/",
		"https://example.com":                                         "https://example.com",
		"https://user:pass@example.com/?token=abc#top":                "https://example.com/",
		"::not a url": "the archive",
	} {
		if got := archiveName(in); got != want {
			t.Errorf("archiveName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestZipSize: a zip size up to maxBinarySize is the size. A larger size is
// refused, also one that does not fit an int64 (it was "no size" before).
func TestZipSize(t *testing.T) {
	limit := uint64(maxBinarySize)
	for _, size := range []uint64{0, 7, limit} {
		got, err := zipSize(&zip.File{FileHeader: zip.FileHeader{Name: "tool", UncompressedSize64: size}}, "tool.zip")
		if err != nil || got != int64(size) {
			t.Errorf("zipSize(%d) = %d, %v; want %d, no error", size, got, err, size)
		}
	}
	for _, size := range []uint64{limit + 1, math.MaxInt64, math.MaxInt64 + 1, math.MaxUint64} {
		want := "extraction of \"tool\" from tool.zip is too large: its size in the archive is " +
			strconv.FormatUint(size, 10) + " bytes, and the limit for a binary is " + strconv.FormatUint(limit, 10) + " bytes"
		if _, err := zipSize(&zip.File{FileHeader: zip.FileHeader{Name: "tool", UncompressedSize64: size}}, "tool.zip"); err == nil || err.Error() != want {
			t.Errorf("zipSize(%d): error %v, want %q", size, err, want)
		}
	}
}
