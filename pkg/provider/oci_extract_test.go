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
	found, err := extractBinaryFromLayer(layer, searchPaths, dest, map[string]bool{})
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
	found, err := extractBinaryFromLayer(layer, []string{"/nope"}, dest, map[string]bool{})
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
	found, err := extractBinaryFromLayer(layer, []string{"/usr/bin/busybox"}, dest, map[string]bool{})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if found {
		t.Error("expected symlink to be ignored (non-regular)")
	}
}

func TestExtractBinaryFromLayer_EmptySearchPaths(t *testing.T) {
	layer := fakeLayer(t, nil, nil)
	found, err := extractBinaryFromLayer(layer, nil, filepath.Join(t.TempDir(), "out"), map[string]bool{})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if found {
		t.Error("expected found=false for empty searchPaths")
	}
}

func TestExtractBinaryFromLayer_RespectsWhiteout(t *testing.T) {
	// A whiteout from a newer layer blocks extraction of /usr/bin/tool
	// from this (older) layer.
	entries := []tar.Header{
		{Name: "usr/bin/tool", Typeflag: tar.TypeReg, Mode: 0755},
	}
	contents := map[string][]byte{"usr/bin/tool": []byte("stale")}
	layer := fakeLayer(t, entries, contents)

	dest := filepath.Join(t.TempDir(), "out")
	whiteouts := map[string]bool{"/usr/bin/tool": true}
	found, err := extractBinaryFromLayer(layer, []string{"/usr/bin/tool"}, dest, whiteouts)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if found {
		t.Error("whiteout from newer layer should block extraction")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Error("dest should not be created when path is whited out")
	}
}

func TestExtractBinaryFromLayer_RecordsWhiteouts(t *testing.T) {
	// A whiteout marker in this layer populates the shared map so older
	// layers skip that path.
	entries := []tar.Header{
		{Name: "usr/bin/.wh.deleted", Typeflag: tar.TypeReg, Mode: 0644},
		{Name: "opt/.wh..wh..opq", Typeflag: tar.TypeReg, Mode: 0644},
	}
	layer := fakeLayer(t, entries, nil)

	whiteouts := map[string]bool{}
	_, err := extractBinaryFromLayer(layer, []string{"/nope"}, filepath.Join(t.TempDir(), "out"), whiteouts)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if !whiteouts["/usr/bin/deleted"] {
		t.Errorf("expected /usr/bin/deleted to be whited out, got %v", whiteouts)
	}
	if !whiteouts["/opt/"] {
		t.Errorf("expected /opt/ opaque marker, got %v", whiteouts)
	}
}

func TestExtractBinaryFromLayer_FallsBackWhenFirstWhitedOut(t *testing.T) {
	// Layer contains both candidates; the higher-priority one is whited out
	// by a newer layer, so the lower-priority candidate must be extracted.
	entries := []tar.Header{
		{Name: "usr/local/bin/tool", Typeflag: tar.TypeReg, Mode: 0755},
		{Name: "usr/bin/tool", Typeflag: tar.TypeReg, Mode: 0755},
	}
	contents := map[string][]byte{
		"usr/local/bin/tool": []byte("blocked"),
		"usr/bin/tool":       []byte("fallback"),
	}
	layer := fakeLayer(t, entries, contents)

	dest := filepath.Join(t.TempDir(), "out")
	whiteouts := map[string]bool{"/usr/local/bin/tool": true}
	searchPaths := []string{"/usr/local/bin/tool", "/usr/bin/tool"}
	found, err := extractBinaryFromLayer(layer, searchPaths, dest, whiteouts)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if !found {
		t.Fatal("expected fallback candidate to be extracted")
	}
	body, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if string(body) != "fallback" {
		t.Errorf("got %q, want %q", body, "fallback")
	}
}

func TestExtractBinaryFromLayer_AcceptsLegacyRegular(t *testing.T) {
	// Legacy NUL-typeflag regular file must be accepted via FileInfo.IsRegular().
	entries := []tar.Header{
		{Name: "usr/bin/tool", Typeflag: 0x00, Mode: 0755},
	}
	layer := fakeLayer(t, entries, map[string][]byte{"usr/bin/tool": []byte("x")})
	dest := filepath.Join(t.TempDir(), "out")
	found, err := extractBinaryFromLayer(layer, []string{"/usr/bin/tool"}, dest, map[string]bool{})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if !found {
		t.Error("legacy NUL typeflag regular file should be accepted")
	}
}

func TestExtractBinaryFromLayer_RootOpaqueBlocksEverything(t *testing.T) {
	// An opaque whiteout at the image root ("/.wh..wh..opq") must be stored
	// under the "/" sentinel so isWhiteoutBlocked hides older layers.
	entries := []tar.Header{
		{Name: ".wh..wh..opq", Typeflag: tar.TypeReg, Mode: 0644},
	}
	layer := fakeLayer(t, entries, nil)
	whiteouts := map[string]bool{}
	_, err := extractBinaryFromLayer(layer, []string{"/nope"}, filepath.Join(t.TempDir(), "out"), whiteouts)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if !whiteouts["/"] {
		t.Errorf("expected root opaque to set whiteouts[\"/\"], got %v", whiteouts)
	}
}

func TestExtractBinaryFromLayer_OpaqueDirBlocksDescendant(t *testing.T) {
	// An opaque-dir whiteout on "/usr/local/bin/" should block extraction of
	// /usr/local/bin/tool from an older layer.
	entries := []tar.Header{
		{Name: "usr/local/bin/tool", Typeflag: tar.TypeReg, Mode: 0755},
	}
	layer := fakeLayer(t, entries, map[string][]byte{"usr/local/bin/tool": []byte("x")})

	whiteouts := map[string]bool{"/usr/local/bin/": true}
	found, err := extractBinaryFromLayer(layer, []string{"/usr/local/bin/tool"}, filepath.Join(t.TempDir(), "out"), whiteouts)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if found {
		t.Error("opaque-dir whiteout should block extraction of descendants")
	}
}
