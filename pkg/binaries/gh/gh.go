package gh

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
		GitHubRepo: "cli/cli",
		// https://github.com/cli/cli/releases/download/v2.46.0/gh_2.46.0_linux_amd64.tar.gz
		GitHubFileF: func(b *binary.Binary) (string, error) {
			return fmt.Sprintf("gh_%s_%s_%s.tar.gz",
				b.Version[1:],
				runtime.GOOS,
				runtime.GOARCH,
			), nil
		},
		Name:     "gh",
		VersionF: binary.GithubLatest,
		IsTarGz:  true,
		VersionLocalF: func(b *binary.Binary) (string, error) {
			v, err := b.Exec("version")
			if err != nil {
				return "", err
			}
			s := strings.Split(v, "/")
			return s[len(s)-1], nil
		},
	}
}
