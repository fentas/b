package kubectl

import (
	"context"
	"fmt"
	"runtime"
	"strings"

	"github.com/fentas/b/pkg/binaries"
	"github.com/fentas/b/pkg/binary"
)

// The Kubernetes release host. The old storage.googleapis.com/kubernetes-release
// bucket is frozen: it does not get new releases.
var (
	// kubectlLatestVersion holds the latest stable version tag (e.g. "v1.36.2").
	kubectlLatestVersion = "https://dl.k8s.io/release/stable.txt"
	// kubectlURLFormat is the download URL of the kubectl binary, with the
	// version, the OS and the architecture as parameters.
	kubectlURLFormat = "https://dl.k8s.io/release/%s/bin/%s/%s/kubectl"
)

// kubectlURL returns the download URL of kubectl version for goos/goarch.
func kubectlURL(version, goos, goarch string) string {
	return fmt.Sprintf(kubectlURLFormat, version, goos, goarch)
}

func Binary(options *binaries.BinaryOptions) *binary.Binary {
	if options == nil {
		options = &binaries.BinaryOptions{
			Context: context.Background(),
		}
	}
	return &binary.Binary{
		Context: options.Context,
		Envs:    options.Envs,
		Tracker: options.Tracker,
		Version: options.Version,
		Name:    "kubectl",
		URLF: func(b *binary.Binary) (string, error) {
			return kubectlURL(b.Version, runtime.GOOS, runtime.GOARCH), nil
		},
		VersionF: func(b *binary.Binary) (string, error) {
			// stable.txt has no trailing newline today; trim it anyway, so a
			// newline can never end up in the download URL.
			v, err := binary.GetBody(kubectlLatestVersion)
			return strings.TrimSpace(v), err
		},
		VersionLocalF: func(b *binary.Binary) (string, error) {
			s, err := b.Exec("version", "--client")
			if err != nil {
				return "", err
			}
			v := strings.Split(strings.Split(s, "\n")[0], " ")
			return v[len(v)-1], nil
		},
	}
}
