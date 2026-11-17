package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// sameFileFixture is a project for the duplicate check of b.yaml: b.yaml in
// .bin (the binary directory), the directory tools, and, where the system
// allows symlinks:
//   - tools-link, a symlink to tools;
//   - tools/t2, a symlink to the file .bin/t2, which is there;
//   - tools/ta, a relative symlink to .bin/ta, which is not there;
//   - tools/loop, a symlink to itself, and tools/loop-a and tools/loop-b,
//     two symlinks to each other.
type sameFileFixture struct {
	project, binDir, config string
	realProject             string // project with its symlinks resolved
	links                   bool   // the symlinks are there
}

func newSameFileFixture(t *testing.T, project, binDir string) *sameFileFixture {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatal(err)
	}
	f := &sameFileFixture{project: project, binDir: binDir, config: filepath.Join(binDir, "b.yaml"), realProject: resolved}
	tools := filepath.Join(project, "tools")
	mustMkdir(t, tools)
	mustWriteExec(t, filepath.Join(binDir, "t2"), []byte("t2\n"))
	f.links = true
	for link, target := range map[string]string{
		filepath.Join(project, "tools-link"): tools,
		filepath.Join(tools, "t2"):           filepath.Join(binDir, "t2"),
		filepath.Join(tools, "ta"):           filepath.Join("..", ".bin", "ta"),
		filepath.Join(tools, "loop"):         "loop",
		filepath.Join(tools, "loop-a"):       "loop-b",
		filepath.Join(tools, "loop-b"):       "loop-a",
	} {
		if os.Symlink(target, link) != nil {
			f.links = false
		}
	}
	return f
}

// abs is the absolute path of the file rel in the project, with "/" as
// separator, as b.yaml can write it.
func (f *sameFileFixture) abs(rel string) string {
	return filepath.ToSlash(filepath.Join(f.project, filepath.FromSlash(rel)))
}

// resolved is the path of the file rel in the project with its symlinks
// resolved, as the error names a file that b.yaml writes in two forms.
func (f *sameFileFixture) resolved(rel string) string {
	return filepath.Join(f.realProject, filepath.FromSlash(rel))
}

