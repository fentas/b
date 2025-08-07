package cilium

import (
	"context"
	"fmt"
	"runtime"
	"strings"

	"github.com/fentas/b/pkg/binaries"
	"github.com/fentas/b/pkg/binary"
)

func Binary(options *binaries.BinaryOptions) *binary.Binary {
	if options == nil {
		options = &binaries.BinaryOptions{
			Context: context.Background(),
		}
	}
	return &binary.Binary{
		Context:    options.Context,
		Envs:       options.Envs,
		Tracker:    options.Tracker,
		Version:    options.Version,
		Name:       "cilium",
		GitHubRepo: "cilium/cilium-cli",
		// https://github.com/cilium/cilium-cli/releases/download/v0.18.6/cilium-linux-amd64.tar.gz
		URLF: func(b *binary.Binary) (string, error) {
			extension := "tar.gz"
			if runtime.GOOS == "windows" {
				extension = "zip"
			}
			return fmt.Sprintf(
				"https://github.com/cilium/cilium-cli/releases/download/%s/cilium-%s-%s.%s",
				b.Version,
				runtime.GOOS,
				runtime.GOARCH,
				extension,
			), nil
		},
		VersionF:  binary.GithubLatest,
		IsDynamic: true,
		VersionLocalF: func(b *binary.Binary) (string, error) {
			s, err := b.Exec("version", "--client")
			if err != nil {
				return "", err
			}
			version := strings.Split(s, " ")
			if len(version) < 2 {
				return "", fmt.Errorf("version not found")
			}
			return version[1], nil
		},
	}
}
