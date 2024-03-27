package mkcert

import (
	"context"
	"fmt"
	"runtime"

	"github.com/buyoio/b/pkg/binaries"
	"github.com/buyoio/b/pkg/binary"
)

func NewMkcert(options *binaries.BinaryOptions) *binary.Binary {
	if options == nil {
		options = &binaries.BinaryOptions{
			Context: context.Background(),
		}
	}
	return &binary.Binary{
		Context:    options.Context,
		Envs:       options.Envs,
		Tracker:    options.Tracker,
		Name:       "mkcert",
		GitHubRepo: "FiloSottile/mkcert",
		GitHubFileF: func(b *binary.Binary) (string, error) {
			return fmt.Sprintf(
				"mkcert-%s-%s-%s",
				b.Version,
				runtime.GOOS,
				runtime.GOARCH,
			), nil
		},
		VersionF: binary.GithubLatest,
		VersionLocalF: func(b *binary.Binary) (string, error) {
			return b.Exec("-version")
		},
	}
}
