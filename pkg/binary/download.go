package binary

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/fentas/b/pkg/provider"
	"github.com/fentas/goodies/progress"
	"github.com/ulikunitz/xz"
)

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

		file, err := os.OpenFile(b.File, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0755)
		if err != nil {
			return err
		}
		defer file.Close()
		if _, err = io.Copy(file, tarReader); err != nil {
			return err
		}
		return os.Chmod(b.File, 0755)
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

			bfile, err := os.OpenFile(b.File, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0755)
			if err != nil {
				return err
			}
			defer bfile.Close()

			var reader io.Reader = zippedFile
			if b.Writer != nil {
				b.Tracker = b.Writer.AddTracker(fmt.Sprintf("Extracting %s", b.Name), int64(file.UncompressedSize64))
				reader = progress.NewReader(zippedFile, b.Tracker)
				defer b.Tracker.MarkAsDone()
			}

			if _, err = io.Copy(bfile, reader); err != nil {
				return err
			}
			return os.Chmod(b.File, 0755)
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
	asset, err := provider.MatchAsset(release.Assets, repoName)
	if err != nil {
		return fmt.Errorf("%s@%s: %w", b.ProviderRef, b.Version, err)
	}

	return b.downloadAsset(asset)
}

// downloadAsset downloads a release asset and extracts the binary if archived.
func (b *Binary) downloadAsset(asset *provider.Asset) error {
	resp, err := http.Get(asset.URL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d downloading %s", resp.StatusCode, asset.Name)
	}

	var reader io.ReadCloser = resp.Body
	if b.Tracker != nil {
		b.Tracker.UpdateMessage(fmt.Sprintf("Downloading %s", asset.Name))
		b.Tracker.UpdateTotal(resp.ContentLength)
		reader = io.NopCloser(progress.NewReader(resp.Body, b.Tracker))
	}

	archiveType := provider.DetectArchiveType(asset.Name)
	switch archiveType {
	case "tar.gz":
		return b.extractFromTarAuto(reader, "gz")
	case "tar.xz":
		return b.extractFromTarAuto(reader, "xz")
	case "zip":
		return b.extractFromZipAuto(reader)
	default:
		// Raw binary (no archive)
		file, err := os.OpenFile(b.File, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0755)
		if err != nil {
			return err
		}
		_, err = io.Copy(file, reader)
		file.Close()
		if err != nil {
			return err
		}
		return os.Chmod(b.File, 0755)
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

	if err := os.WriteFile(b.File, chosen.data, 0755); err != nil {
		return err
	}
	return nil
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

	outFile, err := os.OpenFile(b.File, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	defer outFile.Close()

	if _, err := io.Copy(outFile, rc); err != nil {
		return err
	}
	return os.Chmod(b.File, 0755)
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
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Check HTTP status code
	if resp.StatusCode != http.StatusOK {
		switch resp.StatusCode {
		case http.StatusNotFound:
			var latest string
			if b.VersionF != nil {
				latest, _ = b.VersionF(b)
			}
			return fmt.Errorf("%s not found ^^ %s", b.Version, latest)
		case http.StatusForbidden:
		case http.StatusUnauthorized:
			return fmt.Errorf("Unauthorized")
		case http.StatusTooManyRequests:
			return fmt.Errorf("Rate limited")
		default:
			return fmt.Errorf("HTTP error %d", resp.StatusCode)
		}
	}

	reader := resp.Body
	if b.Tracker != nil {
		b.Tracker.UpdateMessage(fmt.Sprintf("Downloading %s", b.Name))
		b.Tracker.UpdateTotal(resp.ContentLength)
		reader = progress.NewReader(resp.Body, b.Tracker)
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
		return b.extractSingleFileFromTar(reader, "gz")
	}
	if b.IsTarXz {
		return b.extractSingleFileFromTar(reader, "xz")
	}
	if b.IsZip {
		return b.extractSingleFileFromZip(reader)
	}

	file, err := os.OpenFile(b.File, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	_, err = io.Copy(file, reader)
	file.Close()
	if err != nil {
		return err
	}

	return os.Chmod(b.File, 0755)
}
