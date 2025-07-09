package kustomize

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
		Name:       "kustomize",
		GitHubRepo: "kubernetes-sigs/kustomize",
		URLF: func(b *binary.Binary) (string, error) {
			return fmt.Sprintf(
				"https://github.com/kubernetes-sigs/kustomize/releases/download/kustomize%%2F%s/kustomize_%s_%s_%s.tar.gz",
				b.Version,
				b.Version,
				runtime.GOOS,
				runtime.GOARCH,
			), nil
		},
		VersionF: binary.GithubLatest,
		IsTarGz:  true,
		VersionLocalF: func(b *binary.Binary) (string, error) {
			s, err := b.Exec("version")
			if err != nil {
				return "", err
			}
			return s, nil
		},
	}
}
