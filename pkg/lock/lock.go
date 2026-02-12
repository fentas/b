// Package lock manages the b.lock file — a JSON lockfile that pins
// binaries and env files to exact versions and checksums.
package lock

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// Lock is the top-level b.lock structure.
type Lock struct {
	Version   int        `json:"version"`
	Tool      ToolInfo   `json:"tool"`
	Timestamp string     `json:"timestamp"`
	Binaries  []BinEntry `json:"binaries"`
	Envs      []EnvEntry `json:"envs,omitempty"`
}

// ToolInfo records the b version that wrote the lock.
type ToolInfo struct {
	B string `json:"b"`
}

// BinEntry is a single binary in the lockfile.
type BinEntry struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	SHA256   string `json:"sha256"`
	Source   string `json:"source"`
	Preset   bool   `json:"preset,omitempty"`
	Asset    string `json:"asset,omitempty"`
	Provider string `json:"provider,omitempty"`
}

// EnvEntry is a single env in the lockfile (Phase 2).
type EnvEntry struct {
	Ref            string     `json:"ref"`
	Label          string     `json:"label,omitempty"`
	Version        string     `json:"version"`
	Commit         string     `json:"commit"`
	PreviousCommit string     `json:"previousCommit,omitempty"`
	Files          []LockFile `json:"files"`
}

// LockFile is a single file tracked in an env entry.
type LockFile struct {
	Path   string `json:"path"`
	Dest   string `json:"dest"`
	SHA256 string `json:"sha256"`
	Mode   string `json:"mode"`
}

const lockFileName = "b.lock"

// ReadLock reads and parses the lockfile from the given directory.
// Returns an empty Lock (not nil) if the file doesn't exist.
func ReadLock(dir string) (*Lock, error) {
	path := filepath.Join(dir, lockFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Lock{Version: 1}, nil
		}
		return nil, err
	}
	var lock Lock
	if err := json.Unmarshal(data, &lock); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &lock, nil
}

// WriteLock writes the lockfile to the given directory.
func WriteLock(dir string, lock *Lock, toolVersion string) error {
	lock.Version = 1
	lock.Tool = ToolInfo{B: toolVersion}
	lock.Timestamp = time.Now().UTC().Format(time.RFC3339)

	data, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(filepath.Join(dir, lockFileName), data, 0644)
}

// FindBinary returns the lock entry for a named binary, or nil.
func (l *Lock) FindBinary(name string) *BinEntry {
	for i := range l.Binaries {
		if l.Binaries[i].Name == name {
			return &l.Binaries[i]
		}
	}
	return nil
}

// UpsertBinary adds or updates a binary entry in the lock.
func (l *Lock) UpsertBinary(entry BinEntry) {
	for i := range l.Binaries {
		if l.Binaries[i].Name == entry.Name {
			l.Binaries[i] = entry
			return
		}
	}
	l.Binaries = append(l.Binaries, entry)
}

// FindEnv returns the lock entry for a given env ref (and optional label), or nil.
func (l *Lock) FindEnv(ref, label string) *EnvEntry {
	for i := range l.Envs {
		if l.Envs[i].Ref == ref && l.Envs[i].Label == label {
			return &l.Envs[i]
		}
	}
	return nil
}

// UpsertEnv adds or updates an env entry in the lock.
func (l *Lock) UpsertEnv(entry EnvEntry) {
	for i := range l.Envs {
		if l.Envs[i].Ref == entry.Ref && l.Envs[i].Label == entry.Label {
			l.Envs[i] = entry
			return
		}
	}
	l.Envs = append(l.Envs, entry)
}

// SHA256File computes the SHA256 checksum of a file.
func SHA256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}
