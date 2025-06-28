package kubectl

import (
	"context"
	"fmt"
	"runtime"
	"strings"

	"github.com/fentas/b/pkg/binaries"
	"github.com/fentas/b/pkg/binary"
)

var (
	kubectlLatestVersion = "https://storage.googleapis.com/kubernetes-release/release/stable.txt"
)

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
			return fmt.Sprintf(
				"https://storage.googleapis.com/kubernetes-release/release/%s/bin/%s/%s/kubectl",
				b.Version,
				runtime.GOOS,
				runtime.GOARCH,
			), nil
		},
		VersionF: func(b *binary.Binary) (string, error) {
			return binary.GetBody(kubectlLatestVersion)
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
