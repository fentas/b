// Package hubble provides a binary for hubble
package hubble

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
		Name:       "hubble",
		GitHubRepo: "cilium/hubble",
		// https://github.com/cilium/hubble/releases/download/v1.17.5/hubble-linux-amd64.tar.gz
		GitHubFile: fmt.Sprintf("hubble-%s-%s.tar.gz", runtime.GOOS, runtime.GOARCH),
		VersionF:   binary.GithubLatest,
		IsTarGz:    true,
		// hubble v1.17.5@HEAD-13fb5dc compiled with go1.24.4 on linux/amd64
		VersionLocalF: func(b *binary.Binary) (string, error) {
			s, err := b.Exec("version")
			if err != nil {
				return "", err
			}
			v := strings.Split(s, " ")
			if len(v) < 2 {
				return "", fmt.Errorf("version not found")
			}
			v = strings.Split(v[1], "@")
			return v[0], nil
		},
	}
}
