package atomicfile

import (
	"bufio"
	"go/build/constraint"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// buildTags is the //go:build line of the source file file, parsed.
func buildTags(t *testing.T, file string) constraint.Expr {
	t.Helper()
	f, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if constraint.IsGoBuild(sc.Text()) {
			expr, err := constraint.Parse(sc.Text())
			if err != nil {
				t.Fatalf("%s: %v", file, err)
			}
			return expr
		}
	}
	t.Fatalf("%s has no //go:build line", file)
	return nil
}

// builds reports whether a file with the tags expr builds for goos. The go
// tool also sets linux for android, darwin for ios, and solaris for illumos.
func builds(expr constraint.Expr, goos string) bool {
	implied := map[string]string{"android": "linux", "ios": "darwin", "illumos": "solaris"}[goos]
	return expr.Eval(func(tag string) bool { return tag == goos || tag == implied })
}

// The platforms of the lock are in the build tags of stale_flock.go only.
// Lockable follows them, and so do the tests, which read Lockable. The
// other files follow them too: stale_other.go and stale_windows.go cover
// each other platform, the stale_symlink_*.go files split the platforms of
// the lock, and stale_flock_test.go has the same tags as stale_flock.go. A
// file whose tags drift makes the tests of the lock skip on a platform, or
// the package not build there.
func TestLockable_MatchesTheBuildTags(t *testing.T) {
	flock := buildTags(t, "stale_flock.go")
	if want := builds(flock, runtime.GOOS); Lockable != want {
		t.Errorf("Lockable is %v on %s, but stale_flock.go builds there: %v", Lockable, runtime.GOOS, want)
	}
	flockTest := buildTags(t, "stale_flock_test.go")
	rest := []constraint.Expr{flock, buildTags(t, "stale_other.go"), buildTags(t, "stale_windows.go")}
	symlinks := []constraint.Expr{
		buildTags(t, "stale_symlink_bsd.go"),
		buildTags(t, "stale_symlink_netbsd.go"),
		buildTags(t, "stale_symlink_other.go"),
	}
	count := func(exprs []constraint.Expr, goos string) int {
		n := 0
		for _, e := range exprs {
			if builds(e, goos) {
				n++
			}
		}
		return n
	}
	for _, goos := range strings.Fields("aix android darwin dragonfly freebsd hurd illumos ios js linux netbsd openbsd plan9 solaris wasip1 windows zos") {
		locks := builds(flock, goos)
		if builds(flockTest, goos) != locks {
			t.Errorf("%s: stale_flock_test.go builds: %v, stale_flock.go: %v", goos, !locks, locks)
		}
		if n := count(rest, goos); n != 1 {
			t.Errorf("%s: %d of stale_flock.go, stale_other.go and stale_windows.go build, want 1", goos, n)
		}
		want := 0
		if locks {
			want = 1
		}
		if n := count(symlinks, goos); n != want {
			t.Errorf("%s: %d stale_symlink_*.go files build, want %d", goos, n, want)
		}
	}
}

// names are the names of the entries of dir, sorted.
func names(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	slices.Sort(out)
	return out
}

func TestIsTemp(t *testing.T) {
	for _, c := range []struct {
		name string
		want bool
	}{
		{".tool.123.tmp", true},
		{".tool.0.tmp", true},
		{".tool.4294967295.tmp", true},
		{".tool.tmp", false},      // no number
		{".tool..tmp", false},     // an empty number
		{".tool.abc.tmp", false},  // not a number
		{".tool.1.2.tmp", false},  // the temp file of "tool.1"
		{".tool.12a.tmp", false},  // not a number
		{".toolx.123.tmp", false}, // the temp file of "toolx"
		{".too.123.tmp", false},   // the temp file of "too"
		{"tool.123.tmp", false},   // no dot at the start
		{".tool.123.tmp~", false}, // another suffix
		{".tool.123.tmpx", false}, // another suffix
		{".tool.123", false},      // no suffix
		{".tool.-1.tmp", false},   // a sign is not a digit
		{".tool.123.tmp.tmp", false},
	} {
		if got := isTemp(c.name, "tool"); got != c.want {
			t.Errorf("isTemp(%q, %q) = %v, want %v", c.name, "tool", got, c.want)
		}
	}
	// A base with dots: its own temp files match, the ones of "tool" do not.
	if !isTemp(".tool.exe.42.tmp", "tool.exe") || isTemp(".tool.42.tmp", "tool.exe") {
		t.Error("isTemp with the base tool.exe")
	}
}

// The temp files that Create makes are temp files of isTemp: RemoveStale
// finds them. If os.CreateTemp changes the names it makes, this test fails.
func TestIsTemp_MatchesTheTempFilesOfCreate(t *testing.T) {
	dir := t.TempDir()
	for range 20 {
		f, err := Create(filepath.Join(dir, "tool"))
		if err != nil {
			t.Fatal(err)
		}
		name := filepath.Base(f.tmp.Name())
		f.Abort()
		if !isTemp(name, "tool") {
			t.Fatalf("the temp file %q of Create is not a temp file of isTemp", name)
		}
	}
}

