package argsh

import (
	"context"
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
		Name:       "argsh",
		GitHubRepo: "arg-sh/argsh",
		GitHubFile: "argsh",
		VersionF:   binary.GithubLatest,
		VersionLocalF: func(b *binary.Binary) (string, error) {
			// argsh prints 'argsh v0.6.6 (<sha>)' to stdout for --version.
			// Parse the second whitespace-separated token so b's version
			// compare sees 'v0.6.6' instead of the commit sha that the
			// old 'strings.Split(s, " ")[last]' yielded.
			s, err := b.Exec("--version")
			if err != nil {
				return "", err
			}
			fields := strings.Fields(s)
			if len(fields) < 2 {
				return "", nil
			}
			return fields[1], nil
		},
	}
}
