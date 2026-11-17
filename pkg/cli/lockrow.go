package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
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

// A b.lock row describes one installed file. Its name identifies the file
// (fileName, lockName):
//   - a file in the binary directory: its name, the alias of the binary when
//     it has one, else the name of the binary;
//   - a file that b.yaml puts elsewhere with a relative `file:`: that path as
//     b.yaml writes it, cleaned, relative to the directory of b.yaml (and of
//     b.lock), with "./" or "../" in front, for example ../tools/x;
//   - a file that b.yaml puts elsewhere with an absolute `file:`: that path
//     as b.yaml writes it, cleaned.
//
// So the name is the same in each checkout of the project with the same
// layout, and it holds no path of one machine that b.yaml does not hold.
// Each b.yaml entry has its own row, also when two entries install the same
// preset or two provider refs have the same binary name. b verify and the
// prune find the file of each row by that name (lockFiles).
//
// Older versions of b wrote some rows under another name: the name of the
// binary for an alias entry of a preset (before alias rows) and for a
// provider ref with an alias, and the alias or the name of the binary for an
// entry with `file:`. Before any download and before any prune, b install
// and b update move such a row to the name of the file (moveOldLockRows). A
// row under an old name counts only when its sha256 is the sha256 of the
// file (describesFile): under that name it can also be the row of another
// file. So a row that moved does not cause a download. The row under the old
// name stays while it can describe a file (describesNoFile).

// fileName is the name of the b.lock row of the file at file, with "/" as
// separator. asWritten is the `file:` of the b.yaml entry as b.yaml writes
// it ("" when b.yaml does not give the file). When file is "", the file is
// the one of that name in the binary directory, and so is a file in the
// binary directory (also through a symlink, realDir): the name is its name.
// For a relative asWritten the name is asWritten, cleaned, with "./" or
// "../" in front: a path relative to the directory of b.yaml, the same in
// each checkout with the same layout. Else the name is the absolute path,
// cleaned, as b.yaml writes it: b does not resolve its symlinks, because a
// resolved path can be another one on each machine.
func fileName(file, asWritten, name string) string {
	if file == "" {
		return name
	}
	binDir := path.GetBinaryPath()
	if dir := filepath.Dir(filepath.Clean(file)); dir == filepath.Clean(binDir) || realDir(dir) == realDir(binDir) {
		return filepath.Base(file)
	}
	if asWritten != "" && !filepath.IsAbs(asWritten) {
		rel := filepath.ToSlash(filepath.Clean(asWritten))
		if rel != ".." && !strings.HasPrefix(rel, "../") {
			rel = "./" + rel
		}
		return rel
	}
	abs, err := filepath.Abs(file)
	if err != nil {
		abs = file
	}
	return filepath.ToSlash(abs)
}

// lockDirName reports whether the b.lock row name is a path relative to the
// directory of b.yaml (fileName): it starts with "./" or "../".
func lockDirName(name string) bool {
	return name == ".." || strings.HasPrefix(name, "./") || strings.HasPrefix(name, "../")
}

// within reports whether file is dir or in dir. Both are absolute and clean.
func within(dir, file string) bool {
	rel, err := filepath.Rel(dir, file)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// realDir is the absolute path of the directory dir with its symlinks
// resolved: the longest part of it that exists, through
// filepath.EvalSymlinks, and the rest as it is. The directory need not
// exist.
func realDir(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}
	rest := ""
	for d := abs; ; {
		if resolved, err := filepath.EvalSymlinks(d); err == nil {
			return filepath.Join(resolved, rest)
		}
		parent := filepath.Dir(d)
		if parent == d {
			return abs
		}
		rest = filepath.Join(filepath.Base(d), rest)
		d = parent
	}
}

// lockFiles finds the files of b.lock rows by their names (fileName): an
// absolute name is the path of the file, a name with "./" or "../" in
// front is relative to the directory of b.yaml, another name is relative
// to the binary directory. b reads the file of a row only inside the roots
// (the project, the directory of b.yaml and the binary directory), or when
// a b.yaml entry installs the file of that row (named: for example a
// `file:` outside the project). A crafted name can make b read no other
// file.
type lockFiles struct {
	binDir  string          // the binary directory, its symlinks resolved
	lockDir string          // the directory of b.yaml and b.lock, its symlinks resolved
	roots   []string        // their symlinks resolved
	named   map[string]bool // the rows of the files that b.yaml installs
}

// lockFiles is the lockFiles of this project and its b.yaml.
func (o *SharedOptions) lockFiles() lockFiles {
	binDir := path.GetBinaryPath()
	l := lockFiles{binDir: realDir(binDir), lockDir: realDir(o.LockDir()), named: lockFileNames(o.Config)}
	for _, root := range []string{binDir, o.LockDir(), o.ProjectRoot()} {
		if root != "" {
			l.roots = append(l.roots, realDir(root))
		}
	}
	return l
}

