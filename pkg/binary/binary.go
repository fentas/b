package binary

import (
	"context"
	"fmt"
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
	Context context.Context
	// for installation
	URL           string
	URLF          Callback
	GitHubRepo    string
	GitHubFile    string
	GitHubFileF   Callback
	Version       string
	VersionF      Callback
	VersionLocalF Callback
	Name          string
	File          string
	IsTarGz       bool
	TarFile       string
	TarFileF      Callback
	Tracker       *progress.Tracker
	// for execution
	Envs map[string]string
}

type LocalBinary struct {
	Name    string `json:"name"`
	File    string `json:"file"`
	Version string `json:"version"`
	Latest  string `json:"latest"`
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
	return &LocalBinary{
		Name:    b.Name,
		File:    b.BinaryPath(),
		Version: version,
		Latest:  latest,
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
	exists := b.BinaryExists()
	if b.File == "" {
		return fmt.Errorf("unable to determine binary path")
	}

	if exists {
		if !update {
			return nil
		}
		local := b.LocalBinary()
		if local.Version != "" && local.Version == local.Latest {
			return nil
		}
	}

	err := os.MkdirAll(filepath.Dir(b.File), 0755)
	if err != nil {
		return err
	}
	return b.downloadBinary()
}