// sameFileCases are b.yaml files (in .bin) and the error of the duplicate
// check: the entries that install one file, and the file. When the entries
// write the file in one form, the error names that form. When they write it
// in two forms, it names the file with its symlinks resolved and both
// forms. link is true for a case that needs the symlinks.
func sameFileCases(f *sameFileFixture) []struct {
	name, config, want string
	link               bool
} {
	return []struct {
		name, config, want string
		link               bool
	}{{
		name:   "a relative file and its absolute path",
		config: "binaries:\n  fakerel://a/tool:\n    file: ../tools/x\n  fakerel://b/tool:\n    file: " + f.abs("tools/x") + "\n",
		want:   "b.yaml: the entries fakerel://a/tool and fakerel://b/tool install the same file " + f.resolved("tools/x") + ", written as ../tools/x and " + f.abs("tools/x"),
	}, {
		name:   "an untidy relative file and its absolute path",
		config: "binaries:\n  fakerel://a/tool:\n    file: " + f.abs("tools/x") + "\n  fakerel://b/tool:\n    file: ./../tools/./x\n",
		want:   "b.yaml: the entries fakerel://a/tool and fakerel://b/tool install the same file " + f.resolved("tools/x") + ", written as ../tools/x and " + f.abs("tools/x"),
	}, {
		name:   "an alias entry and the absolute path of its file",
		config: "binaries:\n  t2:\n    alias: tool\n  fakerel://x/y:\n    file: " + f.abs(".bin/t2") + "\n",
		want:   "b.yaml: the entries fakerel://x/y and t2 install the same file t2\n",
	}, {
		name:   "an alias entry and a symlink to its file",
		config: "binaries:\n  t2:\n    alias: tool\n  fakerel://x/y:\n    file: ../tools/t2\n",
		want:   "b.yaml: the entries fakerel://x/y and t2 install the same file " + f.resolved(".bin/t2") + ", written as ../tools/t2 and t2",
		link:   true,
	}, {
		name:   "a file through a symlinked directory",
		config: "binaries:\n  fakerel://a/tool:\n    file: ../tools-link/x\n  fakerel://b/tool:\n    file: ../tools/x\n",
		want:   "b.yaml: the entries fakerel://a/tool and fakerel://b/tool install the same file " + f.resolved("tools/x") + ", written as ../tools-link/x and ../tools/x",
		link:   true,
	}, {
		name:   "an alias entry and a dangling symlink to its file",
		config: "binaries:\n  ta:\n    alias: tool\n  fakerel://x/y:\n    file: ../tools/ta\n",
		want:   "b.yaml: the entries fakerel://x/y and ta install the same file " + f.resolved(".bin/ta") + ", written as ../tools/ta and ta",
		link:   true,
	}, {
		name:   "two symlink loops",
		config: "binaries:\n  fakerel://a/tool:\n    file: ../tools/loop\n  fakerel://b/tool:\n    file: ../tools/loop-a\n  fakerel://c/tool:\n    file: ../tools/loop-b\n",
		link:   true,
	}, {
		name: "two groups of entries",
		config: "binaries:\n  t2:\n    alias: tool\n  fakerel://x/y:\n    file: " + f.abs(".bin/t2") + "\n" +
			"  fakerel://a/tool:\n    file: ../tools/x\n  fakerel://b/tool:\n    file: " + f.abs("tools/x") + "\n",
		want: "b.yaml: the entries fakerel://a/tool and fakerel://b/tool install the same file " + f.resolved("tools/x") + ", written as ../tools/x and " + f.abs("tools/x") + "\n" +
			"b.yaml: the entries fakerel://x/y and t2 install the same file t2\n",
	}, {
		name:   "two files of one name in two directories",
		config: "binaries:\n  fakerel://a/tool:\n    file: ../a/x\n  fakerel://b/tool:\n    file: " + f.abs("b/x") + "\n",
	}}
}

// TestCheckConfigFiles_ResolvedPath: two entries install the same file
// when the paths of their files are the same, absolute and clean, with
// their symlinks resolved, as the install writes them: also when b.yaml
// writes the file in two forms (relative and absolute, untidy, through a
// symlink, also one whose target is not there yet). A symlink loop ends.
// The error is the same in both orders of the entries, and in each run:
// the check reads a map, so each order runs 20 times when there is an
// error.
func TestCheckConfigFiles_ResolvedPath(t *testing.T) {
	project := t.TempDir()
	binDir := filepath.Join(project, ".bin")
	mustMkdir(t, binDir)
	t.Setenv("PATH_BIN", binDir)
	t.Setenv("PATH_BASE", project)
	f := newSameFileFixture(t, project, binDir)
	for _, tc := range sameFileCases(f) {
		t.Run(tc.name, func(t *testing.T) {
			if tc.link && !f.links {
				t.Skip("no symlinks on this system")
			}
			mustWrite(t, f.config, []byte(tc.config))
			shared := NewSharedOptions(mkIO(), nil)
			shared.ConfigPath = f.config
			if err := shared.LoadConfig(); err != nil {
				t.Fatal(err)
			}
			// The order of the lines of the error is the one to pin: run each
			// order 20 times when there is an error.
			runs := 20
			if tc.want == "" {
				runs = 1
			}
			first := ""
			for _, order := range []string{"as loaded", "reversed"} {
				for range runs {
					err := checkConfigFiles(shared.Config)
					switch {
					case tc.want == "" && err != nil:
						t.Fatalf("%s: %v, want no error", order, err)
					case tc.want == "":
						continue
					case err == nil || !strings.Contains(err.Error()+"\n", tc.want):
						t.Fatalf("%s: %v, want %q", order, err, tc.want)
					case first == "":
						first = err.Error()
					case err.Error() != first:
						t.Fatalf("%s: the error changes from run to run:\n%s\n%s", order, first, err)
					}
				}
				slices.Reverse(shared.Config.Binaries)
			}
		})
	}
}

