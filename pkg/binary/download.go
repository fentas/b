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
)

func (b *Binary) githubURL() (string, error) {
	var err error
	file := b.GitHubFile
	if b.GitHubFileF != nil {
		file, err = b.GitHubFileF(b)
	}
	return fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", b.GitHubRepo, b.Version, file), err
}

func (b *Binary) extractSingleFileFromTarGz(stream io.Reader) error {
	gzipReader, err := gzip.NewReader(stream)
	if err != nil {
		return err
	}
	tarReader := tar.NewReader(gzipReader)
	defer gzipReader.Close()

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
	}

	return os.Chmod(b.File, 0755)
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
			if _, err = io.Copy(bfile, zippedFile); err != nil {
				return err
			}
		}
	}

	return os.Chmod(b.File, 0755)
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

	reader := resp.Body
	if b.Tracker != nil {
		b.Tracker.UpdateMessage(fmt.Sprintf("Downloading %s", b.Name))
		b.Tracker.UpdateTotal(resp.ContentLength)
		reader = progress.NewReader(resp.Body, b.Tracker)
	}
	if b.IsTarGz {
		return b.extractSingleFileFromTarGz(reader)
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
