package provider

import (
	"archive/tar"
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
	ociregistry "github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// The go, git, docker and oci providers write the file that the caller
// gives (dest): the file of the b.yaml entry, under its alias or its
// `file:`. They do not write a file named after the binary.

// fakeGo is a go command for tests: "go install <module>@<version>" writes
// $GOBIN/<name> with "go <module>@<version>". The name is the last element
// of the module path, or the element before a major version suffix, as
// the go command names a binary.
const fakeGo = `#!/bin/sh
[ "$1" = install ] || exit 2
m=${2%@*}
n=${m##*/}
case $n in v1|v0*|v*[!0-9]*) ;; v?*) m=${m%/*}; n=${m##*/} ;; esac
printf 'go %s\n' "$2" > "$GOBIN/$n"
`

// fakeDocker is a container runtime for tests. "create <image>" prints the
// container ID cid-<image>. "cp [-L] <id>:/usr/local/bin/tool <file>" writes
// <file> with "<id>:/usr/local/bin/tool"; cp of another path fails. pull
// and rm do nothing.
const fakeDocker = `#!/bin/sh
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

// fakeTools makes PATH the directory of the given fake commands, and
// nothing else: the test runs no real go command or container runtime.
func fakeTools(t *testing.T, tools map[string]string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake commands are shell scripts")
	}
	dir := t.TempDir()
	for name, script := range tools {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
}

// checkOnlyFile checks that dest has want, and that the file of the
// binary name beside it is not there.
func checkOnlyFile(t *testing.T, dest, want, binName string) {
	t.Helper()
	if got, err := os.ReadFile(dest); err != nil || string(got) != want {
		t.Errorf("%s is %q (%v), want %q", dest, got, err, want)
	}
	if other := filepath.Join(filepath.Dir(dest), binName); other != dest {
		if _, err := os.Lstat(other); !os.IsNotExist(err) {
			t.Errorf("the install wrote %s (%v), want only %s", other, err, dest)
		}
	}
}

// TestGoInstall_WritesTheFileItIsGiven: go install compiles tool, and the
// provider writes it to the file of the entry: an alias in the binary
// directory, or a `file:` in a directory that is not there yet.
func TestGoInstall_WritesTheFileItIsGiven(t *testing.T) {
	fakeTools(t, map[string]string{"go": fakeGo})
	dir := t.TempDir()
	for _, dest := range []string{filepath.Join(dir, "ta"), filepath.Join(dir, "tools", "sub", "x")} {
		if err := (&GoInstall{}).Install("go://example.com/org/tool", "v1.0.0", dest); err != nil {
			t.Fatalf("Install to %s: %v", dest, err)
		}
		checkOnlyFile(t, dest, "go example.com/org/tool@v1.0.0\n", "tool")
	}
}

// fakeGoTwoFiles is fakeGo that also writes $GOBIN/other. With two files
// in GOBIN, the provider finds the binary only by its name (BinaryName),
// not as the only file in GOBIN.
const fakeGoTwoFiles = fakeGo + `printf 'other\n' > "$GOBIN/other"
`

// TestGoInstall_MajorVersionModule: go install of example.com/tool/v2
// writes the binary tool, and BinaryName names it tool. The provider
// writes it to the file it is given, also when GOBIN has another file.
func TestGoInstall_MajorVersionModule(t *testing.T) {
	for name, goCmd := range map[string]string{"the binary only": fakeGo, "the binary and another file": fakeGoTwoFiles} {
		t.Run(name, func(t *testing.T) {
			fakeTools(t, map[string]string{"go": goCmd})
			dest := filepath.Join(t.TempDir(), "tool")
			if err := (&GoInstall{}).Install("go://example.com/tool/v2", "", dest); err != nil {
				t.Fatalf("Install: %v", err)
			}
			checkOnlyFile(t, dest, "go example.com/tool/v2@latest\n", "v2")
		})
	}
}

// TestDocker_WritesTheFileItIsGiven: the provider copies /usr/local/bin/tool
// out of the container to the file of the entry, not to the file tool.
func TestDocker_WritesTheFileItIsGiven(t *testing.T) {
	fakeTools(t, map[string]string{"docker": fakeDocker})
	dir := t.TempDir()
	for _, dest := range []string{filepath.Join(dir, "ta"), filepath.Join(dir, "tools", "x")} {
		if err := (&Docker{}).Install("docker://example.com/org/tool", "v1", dest, nil); err != nil {
			t.Fatalf("Install to %s: %v", dest, err)
		}
		checkOnlyFile(t, dest, "cid-example.com/org/tool:v1:/usr/local/bin/tool\n", "tool")
	}
}

// testRegistry is an OCI registry in memory, on a local port. It returns
// the host and port. The docker configuration of the test is empty, so
// the keychain finds no credentials.
func testRegistry(t *testing.T) string {
	t.Helper()
	t.Setenv("DOCKER_CONFIG", t.TempDir())
	srv := httptest.NewServer(ociregistry.New(ociregistry.Logger(log.New(io.Discard, "", 0))))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

// pushImage pushes an image with the regular files of files (path in the
// image → content) to ref, for this platform.
func pushImage(t *testing.T, ref string, files map[string]string) {
	t.Helper()
	var headers []tar.Header
	contents := map[string][]byte{}
	for p, content := range files {
		headers = append(headers, tar.Header{Name: p, Typeflag: tar.TypeReg, Mode: 0755})
		contents[p] = []byte(content)
	}
	base, err := mutate.ConfigFile(empty.Image, &v1.ConfigFile{OS: runtime.GOOS, Architecture: runtime.GOARCH})
	if err != nil {
		t.Fatal(err)
	}
	img, err := mutate.AppendLayers(base, fakeLayer(t, headers, contents))
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

// TestOCI_WritesTheFileItIsGiven: the provider extracts /usr/local/bin/tool
// from the image to the file of the entry, not to the file tool.
func TestOCI_WritesTheFileItIsGiven(t *testing.T) {
	host := testRegistry(t)
	pushImage(t, host+"/org/tool:v1", map[string]string{"usr/local/bin/tool": "oci tool v1\n"})
	dir := t.TempDir()
	for _, dest := range []string{filepath.Join(dir, "ta"), filepath.Join(dir, "tools", "x")} {
		if err := (&OCI{}).Install("oci://"+host+"/org/tool", "v1", dest); err != nil {
			t.Fatalf("Install to %s: %v", dest, err)
		}
		checkOnlyFile(t, dest, "oci tool v1\n", "tool")
	}
}

// TestGit_WritesTheFileItIsGiven: the provider writes the file of the repo
// to the file of the entry, also in a directory that is not there yet.
func TestGit_WritesTheFileItIsGiven(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repoDir, run := initTestRepo(t)
	if err := os.WriteFile(filepath.Join(repoDir, "tool"), []byte("git tool\n"), 0755); err != nil {
		t.Fatal(err)
	}
	run("git", "add", "-A")
	run("git", "commit", "-m", "tool")
	dir := t.TempDir()
	for _, dest := range []string{filepath.Join(dir, "ta"), filepath.Join(dir, "tools", "x")} {
		if err := (&Git{}).Install("git://"+repoDir+":tool", "", dest); err != nil {
			t.Fatalf("Install to %s: %v", dest, err)
		}
		checkOnlyFile(t, dest, "git tool\n", "tool")
	}
}

// TestBinaryName_GoMajorVersion: for a go:// ref, BinaryName skips a major
// version suffix, as the go command does. Other refs keep the last element.
func TestBinaryName_GoMajorVersion(t *testing.T) {
	for ref, want := range map[string]string{
		"go://example.com/tool/v2":                                    "tool",
		"go://example.com/tool/v10@v10.1.0":                           "tool",
		"go://github.com/golangci/golangci-lint/v2/cmd/golangci-lint": "golangci-lint",
		"go://example.com/tool/v1":                                    "v1",
		"go://example.com/tool/v0":                                    "v0",
		"go://example.com/tool/v02":                                   "v02",
		"go://example.com/tool/v2x":                                   "v2x",
		"github.com/org/tool/v2":                                      "v2",
	} {
		if got := BinaryName(ref); got != want {
			t.Errorf("BinaryName(%q) = %q, want %q", ref, got, want)
		}
	}
}