// TestInstall_RefusesOneFileInTwoForms: `b install` and `b update` refuse a
// b.yaml in which two entries write one file in two forms, in both orders
// in b.yaml, before any download. `b install --add` of one of the entries
// refuses it too, and b.yaml does not change. The b.lock rows keep the
// form that b.yaml writes: an accepted b.yaml with a relative file has its
// row under that relative path.
func TestInstall_RefusesOneFileInTwoForms(t *testing.T) {
	for _, relFirst := range []bool{true, false} {
		f := newPinsFixture(t)
		f.serveRefs(t)
		sf := newSameFileFixture(t, f.dir, f.binDir)
		rel, abs := "  fakerel://a/tool:\n    file: ../tools/x\n", "  fakerel://b/tool:\n    file: "+sf.abs("tools/x")+"\n"
		config := "binaries:\n" + rel + abs
		if !relFirst {
			config = "binaries:\n" + abs + rel
		}
		mustWrite(t, f.config, []byte(config))
		want := "b.yaml: the entries fakerel://a/tool and fakerel://b/tool install the same file " + sf.resolved("tools/x") + ", written as ../tools/x and " + sf.abs("tools/x")
		for _, run := range []struct {
			cmd  string
			args []string
		}{{"install", nil}, {"install", []string{"other"}}, {"update", nil}, {"update", []string{"other"}}} {
			err := f.cmdErr(t, run.cmd, func(*UpdateOptions) {}, run.args...)
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("relative first %v: b %s %v: error %v, want %q", relFirst, run.cmd, run.args, err, want)
			}
		}
		err := f.installErr(t, func(o *InstallOptions) { o.Add = true }, "fakerel://a/tool@v3.0.0")
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("relative first %v: b install --add fakerel://a/tool@v3.0.0: error %v, want %q", relFirst, err, want)
		}
		if n := f.hits.Load(); n != 0 {
			t.Errorf("relative first %v: %d downloads, want 0", relFirst, n)
		}
		if data, err := os.ReadFile(f.config); err != nil || string(data) != config {
			t.Errorf("relative first %v: b.yaml: %q (%v), want it unchanged", relFirst, data, err)
		}
	}

	// One entry of the file: the row has the form that b.yaml writes.
	f := newPinsFixture(t)
	f.serveRefs(t)
	mustWrite(t, f.config, []byte("binaries:\n  fakerel://a/tool:\n    version: v1.0.0\n    file: ../tools/x\n"))
	f.install(t, false)
	if e := f.readLock(t).FindBinary("../tools/x"); e == nil || e.Version != "v1.0.0" {
		t.Errorf("b.lock row ../tools/x: %+v, want version v1.0.0", e)
	}
}

