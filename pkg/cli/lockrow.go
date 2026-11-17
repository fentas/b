package cli

import (
	"sort"

	"github.com/fentas/b/pkg/binary"
	"github.com/fentas/b/pkg/lock"
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
// the row that an older b wrote for b's file: a row of a provider ref with
// b's ref, or of a preset when b is a preset. It can also be the row of a
// present file of that name. The caller must check the sha256 of b's file
// against it before it trusts the version.
func oldLockRow(e *lock.BinEntry, b *binary.Binary) bool {
	if isProviderRef(b) {
		return !e.Preset && e.Source == b.ProviderRef
	}
	return e.Preset
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
