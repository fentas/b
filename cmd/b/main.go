package main

import (
	"context"
	"fmt"
	"os"

	"github.com/fentas/b/pkg/binaries"
	"github.com/fentas/b/pkg/binaries/argsh"
	"github.com/fentas/b/pkg/binaries/b"
	"github.com/fentas/b/pkg/binaries/clusterctl"
	compose "github.com/fentas/b/pkg/binaries/docker-compose"
	"github.com/fentas/b/pkg/binaries/gh"
	"github.com/fentas/b/pkg/binaries/hcloud"
	"github.com/fentas/b/pkg/binaries/jq"
	"github.com/fentas/b/pkg/binaries/k9s"
	"github.com/fentas/b/pkg/binaries/kind"
	"github.com/fentas/b/pkg/binaries/kubectl"
	"github.com/fentas/b/pkg/binaries/kustomize"
	"github.com/fentas/b/pkg/binaries/mkcert"
	"github.com/fentas/b/pkg/binaries/stern"
	"github.com/fentas/b/pkg/binaries/tilt"
	"github.com/fentas/b/pkg/binaries/yq"
	"github.com/spf13/cobra"

	"github.com/fentas/b/pkg/binary"
	"github.com/fentas/b/pkg/cli"
	"github.com/fentas/goodies/streams"
)

// Magic variables set by goreleaser
var (
	version           = "v1.7.0" // x-release-please-version
	versionPreRelease = ""
)

func main() {
	o := &binaries.BinaryOptions{
		Context: context.Background(),
	}
	root := cli.NewCmdBinary(&cli.CmdBinaryOptions{
		Binaries: []*binary.Binary{
			argsh.Binary(o),
			b.Binary(o),
			compose.Binary(o),
			clusterctl.Binary(o),
			gh.Binary(o),
			hcloud.Binary(o),
			jq.Binary(o),
			k9s.Binary(o),
			kind.Binary(o),
			kubectl.Binary(o),
			kustomize.Binary(o),
			mkcert.Binary(o),
			stern.Binary(o),
			tilt.Binary(o),
			yq.Binary(o),
		},
		IO: &streams.IO{
			In:     os.Stdin,
			Out:    os.Stdout,
			ErrOut: os.Stderr,
		},
	})

	root.PersistentPreRun = func(cmd *cobra.Command, args []string) {
		if cmd.Flags().Changed("version") {
			if versionPreRelease != "" {
				version = fmt.Sprintf("%s-%s", version, versionPreRelease)
			}
			fmt.Printf("%s", version)
			os.Exit(0)
		}
	}
	flags := root.Flags()
	flags.BoolP("version", "v", false, "Print version information and quit")

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
