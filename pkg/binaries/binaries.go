package binaries

import (
	"context"

	"github.com/jedib0t/go-pretty/v6/progress"
)

type BinaryOptions struct {
	Context context.Context
	Tracker *progress.Tracker
	Envs    map[string]string
}
