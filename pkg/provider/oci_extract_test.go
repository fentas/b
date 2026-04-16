package provider

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/google/go-containerregistry/pkg/v1/types"
)

// fakeLayer adapts an in-memory uncompressed tar stream into a v1.Layer for
// test purposes by delegating to tarball.LayerFromOpener.
func fakeLayer(t *testing.T, entries []tar.Header, contents map[string][]byte) v1.Layer {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, h := range entries {
		hdr := h
		body := contents[hdr.Name]
		hdr.Size = int64(len(body))
		if err := tw.WriteHeader(&hdr); err != nil {
			t.Fatalf("tar write header: %v", err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatalf("tar write body: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	layer, err := tarball.LayerFromOpener(
		func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(buf.Bytes())), nil },
		tarball.WithMediaType(types.DockerLayer),
		tarball.WithCompressionLevel(0),
	)
	if err != nil {
		t.Fatalf("LayerFromOpener: %v", err)
	}
	return layer
}

func TestExtractBinaryFromLayer_FindsHighestPriority(t *testing.T) {
	// Two candidates in the same layer; earlier searchPaths entry wins.
	entries := []tar.Header{
		{Name: "usr/bin/docker", Typeflag: tar.TypeReg, Mode: 0755},
		{Name: "usr/local/bin/docker", Typeflag: tar.TypeReg, Mode: 0755},
	}
	contents := map[string][]byte{
		"usr/bin/docker":       []byte("low"),
		"usr/local/bin/docker": []byte("high"),
	}
	layer := fakeLayer(t, entries, contents)

	dest := filepath.Join(t.TempDir(), "out")
	searchPaths := []string{"/usr/local/bin/docker", "/usr/bin/docker"}
	found, err := extractBinaryFromLayer(layer, searchPaths, dest)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if !found {
		t.Fatal("expected found=true")
	}
	body, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if string(body) != "high" {
		t.Errorf("got %q, want %q (highest-priority match)", body, "high")
	}
}

func TestExtractBinaryFromLayer_NoMatch(t *testing.T) {
	entries := []tar.Header{
		{Name: "bin/busybox", Typeflag: tar.TypeReg, Mode: 0755},
	}
	contents := map[string][]byte{"bin/busybox": []byte("bb")}
	layer := fakeLayer(t, entries, contents)

	dest := filepath.Join(t.TempDir(), "out")
	found, err := extractBinaryFromLayer(layer, []string{"/nope"}, dest)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if found {
		t.Error("expected found=false")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Error("dest should not be created when nothing matched")
	}
}

func TestExtractBinaryFromLayer_SkipsNonRegular(t *testing.T) {
	// A symlink at the exact target path must not be copied as the binary.
	entries := []tar.Header{
		{Name: "usr/bin/busybox", Typeflag: tar.TypeSymlink, Linkname: "busybox", Mode: 0777},
	}
	layer := fakeLayer(t, entries, nil)

	dest := filepath.Join(t.TempDir(), "out")
	found, err := extractBinaryFromLayer(layer, []string{"/usr/bin/busybox"}, dest)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if found {
		t.Error("expected symlink to be ignored (non-regular)")
	}
}

func TestExtractBinaryFromLayer_EmptySearchPaths(t *testing.T) {
	layer := fakeLayer(t, nil, nil)
	found, err := extractBinaryFromLayer(layer, nil, filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if found {
		t.Error("expected found=false for empty searchPaths")
	}
}