// RemoveStale removes the temp files of the file that no process uses, and
// only them: the files of other names, a directory and a symlink with a
// temp file name, and the file itself stay.
func TestRemoveStale_RemovesOnlyStaleTempFilesOfTheFile(t *testing.T) {
	dir := realTempDir(t)
	path := filepath.Join(dir, "tool")
	mustWrite := func(name string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("tool")
	mustWrite(".tool.123.tmp")
	mustWrite(".tool.9.tmp")
	keep := []string{".other.123.tmp", ".tool.1.2.tmp", ".tool.abc.tmp", ".tool.tmp", ".toolx.123.tmp", "tool", "tool.123.tmp"}
	for _, name := range keep {
		if name != "tool" {
			mustWrite(name)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, ".tool.456.tmp"), 0755); err != nil {
		t.Fatal(err)
	}
	keep = append(keep, ".tool.456.tmp")
	if runtime.GOOS != "windows" {
		if err := os.Symlink(filepath.Join(dir, ".other.123.tmp"), filepath.Join(dir, ".tool.789.tmp")); err != nil {
			t.Fatal(err)
		}
		keep = append(keep, ".tool.789.tmp")
	}
	if !Lockable && runtime.GOOS != "windows" {
		t.Skip("no flock here: RemoveStale removes nothing")
	}

	removed, err := RemoveStale(path)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(removed)
	want := []string{filepath.Join(dir, ".tool.123.tmp"), filepath.Join(dir, ".tool.9.tmp")}
	if !slices.Equal(removed, want) {
		t.Errorf("removed %v, want %v", removed, want)
	}
	slices.Sort(keep)
	if got := names(t, dir); !slices.Equal(got, keep) {
		t.Errorf("left %v, want %v", got, keep)
	}
}

// A temp file that a write uses (Create holds its lock) stays, also when
// this process made it: a flock belongs to an open file.
// realTempDir is t.TempDir with its symlinks resolved. RemoveStale looks
// beside the resolved target and names the files there; on macOS the temp
// dir is under /var, a symlink to /private/var.
func realTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRemoveStale_KeepsATempFileInUse(t *testing.T) {
	if !Lockable {
		t.Skip("no flock here")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "tool")
	f, err := Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Abort()
	removed, err := RemoveStale(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) > 0 {
		t.Fatalf("removed %v, a temp file in use", removed)
	}
	if _, err := os.Stat(f.tmp.Name()); err != nil {
		t.Fatalf("the temp file in use is gone: %v", err)
	}
	// The write goes on and commits.
	if _, err := f.Write([]byte("new")); err != nil {
		t.Fatal(err)
	}
	if err := f.Commit(0755); err != nil {
		t.Fatalf("Commit after RemoveStale: %v", err)
	}
	if got := read(t, path); got != "new" {
		t.Errorf("the file is %q, want %q", got, "new")
	}
}

// For a symlink, Create puts the temp file beside the target, named after
// the target. RemoveStale looks there.
func TestRemoveStale_ASymlinkLooksBesideTheTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	if !Lockable {
		t.Skip("no flock here")
	}
	real, links := realTempDir(t), realTempDir(t)
	target := filepath.Join(real, "tool-1.0")
	if err := os.WriteFile(target, []byte("old"), 0755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(links, "tool")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(real, ".tool-1.0.5.tmp")
	other := filepath.Join(links, ".tool.5.tmp") // not beside the target
	for _, p := range []string{stale, other} {
		if err := os.WriteFile(p, []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := RemoveStale(link)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(removed, []string{stale}) {
		t.Errorf("removed %v, want [%s]", removed, stale)
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("%s is gone: %v", other, err)
	}
}

// A missing directory and a symlink that does not resolve are no error:
// Create cannot have made a temp file there.
func TestRemoveStale_NothingToDo(t *testing.T) {
	dir := t.TempDir()
	if removed, err := RemoveStale(filepath.Join(dir, "missing", "tool")); err != nil || len(removed) > 0 {
		t.Errorf("a missing directory: %v, %v", removed, err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	link := filepath.Join(dir, "tool")
	if err := os.Symlink(filepath.Join(dir, "missing"), link); err != nil {
		t.Fatal(err)
	}
	if removed, err := RemoveStale(link); err != nil || len(removed) > 0 {
		t.Errorf("a dangling symlink: %v, %v", removed, err)
	}
}

// takeTemps makes createTemp remove the first n temp files that it makes,
// as a cleanup of another process does that finds a temp file before
// Create locks it. It counts the calls.
func takeTemps(t *testing.T, n int) *int {
	t.Helper()
	calls := 0
	orig := createTemp
	t.Cleanup(func() { createTemp = orig })
	createTemp = func(dir, pattern string) (*os.File, error) {
		calls++
		f, err := orig(dir, pattern)
		if err == nil && calls <= n {
			if err := os.Remove(f.Name()); err != nil {
				t.Fatal(err)
			}
		}
		return f, err
	}
	return &calls
}

// When a cleanup took the new temp file, Create makes another one, and the
// write commits.
func TestCreate_MakesANewTempFileWhenACleanupTookIt(t *testing.T) {
	if !Lockable {
		t.Skip("no flock here: Create cannot see a cleanup")
	}
	calls := takeTemps(t, 1)
	dir := t.TempDir()
	path := filepath.Join(dir, "tool")
	if err := Write(path, 0755, writeString("new")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if *calls != 2 {
		t.Errorf("createTemp ran %d times, want 2", *calls)
	}
	if got := read(t, path); got != "new" {
		t.Errorf("the file is %q, want %q", got, "new")
	}
	noTemp(t, dir)
}

// When a cleanup takes each temp file, Create fails after createTries.
func TestCreate_FailsWhenACleanupTakesEachTempFile(t *testing.T) {
	if !Lockable {
		t.Skip("no flock here: Create cannot see a cleanup")
	}
	calls := takeTemps(t, createTries)
	dir := t.TempDir()
	if f, err := Create(filepath.Join(dir, "tool")); err == nil {
		f.Abort()
		t.Fatal("Create: no error, want one")
	}
	if *calls != createTries {
		t.Errorf("createTemp ran %d times, want %d", *calls, createTries)
	}
	noTemp(t, dir)
}
