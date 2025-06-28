package compose

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
	// https://github.com/docker/compose/releases/download/v2.26.1/docker-compose-linux-x86_64
	return &binary.Binary{
		Context:    options.Context,
		Envs:       options.Envs,
		Tracker:    options.Tracker,
		Version:    options.Version,
		Name:       "docker-compose",
		GitHubRepo: "docker/compose",
		GitHubFileF: func(b *binary.Binary) (string, error) {
			return fmt.Sprintf("docker-compose-%s-%s",
				runtime.GOOS,
				binaries.Arch(runtime.GOARCH),
			), nil
		},
		VersionF: binary.GithubLatest,
		VersionLocalF: func(b *binary.Binary) (string, error) {
			v, err := b.Exec("version")
			if err != nil {
				return "", err
			}
			s := strings.Split(v, " ")
			return s[len(s)-1], nil
		},
	}
}
