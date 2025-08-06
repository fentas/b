package main

import (
	"context"
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
	"github.com/fentas/b/pkg/binaries/kubeseal"
	"github.com/fentas/b/pkg/binaries/kustomize"
	"github.com/fentas/b/pkg/binaries/mkcert"
	"github.com/fentas/b/pkg/binaries/packer"
	"github.com/fentas/b/pkg/binaries/sops"
	"github.com/fentas/b/pkg/binaries/stern"
	"github.com/fentas/b/pkg/binaries/tilt"
	"github.com/fentas/b/pkg/binaries/yq"

	"github.com/fentas/b/pkg/binary"
	"github.com/fentas/b/pkg/cli"
	"github.com/fentas/goodies/streams"
)

// Magic variables set by goreleaser
var (
	version           = "v2.0.0" // x-release-please-version
	versionPreRelease = ""
)

func main() {
	o := &binaries.BinaryOptions{
		Context: context.Background(),
	}

	binaries := []*binary.Binary{
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
		kubeseal.Binary(o),
		kustomize.Binary(o),
		mkcert.Binary(o),
		packer.Binary(o),
		sops.Binary(o),
		stern.Binary(o),
		tilt.Binary(o),
		yq.Binary(o),
	}

	io := &streams.IO{
		In:     os.Stdin,
		Out:    os.Stdout,
		ErrOut: os.Stderr,
	}

	if err := cli.Execute(binaries, io, version, versionPreRelease); err != nil {
		os.Exit(1)
	}
}
