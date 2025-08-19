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
		case "amd64":
			return "x86_64-unknown-linux-musl"
		case "arm":
			return "armv7-unknown-linux-musleabihf"
		}
	}
	return ""
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
			s := sys()
			if s == "" {
				return "", fmt.Errorf("unsupported platform: %s/%s", runtime.GOOS, runtime.GOARCH)
			}
			return fmt.Sprintf("renvsubst-%s-%s.tar.gz",
				b.Version,
				s,
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
