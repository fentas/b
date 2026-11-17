package cli

import (
	"archive/tar"
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/google/go-containerregistry/pkg/v1/types"

	"github.com/fentas/b/pkg/lock"
)

// The go, git, docker and oci providers install the file themselves. They
// must write the file that b.yaml and b.lock name: the alias or the
// `file:` of the entry, as the release providers do.

// fakeGoCmd is a go command for tests: "go install <module>@<version>"
// writes $GOBIN/<name> with "go <module>@<version>". The name is the last
// element of the module path, or the element before a major version
// suffix, as the go command names a binary.
const fakeGoCmd = `#!/bin/sh
[ "$1" = install ] || exit 2
m=${2%@*}
n=${m##*/}
case $n in v1|v0*|v*[!0-9]*) ;; v?*) m=${m%/*}; n=${m##*/} ;; esac
printf 'go %s\n' "$2" > "$GOBIN/$n"
`

// fakeDockerCmd is a container runtime for tests. "create <image>" prints
// the container ID cid-<image>. "cp [-L] <id>:/usr/local/bin/tool <file>"
// writes <file> with "<id>:/usr/local/bin/tool"; cp of another path fails.
// pull and rm do nothing.
const fakeDockerCmd = `#!/bin/sh
case "$1" in
pull|rm) exit 0 ;;
create) printf 'cid-%s\n' "$2"; exit 0 ;;
cp)
	[ "$2" = -L ] && shift
	case "$2" in
	*:/usr/local/bin/tool) printf '%s\n' "$2" > "$3"; exit 0 ;;
	esac
	exit 1 ;;
esac
exit 2
`

// fakeCommands makes PATH the directory of the given fake commands, and
// nothing else: the test runs no real go command or container runtime.
func fakeCommands(t *testing.T, cmds map[string]string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake commands are shell scripts")
	}
	dir := t.TempDir()
	for name, script := range cmds {
		mustWriteExec(t, filepath.Join(dir, name), []byte(script))
	}
	t.Setenv("PATH", dir)
}

// toolRepo is a git repository with the file tool, which has content.
func toolRepo(t *testing.T, content string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	mustWriteExec(t, filepath.Join(repo, "tool"), []byte(content))
	run("add", "tool")
	run("-c", "user.name=T", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "tool")
	return repo
}

// ociRegistry is an OCI registry in memory, on a local port. It returns
// the host and port. The docker configuration of the test is empty, so
// the keychain finds no credentials.
func ociRegistry(t *testing.T) string {
	t.Helper()
	t.Setenv("DOCKER_CONFIG", t.TempDir())
	srv := httptest.NewServer(registry.New(registry.Logger(log.New(io.Discard, "", 0))))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

// pushTool pushes an image for this platform with the file
// /usr/local/bin/tool, which has content, to ref.
func pushTool(t *testing.T, ref, content string) {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{Name: "usr/local/bin/tool", Typeflag: tar.TypeReg, Mode: 0755, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(tw, content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	layer, err := tarball.LayerFromOpener(
		func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(buf.Bytes())), nil },
		tarball.WithMediaType(types.DockerLayer),
	)
	if err != nil {
		t.Fatal(err)
	}
	base, err := mutate.ConfigFile(empty.Image, &v1.ConfigFile{OS: runtime.GOOS, Architecture: runtime.GOARCH})
	if err != nil {
		t.Fatal(err)
	}
	img, err := mutate.AppendLayers(base, layer)
	if err != nil {
		t.Fatal(err)
	}
	r, err := name.ParseReference(ref)
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.Write(r, img); err != nil {
		t.Fatalf("push %s: %v", ref, err)
	}
}

// fileProvider is a provider that installs the file itself. setup gives
// two refs of the binary tool, their version ("" for none), and the
// content of the file that each one installs.
type fileProvider struct {
	name  string
	setup func(t *testing.T) (refs, want [2]string, version string)
}

func fileProviders() []fileProvider {
	return []fileProvider{{
		name: "go",
		setup: func(t *testing.T) ([2]string, [2]string, string) {
			fakeCommands(t, map[string]string{"go": fakeGoCmd})
			return [2]string{"go://example.com/a/tool", "go://example.com/b/tool"},
				[2]string{"go example.com/a/tool@v1.0.0\n", "go example.com/b/tool@v1.0.0\n"}, "v1.0.0"
		},
	}, {
		name: "git",
		setup: func(t *testing.T) ([2]string, [2]string, string) {
			a, b := toolRepo(t, "git a\n"), toolRepo(t, "git b\n")
			return [2]string{"git://" + a + ":tool", "git://" + b + ":tool"}, [2]string{"git a\n", "git b\n"}, ""
		},
	}, {
		name: "docker",
		setup: func(t *testing.T) ([2]string, [2]string, string) {
			// The registry answers the digest requests of b install.
			host := ociRegistry(t)
			fakeCommands(t, map[string]string{"docker": fakeDockerCmd})
			return [2]string{"docker://" + host + "/a/tool", "docker://" + host + "/b/tool"},
				[2]string{"cid-" + host + "/a/tool:v1:/usr/local/bin/tool\n", "cid-" + host + "/b/tool:v1:/usr/local/bin/tool\n"}, "v1"
		},
	}, {
		name: "oci",
		setup: func(t *testing.T) ([2]string, [2]string, string) {
			host := ociRegistry(t)
			pushTool(t, host+"/a/tool:v1", "oci a\n")
			pushTool(t, host+"/b/tool:v1", "oci b\n")
			return [2]string{"oci://" + host + "/a/tool", "oci://" + host + "/b/tool"}, [2]string{"oci a\n", "oci b\n"}, "v1"
		},
	}}
}

// checkEntryFile checks that the file of the b.lock row name is file, with
// the content want (when want is not ""), and that the row has the ref, the
// binary tool and the sha256 of the file.
func checkEntryFile(t *testing.T, lk *lock.Lock, name, file, ref, want string) {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Errorf("the file of %s: %v", ref, err)
		return
	}
	if want != "" && string(data) != want {
		t.Errorf("%s is %q, want %q", file, data, want)
	}
	hash, err := lock.SHA256File(file)
	if err != nil {
		t.Fatal(err)
	}
	if e := lk.FindBinary(name); e == nil || e.Source != ref || e.Binary != "tool" || e.SHA256 != hash {
		t.Errorf("b.lock row %q: %+v, want the source %s, the binary tool and the sha256 of %s", name, e, ref, file)
	}
}

