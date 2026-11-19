package binary

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/fentas/b/pkg/atomicfile"
	"github.com/fentas/b/pkg/fetch"
	"github.com/fentas/b/pkg/provider"
	"github.com/fentas/goodies/progress"
	"github.com/ulikunitz/xz"
)

// rateLimitShaped reports whether a 401/403 response looks like a GitHub
// rate-limit rejection: X-RateLimit-Remaining is 0, or the body mentions
// "rate limit". It consumes (part of) the response body, so call it only on
// a response that is already being rejected.
func rateLimitShaped(resp *http.Response) bool {
	if resp.Header.Get("X-RateLimit-Remaining") == "0" {
		return true
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return strings.Contains(strings.ToLower(string(body)), "rate limit")
}

// accessDeniedError builds the error for a 401/403 download response. It
// names the URL without its user info, query and fragment
// (fetch.RedactRawURL): they can hold a credential.
func accessDeniedError(resp *http.Response, rawURL string) error {
	u := fetch.RedactRawURL(rawURL)
	if rateLimitShaped(resp) {
		return fmt.Errorf("HTTP %d for %s: rate limit exhausted; set GITHUB_TOKEN to authenticate", resp.StatusCode, u)
	}
	return fmt.Errorf("HTTP %d for %s: access denied; check credentials (for GitHub, set GITHUB_TOKEN)", resp.StatusCode, u)
}

// maxBinarySize is the largest binary that b installs: 2 GiB. A member of an
// archive or a raw download that is larger is refused: before the copy when
// its size is known, else when the copy passes the limit. Tests replace it.
var maxBinarySize int64 = 2 << 30

// maxDownloadSize is the largest download that b reads: 2 GiB. A download
// (an archive or a raw binary) is written to a temp file before b reads it.
// A longer download is refused while it is read. Tests replace it.
var maxDownloadSize int64 = 2 << 30

// maxUnpackedSize is the most bytes that b unpacks from a compressed tar
// archive: 8 GiB, four times maxBinarySize. b counts every byte that gzip or
// xz gives, the files that it skips included, and refuses a larger archive.
// Tests replace it.
var maxUnpackedSize int64 = 4 * (2 << 30)

// maxEntries is the most entries (files, directories, links) that b reads
// in an archive. An archive with more is refused. Tests replace it.
var maxEntries = 100000

// maxListedNames is the most file names that an error lists.
const maxListedNames = 10

// tooManyEntries refuses archive: it has more than maxEntries entries.
func tooManyEntries(archive string) error {
	return fmt.Errorf("refusing %s: it has more than %d entries, the limit for an archive", archive, maxEntries)
}

// downloadTooLarge refuses the download of label: it has more bytes than
// maxDownloadSize.
func downloadTooLarge(label string) error {
	return fmt.Errorf("refusing the download of %s: it is larger than %d bytes, the limit for a download", label, maxDownloadSize)
}

// limitedBody reads a download body and fails once the body has more bytes
// than maxDownloadSize, or at once when its Content-Length says so. The
// error is not retried. Close closes the body.
type limitedBody struct {
	r     io.ReadCloser
	label string
	read  int64
}

func newLimitedBody(resp *http.Response, label string) *limitedBody {
	l := &limitedBody{r: resp.Body, label: label}
	if resp.ContentLength > maxDownloadSize {
		l.read = resp.ContentLength
	}
	return l
}

func (l *limitedBody) Read(p []byte) (int, error) {
	if l.read > maxDownloadSize {
		return 0, downloadTooLarge(l.label)
	}
	k, err := l.r.Read(p)
	l.read += int64(k)
	if l.read > maxDownloadSize {
		return 0, downloadTooLarge(l.label)
	}
	return k, err
}

func (l *limitedBody) Close() error { return l.r.Close() }

// unsafeName reports whether the name of a file in an archive is absolute,
// names a drive (C:/x, C:\x, c:x), or has a ".." element after path.Clean.
// A backslash counts as a separator, as it does on Windows. archive/tar and
// archive/zip give the effective name (a PAX or GNU long name included). b
// never installs such a file, and refuses an archive that holds one as a
// regular file.
func unsafeName(name string) bool {
	slashed := strings.ReplaceAll(name, `\`, "/")
	if len(slashed) >= 2 && slashed[1] == ':' && isLetter(slashed[0]) {
		return true
	}
	clean := path.Clean(slashed)
	return path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../")
}

func isLetter(c byte) bool { return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' }

// memberBase is the last element of the name of a file in an archive. Like
// unsafeName, it takes "/" and "\" as separators, so the result does not
// depend on the OS that b runs on.
func memberBase(name string) string {
	return path.Base(strings.ReplaceAll(name, `\`, "/"))
}

// unsafeNameError refuses archive for the file name.
func unsafeNameError(archive, name string) error {
	return fmt.Errorf("refusing %s: the file name %q is absolute, names a drive or has a \"..\" element", archive, name)
}

// ambiguousError refuses archive because more than one file in it matches
// the binary.
func ambiguousError(archive, binary string, names nameList) error {
	return fmt.Errorf("refusing %s: %d files in it match the binary %s: %s", archive, names.n, binary, names)
}

// tieError refuses archive because no file matches the binary by name and
// the largest candidates have the same size.
func tieError(archive, binary string, names nameList) error {
	return fmt.Errorf("refusing %s: no file in it is named %s, and %d files tie as the largest: %s", archive, binary, names.n, names)
}

// nameList counts the names of files in an archive, and keeps the first
// maxListedNames of them for an error.
type nameList struct {
	n     int
	names []string
}

func (l *nameList) add(name string) {
	l.n++
	if len(l.names) < maxListedNames {
		l.names = append(l.names, name)
	}
}

// String lists the kept names, each quoted (%q also escapes control
// characters), then the count of the others.
func (l nameList) String() string {
	q := make([]string, len(l.names))
	for i, n := range l.names {
		q[i] = fmt.Sprintf("%q", n)
	}
	s := strings.Join(q, ", ")
	if more := l.n - len(l.names); more > 0 {
		s += fmt.Sprintf(" and %d more", more)
	}
	return s
}

// writeSource names where the bytes of a binary come from, for the errors
// of verifyWritten.
type writeSource struct {
	what  string // the subject of the errors
	empty string // why no byte came, and the next step
	size  string // the size that the byte count must match
	retry string // the next step when the byte count differs
}

// downloadOf is the source of a download into file. Its size is the
// Content-Length.
func downloadOf(file string) writeSource {
	return writeSource{
		what:  "download of " + file,
		empty: "the server sent an empty body; retry the download",
		size:  "Content-Length",
		retry: "retry the download",
	}
}

// memberOf is the source of the file member of the archive archive. Its
// size is the size in the archive header. The name of the member is quoted:
// it comes from the archive, and %q escapes control characters.
func memberOf(member, archive string) writeSource {
	return writeSource{
		what:  fmt.Sprintf("extraction of %q from %s", member, archive),
		empty: "the file in the archive is empty; the archive is damaged or does not hold the binary",
		size:  "its size in the archive",
		retry: "the archive is damaged",
	}
}

// verifyWritten confirms that the bytes of a binary landed on disk: at least
// one byte was written, no more than maxBinarySize, and the byte count
// matches want when the source gives a size (want >= 0). An empty file is
// never a binary: it cannot run.
func verifyWritten(src writeSource, written, want int64) error {
	if written == 0 {
		return fmt.Errorf("%s wrote 0 bytes: %s", src.what, src.empty)
	}
	if written > maxBinarySize {
		return fmt.Errorf("%s is too large: it has more than %d bytes, the limit for a binary", src.what, maxBinarySize)
	}
	if want >= 0 && written != want {
		return fmt.Errorf("%s is incomplete: wrote %d bytes, %s is %d; %s", src.what, written, src.size, want, src.retry)
	}
	return nil
}

// tooLarge refuses src before the copy: the size that src gives is above
// maxBinarySize.
func tooLarge(src writeSource, size uint64) error {
	return fmt.Errorf("%s is too large: %s is %d bytes, and the limit for a binary is %d bytes", src.what, src.size, size, maxBinarySize)
}

// writeFile writes the binary file b.File from r in one step
// (atomicfile.Write), with mode 0755, and checks it before the new file
// replaces the old one:
//   - a size from src (want >= 0) above maxBinarySize is refused before the
//     copy starts;
//   - the copy reads at most one byte more than maxBinarySize;
//   - verifyWritten checks the byte count.
//
// A download or an extraction that fails leaves the old file as it was.
func (b *Binary) writeFile(r io.Reader, src writeSource, want int64) error {
	if want > maxBinarySize {
		return tooLarge(src, uint64(want))
	}
	return atomicfile.Write(b.File, 0755, func(w io.Writer) error {
		n, err := io.Copy(w, io.LimitReader(r, maxBinarySize+1))
		if err != nil {
			return err
		}
		return verifyWritten(src, n, want)
	})
}

// writeMember writes the binary from the member of an archive (writeFile)
// and checks it (verifyWritten): the member is not empty, not larger than
// maxBinarySize, and its byte count is size when the archive header gives
// one (size >= 0). The archive readers refuse a short member before this
// count; the count is taken where the bytes are written, after every reader
// between. A read error names the member and the archive.
func (b *Binary) writeMember(r io.Reader, member, archive string, size int64) error {
	src := memberOf(member, archive)
	return b.writeFile(namedReader{r: r, name: src.what}, src, size)
}

// namedReader puts name in front of the read errors of r. io.EOF stays as it
// is, because io.Copy ends on it.
type namedReader struct {
	r    io.Reader
	name string
}

func (n namedReader) Read(p []byte) (int, error) {
	k, err := n.r.Read(p)
	if err != nil && err != io.EOF {
		err = fmt.Errorf("%s: %w", n.name, err)
	}
	return k, err
}

// zipSize is the size of f in the zip header. A size above maxBinarySize is
// refused (tooLarge), so the size always fits an int64.
func zipSize(f *zip.File, archive string) (int64, error) {
	if f.UncompressedSize64 > uint64(maxBinarySize) {
		return 0, tooLarge(memberOf(f.Name, archive), f.UncompressedSize64)
	}
	return int64(f.UncompressedSize64), nil
}

// archiveFile is a downloaded archive. fetch.Download writes a 200 body to a
// temp file before it returns, and the Body of the response reads that
// file. So b reads an archive at an offset (ReadAt) and more than once
// (Seek), and keeps no part of it in memory.
type archiveFile interface {
	io.Reader
	io.ReaderAt
	io.Seeker
}

// asArchive is the body of a download as an archiveFile.
func asArchive(body io.Reader, archive string) (archiveFile, error) {
	f, ok := body.(archiveFile)
	if !ok {
		return nil, fmt.Errorf("reading %s: the download is not in a file", archive)
	}
	return f, nil
}

// openZip reads the directory of the zip archive f. The directory is at the
// end of a zip archive, so the zip reader reads f at its offsets (ReadAt).
// An archive larger than maxDownloadSize (the download limit refuses it
// first), or with more than maxEntries entries, is refused.
func openZip(f archiveFile, archive string) (*zip.Reader, error) {
	size, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", archive, err)
	}
	if size > maxDownloadSize {
		return nil, fmt.Errorf("refusing %s: the zip archive is larger than %d bytes, the limit for a download", archive, maxDownloadSize)
	}
	zipReader, err := zip.NewReader(f, size)
	if err != nil {
		return nil, fmt.Errorf("reading the zip directory of %s: %w", archive, err)
	}
	if len(zipReader.File) > maxEntries {
		return nil, tooManyEntries(archive)
	}
	return zipReader, nil
}

// zipCreatorUnix and zipCreatorMacOS are the creator systems of a zip file
// that carry a Unix mode (archive/zip reads the mode for these two only).
const (
	zipCreatorUnix  = 3
	zipCreatorMacOS = 19
)

// execMode is what an archive says about the execute bit of a file.
type execMode int

const (
	execUnknown execMode = iota // a zip file without a Unix mode
	execSet
	execUnset
)

// tarExec reads the execute bit of a tar file: tar always gives a mode.
func tarExec(mode int64) execMode {
	if mode&0o111 != 0 {
		return execSet
	}
	return execUnset
}

// zipExec reads the execute bit of a zip file. Only a file whose creator is
// Unix or macOS carries a Unix mode, and a mode without permission bits is
// no mode.
func zipExec(f *zip.File) execMode {
	if c := f.CreatorVersion >> 8; c != zipCreatorUnix && c != zipCreatorMacOS {
		return execUnknown
	}
	switch perm := f.Mode().Perm(); {
	case perm == 0:
		return execUnknown
	case perm&0o111 != 0:
		return execSet
	}
	return execUnset
}

// entry is a regular file of an archive.
type entry struct {
	index int // its position among the entries of the archive
	name  string
	size  uint64 // the size in the archive header
	exec  execMode
}

// mayRun reports whether the fallback can take the file when no file matches
// the binary by name: it has an execute bit, or the archive gives no mode
// for it.
func (e entry) mayRun() bool { return e.exec != execUnset }

// selector chooses the binary among the regular files of an archive, with
// the same rule on every path:
//   - A file that matches the binary by name is the binary, with or without
//     an execute bit (b installs the binary with mode 0755). A file matches
//     when its base name (memberBase) is the name of the binary; a tar
//     preset also takes the names of tarFileMatches. Two or more matches
//     refuse the archive as ambiguous.
//   - When no file matches, the asset paths (fallback) take the single
//     largest file that may run (mayRun). Two largest candidates of the
//     same size refuse the archive.
type selector struct {
	archive, binary string
	none            string // the error when no file is the binary
	fallback        bool
	matches         nameList // the files that match by name
	match           *entry   // the first of them
	largest         *entry   // the first of the largest candidates
	ties            nameList // the candidates of the largest size
}

// add takes a regular file of the archive. match: it matches the binary by
// name. candidate: the fallback may take it.
func (s *selector) add(e entry, match, candidate bool) {
	switch {
	case match:
		s.matches.add(e.name)
		if s.match == nil {
			s.match = &e
		}
	case candidate && s.fallback:
		switch {
		case s.largest == nil || e.size > s.largest.size:
			s.largest, s.ties = &e, nameList{}
			s.ties.add(e.name)
		case e.size == s.largest.size:
			s.ties.add(e.name)
		}
	}
}

// choose returns the binary, or the error that refuses the archive.
func (s *selector) choose() (entry, error) {
	switch {
	case s.matches.n == 1:
		return *s.match, nil
	case s.matches.n > 1:
		return entry{}, ambiguousError(s.archive, s.binary, s.matches)
	case s.largest == nil:
		return entry{}, errors.New(s.none)
	case s.ties.n > 1:
		return entry{}, tieError(s.archive, s.binary, s.ties)
	}
	return *s.largest, nil
}

// archiveName is the last element of the path of rawURL: the name of an
// archive in errors. A URL without a path element gives the URL without its
// user info, query and fragment (fetch.RedactURL), so that no credential
// goes into an error. A URL that does not parse gives "the archive".
func archiveName(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "the archive"
	}
	if name := path.Base(u.Path); name != "." && name != "/" {
		return name
	}
	return fetch.RedactURL(u)
}

// ctx returns the context of b, or context.Background if it has none.
func (b *Binary) ctx() context.Context {
	if b.Context != nil {
		return b.Context
	}
	return context.Background()
}

// download GETs url with fetch.Download: transient failures are retried,
// and a 200 body is complete before extraction starts. A body with more
// bytes than maxDownloadSize is refused (limitedBody). label names the
// download in the progress tracker and in errors.
func (b *Binary) download(url, label string) (*http.Response, error) {
	return fetch.Download(b.ctx(), url, func(resp *http.Response) io.Reader {
		body := newLimitedBody(resp, label)
		if b.Tracker == nil {
			return body
		}
		// A retry starts the count again.
		b.Tracker.UpdateMessage(fmt.Sprintf("Downloading %s", label))
		b.Tracker.UpdateTotal(resp.ContentLength)
		b.Tracker.SetValue(0)
		return progress.NewReader(body, b.Tracker)
	})
}

func (b *Binary) githubURL() (string, error) {
	var err error
	file := b.GitHubFile
	if b.GitHubFileF != nil {
		file, err = b.GitHubFileF(b)
	}
	return fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", b.GitHubRepo, b.Version, file), err
}

// unpackLimit fails once the decompressor of a tar archive has given more
// than maxUnpackedSize bytes.
type unpackLimit struct {
	r   io.Reader
	n   int64
	err error // the error that refuses the archive
}

func (u *unpackLimit) Read(p []byte) (int, error) {
	k, err := u.r.Read(p)
	u.n += int64(k)
	if u.n > maxUnpackedSize {
		return 0, u.err
	}
	return k, err
}

// tarStream is a tar archive read from its start, through unpackLimit.
type tarStream struct {
	*tar.Reader
	lim     *unpackLimit
	kind    string       // "gzip" or "xz"
	members *gzipMembers // gzip only
	close   func() error
}

// gzipMembers reads the gzip members of a tar.gz one after the other, as one
// stream: a tar archive can go on in the next member (concatenated gzip
// files, or a compressor that writes a member per block). Once the tar
// archive ended (last), it reads no next member: readToEnd reads the member
// that holds the end of the tar archive to its end, and the bytes after
// that member are not read. zr reads br, a bufio.Reader, so that zr reads no
// byte past the end of a member and Reset finds the next member.
type gzipMembers struct {
	br    *bufio.Reader
	zr    *gzip.Reader
	ended bool // the member in zr ended, and its checksum is checked
	last  bool // the tar archive ended
}

func (g *gzipMembers) Read(p []byte) (int, error) {
	for {
		if g.ended {
			if g.last {
				return 0, io.EOF
			}
			// Reset reads the header of the next member. It gives io.EOF
			// when no byte follows: the end of the stream.
			if err := g.zr.Reset(g.br); err != nil {
				return 0, err
			}
			g.zr.Multistream(false) // Reset sets it to true
			g.ended = false
		}
		n, err := g.zr.Read(p)
		if err != io.EOF {
			return n, err
		}
		// The next Read goes on in the next member, unless the tar
		// archive ends in this one.
		g.ended = true
		if n > 0 {
			return n, nil
		}
	}
}

// openTar opens the tar archive in f from its start. compression is "gz" or
// "xz". A tar archive can go on over several gzip members (gzipMembers); an
// xz stream must hold all of it. After the tar archive, b reads no next
// member or stream (readToEnd).
func openTar(f archiveFile, compression, archive string) (*tarStream, error) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("reading %s: %w", archive, err)
	}
	ts := &tarStream{close: func() error { return nil }}
	var r io.Reader
	switch compression {
	case "gz":
		br := bufio.NewReader(f)
		gzipReader, err := gzip.NewReader(br)
		if err != nil {
			return nil, fmt.Errorf("reading the gzip header of %s: %w", archive, err)
		}
		gzipReader.Multistream(false)
		ts.members = &gzipMembers{br: br, zr: gzipReader}
		r, ts.kind, ts.close = ts.members, "gzip", gzipReader.Close
	case "xz":
		xzReader, err := xz.ReaderConfig{SingleStream: true}.NewReader(f)
		if err != nil {
			return nil, fmt.Errorf("reading the xz header of %s: %w", archive, err)
		}
		r, ts.kind = xzReader, "xz"
	default:
		return nil, fmt.Errorf("unknown compression type: %s", compression)
	}
	ts.lim = &unpackLimit{r: r, err: fmt.Errorf("refusing %s: it unpacks to more than %d bytes, the limit for an archive", archive, maxUnpackedSize)}
	ts.Reader = tar.NewReader(ts.lim)
	return ts, nil
}

// fail is the error for err from the tar reader: the error of unpackLimit as
// it is, any other error with the name of the archive.
func (ts *tarStream) fail(err error, archive string) error {
	if errors.Is(err, ts.lim.err) {
		return ts.lim.err
	}
	return fmt.Errorf("reading the tar archive %s: %w", archive, err)
}

// xzDataAfterStream is the text of the error that the xz reader in
// single-stream mode gives for bytes after the stream. It gives it only
// after it read the stream to its end and checked the index and the footer.
// The xz package does not export the error. If a later version changes the
// text, b refuses such an archive as damaged, and TestExtract_StreamEnd
// fails.
const xzDataAfterStream = "xz: unexpected data after stream"

// readToEnd reads the compressed stream of ts after the end of the tar
// archive, to the end of the gzip member or the xz stream that holds that
// end, so that gzip or xz checks the checksum there. The bytes after it
// (padding, a next gzip member or xz stream, other bytes) are not
// decompressed and do not count: as before this check, b ignores them.
func (ts *tarStream) readToEnd(archive string) error {
	if ts.members != nil {
		ts.members.last = true
	}
	_, err := io.Copy(io.Discard, ts.lim)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ts.lim.err):
		return err
	case ts.kind == "xz" && err.Error() == xzDataAfterStream:
		return nil
	}
	return fmt.Errorf("refusing %s: the %s stream after the tar archive is damaged: %w", archive, ts.kind, err)
}

// selectTar reads the whole tar archive in f and selects the binary
// (selector). A preset matches the regular files that tarFileMatches; a
// release asset matches by base name, with the fallback over the regular
// files with an execute bit. The archive is refused when it holds a regular
// file with an unsafe name, has more than maxEntries entries or unpacks to
// more than maxUnpackedSize bytes, and when the compressed stream does not
// end cleanly after the tar archive (gzip and xz check a checksum at its
// end, readToEnd).
func (b *Binary) selectTar(f archiveFile, compression, archive string, preset bool) (entry, error) {
	ts, err := openTar(f, compression, archive)
	if err != nil {
		return entry{}, err
	}
	defer ts.close()

	none := fmt.Sprintf("file %s not found", b.Name)
	if !preset {
		none = fmt.Sprintf("no executable found in archive for %s", b.Name)
	}
	s := &selector{archive: archive, binary: b.Name, none: none, fallback: !preset}
	for i := 0; ; i++ {
		header, err := ts.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return entry{}, ts.fail(err, archive)
		}
		if i >= maxEntries {
			return entry{}, tooManyEntries(archive)
		}
		// The tar reader reads past the data of each entry, so a header
		// size above what is left of maxUnpackedSize refuses the archive
		// now, before b unpacks those bytes.
		if header.Size > maxUnpackedSize-ts.lim.n {
			return entry{}, ts.lim.err
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		if unsafeName(header.Name) {
			return entry{}, unsafeNameError(archive, header.Name)
		}
		e := entry{index: i, name: header.Name, size: uint64(header.Size), exec: tarExec(header.Mode)}
		match := memberBase(header.Name) == b.Name
		if preset {
			if match, err = b.tarFileMatches(header.Name); err != nil {
				return entry{}, err
			}
		}
		s.add(e, match, e.mayRun())
	}
	if err := ts.readToEnd(archive); err != nil {
		return entry{}, err
	}
	return s.choose()
}

// extractTar installs the binary from the tar archive in f. selectTar
// selects it in a first read of the whole archive. A second read from the
// start writes it. No part of the archive is kept in memory.
func (b *Binary) extractTar(f archiveFile, compression, archive string, preset bool) error {
	chosen, err := b.selectTar(f, compression, archive, preset)
	if err != nil {
		return err
	}
	ts, err := openTar(f, compression, archive)
	if err != nil {
		return err
	}
	defer ts.close()
	for i := 0; ; i++ {
		header, err := ts.Next()
		if err != nil {
			return ts.fail(err, archive)
		}
		if i < chosen.index {
			continue
		}
		if header.Typeflag != tar.TypeReg || header.Name != chosen.name || uint64(header.Size) != chosen.size {
			return fmt.Errorf("refusing %s: the archive changed between two reads", archive)
		}
		return b.writeMember(ts.Reader, header.Name, archive, header.Size)
	}
}

// extractSingleFileFromTar installs the binary of a preset from a tar
// archive: the regular file that tarFileMatches, by the rule of selector.
// compression can be "gz" or "xz". archive names the archive in errors.
func (b *Binary) extractSingleFileFromTar(f archiveFile, compression, archive string) error {
	return b.extractTar(f, compression, archive, true)
}

// tarFileMatches reports whether the file name of a preset tar archive is
// the binary: its base name (memberBase) is the name of b, the GitHub file
// name up to the first ".", or b.TarFile; or the whole name is the one that
// b.TarFileF gives.
func (b *Binary) tarFileMatches(name string) (bool, error) {
	switch memberBase(name) {
	case b.Name, strings.Split(b.GitHubFile, ".")[0], b.TarFile:
		return true, nil
	}
	if b.TarFileF == nil {
		return false, nil
	}
	want, err := b.TarFileF(b)
	if err != nil {
		return false, err
	}
	return name == want, nil
}

// extractZip installs the binary from the zip archive in f, by the rule of
// selector: a preset and a release asset match the regular files whose base
// name (memberBase) is b.Name. A release asset has the fallback over the
// files that may run (mayRun): without a Unix mode, every regular file. A
// symlink, a device or a directory is never the binary. A regular file with
// an unsafe name refuses the archive.
func (b *Binary) extractZip(f archiveFile, archive string, preset bool) error {
	zipReader, err := openZip(f, archive)
	if err != nil {
		return err
	}

	none := fmt.Sprintf("file %s not found", b.Name)
	if !preset {
		none = fmt.Sprintf("no file found in archive for %s", b.Name)
	}
	s := &selector{archive: archive, binary: b.Name, none: none, fallback: !preset}
	for i, zf := range zipReader.File {
		if !zf.Mode().IsRegular() {
			continue
		}
		if unsafeName(zf.Name) {
			return unsafeNameError(archive, zf.Name)
		}
		e := entry{index: i, name: zf.Name, size: zf.UncompressedSize64, exec: zipExec(zf)}
		s.add(e, memberBase(zf.Name) == b.Name, e.mayRun())
	}
	chosen, err := s.choose()
	if err != nil {
		return err
	}
	file := zipReader.File[chosen.index]
	size, err := zipSize(file, archive)
	if err != nil {
		return err
	}

	zippedFile, err := file.Open()
	if err != nil {
		return fmt.Errorf("opening %q in %s: %w", file.Name, archive, err)
	}
	defer zippedFile.Close()

	var reader io.Reader = zippedFile
	if preset && b.Writer != nil {
		b.Tracker = b.Writer.AddTracker(fmt.Sprintf("Extracting %s", b.Name), size)
		reader = progress.NewReader(zippedFile, b.Tracker)
		defer b.Tracker.MarkAsDone()
	}

	return b.writeMember(reader, file.Name, archive, size)
}

// extractSingleFileFromZip installs the binary of a preset from a zip
// archive (extractZip). archive names the archive in errors.
func (b *Binary) extractSingleFileFromZip(f archiveFile, archive string) error {
	return b.extractZip(f, archive, true)
}

func (b *Binary) downloadBinary() error {
	// Provider-based auto-detection path
	if b.AutoDetect {
		return b.downloadViaProvider()
	}

	// Legacy preset path
	return b.downloadPreset()
}

// downloadViaProvider resolves and downloads via the provider system.
func (b *Binary) downloadViaProvider() error {
	p, err := provider.Detect(b.ProviderRef)
	if err != nil {
		return err
	}

	// Each provider writes the file of b (BinaryPath): its `file:`, else its
	// alias or its name in the binary directory. b.yaml and b.lock name
	// that file.
	dest := b.BinaryPath()
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}

	// Special providers that compile/extract directly
	switch pt := p.(type) {
	case *provider.GoInstall:
		return pt.Install(b.ProviderRef, b.Version, dest)
	case *provider.Docker:
		return pt.Install(b.ProviderRef, b.Version, dest, nil)
	case *provider.OCI:
		return pt.Install(b.ProviderRef, b.Version, dest)
	case *provider.Git:
		return pt.Install(b.ProviderRef, b.Version, dest)
	}

	// If asset was pre-resolved (e.g. via interactive prompt before download),
	// skip all provider API calls entirely.
	if b.ResolvedAsset != nil {
		return b.downloadAsset(b.ResolvedAsset)
	}

	// Release-based providers (GitHub, GitLab, Gitea)
	if b.Version == "" {
		b.Version, err = p.LatestVersion(b.ProviderRef)
		if err != nil {
			return err
		}
	}

	release, err := p.FetchRelease(b.ProviderRef, b.Version)
	if err != nil {
		return err
	}

	repoName := provider.BinaryName(b.ProviderRef)
	candidates := provider.MatchAssets(release.Assets, repoName, b.AssetFilter)

	if len(candidates) == 0 {
		if b.AssetFilter != "" {
			return fmt.Errorf("%s@%s: no matching asset for filter %q among %d assets", b.ProviderRef, b.Version, b.AssetFilter, len(release.Assets))
		}
		return fmt.Errorf("%s@%s: no matching asset among %d assets", b.ProviderRef, b.Version, len(release.Assets))
	}

	// If there are multiple candidates with the same top score, let the caller
	// handle interactive selection via SelectAsset callback.
	if len(candidates) > 1 && candidates[0].Score == candidates[1].Score && b.SelectAsset != nil {
		asset, err := b.SelectAsset(candidates)
		if err != nil {
			return err
		}
		return b.downloadAsset(asset)
	}

	return b.downloadAsset(candidates[0].Asset)
}

// downloadAsset downloads a release asset and extracts the binary if archived.
func (b *Binary) downloadAsset(asset *provider.Asset) error {
	resp, err := b.download(asset.URL, asset.Name)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
			return accessDeniedError(resp, asset.URL)
		}
		return fmt.Errorf("HTTP %d downloading %s", resp.StatusCode, asset.Name)
	}

	archiveType := provider.DetectArchiveType(asset.Name)
	if archiveType != "tar.gz" && archiveType != "tar.xz" && archiveType != "zip" {
		// Raw binary (no archive)
		return b.writeFile(resp.Body, downloadOf(b.File), resp.ContentLength)
	}
	f, err := asArchive(resp.Body, asset.Name)
	if err != nil {
		return err
	}
	switch archiveType {
	case "tar.gz":
		return b.extractFromTarAuto(f, "gz", asset.Name)
	case "tar.xz":
		return b.extractFromTarAuto(f, "xz", asset.Name)
	}
	return b.extractFromZipAuto(f, asset.Name)
}

// extractFromTarAuto installs the binary from a tar archive of a release
// asset (extractTar). The binary is the regular file whose base name is
// b.Name, by the rule of selector; without one, the single largest regular
// file with an execute bit (also when it is the only one). archive names
// the archive in errors.
func (b *Binary) extractFromTarAuto(f archiveFile, compression, archive string) error {
	return b.extractTar(f, compression, archive, false)
}

// extractFromZipAuto installs the binary from a zip archive of a release
// asset (extractZip). The binary is the regular file whose base name is
// b.Name, by the rule of selector; without one, the single largest file that
// may run (also when it is the only one). archive names the archive in
// errors.
func (b *Binary) extractFromZipAuto(f archiveFile, archive string) error {
	return b.extractZip(f, archive, false)
}

// downloadPreset is the original preset-based download path.
func (b *Binary) downloadPreset() error {
	path := b.BinaryPath()
	if path == "" {
		return fmt.Errorf("unable to determine binary path")
	}
	var err error
	if b.Version == "" && b.VersionF != nil {
		b.Version, err = b.VersionF(b)
	}
	if err != nil {
		return err
	}

	var url string
	switch {
	case b.URL != "":
		url = b.URL
	case b.URLF != nil:
		url, err = b.URLF(b)
	case b.GitHubRepo != "":
		url, err = b.githubURL()
	default:
		return fmt.Errorf("no URL provided")
	}
	if err != nil {
		return err
	}
	resp, err := b.download(url, b.Name)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Check HTTP status code. fetch.Download has retried 429 and 5xx.
	if resp.StatusCode != http.StatusOK {
		switch resp.StatusCode {
		case http.StatusNotFound:
			var latest string
			if b.VersionF != nil {
				latest, _ = b.VersionF(b)
			}
			// A 404 on the download URL means the requested version+platform
			// asset doesn't exist. If the requested version differs from the
			// latest, the version is the likely culprit; otherwise the version
			// is fine and there's simply no asset for this OS/arch.
			if latest != "" && latest != b.Version {
				return fmt.Errorf("%s %s not found (latest: %s)", b.Name, b.Version, latest)
			}
			return fmt.Errorf("%s %s not found for %s/%s", b.Name, b.Version, runtime.GOOS, runtime.GOARCH)
		case http.StatusForbidden, http.StatusUnauthorized:
			return accessDeniedError(resp, url)
		default:
			return fmt.Errorf("HTTP error %d", resp.StatusCode)
		}
	}

	// Checks file extension
	if b.IsDynamic {
		extension, err := GetFileExtensionFromURL(url)
		if err != nil {
			return err
		}
		switch extension {
		case "tar.gz":
			b.IsTarGz = true
		case "tar.xz":
			b.IsTarXz = true
		case "zip":
			b.IsZip = true
		default:
			return fmt.Errorf("Unknown file extension: %s", extension)
		}
	}

	if !b.IsTarGz && !b.IsTarXz && !b.IsZip {
		return b.writeFile(resp.Body, downloadOf(b.File), resp.ContentLength)
	}
	archive := archiveName(url)
	f, err := asArchive(resp.Body, archive)
	if err != nil {
		return err
	}
	switch {
	case b.IsTarGz:
		return b.extractSingleFileFromTar(f, "gz", archive)
	case b.IsTarXz:
		return b.extractSingleFileFromTar(f, "xz", archive)
	}
	return b.extractSingleFileFromZip(f, archive)
}
