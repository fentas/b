package kubeseal

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
		Name:       "kubeseal",
		GitHubRepo: "bitnami-labs/sealed-secrets",
		GitHubFileF: func(b *binary.Binary) (string, error) {
			return fmt.Sprintf(
				"kubeseal-%s-%s-%s.tar.gz",
				b.Version[1:],
				runtime.GOOS,
				runtime.GOARCH,
			), nil
		},
		VersionF: binary.GithubLatest,
		IsTarGz:  true,
		VersionLocalF: func(b *binary.Binary) (string, error) {
			s, err := b.Exec("--version")
			if err != nil {
				return "", err
			}
			v := strings.Split(s, " ")
			return "v" + v[len(v)-1], nil
		},
	}
}
