package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fentas/b/pkg/binary"
	"github.com/fentas/b/pkg/lock"
	"github.com/fentas/b/pkg/path"
	"github.com/fentas/b/pkg/provider"
	"github.com/fentas/b/pkg/state"
)

// A b.lock row describes one installed file. Its name is the name of the
// file in the binary directory (lockName): the alias of the binary when it
// has one, else the name of the binary. So each b.yaml entry has its own row,
// also when two entries install the same preset or two provider refs have
// the same binary name. b verify finds the file of each row under that name.
//
// Older versions of b wrote some rows under the name of the binary instead:
// an alias entry of a preset (before alias rows), and a provider ref with an
// alias. findLockRow finds such a row once under the old name, and the
// install writes it under the file name. An install trusts the version of
// a row only when its sha256 is the sha256 of the file (planInstall), so a
// row that moved does not cause a download, and a row of another file does
// not count as the row of this one.

// entryFileName is the name of the file that a b.yaml entry installs, the
// name of its b.lock row: for a provider ref its alias, else the binary name
// from the ref; for a preset entry its own name (the name of an alias entry
// is the alias).
func entryFileName(e *binary.LocalBinary) string {
	if e.IsProviderRef {
		if e.Alias != "" {
			return e.Alias
		}
		return provider.BinaryName(e.Name)
	}
	return e.Name
}

// configFileNames maps the name of each file that b.yaml installs to the
// names of the entries that install it, sorted.
func configFileNames(cfg *state.State) map[string][]string {
	files := map[string][]string{}
	if cfg == nil {
		return files
	}
	for _, e := range cfg.Binaries {
		if e == nil {
			continue
		}
		file := entryFileName(e)
		files[file] = append(files[file], e.Name)
	}
	for _, names := range files {
		sort.Strings(names)
	}
	return files
}

// checkConfigFiles refuses a b.yaml in which two entries install the same
// file. The two binaries would replace each other at each install, and
// b.lock has one row for the file. Two preset entries always have their own
// names, so at least one of the entries is a provider ref.
func checkConfigFiles(cfg *state.State) error {
	files := configFileNames(cfg)
	var dups []string
	for file, names := range files {
		if len(names) > 1 {
			dups = append(dups, file)
		}
	}
	if len(dups) == 0 {
		return nil
	}
	sort.Strings(dups)
	lines := make([]string, 0, len(dups))
	for _, file := range dups {
		lines = append(lines, fmt.Sprintf("b.yaml: the entries %s install the same file %s", joinNames(files[file]), file))
	}
	return fmt.Errorf("%s\n  Hint: give a provider ref its own file name with `alias: <name>`, or remove one of the entries", strings.Join(lines, "\n"))
}

// joinNames joins names as "a and b", or "a, b and c".
func joinNames(names []string) string {
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// findLockRow is b's b.lock row, and the name that it has in b.lock: the row
// of b's file (lockName), or else a row that an older b wrote for b under the
// binary's name (oldLockRow). It is nil when there is none.
func findLockRow(lk *lock.Lock, b *binary.Binary) (*lock.BinEntry, string) {
	name := lockName(b)
	if e := lk.FindBinary(name); e != nil {
		return e, name
	}
	if name == b.Name {
		return nil, ""
	}
	if e := lk.FindBinary(b.Name); e != nil && oldLockRow(e, b) {
		return e, b.Name
	}
	return nil, ""
}

// oldLockRow reports whether e, the row under the name of b's binary, can be
// the row that an older b wrote for b's file: a row without a Binary field,
// of a provider ref with b's ref, or of a preset when b is a preset. It can
// also be the row of a present file of that name. The caller must check the
// sha256 of b's file against it before it trusts the version.
func oldLockRow(e *lock.BinEntry, b *binary.Binary) bool {
	if e.Binary != "" {
		return false
	}
	if isProviderRef(b) {
		return !e.Preset && e.Source == b.ProviderRef
	}
	return e.Preset
}

// lockBinary is the Binary field of b's b.lock row: the name of b when its
// file has another name, else "".
func lockBinary(b *binary.Binary) string {
	if lockName(b) != b.Name {
		return b.Name
	}
	return ""
}

// otherBinary reports whether the b.lock row e says that its file is another
// binary than b. Then the file is not b, also when its sha256 is the one in
// the row: `b install --alias <preset> --force` put another binary at the
// file of that preset, or b.yaml now names another binary for the file. A
// row of an older b has no Binary field; for it, only its kind (preset or
// provider ref) and the source of a provider ref count.
func otherBinary(e *lock.BinEntry, b *binary.Binary) bool {
	if e.Binary != "" && e.Binary != b.Name {
		return true
	}
	if isProviderRef(b) {
		return e.Preset || (e.Source != "" && e.Source != b.ProviderRef)
	}
	return e.Provider != ""
}

// lockFileNames is the set of file names that the b.yaml entries and the
// given binaries install: the names of the b.lock rows that stay.
func lockFileNames(cfg *state.State, binaries []*binary.Binary) map[string]bool {
	names := map[string]bool{}
	for file := range configFileNames(cfg) {
		names[file] = true
	}
	for _, b := range binaries {
		names[lockName(b)] = true
	}
	return names
}

// pruneLock removes the b.lock rows that describe no file: no b.yaml entry
// installs a file of that name, and the binary directory has no file of that
// name. Such a row makes b verify fail, and no install writes it again: the
// row of an entry that b.yaml does not have any more, or a row of an older b
// under an old name. A row of a file that is there stays, also without an
// entry: an install of a binary that is not in b.yaml, or an entry of another
// b.yaml (b --config) that uses the same b.lock. It returns the names of the
// rows that it removed.
func (o *SharedOptions) pruneLock() ([]string, error) {
	if o.Config == nil {
		return nil, nil
	}
	binDir := path.GetBinaryPath()
	if binDir == "" {
		return nil, nil
	}
	lockDir := o.LockDir()
	lk, err := lock.ReadLock(lockDir)
	if err != nil {
		return nil, err
	}
	installed := configFileNames(o.Config)
	var removed []string
	kept := lk.Binaries[:0]
	for _, e := range lk.Binaries {
		if _, ok := installed[e.Name]; !ok && e.Name != "" {
			if _, err := os.Lstat(filepath.Join(binDir, e.Name)); os.IsNotExist(err) {
				removed = append(removed, e.Name)
				continue
			}
		}
		kept = append(kept, e)
	}
	if len(removed) == 0 {
		return nil, nil
	}
	lk.Binaries = kept
	if err := lock.WriteLock(lockDir, lk, o.bVersion); err != nil {
		return nil, err
	}
	return removed, nil
}

// reportPruned tells which b.lock rows pruneLock removed, or warns when it
// failed.
func (o *SharedOptions) reportPruned(removed []string, err error) {
	if err != nil {
		fmt.Fprintf(o.IO.ErrOut, "Warning: failed to remove stale rows from b.lock: %v\n", err)
		return
	}
	if len(removed) > 0 {
		fmt.Fprintf(o.IO.ErrOut, "Removed from b.lock: %s (no b.yaml entry installs them, and their files are not there)\n", strings.Join(removed, ", "))
	}
}