// TestInstall_AddRefusesASymlinkToTheFileOfAnEntry: b.yaml has an entry
// with `file: ../tools/tb`, a symlink to .bin/tb. `b install --add --alias
// tb <ref>` adds an entry that writes .bin/tb, the file of that entry. It
// is refused before any download, and b.yaml does not change.
func TestInstall_AddRefusesASymlinkToTheFileOfAnEntry(t *testing.T) {
	f := newPinsFixture(t)
	f.serveRefs(t)
	mustMkdir(t, filepath.Join(f.dir, "tools"))
	mustWriteExec(t, filepath.Join(f.binDir, "tb"), []byte("tb\n"))
	if err := os.Symlink(filepath.Join(f.binDir, "tb"), filepath.Join(f.dir, "tools", "tb")); err != nil {
		t.Skipf("no symlinks on this system: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(f.binDir, "tb"))
	if err != nil {
		t.Fatal(err)
	}
	config := "binaries:\n  fakerel://a/z:\n    file: ../tools/tb\n"
	mustWrite(t, f.config, []byte(config))
	err = f.installErr(t, func(o *InstallOptions) { o.Add = true; o.Alias = "tb" }, "fakerel://b/tool@v3.0.0")
	want := "b.yaml: the entries fakerel://a/z and fakerel://b/tool install the same file " + resolved + ", written as ../tools/tb and tb"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("b install --add --alias tb fakerel://b/tool@v3.0.0: error %v, want %q", err, want)
	}
	if n := f.hits.Load(); n != 0 {
		t.Errorf("%d downloads, want 0", n)
	}
	if data, err := os.ReadFile(f.config); err != nil || string(data) != config {
		t.Errorf("b.yaml: %q (%v), want it unchanged", data, err)
	}
}

// TestInstall_RefusesADanglingSymlinkToTheFileOfAnEntry: the entry
// fakerel://a/tool writes .bin/ta, which is not there yet. The `file:` of
// fakerel://b/tool is tools/tb, a relative symlink to .bin/ta. The install
// of a would make .bin/ta, and the install of b would then write over it
// through the link. The first `b install` and `b update` refuse b.yaml
// before any download, and .bin/ta stays absent.
func TestInstall_RefusesADanglingSymlinkToTheFileOfAnEntry(t *testing.T) {
	f := newPinsFixture(t)
	f.serveRefs(t)
	mustMkdir(t, filepath.Join(f.dir, "tools"))
	if err := os.Symlink(filepath.Join("..", ".bin", "ta"), filepath.Join(f.dir, "tools", "tb")); err != nil {
		t.Skipf("no symlinks on this system: %v", err)
	}
	realBin, err := filepath.EvalSymlinks(f.binDir)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, f.config, []byte("binaries:\n  fakerel://a/tool:\n    alias: ta\n  fakerel://b/tool:\n    file: ../tools/tb\n"))
	want := "b.yaml: the entries fakerel://a/tool and fakerel://b/tool install the same file " + filepath.Join(realBin, "ta") + ", written as ../tools/tb and ta"
	for _, cmd := range []string{"install", "update"} {
		err := f.cmdErr(t, cmd, func(*UpdateOptions) {})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("b %s: error %v, want %q", cmd, err, want)
		}
	}
	if n := f.hits.Load(); n != 0 {
		t.Errorf("%d downloads, want 0", n)
	}
	if _, err := os.Lstat(filepath.Join(f.binDir, "ta")); !os.IsNotExist(err) {
		t.Errorf(".bin/ta: %v, want no file", err)
	}
}

