// Package binary provides a binary manager
package binary

import (
	"os"
	"path/filepath"

	"github.com/fentas/b/pkg/path"
)

func (b *Binary) LocalBinary(remote bool) *LocalBinary {
	var latest string
	if b.VersionF != nil && remote {
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
		Alias:    b.Alias,
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

	name := b.Alias
	if name == "" {
		name = b.Name
	}
	path := path.GetBinaryPath()
	b.File = filepath.Join(path, name)
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
		local := b.LocalBinary(true)

		// Don't short-circuit when we can't tell what's installed. An empty
		// local.Version here means VersionLocalF either didn't run or
		// errored (e.g. preset uses the wrong subcommand and the binary
		// errors out); we must not mistake that for "matches the pin".
		if local.Version != "" {
			if local.Version == local.Enforced || local.Enforced == "" && local.Latest == local.Version {
				return nil
			}
		}
	}

	return b.DownloadBinary()
}

func (b *Binary) DownloadBinary() error {
	err := os.MkdirAll(filepath.Dir(b.File), 0755)
	if err != nil {
		return err
	}
	path := b.BinaryPath()
	if ex, err := os.Executable(); err != nil || ex != path {
		return b.downloadBinary()
	}
	// Self update
	old := path + ".old"
	err = os.Rename(path, old)
	if err != nil {
		return err
	}
	defer os.Remove(old)

	err = b.downloadBinary()
	if err != nil {
		// Rollback
		_ = os.Rename(old, path)
	}
	return err
}
