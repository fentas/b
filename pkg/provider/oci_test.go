package provider

import "testing"

func TestOCIMatch(t *testing.T) {
	o := &OCI{}
	tests := []struct {
		ref  string
		want bool
	}{
		{"oci://alpine", true},
		{"oci://ghcr.io/org/img", true},
		{"oci://ghcr.io/org/img@v1:/bin/tool", true},
		{"docker://alpine", false},
		{"github.com/org/repo", false},
	}
	for _, tt := range tests {
		if got := o.Match(tt.ref); got != tt.want {
			t.Errorf("OCI.Match(%q) = %v, want %v", tt.ref, got, tt.want)
		}
	}
}

func TestOCIName(t *testing.T) {
	o := &OCI{}
	if o.Name() != "oci" {
		t.Errorf("OCI.Name() = %q", o.Name())
	}
}

func TestOCILatestVersion(t *testing.T) {
	o := &OCI{}
	v, err := o.LatestVersion("oci://alpine")
	if err != nil {
		t.Fatalf("LatestVersion() error = %v", err)
	}
	if v != "latest" {
		t.Errorf("LatestVersion() = %q, want %q", v, "latest")
	}
}

func TestOCIFetchRelease(t *testing.T) {
	o := &OCI{}
	if _, err := o.FetchRelease("oci://alpine", "latest"); err == nil {
		t.Error("expected error from OCI.FetchRelease")
	}
}

// TestOCI_ImplementsDigestResolver is a compile-time check so the
// interface wiring for 'b update' can't silently break.
func TestOCI_ImplementsDigestResolver(t *testing.T) {
	var _ DigestResolver = (*OCI)(nil)
	var _ DigestResolver = (*Docker)(nil)
}

// TestOCI_ResolveDigest_ErrorTolerant asserts ResolveDigest does NOT
// return a hard error when the registry is unreachable or the image
// doesn't exist — a transient registry outage must not break 'b update'.
// We use a bogus localhost ref to guarantee the HEAD fails.
func TestOCI_ResolveDigest_ErrorTolerant(t *testing.T) {
	o := &OCI{}
	// 127.0.0.1:1 is almost guaranteed to be closed.
	digest, err := o.ResolveDigest("oci://127.0.0.1:1/no/such@nope", "")
	if err != nil {
		t.Errorf("ResolveDigest on unreachable registry returned error %v; want empty+nil (caller treats as unknown)", err)
	}
	if digest != "" {
		t.Errorf("digest = %q, want empty on unreachable registry", digest)
	}
}

// TestOCI_ResolveDigest_InvalidRefIsHardError: malformed refs should
// surface — that's a programmer error, not a transient network issue.
func TestOCI_ResolveDigest_InvalidRefIsHardError(t *testing.T) {
	o := &OCI{}
	// Images can't contain '@' in the name portion; an image "BAD@@"
	// fails parse.
	if _, err := o.ResolveDigest("oci://BAD@@@garbage", ""); err == nil {
		t.Error("expected parse error for malformed ref")
	}
}

func TestParseImageRef(t *testing.T) {
	tests := []struct {
		in              string
		wantImage       string
		wantTag         string
		wantInContainer string
	}{
		{"alpine", "alpine", "", ""},
		{"alpine@3.19", "alpine", "3.19", ""},
		{"docker@cli:/usr/local/bin/docker", "docker", "cli", "/usr/local/bin/docker"},
		{"ghcr.io/org/img@v1:/bin/tool", "ghcr.io/org/img", "v1", "/bin/tool"},
		{"alpine:/bin/busybox", "alpine", "", "/bin/busybox"},
		// Registry port is preserved, not mistaken for path.
		{"localhost:5000/org/img@v1:/bin/tool", "localhost:5000/org/img", "v1", "/bin/tool"},
		{"localhost:5000/org/img", "localhost:5000/org/img", "", ""},
		// Docker-style "image:tag" is tolerated as a copy-paste convenience.
		{"alpine:3.19", "alpine", "3.19", ""},
		{"ghcr.io/org/img:v1", "ghcr.io/org/img", "v1", ""},
	}
	for _, tt := range tests {
		img, tag, p := ParseImageRef(tt.in)
		if img != tt.wantImage || tag != tt.wantTag || p != tt.wantInContainer {
			t.Errorf("ParseImageRef(%q) = (%q, %q, %q), want (%q, %q, %q)",
				tt.in, img, tag, p, tt.wantImage, tt.wantTag, tt.wantInContainer)
		}
	}
}