// rowFileState is what lockFiles.find finds at the file of a row.
type rowFileState int

const (
	rowFileMissing rowFileState = iota // no file (brokenPath), also a dangling symlink
	rowFileOutside                     // a file that b does not read: outside the roots, not named
	rowFileThere                       // a file that b can read, of any type
	rowFileUnknown                     // b cannot follow the path (for example no permission)
)

// find is the file of the b.lock row of that name, its state, and for
// rowFileUnknown the error. It follows symlinks, as b verify reads a file:
// a symlink to a file inside the roots is that file, a symlink to a file
// outside them is outside, and a dangling symlink, a symlink loop or a path
// through a file (plain/sub) is no file. It does not open the file: the
// prune can find out whether the file of a row outside the roots is there.
func (l lockFiles) find(name string) (string, rowFileState, error) {
	file := filepath.FromSlash(name)
	switch {
	case filepath.IsAbs(file):
	case lockDirName(name):
		file = filepath.Join(l.lockDir, file)
	default:
		file = filepath.Join(l.binDir, file)
	}
	file = filepath.Clean(file)
	resolved, err := filepath.EvalSymlinks(file)
	if err != nil {
		// EvalSymlinks gives no error number for a loop; os.Stat does.
		if _, statErr := os.Stat(file); statErr == nil || !brokenPath(statErr) {
			return file, rowFileUnknown, err
		}
		return file, rowFileMissing, nil
	}
	if !l.named[name] && !l.inside(resolved) {
		return file, rowFileOutside, nil
	}
	return resolved, rowFileThere, nil
}

// inside reports whether file is in one of the roots.
func (l lockFiles) inside(file string) bool {
	for _, root := range l.roots {
		if within(root, file) {
			return true
		}
	}
	return false
}

// errNotRegular is the error of regularFileSHA256 for a file that is not a
// regular file.
var errNotRegular = errors.New("not a regular file")

// fileSHA256 is the sha256 of the regular file at file, through its
// symlinks (regularFileSHA256).
func fileSHA256(file string) (string, error) {
	resolved, err := filepath.EvalSymlinks(file)
	if err != nil {
		return "", err
	}
	return regularFileSHA256(resolved)
}

