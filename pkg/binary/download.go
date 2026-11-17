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
	"os"
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

// verifyWritten confirms a download landed on disk: at least one byte was
// written, and the byte count matches Content-Length when the server sent one.
func verifyWritten(file string, written, contentLength int64) error {
	if written == 0 {
		return fmt.Errorf("download of %s wrote 0 bytes: the server sent an empty body; retry the download", file)
	}
	if contentLength >= 0 && written != contentLength {
		return fmt.Errorf("download of %s is incomplete: wrote %d bytes, Content-Length is %d; retry the download", file, written, contentLength)
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
// compression can be "gz" or "xz".
func (b *Binary) extractSingleFileFromTar(stream io.Reader, compression string) error {
	var tarReader *tar.Reader

	switch compression {
	case "gz":
		gzipReader, err := gzip.NewReader(stream)
		if err != nil {
			return err
		}
		defer gzipReader.Close()
		tarReader = tar.NewReader(gzipReader)
	case "xz":
		xzReader, err := xz.NewReader(stream)
		if err != nil {
			return err
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
			return err
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

		return b.writeFile(tarReader, nil)
	}

	return fmt.Errorf("file %s not found", b.Name)
}

func (b *Binary) extractSingleFileFromZip(stream io.Reader) error {
	zipData, err := io.ReadAll(stream)
	if err != nil {
		return err
	}

	zipReader, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return err
	}

	for _, file := range zipReader.File {
		if file.Name == b.Name {
			zippedFile, err := file.Open()
			if err != nil {
				return err
			}
			defer zippedFile.Close()

			var reader io.Reader = zippedFile
			if b.Writer != nil {
				b.Tracker = b.Writer.AddTracker(fmt.Sprintf("Extracting %s", b.Name), int64(file.UncompressedSize64))
				reader = progress.NewReader(zippedFile, b.Tracker)
				defer b.Tracker.MarkAsDone()
			}

			return b.writeFile(reader, nil)
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

	destDir := filepath.Dir(b.BinaryPath())
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return err
	}

	// Special providers that compile/extract directly
	switch pt := p.(type) {
	case *provider.GoInstall:
		path, err := pt.Install(b.ProviderRef, b.Version, destDir)
		if err != nil {
			return err
		}
		b.File = path
		return nil
	case *provider.Docker:
		path, err := pt.Install(b.ProviderRef, b.Version, destDir, nil)
		if err != nil {
			return err
		}
		b.File = path
		return nil
	case *provider.OCI:
		path, err := pt.Install(b.ProviderRef, b.Version, destDir)
		if err != nil {
			return err
		}
		b.File = path
		return nil
	case *provider.Git:
		path, err := pt.Install(b.ProviderRef, b.Version, destDir)
		if err != nil {
			return err
		}
		b.File = path
		return nil
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
		return b.extractFromTarAuto(resp.Body, "gz")
	case "tar.xz":
		return b.extractFromTarAuto(resp.Body, "xz")
	case "zip":
		return b.extractFromZipAuto(resp.Body)
	default:
		// Raw binary (no archive)
		return b.writeFile(resp.Body, func(written int64) error {
			return verifyWritten(b.File, written, resp.ContentLength)
		})
	}
}

// extractFromTarAuto extracts the best-matching binary from a tar archive
// using heuristic detection (name match > largest executable > only executable).
func (b *Binary) extractFromTarAuto(stream io.Reader, compression string) error {
	var tarReader *tar.Reader

	switch compression {
	case "gz":
		gzipReader, err := gzip.NewReader(stream)
		if err != nil {
			return err
		}
		defer gzipReader.Close()
		tarReader = tar.NewReader(gzipReader)
	case "xz":
		xzReader, err := xz.NewReader(stream)
		if err != nil {
			return err
		}
		tarReader = tar.NewReader(xzReader)
	default:
		return fmt.Errorf("unknown compression type: %s", compression)
	}

	type candidate struct {
		name string
		data []byte
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
			return err
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
			return err
		}

		c := candidate{
			name: header.Name,
			data: data,
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

	return b.writeFile(bytes.NewReader(chosen.data), nil)
}

// extractFromZipAuto extracts the best-matching binary from a zip archive.
func (b *Binary) extractFromZipAuto(stream io.Reader) error {
	zipData, err := io.ReadAll(stream)
	if err != nil {
		return err
	}

	zipReader, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return err
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
		return err
	}
	defer rc.Close()

	return b.writeFile(rc, nil)
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

	if b.IsTarGz {
		return b.extractSingleFileFromTar(resp.Body, "gz")
	}
	if b.IsTarXz {
		return b.extractSingleFileFromTar(resp.Body, "xz")
	}
	if b.IsZip {
		return b.extractSingleFileFromZip(resp.Body)
	}

	return b.writeFile(resp.Body, func(written int64) error {
		return verifyWritten(b.File, written, resp.ContentLength)
	})
}
