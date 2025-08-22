// Package sshtoage binary converts SSH keys to Age keys.
package sshtoage

import (
	"context"
	"crypto/sha256"
	"fmt"
	"runtime"

	"github.com/fentas/b/pkg/binaries"
	"github.com/fentas/b/pkg/binary"
)

// workaround for version lookup
// wait for fix https://github.com/Mic92/ssh-to-age/issues/180
// or get binary sha https://github.com/fentas/b/issues/76
var versions = map[string]string{
	"73513156cc8821915ff96b83a9a5780a2993199497c2a3106795de1c54429578": "1.2.0",
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
		Name:       "ssh-to-age",
		GitHubRepo: "Mic92/ssh-to-age",
		GitHubFile: fmt.Sprintf("ssh-to-age.%s-%s", runtime.GOOS, runtime.GOARCH),
		VersionF:   binary.GithubLatest,
		IsTarGz:    false,
		VersionLocalF: func(b *binary.Binary) (string, error) {
			s, err := b.Exec("-h")
			if err != nil {
				return "", err
			}
			// create hash from output
			hash := sha256.New()
			if _, err := hash.Write([]byte(s)); err != nil {
				return "", err
			}
			v := fmt.Sprintf("%x", hash.Sum(nil))
			if _, ok := versions[v]; !ok {
				return v, nil
			}
			return versions[v], nil
		},
	}
}
