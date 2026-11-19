package binary

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"math"
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

// accessDeniedError builds the error for a 401/403 download response.
func accessDeniedError(resp *http.Response, url string) error {
	if rateLimitShaped(resp) {
		return fmt.Errorf("HTTP %d for %s: rate limit exhausted; set GITHUB_TOKEN to authenticate", resp.StatusCode, url)
	}
	return fmt.Errorf("HTTP %d for %s: access denied; check credentials (for GitHub, set GITHUB_TOKEN)", resp.StatusCode, url)
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
// one byte was written, and the byte count matches want when the source
// gives a size (want >= 0). An empty file is never a binary: it cannot run.
func verifyWritten(src writeSource, written, want int64) error {
	if written == 0 {
		return fmt.Errorf("%s wrote 0 bytes: %s", src.what, src.empty)
	}
	if want >= 0 && written != want {
		return fmt.Errorf("%s is incomplete: wrote %d bytes, %s is %d; %s", src.what, written, src.size, want, src.retry)
	}
	return nil
}

// writeFile writes the binary file b.File from r in one step
// (atomicfile.Write), with mode 0755. check, if not nil, gets the number of
// bytes and can refuse them before the new file replaces the old one. A
// download or an extraction that fails leaves the old file as it was.
func (b *Binary) writeFile(r io.Reader, check func(written int64) error) error {
	return atomicfile.Write(b.File, 0755, func(w io.Writer) error {
		n, err := io.Copy(w, r)
		if err == nil && check != nil {
			err = check(n)
		}
		return err
	})
}

// writeMember writes the binary from the member of an archive (writeFile)
// and checks it (verifyWritten): the member is not empty, and its byte count
// is size when the archive header gives one (size >= 0). The archive readers
// refuse a short member too; this count is taken where the bytes are
// written, after every reader between. A read error names the member and the
// archive.
func (b *Binary) writeMember(r io.Reader, member, archive string, size int64) error {
	src := memberOf(member, archive)
	return b.writeFile(namedReader{r: r, name: src.what}, func(written int64) error {
		return verifyWritten(src, written, size)
	})
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

// zipSize is the size of f in the zip header, or -1 when it does not fit an
// int64 (the zip reader refuses such a member).
func zipSize(f *zip.File) int64 {
	if f.UncompressedSize64 > math.MaxInt64 {
		return -1
	}
	return int64(f.UncompressedSize64)
}

// archiveName is the last element of the path of rawURL: the name of an
// archive in errors. A URL without a path element gives the URL without its
// user info, query and fragment, so that no credential goes into an error. A
// URL that does not parse gives "the archive".
func archiveName(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "the archive"
	}
	if name := path.Base(u.Path); name != "." && name != "/" {
		return name
	}
	u.User, u.RawQuery, u.Fragment = nil, "", ""
	return u.String()
}

// ctx returns the context of b, or context.Background if it has none.
func (b *Binary) ctx() context.Context {
	if b.Context != nil {
		return b.Context
	}
	return context.Background()
}

// download GETs url with fetch.Download: transient failures are retried,
// and a 200 body is complete before extraction starts. label names the
// download in the progress tracker.
func (b *Binary) download(url, label string) (*http.Response, error) {
	return fetch.Download(b.ctx(), url, func(resp *http.Response) io.Reader {
		if b.Tracker == nil {
			return resp.Body
		}
		// A retry starts the count again.
		b.Tracker.UpdateMessage(fmt.Sprintf("Downloading %s", label))
		b.Tracker.UpdateTotal(resp.ContentLength)
		b.Tracker.SetValue(0)
		return progress.NewReader(resp.Body, b.Tracker)
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

		switch filepath.Base(header.Name) {
		case b.Name:
		case strings.Split(b.GitHubFile, ".")[0]:
		case b.TarFile:
		default:
			if b.TarFileF == nil {
				continue
			}
			name, err := b.TarFileF(b)
			if err != nil {
				return err
			}
			if header.Name != name {
				continue
			}
		}

		return b.writeMember(tarReader, header.Name, archive, header.Size)
	}

	return fmt.Errorf("file %s not found", b.Name)
}

// extractSingleFileFromZip extracts the file b.Name from a zip archive.
// archive names the archive in errors.
func (b *Binary) extractSingleFileFromZip(stream io.Reader, archive string) error {
	zipData, err := io.ReadAll(stream)
	if err != nil {
		return fmt.Errorf("reading %s: %w", archive, err)
	}

	zipReader, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return fmt.Errorf("reading the zip directory of %s: %w", archive, err)
	}

	for _, file := range zipReader.File {
		if file.Name == b.Name {
			zippedFile, err := file.Open()
			if err != nil {
				return fmt.Errorf("opening %s in %s: %w", file.Name, archive, err)
			}
			defer zippedFile.Close()

			var reader io.Reader = zippedFile
			if b.Writer != nil {
				b.Tracker = b.Writer.AddTracker(fmt.Sprintf("Extracting %s", b.Name), int64(file.UncompressedSize64))
				reader = progress.NewReader(zippedFile, b.Tracker)
				defer b.Tracker.MarkAsDone()
			}

			return b.writeMember(reader, file.Name, archive, zipSize(file))
		}
	}

	return fmt.Errorf("file %s not found", b.Name)
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
		return b.writeFile(resp.Body, func(written int64) error {
			return verifyWritten(downloadOf(b.File), written, resp.ContentLength)
		})
	}
}

