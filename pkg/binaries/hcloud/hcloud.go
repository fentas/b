package hcloud

import (
	"context"
	"fmt"
	"runtime"
	"strings"

	"github.com/buyoio/b/pkg/binaries"
	"github.com/buyoio/b/pkg/binary"
)

func NewHcloud(options *binaries.BinaryOptions) *binary.Binary {
	if options == nil {
		options = &binaries.BinaryOptions{
			Context: context.Background(),
		}
	}
	return &binary.Binary{
		Context:    options.Context,
		Envs:       options.Envs,
		Tracker:    options.Tracker,
		Name:       "hcloud",
		GitHubRepo: "hetznercloud/cli",
		GitHubFile: fmt.Sprintf("hcloud-%s-%s.tar.gz", runtime.GOOS, runtime.GOARCH),
		VersionF:   binary.GithubLatest,
		IsTarGz:    true,
		VersionLocalF: func(b *binary.Binary) (string, error) {
			s, err := b.Exec("version")
			if err != nil {
				return "", err
			}
			return "v" + strings.Split(s, " ")[1], nil
		},
	}
}
