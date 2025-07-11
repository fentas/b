package stern

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
		Name:       "stern",
		GitHubRepo: "stern/stern",
		GitHubFileF: func(b *binary.Binary) (string, error) {
			return fmt.Sprintf("stern_%s_%s_%s.tar.gz",
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
			v := strings.Split(strings.SplitN(s, "\n", 2)[0], " ")
			return "v" + v[len(v)-1], nil
		},
	}
}
