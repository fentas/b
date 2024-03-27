package tilt

import (
	"context"
	"fmt"
	"runtime"
	"strings"

	"github.com/buyoio/b/pkg/binaries"
	"github.com/buyoio/b/pkg/binary"
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
		GitHubRepo: "windmilleng/tilt",
		GitHubFileF: func(b *binary.Binary) (string, error) {
			arch := func(arch string) string {
				if arch == "amd64" {
					return "x86_64"
				}
				return arch
			}
			return fmt.Sprintf("tilt.%s.%s.%s.tar.gz",
				b.Version[1:],
				runtime.GOOS,
				arch(runtime.GOARCH),
			), nil
		},
		Name:     "tilt",
		VersionF: binary.GithubLatest,
		IsTarGz:  true,
		VersionLocalF: func(b *binary.Binary) (string, error) {
			s, err := b.Exec("version")
			if err != nil {
				return "", err
			}
			return strings.Split(s, ",")[0], nil
		},
	}
}