// regularFileSHA256 is the sha256 of the regular file at file, a path
// without symlinks. It reads only a regular file, else it returns
// errNotRegular: it checks the type before it opens the file (so it does
// not open a device or a FIFO), it opens the file without blocking and
// without following a symlink (openFlags), and it checks again that the
// open file is that regular file.
func regularFileSHA256(file string) (string, error) {
	before, err := os.Lstat(file)
	if err != nil {
		return "", err
	}
	if !before.Mode().IsRegular() {
		return "", errNotRegular
	}
	f, err := os.OpenFile(file, os.O_RDONLY|openFlags, 0)
	if err != nil {
		return "", err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !fi.Mode().IsRegular() || !os.SameFile(before, fi) {
		return "", errNotRegular
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// entryFileName is the name of the file that a b.yaml entry installs, the
// name of its b.lock row: lockName of the entry's binary. A provider ref
// has the binary name from the ref and its alias; a preset entry has its
// own name as the alias (resolveBinary), the name of an alias entry is the
// alias.
func entryFileName(e *binary.LocalBinary) string {
	b := &binary.Binary{Alias: e.Name, File: e.File, FileAsWritten: e.FileAsWritten}
	if e.IsProviderRef {
		b.Name, b.Alias = provider.BinaryName(e.Name), e.Alias
	}
	return lockName(b)
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

// describesFile reports whether the b.lock row e describes the file at file:
// its sha256 is the sha256 of the file. Only then does b trust the version
// of the row. It reads only a regular file (fileSHA256).
func describesFile(e *lock.BinEntry, file string) bool {
	if e == nil || e.SHA256 == "" {
		return false
	}
	hash, err := fileSHA256(file)
	return err == nil && hash == e.SHA256
}

// findLockRow is b's b.lock row, and the name that it has in b.lock: the row
// of b's file (lockName), or else a row that an older b wrote for b under
// its alias or the name of its binary (oldLockRow) when it describes b's
// file. It is nil when there is none.
func findLockRow(lk *lock.Lock, b *binary.Binary) (*lock.BinEntry, string) {
	name := lockName(b)
	if e := lk.FindBinary(name); e != nil {
		return e, name
	}
	for _, old := range []string{b.Alias, b.Name} {
		if old == "" || old == name {
			continue
		}
		if e := lk.FindBinary(old); e != nil && oldLockRow(e, b) && describesFile(e, b.BinaryPath()) {
			return e, old
		}
	}
	return nil, ""
}

// oldLockRow reports whether e, a row under the alias of b or the name of
// its binary, can be the row that an older b wrote for b's file: a row without a Binary field,
// of a provider ref with b's ref, or of a preset when b is a preset. It can
// also be the row of a present file of that name.
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
// row of an older b has no Binary field, also a row that an older b wrote
// again; for it, its kind (preset or provider ref) and its source count:
// the ref of a provider ref, and the repository of a preset when the row
// and the preset have one (presetSource).
func otherBinary(e *lock.BinEntry, b *binary.Binary) bool {
	if e.Binary != "" && e.Binary != b.Name {
		return true
	}
	if isProviderRef(b) {
		return e.Preset || (e.Source != "" && e.Source != b.ProviderRef)
	}
	if e.Provider != "" {
		return true
	}
	src := presetSource(b)
	return e.Binary == "" && src != "" && e.Source != "" && e.Source != src
}

// presetSource is the source of a preset in its b.lock row: its GitHub
// repository, "" when it has none.
func presetSource(b *binary.Binary) string {
	if b.GitHubRepo == "" {
		return ""
	}
	return "github.com/" + b.GitHubRepo
}

// lockFileNames is the set of file names that the b.yaml entries install:
// the names of the b.lock rows that stay, also while their files are not
// there.
func lockFileNames(cfg *state.State) map[string]bool {
	names := map[string]bool{}
	for file := range configFileNames(cfg) {
		names[file] = true
	}
	return names
}

// describesNoFile reports whether the b.lock row of that name describes no
// file: keep (the files that b.yaml installs) has no file of that name, and
// no file is at the path of the row (find: missing, also a dangling
// symlink, a symlink loop or a path through a file). This is also true for
// a row outside the roots: find only finds out whether its file is there.
// A row of a file that is there can describe it, also without a b.yaml
// entry; so can a row whose path b cannot follow.
func (l lockFiles) describesNoFile(name string, keep map[string]bool) bool {
	if name == "" || keep[name] {
		return false
	}
	_, state, _ := l.find(name)
	return state == rowFileMissing
}

// moveOldLockRows moves each row that an older b wrote for one of the
// binaries under the name of its binary (findLockRow: only when the row
// describes the binary's file) to the name of the file. The row under the
// old name stays when it can also describe another file: a b.yaml entry
// installs a file of that name (stay), or the file is there
// (describesNoFile). (A binary of this run that installs a file of that
// name, without an entry, writes its row again, or removes it after a
// failed download.) It reports whether it changed lk.
func moveOldLockRows(lk *lock.Lock, binaries []*binary.Binary, stay map[string]bool, files lockFiles) bool {
	old := map[string]bool{}
	for _, b := range binaries {
		e, rowName := findLockRow(lk, b)
		if e == nil || rowName == lockName(b) {
			continue
		}
		moved := *e
		moved.Name = lockName(b)
		// A row that says that the file is another binary (otherBinary:
		// the source of another preset) keeps what it says, so that the
		// install downloads b (planInstall). Else it is b's row.
		if !otherBinary(e, b) {
			moved.Binary = lockBinary(b)
		}
		lk.UpsertBinary(moved)
		old[rowName] = true
	}
	// Remove the old rows after all moves: one old row can be the row of
	// two files with the same content.
	for name := range old {
		if files.describesNoFile(name, stay) {
			lk.RemoveBinary(name)
		}
	}
	return len(old) > 0
}

// moveOldRows moves the b.lock rows that an older b wrote for the binaries
// under other names to the names of their files (moveOldLockRows), in lk
// and in b.lock. b install and b update call it before any download, while
// the files are the ones that the rows describe, and before any prune: a
// row under an old name describes no file, and a prune would remove it.
func (o *SharedOptions) moveOldRows(lk *lock.Lock, binaries []*binary.Binary) {
	if !moveOldLockRows(lk, binaries, lockFileNames(o.Config), o.lockFiles()) {
		return
	}
	if err := lock.WriteLock(o.LockDir(), lk, o.bVersion); err != nil {
		fmt.Fprintf(o.IO.ErrOut, "Warning: failed to move b.lock rows to the names of their files: %v\n", err)
	}
}

// pruneLock removes the b.lock rows that describe no file (describesNoFile):
// no b.yaml entry installs a file of that name, and the file of the row is
// not there. Such a row makes b verify fail, and no install
// writes it again: the row of an entry that b.yaml does not have any more,
// or a row of an older b under an old name that describes no file. A row of
// a file that is there stays, also without an entry: an install of a binary
// that is not in b.yaml, or an entry of another b.yaml (b --config) that
// uses the same b.lock. It returns the names of the rows that it removed.
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
	installed := lockFileNames(o.Config)
	files := o.lockFiles()
	var removed []string
	kept := lk.Binaries[:0]
	for _, e := range lk.Binaries {
		if files.describesNoFile(e.Name, installed) {
			removed = append(removed, e.Name)
			continue
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
