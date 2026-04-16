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

// TestDigestMatchesLock_Matches returns true only when fresh == locked.
func TestDigestMatchesLock_Matches(t *testing.T) {
	lk := &lock.Lock{
		Binaries: []lock.BinEntry{
			{Name: "tool", Source: "fakedigest://example", Digest: "sha256:aaa"},
		},
	}
	b := &binary.Binary{Name: "tool", AutoDetect: true, ProviderRef: "fakedigest://example"}
	if !digestMatchesLock(b, lk, "sha256:aaa") {
		t.Error("expected match when fresh and locked match")
	}
}

// TestDigestMatchesLock_Different returns false so update proceeds.
func TestDigestMatchesLock_Different(t *testing.T) {
	lk := &lock.Lock{
		Binaries: []lock.BinEntry{
			{Name: "tool", Source: "fakedigest://example", Digest: "sha256:old"},
		},
	}
	b := &binary.Binary{Name: "tool", AutoDetect: true, ProviderRef: "fakedigest://example"}
	if digestMatchesLock(b, lk, "sha256:new") {
		t.Error("expected mismatch when fresh and locked differ")
	}
}

// TestDigestMatchesLock_NoLockedDigest — first install (lock has no digest).
// Must return false so the caller re-downloads (and populates the digest).
func TestDigestMatchesLock_NoLockedDigest(t *testing.T) {
	lk := &lock.Lock{
		Binaries: []lock.BinEntry{
			// Legacy entry or freshly-installed without digest.
			{Name: "tool", Source: "fakedigest://example"},
		},
	}
	b := &binary.Binary{Name: "tool", AutoDetect: true, ProviderRef: "fakedigest://example"}
	if digestMatchesLock(b, lk, "sha256:aaa") {
		t.Error("expected false when lock has no digest")
	}
}

// TestDigestMatchesLock_FreshEmpty — caller couldn't resolve (registry
// unreachable etc). Must return false so we don't wrongly skip.
func TestDigestMatchesLock_FreshEmpty(t *testing.T) {
	lk := &lock.Lock{
		Binaries: []lock.BinEntry{
			{Name: "tool", Source: "fakedigest://example", Digest: "sha256:aaa"},
		},
	}
	b := &binary.Binary{Name: "tool", AutoDetect: true, ProviderRef: "fakedigest://example"}
	if digestMatchesLock(b, lk, "") {
		t.Error("expected false when fresh digest is empty — we can't prove it's current")
	}
}

// TestDigestMatchesLock_NoLock — no lockfile at all. Must return false so
// the caller falls through to a regular update.
func TestDigestMatchesLock_NoLock(t *testing.T) {
	b := &binary.Binary{Name: "tool", AutoDetect: true, ProviderRef: "fakedigest://example"}
	if digestMatchesLock(b, nil, "sha256:aaa") {
		t.Error("expected false when lk is nil")
	}
}

// TestDigestMatchesLock_MissingProviderRef guards the empty ProviderRef
// path — a preset binary without AutoDetect must never be treated as
// digest-capable.
func TestDigestMatchesLock_MissingProviderRef(t *testing.T) {
	lk := &lock.Lock{Binaries: []lock.BinEntry{{Name: "tool", Digest: "sha256:aaa"}}}
	b := &binary.Binary{Name: "tool"} // no ProviderRef
	if digestMatchesLock(b, lk, "sha256:aaa") {
		t.Error("expected false when ProviderRef is empty")
	}
}

// TestDigestMatchesLock_SourceChanged covers the case where the derived
// binary name stays the same but the user edited the provider ref in
// b.yaml (e.g. docker://docker@cli → oci://docker@cli, or changed the
// in-container path). The lock's digest refers to the OLD source so we
// must NOT treat a matching digest string as "up to date".
func TestDigestMatchesLock_SourceChanged(t *testing.T) {
	lk := &lock.Lock{
		Binaries: []lock.BinEntry{
			{Name: "tool", Source: "fakedigest://old", Digest: "sha256:aaa"},
		},
	}
	b := &binary.Binary{Name: "tool", AutoDetect: true, ProviderRef: "fakedigest://new"}
	if digestMatchesLock(b, lk, "sha256:aaa") {
		t.Error("expected false when lock Source != ProviderRef, even with matching digest")
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

// TestProviderDigestResolver returns the resolver for digest-capable
// providers and (nil, false) otherwise.
func TestProviderDigestResolver(t *testing.T) {
	dr, ok := providerDigestResolver("fakedigest://x")
	if !ok || dr == nil {
		t.Error("expected resolver for fakedigest://")
	}
	if _, ok := providerDigestResolver("github.com/derailed/k9s"); ok {
		t.Error("github provider must not be digest-capable")
	}
	if _, ok := providerDigestResolver("not-a-ref"); ok {
		t.Error("unknown ref must not be digest-capable")
	}
}
