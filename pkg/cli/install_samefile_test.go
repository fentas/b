package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// sameFileFixture is a project for the duplicate check of b.yaml: b.yaml in
// .bin (the binary directory), the directory tools, and, where the system
// allows symlinks, tools-link (a symlink to tools) and tools/t2 (a symlink
// to the file .bin/t2, which is there).
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
	mustMkdir(t, filepath.Join(project, "tools"))
	mustWriteExec(t, filepath.Join(binDir, "t2"), []byte("t2\n"))
	f.links = os.Symlink(filepath.Join(project, "tools"), filepath.Join(project, "tools-link")) == nil &&
		os.Symlink(filepath.Join(binDir, "t2"), filepath.Join(project, "tools", "t2")) == nil
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
		name:   "two files of one name in two directories",
		config: "binaries:\n  fakerel://a/tool:\n    file: ../a/x\n  fakerel://b/tool:\n    file: " + f.abs("b/x") + "\n",
	}}
}

// TestCheckConfigFiles_ResolvedPath: two entries install the same file
// when the paths of their files are the same, absolute and clean, with
// their symlinks resolved, as the install writes them: also when b.yaml
// writes the file in two forms (relative and absolute, untidy, through a
// symlink). The error is the same in both orders of the entries.
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
			var errs []string
			for _, order := range []string{"as loaded", "reversed"} {
				err := checkConfigFiles(shared.Config)
				switch {
				case tc.want == "" && err != nil:
					t.Errorf("%s: %v, want no error", order, err)
				case tc.want != "" && (err == nil || !strings.Contains(err.Error()+"\n", tc.want)):
					t.Errorf("%s: %v, want %q", order, err, tc.want)
				}
				if err != nil {
					errs = append(errs, err.Error())
				}
				slices.Reverse(shared.Config.Binaries)
			}
			if len(errs) == 2 && errs[0] != errs[1] {
				t.Errorf("the error depends on the order of the entries:\n%s\n%s", errs[0], errs[1])
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