// TestRealDir_DanglingSymlinks: realDir follows a symlink whose target is
// not there: a chain of relative links, an absolute link, a link with ".."
// in its target, a link in the middle of the path, and a link in a
// symlinked directory (its relative target is relative to the resolved
// directory). It follows a chain of 40 links (maxLinkHops), and stops at
// the 41st: it returns the path of that link. It ends on a symlink loop.
// A ".." after a symlink in the target is cleaned as text (a limit of
// realDir): the case pins that result.
func TestRealDir_DanglingSymlinks(t *testing.T) {
	dir := t.TempDir()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"sub", "far/real-dir", "deep/sub"} {
		mustMkdir(t, filepath.Join(dir, filepath.FromSlash(d)))
	}
	links := map[string]string{
		"a":               "b",
		"b":               filepath.Join("missing", "c"),
		"abs":             filepath.Join(dir, "elsewhere", "f"),
		"sub/up":          filepath.Join("..", "up-target"),
		"dir-link":        filepath.Join("far", "real-dir"),
		"far/real-dir/dl": filepath.Join("..", "dl-target"),
		"x":               filepath.Join("deep", "sub"),
		"text-dots":       filepath.Join("x", "..", "y"),
		"loop":            "loop",
		"loop-a":          "loop-b",
		"loop-b":          "loop-a",
		"to-loop":         "loop-a",
	}
	// c1 -> c2 -> ... -> c40 -> c-end: 40 links. d1 -> ... -> d41 -> d-end:
	// 41 links.
	for i := 1; i <= 40; i++ {
		links[fmt.Sprintf("c%d", i)] = fmt.Sprintf("c%d", i+1)
	}
	links["c40"] = "c-end"
	for i := 1; i <= 41; i++ {
		links[fmt.Sprintf("d%d", i)] = fmt.Sprintf("d%d", i+1)
	}
	links["d41"] = "d-end"
	for link, target := range links {
		if err := os.Symlink(target, filepath.Join(dir, filepath.FromSlash(link))); err != nil {
			t.Skipf("no symlinks on this system: %v", err)
		}
	}
	for _, tc := range []struct {
		path string
		want []string
	}{
		{"a", []string{filepath.Join(resolved, "missing", "c")}},
		{"a/x", []string{filepath.Join(resolved, "missing", "c", "x")}},
		{"abs", []string{filepath.Join(resolved, "elsewhere", "f")}},
		{"sub/up", []string{filepath.Join(resolved, "up-target")}},
		{"dir-link/dl", []string{filepath.Join(resolved, "far", "dl-target")}},
		{"none/x", []string{filepath.Join(resolved, "none", "x")}},
		// The system resolves x/../y to deep/y; realDir gives y.
		{"text-dots", []string{filepath.Join(resolved, "y")}},
		{"c1", []string{filepath.Join(resolved, "c-end")}},
		{"d1", []string{filepath.Join(resolved, "d41")}},
		{"loop", []string{filepath.Join(resolved, "loop")}},
		{"loop-a", []string{filepath.Join(resolved, "loop-a"), filepath.Join(resolved, "loop-b")}},
		{"to-loop/x", []string{filepath.Join(resolved, "loop-a", "x"), filepath.Join(resolved, "loop-b", "x")}},
	} {
		done := make(chan string, 1)
		go func() { done <- realDir(filepath.Join(dir, filepath.FromSlash(tc.path))) }()
		select {
		case got := <-done:
			if !slices.Contains(tc.want, got) {
				t.Errorf("realDir(%s) = %s, want one of %v", tc.path, got, tc.want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("realDir(%s) did not return in 5 seconds", tc.path)
		}
	}
}

// TestEntryPath_IsTheBinaryPath: the path of the file of each b.yaml entry
// (entryPath) is the file that the install writes (BinaryPath of the
// binary of the entry), with its symlinks resolved: for a preset, an alias
// entry of a preset, a provider ref with an alias and a version, a provider
// ref with a version, and entries with `file:`. The test also checks the
// expected path, so the two cannot be wrong in the same way.
func TestEntryPath_IsTheBinaryPath(t *testing.T) {
	f := newPinsFixture(t)
	project, err := filepath.EvalSymlinks(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, f.config, []byte("binaries:\n  tool: {}\n  t2:\n    alias: other\n"+
		"  fakerel://org/name@v1.0.0:\n    alias: na\n  fakerel://org/plain@v1.0.0: {}\n"+
		"  fakerel://org/other:\n    file: ../tools/x\n  other:\n    file: ./o\n"))
	want := map[string]string{
		"tool":                       ".bin/tool",
		"t2":                         ".bin/t2",
		"fakerel://org/name@v1.0.0":  ".bin/na",
		"fakerel://org/plain@v1.0.0": ".bin/plain",
		"fakerel://org/other":        "tools/x",
		"other":                      ".bin/o",
	}
	shared := NewSharedOptions(mkIO(), f.presets())
	shared.ConfigPath = f.config
	if err := shared.LoadConfig(); err != nil {
		t.Fatal(err)
	}
	bins := shared.GetBinariesFromConfig()
	if len(bins) != len(want) || len(shared.Config.Binaries) != len(want) {
		t.Fatalf("%d binaries of %d entries, want %d", len(bins), len(shared.Config.Binaries), len(want))
	}
	for i, e := range shared.Config.Binaries {
		got, file := entryPath(e), realDir(bins[i].BinaryPath())
		if got != file {
			t.Errorf("%s: entryPath %s, the install writes %s", e.Name, got, file)
		}
		if w := filepath.Join(project, filepath.FromSlash(want[e.Name])); got != w {
			t.Errorf("%s: entryPath %s, want %s", e.Name, got, w)
		}
	}
}
