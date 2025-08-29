// Package argocd implements the argocd binary.
package argocd

import (
	"context"
	"fmt"
	"regexp"
	"runtime"

	"github.com/fentas/b/pkg/binaries"
	"github.com/fentas/b/pkg/binary"
)

var argocdVersionRegex = regexp.MustCompile(`argocd: (v[\.\d]+)`)

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
		Name:       "argocd",
		GitHubRepo: "argoproj/argo-cd",
		GitHubFile: fmt.Sprintf("argocd-%s-%s", runtime.GOOS, runtime.GOARCH),
		VersionF:   binary.GithubLatest,
		IsTarGz:    false,
		VersionLocalF: func(b *binary.Binary) (string, error) {
			b.Envs = map[string]string{"HOME": "/tmp"}
			s, err := b.Exec("version", "--client", "--short")
			if err != nil {
				return "", err
			}
			v := argocdVersionRegex.FindStringSubmatch(s)
			if len(v) != 2 {
				return "", fmt.Errorf("argocd version regex did not match")
			}
			return v[1], nil
		},
	}
}
