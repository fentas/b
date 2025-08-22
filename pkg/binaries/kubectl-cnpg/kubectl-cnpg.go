// Package kubectlcnpg binary provides a kubectl plugin for managing CNPG resources.
package kubectlcnpg

import (
	"context"
	"fmt"
	"regexp"
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
		Name:       "kubectl-cnpg",
		GitHubRepo: "cloudnative-pg/cloudnative-pg",
		URLF: func(b *binary.Binary) (string, error) {
			// https://github.com/cloudnative-pg/cloudnative-pg/releases/download/v1.27.0/kubectl-cnpg_1.27.0_linux_x86_64.tar.gz
			return fmt.Sprintf(
				"https://github.com/cloudnative-pg/cloudnative-pg/releases/download/%s/kubectl-cnpg_%s_%s_%s.tar.gz",
				b.Version,
				b.Version[1:],
				runtime.GOOS,
				binaries.Arch(runtime.GOARCH),
			), nil
		},
		VersionF: binary.GithubLatest,
		IsTarGz:  true,
		VersionLocalF: func(b *binary.Binary) (string, error) {
			// Build: {Version:1.27.0 Commit:8b442dcc3 Date:2025-08-12}
			s, err := b.Exec("version")
			if err != nil {
				return "", err
			}
			v := regexp.MustCompile(`Version:([\d.]+)`).FindStringSubmatch(s)
			if len(v) < 2 {
				return "", fmt.Errorf("version not found")
			}
			return "v" + v[1], nil
		},
	}
}
