package sops

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
	if options.Envs == nil {
		options.Envs = map[string]string{}
	}
	return &binary.Binary{
		Context:    options.Context,
		Envs:       options.Envs,
		Tracker:    options.Tracker,
		Version:    options.Version,
		Name:       "sops",
		GitHubRepo: "getsops/sops",
		GitHubFileF: func(b *binary.Binary) (string, error) {
			return fmt.Sprintf("sops-%s.%s.%s",
				b.Version,
				runtime.GOOS,
				runtime.GOARCH,
			), nil
		},
		VersionF: binary.GithubLatest,
		VersionLocalF: func(b *binary.Binary) (string, error) {
			// sops 3.10.2 (latest)
			s, err := b.Exec("--version")
			if err != nil {
				return "", err
			}
			v := strings.Split(strings.SplitN(s, "\n", 2)[0], " ")
			return "v" + v[1], nil
		},
	}
}
