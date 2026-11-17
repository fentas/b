package kubectl

import (
	"runtime"
	"testing"

	"github.com/fentas/b/pkg/binary"
)

// The storage.googleapis.com/kubernetes-release bucket is frozen, so the
// preset must use dl.k8s.io for the version and for the download.
func TestKubectlLatestVersionURL(t *testing.T) {
	const want = "https://dl.k8s.io/release/stable.txt"
	if kubectlLatestVersion != want {
		t.Errorf("kubectlLatestVersion = %q, want %q", kubectlLatestVersion, want)
	}
}

func TestKubectlURL(t *testing.T) {
	tests := []struct {
		version, goos, goarch string
		want                  string
	}{
		{"v1.36.2", "linux", "amd64", "https://dl.k8s.io/release/v1.36.2/bin/linux/amd64/kubectl"},
		{"v1.36.2", "linux", "arm64", "https://dl.k8s.io/release/v1.36.2/bin/linux/arm64/kubectl"},
		{"v1.37.2", "darwin", "arm64", "https://dl.k8s.io/release/v1.37.2/bin/darwin/arm64/kubectl"},
	}
	for _, tt := range tests {
		if got := kubectlURL(tt.version, tt.goos, tt.goarch); got != tt.want {
			t.Errorf("kubectlURL(%q, %q, %q) = %q, want %q", tt.version, tt.goos, tt.goarch, got, tt.want)
		}
	}
}

func TestBinary_kubectlURLF(t *testing.T) {
	b := Binary(nil)
	got, err := b.URLF(&binary.Binary{Name: b.Name, Version: "v1.36.2"})
	if err != nil {
		t.Fatalf("URLF: %v", err)
	}
	want := "https://dl.k8s.io/release/v1.36.2/bin/" + runtime.GOOS + "/" + runtime.GOARCH + "/kubectl"
	if got != want {
		t.Errorf("URLF = %q, want %q", got, want)
	}
}
