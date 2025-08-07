package curl

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
		Name:       "curl",
		GitHubRepo: "stunnel/static-curl",
		// https://github.com/stunnel/static-curl/releases/download/8.15.0/curl-linux-x86_64-musl-8.15.0.tar.xz
		URLF: func(b *binary.Binary) (string, error) {
			return fmt.Sprintf(
				"https://github.com/stunnel/static-curl/releases/download/%s/curl-%s-%s-musl-%s.tar.xz",
				b.Version,
				runtime.GOOS,
				binaries.Arch(runtime.GOARCH),
				b.Version,
			), nil
		},
		VersionF: binary.GithubLatest,
		IsTarXz:  true,
		VersionLocalF: func(b *binary.Binary) (string, error) {
			s, err := b.Exec("--version")
			if err != nil {
				return "", err
			}
			return strings.Split(s, " ")[1], nil
		},
	}
}
