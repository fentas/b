package argsh

import (
	"context"
	"strings"

	"github.com/buyoio/b/pkg/binaries"
	"github.com/buyoio/b/pkg/binary"
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
		Name:       "argsh",
		GitHubRepo: "arg-sh/argsh",
		GitHubFile: "argsh",
		VersionF:   binary.GithubLatest,
		VersionLocalF: func(b *binary.Binary) (string, error) {
			s, err := b.Exec("version")
			if err != nil {
				return "", err
			}
			v := strings.Split(s, " ")
			return v[len(v)-1], nil
		},
	}
}
