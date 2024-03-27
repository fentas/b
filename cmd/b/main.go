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
	version string = "dev" // x-release-please-version
)

func main() {
	o := &binaries.BinaryOptions{
		Context: context.Background(),
	}
	root := cli.NewCmdBinary(&cli.CmdBinaryOptions{
		Binaries: []*binary.Binary{
			hcloud.NewHcloud(o),
			jq.NewJq(o),
			k9s.NewK9s(o),
			kind.NewKind(o),
			kubectl.NewKubectl(o),
			mkcert.NewMkcert(o),
			tilt.NewTilt(o),
			yq.NewYq(o),
		},
		IO: &streams.IO{
			In:     os.Stdin,
			Out:    os.Stdout,
			ErrOut: os.Stderr,
		},
	})

	root.PersistentPreRun = func(cmd *cobra.Command, args []string) {
		if cmd.Flags().Changed("version") {
			fmt.Printf("b %s\n", version)
			os.Exit(0)
		}
	}
	flags := root.Flags()
	flags.BoolP("version", "v", false, "Print version information and quit")

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
