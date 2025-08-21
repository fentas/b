package kubelogin

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
		Name:       "kubelogin",
		GitHubRepo: "int128/kubelogin",
		URLF: func(b *binary.Binary) (string, error) {
			return fmt.Sprintf(
				"https://github.com/%s/releases/download/%s/kubelogin_%s_%s.zip",
				b.GitHubRepo,
				b.Version,
				runtime.GOOS,
				runtime.GOARCH,
			), nil
		},
		VersionF: binary.GithubLatest,
		IsZip:    true,
		VersionLocalF: func(b *binary.Binary) (string, error) {
			s, err := b.Exec("--version")
			if err != nil {
				return "", err
			}
			v := strings.Split(s, " ")
			return v[len(v)-1], nil
		},
	}
}
