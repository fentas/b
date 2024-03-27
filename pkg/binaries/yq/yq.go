package yq

import (
	"context"
	"fmt"
	"runtime"
	"strings"

	"github.com/buyoio/b/pkg/binaries"
	"github.com/buyoio/b/pkg/binary"
)

func NewYq(options *binaries.BinaryOptions) *binary.Binary {
	if options == nil {
		options = &binaries.BinaryOptions{
			Context: context.Background(),
		}
	}
	return &binary.Binary{
		Context:    options.Context,
		Envs:       options.Envs,
		Tracker:    options.Tracker,
		GitHubRepo: "mikefarah/yq",
		GitHubFile: fmt.Sprintf("yq_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH),
		IsTarGz:    true,
		Name:       "yq",
		VersionF:   binary.GithubLatest,
		VersionLocalF: func(b *binary.Binary) (string, error) {
			o, err := b.Exec("--version")
			v := strings.Split(o, " ")
			return v[len(v)-1], err
		},
	}
}
