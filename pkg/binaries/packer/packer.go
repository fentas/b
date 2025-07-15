package packer

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
		Name:       "packer",
		GitHubRepo: "hashicorp/packer",
		URLF: func(b *binary.Binary) (string, error) {
			// https://releases.hashicorp.com/packer/1.13.1/packer_1.13.1_linux_amd64.zip
			return fmt.Sprintf(
				"https://releases.hashicorp.com/packer/%s/packer_%s_%s_%s.zip",
				b.Version[1:],
				b.Version[1:],
				runtime.GOOS,
				runtime.GOARCH,
			), nil
		},
		VersionF: binary.GithubLatest,
		IsZip:    true,
		VersionLocalF: func(b *binary.Binary) (string, error) {
			b.Envs["HOME"] = "/tmp"
			s, err := b.Exec("version")
			if err != nil {
				return "", err
			}
			v := strings.Split(s, " ")
			return v[len(v)-1], nil
		},
	}
}
