// Package lock manages the b.lock file — a JSON lockfile that pins
// binaries and env files to exact versions and checksums.
package lock

import (
	"bytes"
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

// BinEntry is a single binary in the lockfile. Name is the name of the
// installed file in the binary directory: the alias of the binary when it
// has one, else the name of the binary.
type BinEntry struct {
	Name string `json:"name"`
	// Binary is the name of the binary in the file when it is not Name: the
	// preset of an alias entry, the binary of a provider ref with an alias,
	// or the binary that `b install --alias <name> --force` put there. Empty
	// when the file has the name of its binary, and in rows of an older b.
	Binary   string `json:"binary,omitempty"`
	Version  string `json:"version"`
	SHA256   string `json:"sha256"`
	Source   string `json:"source"`
	Preset   bool   `json:"preset,omitempty"`
	Asset    string `json:"asset,omitempty"`
	Provider string `json:"provider,omitempty"`
	// Digest is the upstream content identity at the time of the last
	// successful install — for docker:// / oci:// binaries this is the
	// image manifest digest (sha256:...) the tag resolved to. Empty for
	// providers that don't expose a stable digest. When non-empty, `b
	// update` compares it against a freshly-resolved digest and skips
	// the re-download if they match.
	Digest string `json:"digest,omitempty"`
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
	Status string `json:"-"` // runtime only: replaced, kept, merged, conflict
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

// WriteLock writes the lockfile to the given directory. When the file on
// disk already holds the same entries, WriteLock leaves it as it is: the
// timestamp and the tool version change only together with an entry, so a
// run that changes nothing leaves b.lock unchanged (and `git diff` clean).
func WriteLock(dir string, lock *Lock, toolVersion string) error {
	lock.Version = 1
	path := filepath.Join(dir, lockFileName)
	if data, err := os.ReadFile(path); err == nil {
		var prev Lock
		if json.Unmarshal(data, &prev) == nil && prev.Version == lock.Version && sameEntries(&prev, lock) {
			lock.Tool = prev.Tool
			lock.Timestamp = prev.Timestamp
			return nil
		}
	}
	lock.Tool = ToolInfo{B: toolVersion}
	lock.Timestamp = time.Now().UTC().Format(time.RFC3339)

	data, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0644)
}

// sameEntries reports whether two locks hold the same binary and env
// entries. A nil and an empty list are the same.
func sameEntries(a, b *Lock) bool {
	type entries struct {
		Binaries []BinEntry `json:"binaries,omitempty"`
		Envs     []EnvEntry `json:"envs,omitempty"`
	}
	ja, errA := json.Marshal(entries{a.Binaries, a.Envs})
	jb, errB := json.Marshal(entries{b.Binaries, b.Envs})
	return errA == nil && errB == nil && bytes.Equal(ja, jb)
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

// RemoveBinary removes the binary entry of that name. It reports whether
// there was one.
func (l *Lock) RemoveBinary(name string) bool {
	for i := range l.Binaries {
		if l.Binaries[i].Name == name {
			l.Binaries = append(l.Binaries[:i], l.Binaries[i+1:]...)
			return true
		}
	}
	return false
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

// RemoveEnv removes an env entry from the lock. Returns true if found.
func (l *Lock) RemoveEnv(ref, label string) bool {
	for i := range l.Envs {
		if l.Envs[i].Ref == ref && l.Envs[i].Label == label {
			l.Envs = append(l.Envs[:i], l.Envs[i+1:]...)
			return true
		}
	}
	return false
}

// UpsertEnv adds or updates an env entry in the lock. It replaces the first
// matching (ref,label) entry in place and drops any further duplicates, so the
// write is idempotent: a lock that somehow accumulated two entries for the same
// env collapses to one here instead of leaving a stale twin behind.
func (l *Lock) UpsertEnv(entry EnvEntry) {
	replaced := false
	kept := l.Envs[:0]
	for _, e := range l.Envs {
		if e.Ref == entry.Ref && e.Label == entry.Label {
			if !replaced {
				kept = append(kept, entry)
				replaced = true
			}
			continue // drop duplicate
		}
		kept = append(kept, e)
	}
	if !replaced {
		kept = append(kept, entry)
	}
	l.Envs = kept
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
