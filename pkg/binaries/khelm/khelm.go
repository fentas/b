// Package khelm is a Kubernetes Helm wrapper
package khelm

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
		Name:       "khelm",
		GitHubRepo: "mgoltzsche/khelm",
		GitHubFile: fmt.Sprintf("khelm-%s-%s", runtime.GOOS, runtime.GOARCH),
		VersionF:   binary.GithubLatest,
		VersionLocalF: func(b *binary.Binary) (string, error) {
			s, err := b.Exec("version")
			if err != nil {
				return "", err
			}
			return "v" + strings.Split(s, " ")[0], nil
		},
	}
}
