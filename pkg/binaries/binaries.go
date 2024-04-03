package binaries

import (
	"context"

	"github.com/jedib0t/go-pretty/v6/progress"
)

type BinaryOptions struct {
	Context context.Context
	Version string
	Tracker *progress.Tracker
	Envs    map[string]string
}

func Arch(arch string) string {
	if arch == "amd64" {
		return "x86_64"
	}
	return arch
}