// extractFromTarAuto extracts the best-matching binary from a tar archive
// using heuristic detection (name match > largest executable > only executable).
// archive names the archive in errors.
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
		data []byte
		size int64 // the size in the tar header
		mode os.FileMode
	}
	var candidates []candidate
	var nameMatch *candidate

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

		// Skip non-executable files
		if header.Mode&0111 == 0 {
			continue
		}

		data, err := io.ReadAll(tarReader)
		if err != nil {
			return fmt.Errorf("%s: %w", memberOf(header.Name, archive).what, err)
		}

		c := candidate{
			name: header.Name,
			data: data,
			size: header.Size,
			mode: os.FileMode(header.Mode),
		}
		candidates = append(candidates, c)

		base := filepath.Base(header.Name)
		if base == b.Name {
			nameMatch = &candidates[len(candidates)-1]
		}
	}

	var chosen *candidate
	if nameMatch != nil {
		chosen = nameMatch
	} else if len(candidates) == 1 {
		chosen = &candidates[0]
	} else if len(candidates) > 1 {
		// Pick largest executable
		chosen = &candidates[0]
		for i := range candidates {
			if len(candidates[i].data) > len(chosen.data) {
				chosen = &candidates[i]
			}
		}
	}

	if chosen == nil {
		return fmt.Errorf("no executable found in archive for %s", b.Name)
	}

	return b.writeMember(bytes.NewReader(chosen.data), chosen.name, archive, chosen.size)
}

// extractFromZipAuto extracts the best-matching binary from a zip archive.
// archive names the archive in errors.
func (b *Binary) extractFromZipAuto(stream io.Reader, archive string) error {
	zipData, err := io.ReadAll(stream)
	if err != nil {
		return fmt.Errorf("reading %s: %w", archive, err)
	}

	zipReader, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return fmt.Errorf("reading the zip directory of %s: %w", archive, err)
	}

	type candidate struct {
		name string
		file *zip.File
	}
	var candidates []candidate
	var nameMatch *candidate

	for _, f := range zipReader.File {
		if f.FileInfo().IsDir() {
			continue
		}
		// In zip files, we can't reliably check execute bit, so include all regular files
		c := candidate{name: f.Name, file: f}
		candidates = append(candidates, c)

		base := filepath.Base(f.Name)
		if base == b.Name {
			nameMatch = &candidates[len(candidates)-1]
		}
	}

	var chosen *candidate
	if nameMatch != nil {
		chosen = nameMatch
	} else if len(candidates) == 1 {
		chosen = &candidates[0]
	} else if len(candidates) > 1 {
		// Pick largest file
		chosen = &candidates[0]
		for i := range candidates {
			if candidates[i].file.UncompressedSize64 > chosen.file.UncompressedSize64 {
				chosen = &candidates[i]
			}
		}
	}

	if chosen == nil {
		return fmt.Errorf("no file found in archive for %s", b.Name)
	}

	rc, err := chosen.file.Open()
	if err != nil {
		return fmt.Errorf("opening %s in %s: %w", chosen.name, archive, err)
	}
	defer rc.Close()

	return b.writeMember(rc, chosen.name, archive, zipSize(chosen.file))
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

	return b.writeFile(resp.Body, func(written int64) error {
		return verifyWritten(downloadOf(b.File), written, resp.ContentLength)
	})
}