// TestInstall_FileProvidersWriteTheFileOfTheEntry: for each provider that
// installs the file itself, b.yaml has two entries of the binary tool, one
// with `alias: ta` and one with `file: ../tools/tool`. b install writes
// .bin/ta and tools/tool, each with the binary of its own entry, and no
// .bin/tool. b.lock has the rows ta and ../tools/tool, and b verify passes.
// b update keeps the same files and rows.
func TestInstall_FileProvidersWriteTheFileOfTheEntry(t *testing.T) {
	for _, p := range fileProviders() {
		t.Run(p.name, func(t *testing.T) {
			f := newPinsFixture(t)
			refs, want, version := p.setup(t)
			ver := ""
			if version != "" {
				ver = "\n    version: " + version
			}
			mustWrite(t, f.config, []byte(fmt.Sprintf("binaries:\n  %q:\n    alias: ta%s\n  %q:\n    file: ../tools/tool%s\n", refs[0], ver, refs[1], ver)))
			for _, cmd := range []string{"install", "update"} {
				if out, err := f.runB(t, f.presets(), cmd); err != nil {
					t.Fatalf("b %s: %v\n%s", cmd, err, out)
				}
				lk := f.readLock(t)
				checkEntryFile(t, lk, "ta", filepath.Join(f.binDir, "ta"), refs[0], want[0])
				checkEntryFile(t, lk, "../tools/tool", filepath.Join(f.dir, "tools", "tool"), refs[1], want[1])
				if _, err := os.Lstat(filepath.Join(f.binDir, "tool")); !os.IsNotExist(err) {
					t.Errorf("b %s wrote .bin/tool (%v): no entry names that file", cmd, err)
				}
				if e := lk.FindBinary("tool"); e != nil {
					t.Errorf("b %s: b.lock row tool: %+v, want none", cmd, e)
				}
				if out, err := f.runB(t, f.presets(), "verify"); err != nil {
					t.Errorf("b verify after b %s: %v\n%s", cmd, err, out)
				}
			}
		})
	}
}

// TestInstall_AliasFlagNamesTheFileOfAGoRef: `b install --alias ga
// <go ref>` writes .bin/ga, and b.lock has the row ga.
func TestInstall_AliasFlagNamesTheFileOfAGoRef(t *testing.T) {
	f := newPinsFixture(t)
	fakeCommands(t, map[string]string{"go": fakeGoCmd})
	ref := "go://example.com/a/tool"
	if out, err := f.runB(t, f.presets(), "install", "--alias", "ga", ref+"@v1.0.0"); err != nil {
		t.Fatalf("b install --alias ga: %v\n%s", err, out)
	}
	checkEntryFile(t, f.readLock(t), "ga", filepath.Join(f.binDir, "ga"), ref, "go example.com/a/tool@v1.0.0\n")
	if _, err := os.Lstat(filepath.Join(f.binDir, "tool")); !os.IsNotExist(err) {
		t.Errorf("b install --alias ga wrote .bin/tool (%v)", err)
	}
}

// TestInstall_FileProvidersRefuseTwoEntriesOfOneFile: two entries whose
// providers write one file are refused before any install. The go command
// names the binary of example.com/a/tool/v2 tool, so it and
// example.com/b/tool write .bin/tool. A git ref with `alias: tool` and an
// oci ref of the binary tool also write .bin/tool.
func TestInstall_FileProvidersRefuseTwoEntriesOfOneFile(t *testing.T) {
	for _, tc := range []struct{ name, config, want string }{{
		name:   "go module with a major version suffix",
		config: "binaries:\n  go://example.com/a/tool/v2: {}\n  go://example.com/b/tool: {}\n",
		want:   "b.yaml: the entries go://example.com/a/tool/v2 and go://example.com/b/tool install the same file tool",
	}, {
		name:   "git alias and oci ref",
		config: "binaries:\n  \"git:///nowhere/repo:other\":\n    alias: tool\n  \"oci://127.0.0.1:1/org/tool\": {}\n",
		want:   "b.yaml: the entries git:///nowhere/repo:other and oci://127.0.0.1:1/org/tool install the same file tool",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPinsFixture(t)
			fakeCommands(t, map[string]string{"go": fakeGoCmd})
			mustWrite(t, f.config, []byte(tc.config))
			for _, cmd := range []string{"install", "update"} {
				_, err := f.runB(t, f.presets(), cmd)
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Errorf("b %s: error %v, want %q", cmd, err, tc.want)
				}
				if _, err := os.Lstat(filepath.Join(f.binDir, "tool")); !os.IsNotExist(err) {
					t.Errorf("b %s wrote .bin/tool (%v), want no install", cmd, err)
				}
			}
		})
	}
}
