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
		return fmt.Errorf("unknown compression type: %s", compression)
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
			//lint:ignore ST1005 "this error is capitalized because it's presented directly to the user"
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
