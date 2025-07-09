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
			// https://github.com/kubernetes-sigs/cluster-api/issues/3573
			// we will ignore the following error
			// Error: unable to verify clusterctl version: unable to write version state file: mkdir /etc/xdg/cluster-api: permission denied exit status 1
			if err != nil && (s == "" || s[0] != 'v') {
				return "", err
			}
			return strings.Split(s, "\n")[0], nil
		},
	}
}
