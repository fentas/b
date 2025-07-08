package clusterctl

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
		Name:       "clusterctl",
		GitHubRepo: "kubernetes-sigs/cluster-api",
		GitHubFile: fmt.Sprintf("clusterctl-%s-%s", runtime.GOOS, runtime.GOARCH),
		VersionF:   binary.GithubLatest,
		IsTarGz:    false,
		VersionLocalF: func(b *binary.Binary) (string, error) {
			s, err := b.Exec("version", "-o", "short")
			if err != nil {
				return "", err
			}
			return strings.TrimSpace(s), nil
		},
	}
}
