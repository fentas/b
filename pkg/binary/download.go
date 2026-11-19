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
// (an archive or a raw binary) is written to a temp file before b reads it,
// and a zip archive is then read into memory, because its directory is at
// its end. A longer download is refused while it is read. Tests replace it.
var maxDownloadSize int64 = 2 << 30

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

// unsafeName reports whether the name of a file in an archive is absolute or
// has a ".." element after path.Clean. A backslash counts as a separator, as
// it does on Windows. archive/tar and archive/zip give the effective name (a
// PAX or GNU long name included). b never installs such a file, and refuses
// an archive that holds one as a regular file.
func unsafeName(name string) bool {
	clean := path.Clean(strings.ReplaceAll(name, `\`, "/"))
	return path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../")
}

// unsafeNameError refuses archive for the file name.
func unsafeNameError(archive, name string) error {
	return fmt.Errorf("refusing %s: the file name %q is absolute or has a \"..\" element", archive, name)
}

// ambiguousError refuses archive because more than one file in it matches
// the binary.
func ambiguousError(archive, binary string, names []string) error {
	return fmt.Errorf("refusing %s: %d files in it match the binary %s: %s", archive, len(names), binary, quoteNames(names))
}

// tieError refuses archive because no file matches the binary by name and
// the largest candidates have the same size.
func tieError(archive, binary string, names []string) error {
	return fmt.Errorf("refusing %s: no file in it is named %s, and %d files tie as the largest: %s", archive, binary, len(names), quoteNames(names))
}

// quoteNames lists the names of files in an archive for an error, each
// quoted (%q also escapes control characters).
func quoteNames(names []string) string {
	q := make([]string, len(names))
	for i, n := range names {
		q[i] = fmt.Sprintf("%q", n)
	}
	return strings.Join(q, ", ")
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
// size is the size in the archive header.
func memberOf(member, archive string) writeSource {
	return writeSource{
		what:  fmt.Sprintf("extraction of %s from %s", member, archive),
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
//   - verifyWritten checks the byte count;
//   - then, if not nil, runs last and can refuse the file too.
//
// A download or an extraction that fails leaves the old file as it was.
func (b *Binary) writeFile(r io.Reader, src writeSource, want int64, then func() error) error {
	if want > maxBinarySize {
		return tooLarge(src, uint64(want))
	}
	return atomicfile.Write(b.File, 0755, func(w io.Writer) error {
		n, err := io.Copy(w, io.LimitReader(r, maxBinarySize+1))
		if err != nil {
			return err
		}
		if err := verifyWritten(src, n, want); err != nil {
			return err
		}
		if then != nil {
			return then()
		}
		return nil
	})
}

// writeMember writes the binary from the member of an archive (writeFile)
// and checks it (verifyWritten): the member is not empty, not larger than
// maxBinarySize, and its byte count is size when the archive header gives
// one (size >= 0). The archive readers refuse a short member before this
// count; the count is taken where the bytes are written, after every reader
// between. A read error names the member and the archive. then, if not nil,
// runs after the check and before the new file replaces the old one.
func (b *Binary) writeMember(r io.Reader, member, archive string, size int64, then func() error) error {
	src := memberOf(member, archive)
	return b.writeFile(namedReader{r: r, name: src.what}, src, size, then)
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

// openZip reads the zip archive in stream into memory, at most
// maxDownloadSize bytes, and reads its directory.
func openZip(stream io.Reader, archive string) (*zip.Reader, error) {
	zipData, err := io.ReadAll(io.LimitReader(stream, maxDownloadSize+1))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", archive, err)
	}
	if int64(len(zipData)) > maxDownloadSize {
		return nil, fmt.Errorf("refusing %s: the zip archive is larger than %d bytes, the limit for a download", archive, maxDownloadSize)
	}
	zipReader, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return nil, fmt.Errorf("reading the zip directory of %s: %w", archive, err)
	}
	return zipReader, nil
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

// extractSingleFileFromTar extracts a single file from a tar archive.
// compression can be "gz" or "xz". archive names the archive in errors.
func (b *Binary) extractSingleFileFromTar(stream io.Reader, compression, archive string) error {
	var tarReader *tar.Reader

	switch compression {
	case "gz":
		gzipReader, err := gzip.NewReader(stream)
		if err != nil {
			return fmt.Errorf("reading the gzip header of %s: %w", archive, err)
		}
		defer gzipReader.Close()
		tarReader = tar.NewReader(gzipReader)
	case "xz":
		xzReader, err := xz.NewReader(stream)
		if err != nil {
			return fmt.Errorf("reading the xz header of %s: %w", archive, err)
		}
		tarReader = tar.NewReader(xzReader)
	default:
		return fmt.Errorf("Unknown compression type: %s", compression)
	}

	// nextMatch returns the next regular file that matches the binary
	// (tarFileMatches), or nil at the end of the archive. A regular file with
	// an unsafe name refuses the archive.
	nextMatch := func() (*tar.Header, error) {
		for {
			header, err := tarReader.Next()
			if err == io.EOF {
				return nil, nil
			}
			if err != nil {
				return nil, fmt.Errorf("reading the tar archive %s: %w", archive, err)
			}
			if header.Typeflag != tar.TypeReg {
				continue
			}
			if unsafeName(header.Name) {
				return nil, unsafeNameError(archive, header.Name)
			}
			ok, err := b.tarFileMatches(header.Name)
			if err != nil {
				return nil, err
			}
			if ok {
				return header, nil
			}
		}
	}

	header, err := nextMatch()
	if err != nil {
		return err
	}
	if header == nil {
		return fmt.Errorf("file %s not found", b.Name)
	}
	// The first match is written. The rest of the archive is read before the
	// new file replaces the old one: a second match, or a regular file with
	// an unsafe name, refuses the archive.
	return b.writeMember(tarReader, header.Name, archive, header.Size, func() error {
		matches := []string{header.Name}
		for {
			next, err := nextMatch()
			if err != nil {
				return err
			}
			if next == nil {
				break
			}
			matches = append(matches, next.Name)
		}
		if len(matches) > 1 {
			return ambiguousError(archive, b.Name, matches)
		}
		return nil
	})
}

// tarFileMatches reports whether the file name of a preset tar archive is
// the binary: its base name is the name of b, the GitHub file name up to the
// first ".", or b.TarFile; or the whole name is the one that b.TarFileF
// gives.
func (b *Binary) tarFileMatches(name string) (bool, error) {
	switch filepath.Base(name) {
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

// extractSingleFileFromZip extracts the file b.Name from a zip archive: the
// one regular file whose whole name is b.Name. A symlink, a device or a
// directory is never the binary. Two files of that name, or a regular file
// with an unsafe name, refuse the archive. archive names the archive in
// errors.
func (b *Binary) extractSingleFileFromZip(stream io.Reader, archive string) error {
	zipReader, err := openZip(stream, archive)
	if err != nil {
		return err
	}

	var file *zip.File
	var matches []string
	for _, f := range zipReader.File {
		if !f.Mode().IsRegular() {
			continue
		}
		if unsafeName(f.Name) {
			return unsafeNameError(archive, f.Name)
		}
		if f.Name == b.Name {
			matches = append(matches, f.Name)
			if file == nil {
				file = f
			}
		}
	}
	if file == nil {
		return fmt.Errorf("file %s not found", b.Name)
	}
	if len(matches) > 1 {
		return ambiguousError(archive, b.Name, matches)
	}
	size, err := zipSize(file, archive)
	if err != nil {
		return err
	}

	zippedFile, err := file.Open()
	if err != nil {
		return fmt.Errorf("opening %s in %s: %w", file.Name, archive, err)
	}
	defer zippedFile.Close()

	var reader io.Reader = zippedFile
	if b.Writer != nil {
		b.Tracker = b.Writer.AddTracker(fmt.Sprintf("Extracting %s", b.Name), size)
		reader = progress.NewReader(zippedFile, b.Tracker)
		defer b.Tracker.MarkAsDone()
	}

	return b.writeMember(reader, file.Name, archive, size, nil)
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
	switch archiveType {
	case "tar.gz":
		return b.extractFromTarAuto(resp.Body, "gz", asset.Name)
	case "tar.xz":
		return b.extractFromTarAuto(resp.Body, "xz", asset.Name)
	case "zip":
		return b.extractFromZipAuto(resp.Body, asset.Name)
	default:
		// Raw binary (no archive)
		return b.writeFile(resp.Body, downloadOf(b.File), resp.ContentLength, nil)
	}
}

// extractFromTarAuto extracts the binary from a tar archive of a release
// asset. The candidates are the regular files with an execute bit. The
// binary is the one candidate whose base name is b.Name; without one, the
// single largest candidate (also when it is the only one). Two candidates
// named b.Name, two largest candidates of the same size, or a regular file
// with an unsafe name refuse the archive. At most two candidates are in
// memory at a time, each at most maxBinarySize. archive names the archive in
// errors.
func (b *Binary) extractFromTarAuto(stream io.Reader, compression, archive string) error {
	var tarReader *tar.Reader

	switch compression {
	case "gz":
		gzipReader, err := gzip.NewReader(stream)
		if err != nil {
			return fmt.Errorf("reading the gzip header of %s: %w", archive, err)
		}
		defer gzipReader.Close()
		tarReader = tar.NewReader(gzipReader)
	case "xz":
		xzReader, err := xz.NewReader(stream)
		if err != nil {
			return fmt.Errorf("reading the xz header of %s: %w", archive, err)
		}
		tarReader = tar.NewReader(xzReader)
	default:
		return fmt.Errorf("unknown compression type: %s", compression)
	}

	type candidate struct {
		name string
		data []byte // nil when the candidate is not kept, or is too large
		size int64  // the size in the tar header
	}
	var (
		named   *candidate // the first candidate named b.Name
		names   []string   // every candidate named b.Name
		largest *candidate // the first of the largest other candidates
		ties    []string   // every other candidate of the largest size
	)
	// read keeps the data of a candidate that can be installed. A candidate
	// above maxBinarySize is not read: writeMember refuses it by its size.
	read := func(header *tar.Header) (*candidate, error) {
		c := &candidate{name: header.Name, size: header.Size}
		if header.Size > maxBinarySize {
			return c, nil
		}
		data, err := io.ReadAll(tarReader)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", memberOf(header.Name, archive).what, err)
		}
		c.data = data
		return c, nil
	}

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("reading the tar archive %s: %w", archive, err)
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		if unsafeName(header.Name) {
			return unsafeNameError(archive, header.Name)
		}
		// Skip non-executable files
		if header.Mode&0111 == 0 {
			continue
		}

		if filepath.Base(header.Name) == b.Name {
			names = append(names, header.Name)
			if named == nil {
				if named, err = read(header); err != nil {
					return err
				}
				if largest != nil {
					largest.data = nil // the fallback is not used any more
				}
			}
			continue
		}
		switch {
		case largest == nil || header.Size > largest.size:
			ties = []string{header.Name}
			if named != nil {
				largest = &candidate{name: header.Name, size: header.Size}
			} else if largest, err = read(header); err != nil {
				return err
			}
		case header.Size == largest.size:
			ties = append(ties, header.Name)
		}
	}

	var chosen *candidate
	switch {
	case len(names) > 1:
		return ambiguousError(archive, b.Name, names)
	case named != nil:
		chosen = named
	case largest == nil:
		return fmt.Errorf("no executable found in archive for %s", b.Name)
	case len(ties) > 1:
		return tieError(archive, b.Name, ties)
	default:
		chosen = largest
	}

	return b.writeMember(bytes.NewReader(chosen.data), chosen.name, archive, chosen.size, nil)
}

// extractFromZipAuto extracts the binary from a zip archive of a release
// asset. The candidates are the regular files: a symlink, a device or a
// directory is never the binary. The binary is the one candidate whose base
// name is b.Name; without one, the single largest candidate (also when it is
// the only one). Two candidates named b.Name, two largest candidates of the
// same size, or a regular file with an unsafe name refuse the archive.
// archive names the archive in errors.
func (b *Binary) extractFromZipAuto(stream io.Reader, archive string) error {
	zipReader, err := openZip(stream, archive)
	if err != nil {
		return err
	}

	var named, largest *zip.File
	var names, ties []string
	for _, f := range zipReader.File {
		// In zip files, we can't reliably check execute bit, so include all regular files
		if !f.Mode().IsRegular() {
			continue
		}
		if unsafeName(f.Name) {
			return unsafeNameError(archive, f.Name)
		}
		if filepath.Base(f.Name) == b.Name {
			names = append(names, f.Name)
			if named == nil {
				named = f
			}
			continue
		}
		switch {
		case largest == nil || f.UncompressedSize64 > largest.UncompressedSize64:
			largest, ties = f, []string{f.Name}
		case f.UncompressedSize64 == largest.UncompressedSize64:
			ties = append(ties, f.Name)
		}
	}

	var chosen *zip.File
	switch {
	case len(names) > 1:
		return ambiguousError(archive, b.Name, names)
	case named != nil:
		chosen = named
	case largest == nil:
		return fmt.Errorf("no file found in archive for %s", b.Name)
	case len(ties) > 1:
		return tieError(archive, b.Name, ties)
	default:
		chosen = largest
	}
	size, err := zipSize(chosen, archive)
	if err != nil {
		return err
	}

	rc, err := chosen.Open()
	if err != nil {
		return fmt.Errorf("opening %s in %s: %w", chosen.Name, archive, err)
	}
	defer rc.Close()

	return b.writeMember(rc, chosen.Name, archive, size, nil)
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

	archive := archiveName(url)
	if b.IsTarGz {
		return b.extractSingleFileFromTar(resp.Body, "gz", archive)
	}
	if b.IsTarXz {
		return b.extractSingleFileFromTar(resp.Body, "xz", archive)
	}
	if b.IsZip {
		return b.extractSingleFileFromZip(resp.Body, archive)
	}

	return b.writeFile(resp.Body, downloadOf(b.File), resp.ContentLength, nil)
}
