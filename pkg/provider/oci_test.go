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
		{"oci://ghcr.io/org/img@v1::/bin/tool", true},
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

func TestParseImageRef(t *testing.T) {
	tests := []struct {
		in              string
		wantImage       string
		wantTag         string
		wantInContainer string
	}{
		{"alpine", "alpine", "", ""},
		{"alpine@3.19", "alpine", "3.19", ""},
		{"docker@cli::/usr/local/bin/docker", "docker", "cli", "/usr/local/bin/docker"},
		{"ghcr.io/org/img@v1::/bin/tool", "ghcr.io/org/img", "v1", "/bin/tool"},
		{"alpine::/bin/busybox", "alpine", "", "/bin/busybox"},
	}
	for _, tt := range tests {
		img, tag, p := ParseImageRef(tt.in)
		if img != tt.wantImage || tag != tt.wantTag || p != tt.wantInContainer {
			t.Errorf("ParseImageRef(%q) = (%q, %q, %q), want (%q, %q, %q)",
				tt.in, img, tag, p, tt.wantImage, tt.wantTag, tt.wantInContainer)
		}
	}
}
