package provider

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/google/go-containerregistry/pkg/v1/types"
)

// oldBinary writes dest with "old" and returns a hard link to it. When the
// binary is replaced in one step, the link keeps "old".
func oldBinary(t *testing.T, dest string) string {
	t.Helper()
	if err := os.WriteFile(dest, []byte("old"), 0755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Link(dest, link); err != nil {
		t.Skipf("no hard links here: %v", err)
	}
	return link
}

// checkReplaced checks that dest has want and mode 0755, that link (to the
// old file) still has "old", and that no temp file is beside dest.
func checkReplaced(t *testing.T, dest, link, want string) {
	t.Helper()
	if got, err := os.ReadFile(dest); err != nil || string(got) != want {
		t.Errorf("%s is %q (%v), want %q", dest, got, err, want)
	}
	if got, err := os.ReadFile(link); err != nil || string(got) != "old" {
		t.Errorf("a hard link to the old binary is %q (%v), want %q: the install wrote into it", got, err, "old")
	}
	fi, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0755 {
		t.Errorf("mode %v, want 0755", fi.Mode().Perm())
	}
	checkNoTemp(t, filepath.Dir(dest))
}

// checkNoTemp fails the test when a temp file of an install is in dir.
func checkNoTemp(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("temp file %s is still in %s", e.Name(), dir)
		}
	}
}

// TestGitInstall_ReplacesTheBinaryInOneStep: the git provider puts a new
// file in place of the old binary.
func TestGitInstall_ReplacesTheBinaryInOneStep(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repoDir, run := initTestRepo(t)
	if err := os.WriteFile(filepath.Join(repoDir, "tool"), []byte("new"), 0755); err != nil {
		t.Fatal(err)
	}
	run("git", "add", "-A")
	run("git", "commit", "-m", "tool")

	destDir := t.TempDir()
	dest := filepath.Join(destDir, "tool")
	link := oldBinary(t, dest)
	if err := (&Git{}).Install("git://"+repoDir+":tool", "", dest); err != nil {
		t.Fatalf("Install: %v", err)
	}
	checkReplaced(t, dest, link, "new")
}

// TestExtractBinaryFromLayer_ReplacesTheBinaryInOneStep: the oci provider
// puts a new file in place of the old binary, with mode 0755.
func TestExtractBinaryFromLayer_ReplacesTheBinaryInOneStep(t *testing.T) {
	layer := fakeLayer(t,
		[]tar.Header{{Name: "usr/local/bin/tool", Typeflag: tar.TypeReg, Mode: 0644}},
		map[string][]byte{"usr/local/bin/tool": []byte("new")})
	dest := filepath.Join(t.TempDir(), "tool")
	link := oldBinary(t, dest)
	found, err := extractBinaryFromLayer(layer, []string{"/usr/local/bin/tool"}, dest, map[string]bool{})
	if err != nil || !found {
		t.Fatalf("extract: found %v, error %v", found, err)
	}
	checkReplaced(t, dest, link, "new")
}

// TestExtractBinaryFromLayer_CutLayerKeepsTheBinary: a layer that ends in
// the middle of the binary is an error, and leaves the old binary as it was
// and no temp file.
func TestExtractBinaryFromLayer_CutLayerKeepsTheBinary(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	content := bytes.Repeat([]byte("x"), 64<<10)
	if err := tw.WriteHeader(&tar.Header{Name: "usr/local/bin/tool", Typeflag: tar.TypeReg, Mode: 0755, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	cut := buf.Bytes()[:buf.Len()/2]
	layer, err := tarball.LayerFromOpener(
		func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(cut)), nil },
		tarball.WithMediaType(types.DockerLayer),
		tarball.WithCompressionLevel(0),
	)
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "tool")
	if err := os.WriteFile(dest, []byte("old"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := extractBinaryFromLayer(layer, []string{"/usr/local/bin/tool"}, dest, map[string]bool{}); err == nil {
		t.Fatal("extract of a cut layer: no error")
	}
	if got, err := os.ReadFile(dest); err != nil || string(got) != "old" {
		t.Errorf("%s is %q (%v), want the old binary", dest, got, err)
	}
	checkNoTemp(t, filepath.Dir(dest))
}
