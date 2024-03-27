package jq

import (
	"context"
	"fmt"
	"runtime"

	"github.com/buyoio/b/pkg/binaries"
	"github.com/buyoio/b/pkg/binary"
)

func NewJq(options *binaries.BinaryOptions) *binary.Binary {
	if options == nil {
		options = &binaries.BinaryOptions{
			Context: context.Background(),
		}
	}
	return &binary.Binary{
		Context:    options.Context,
		Envs:       options.Envs,
		Tracker:    options.Tracker,
		Name:       "jq",
		GitHubRepo: "jqlang/jq",
		GitHubFile: fmt.Sprintf("jq-%s-%s", runtime.GOOS, runtime.GOARCH),
		VersionF:   binary.GithubLatest,
		VersionLocalF: func(b *binary.Binary) (string, error) {
			return b.Exec("--version")
		},
	}
}
