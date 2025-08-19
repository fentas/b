package renvsubst

import (
	"context"
	"fmt"
	"runtime"

	"github.com/fentas/b/pkg/binaries"
	"github.com/fentas/b/pkg/binary"
)

func sys() string {
	switch runtime.GOOS {
	case "darwin":
		return "x86_64-apple-darwin"
	case "linux":
		switch runtime.GOARCH {
		case "arm":
			return "armv7-unknown-linux-musleabihf"
		}
	}
	return "x86_64-unknown-linux-musl"
}

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
		Name:       "renvsubst",
		GitHubRepo: "containeroo/renvsubst",
		GitHubFileF: func(b *binary.Binary) (string, error) {
			return fmt.Sprintf("renvsubst-%s-%s.tar.gz",
				b.Version,
				sys(),
			), nil
		},
		IsTarGz:  true,
		VersionF: binary.GithubLatest,
		VersionLocalF: func(b *binary.Binary) (string, error) {
			version, err := b.Exec("--version")
			if err != nil {
				return "", err
			}
			return "v" + version, nil
		},
	}
}
