// Package sshtoage binary converts SSH keys to Age keys.
package sshtoage

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
		Name:       "ssh-to-age",
		GitHubRepo: "Mic92/ssh-to-age",
		GitHubFile: fmt.Sprintf("ssh-to-age.%s-%s", runtime.GOOS, runtime.GOARCH),
		VersionF:   binary.GithubLatest,
		IsTarGz:    false,
		VersionLocalF: func(b *binary.Binary) (string, error) {
			// ssh-to-age -version prints a bare version like "1.2.0".
			// GithubLatest returns "v1.2.0", so prepend "v" for comparison.
			s, err := b.Exec("-version")
			if err != nil {
				return "", err
			}
			v := strings.TrimSpace(s)
			if v != "" && !strings.HasPrefix(v, "v") {
				v = "v" + v
			}
			return v, nil
		},
	}
}
