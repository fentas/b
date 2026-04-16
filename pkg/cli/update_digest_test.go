package cli

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/fentas/b/pkg/binary"
	"github.com/fentas/b/pkg/lock"
	"github.com/fentas/b/pkg/provider"
)

// fakeDigestProvider implements both Provider and DigestResolver for tests.
// The digest it returns is controlled by an atomic string pointer so tests
// can simulate "tag moved upstream" mid-run.
type fakeDigestProvider struct {
	digest    atomic.Value // string
	callCount atomic.Int32
}

func (f *fakeDigestProvider) Name() string { return "fakedigest" }
func (f *fakeDigestProvider) Match(ref string) bool {
	return strings.HasPrefix(ref, "fakedigest://")
}
func (f *fakeDigestProvider) LatestVersion(ref string) (string, error) { return "latest", nil }
func (f *fakeDigestProvider) FetchRelease(ref, version string) (*provider.Release, error) {
	return nil, fmt.Errorf("fakedigest does not use FetchRelease")
}
func (f *fakeDigestProvider) ResolveDigest(ref, version string) (string, error) {
	f.callCount.Add(1)
	v := f.digest.Load()
	if v == nil {
		return "", nil
	}
	return v.(string), nil
}

var globalFakeDigest = &fakeDigestProvider{}

func init() {
	globalFakeDigest.digest.Store("sha256:aaa")
	provider.Register(globalFakeDigest)
}

// TestDigestUnchanged_Matches returns true only when fresh == locked.
func TestDigestUnchanged_Matches(t *testing.T) {
	globalFakeDigest.digest.Store("sha256:aaa")

	lk := &lock.Lock{
		Binaries: []lock.BinEntry{
			{Name: "tool", Source: "fakedigest://example", Digest: "sha256:aaa"},
		},
	}
	b := &binary.Binary{Name: "tool", AutoDetect: true, ProviderRef: "fakedigest://example"}

	if !digestUnchanged(b, lk) {
		t.Error("expected digestUnchanged=true when fresh and locked match")
	}
}

// TestDigestUnchanged_Different returns false so update proceeds.
func TestDigestUnchanged_Different(t *testing.T) {
	globalFakeDigest.digest.Store("sha256:new")
	lk := &lock.Lock{
		Binaries: []lock.BinEntry{
			{Name: "tool", Source: "fakedigest://example", Digest: "sha256:old"},
		},
	}
	b := &binary.Binary{Name: "tool", AutoDetect: true, ProviderRef: "fakedigest://example"}

	if digestUnchanged(b, lk) {
		t.Error("expected digestUnchanged=false when fresh and locked differ")
	}
}

// TestDigestUnchanged_NoLock — first install (lock has no digest yet).
// Must return false so the caller re-downloads (and populates the digest).
func TestDigestUnchanged_NoLockedDigest(t *testing.T) {
	globalFakeDigest.digest.Store("sha256:aaa")
	lk := &lock.Lock{
		Binaries: []lock.BinEntry{
			// No Digest set — legacy entry or freshly-installed without digest.
			{Name: "tool", Source: "fakedigest://example"},
		},
	}
	b := &binary.Binary{Name: "tool", AutoDetect: true, ProviderRef: "fakedigest://example"}

	if digestUnchanged(b, lk) {
		t.Error("expected digestUnchanged=false when lock has no digest")
	}
}

// TestDigestUnchanged_ResolverReturnsEmpty — registry unreachable etc.
// Must return false so we don't wrongly skip.
func TestDigestUnchanged_ResolverEmpty(t *testing.T) {
	globalFakeDigest.digest.Store("") // resolver returns empty
	lk := &lock.Lock{
		Binaries: []lock.BinEntry{
			{Name: "tool", Source: "fakedigest://example", Digest: "sha256:aaa"},
		},
	}
	b := &binary.Binary{Name: "tool", AutoDetect: true, ProviderRef: "fakedigest://example"}

	if digestUnchanged(b, lk) {
		t.Error("expected digestUnchanged=false when resolver returns empty — we can't prove it's current")
	}
}

// TestDigestUnchanged_NonDigestProvider — e.g. github preset.
// Must return false so the existing update path runs.
func TestDigestUnchanged_NonDigestProvider(t *testing.T) {
	lk := &lock.Lock{
		Binaries: []lock.BinEntry{
			{Name: "jq", Source: "github.com/jqlang/jq", Digest: "should-be-ignored"},
		},
	}
	b := &binary.Binary{Name: "jq", AutoDetect: true, ProviderRef: "github.com/jqlang/jq"}

	if digestUnchanged(b, lk) {
		t.Error("non-digest providers must never short-circuit update")
	}
}

// TestIsDigestProvider covers the interface detection.
func TestIsDigestProvider(t *testing.T) {
	if !isDigestProvider("fakedigest://whatever") {
		t.Error("fakedigest should be a digest provider")
	}
	if !isDigestProvider("oci://alpine") {
		t.Error("oci:// should be a digest provider")
	}
	if !isDigestProvider("docker://alpine") {
		t.Error("docker:// should be a digest provider")
	}
	if isDigestProvider("github.com/jqlang/jq") {
		t.Error("github provider must not report as digest-capable")
	}
}
