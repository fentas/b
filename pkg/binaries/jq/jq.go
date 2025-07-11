package jq

import (
	"context"
	"fmt"
	"runtime"

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
		Name:       "jq",
		GitHubRepo: "jqlang/jq",
		GitHubFile: fmt.Sprintf("jq-%s-%s", runtime.GOOS, runtime.GOARCH),
		VersionF:   binary.GithubLatest,
		VersionLocalF: func(b *binary.Binary) (string, error) {
			return b.Exec("--version")
		},
	}
}
