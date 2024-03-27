package main

import (
	"context"
	"fmt"
	"os"

	"github.com/buyoio/b/pkg/binaries"
	"github.com/buyoio/b/pkg/binaries/hcloud"
	"github.com/buyoio/b/pkg/binaries/jq"
	"github.com/buyoio/b/pkg/binaries/k9s"
	"github.com/buyoio/b/pkg/binaries/kind"
	"github.com/buyoio/b/pkg/binaries/kubectl"
	"github.com/buyoio/b/pkg/binaries/mkcert"
	"github.com/buyoio/b/pkg/binaries/tilt"
	"github.com/buyoio/b/pkg/binaries/yq"
	"github.com/spf13/cobra"

	"github.com/buyoio/b/pkg/binary"
	"github.com/buyoio/b/pkg/cli"
	"github.com/buyoio/goodies/streams"
)

// Magic variables set by goreleaser
var (
	version           = "0.0.1" // x-release-please-version
	versionPreRelease = "dev"
)

func main() {
	o := &binaries.BinaryOptions{
		Context: context.Background(),
	}
	root := cli.NewCmdBinary(&cli.CmdBinaryOptions{
		Binaries: []*binary.Binary{
			hcloud.Binary(o),
			jq.Binary(o),
			k9s.Binary(o),
			kind.Binary(o),
			kubectl.Binary(o),
			mkcert.Binary(o),
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
