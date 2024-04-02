package binary

import (
	"context"
	"os"
	"path/filepath"

	"github.com/jedib0t/go-pretty/v6/progress"
)

type IsBinary interface {
	EnsureBinary(bool) error
	LocalBinary() *LocalBinary
}

type Callback func(*Binary) (string, error)

type Binary struct {
	Context context.Context `json:"-"`
	// for installation
	URL           string            `json:"-"`
	URLF          Callback          `json:"-"`
	GitHubRepo    string            `json:"repo"`
	GitHubFile    string            `json:"-"`
	GitHubFileF   Callback          `json:"-"`
	Version       string            `json:"-"`
	VersionF      Callback          `json:"-"`
	VersionLocalF Callback          `json:"-"`
	Name          string            `json:"name" yaml:"name"`
	File          string            `json:"-"`
	IsTarGz       bool              `json:"-"`
	TarFile       string            `json:"-"`
	TarFileF      Callback          `json:"-"`
	Tracker       *progress.Tracker `json:"-"`
	// for execution
	Envs map[string]string `json:"-"`
}

type LocalBinary struct {
	Name     string `json:"name"`
	File     string `json:"file,omitempty"`
	Version  string `json:"version,omitempty"`
	Latest   string `json:"latest"`
	Enforced string `json:"enforced,omitempty"`
}

func (b *Binary) LocalBinary() *LocalBinary {
	var latest string
	if b.VersionF != nil {
		latest, _ = b.VersionF(b)
	}
	version := b.Version
	if b.VersionLocalF != nil {
		version, _ = b.VersionLocalF(b)
	}
	file := b.BinaryPath()
	if !b.BinaryExists() {
		file = ""
	}
	return &LocalBinary{
		Name:     b.Name,
		File:     file,
		Version:  version,
		Latest:   latest,
		Enforced: b.Version,
	}
}

func (b *Binary) BinaryPath() string {
	if b.File != "" {
		return b.File
	}

	// if we find the binary in the PATH, we are done
	// var err error
	// if b.File, err = exec.LookPath(b.Name); err == nil {
	// 	// todo should we block the binary from being updated?
	// 	return b.File
	// }

	path := GetBinaryPath()
	b.File = filepath.Join(path, b.Name)
	return b.File
}

func (b *Binary) BinaryExists() bool {
	path := b.BinaryPath()
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

func (b *Binary) EnsureBinary(update bool) error {
	if b.BinaryExists() {
		if !update {
			return nil
		}
		local := b.LocalBinary()

		if local.Version == local.Enforced || local.Enforced == "" && local.Latest == local.Version {
			return nil
		}
	}

	return b.DownloadBinary()
}

func (b *Binary) DownloadBinary() error {
	err := os.MkdirAll(filepath.Dir(b.File), 0755)
	if err != nil {
		return err
	}
	return b.downloadBinary()
}
