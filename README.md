<h3 align="center">
	b
</h3>

<p align="center">
	<a href="https://github.com/fentas/b/stargazers">
		<img alt="Stargazers" src="https://img.shields.io/github/stars/fentas/b?style=for-the-badge&logo=starship&color=C9CBFF&logoColor=D9E0EE&labelColor=302D41"></a>
	<a href="https://github.com/fentas/b/releases/latest">
		<img alt="Releases" src="https://img.shields.io/github/release/fentas/b.svg?style=for-the-badge&logo=github&color=F2CDCD&logoColor=D9E0EE&labelColor=302D41"/></a>
	<a href="https://github.com/fentas/b/issues">
		<img alt="Issues" src="https://img.shields.io/github/issues/fentas/b?style=for-the-badge&logo=gitbook&color=B5E8E0&logoColor=D9E0EE&labelColor=302D41"></a>
</p>

&nbsp;

<p align="left">
`b` is a binary or a Go package that provides a set of utilities for managing and executing binary files. It is particularly useful for binaries hosted on GitHub.

The package includes a `Binary` struct that represents a binary file, including its name, file path, version, and other related properties. You can create a `Binary` struct by providing the binary name and version, and then use the `EnsureBinary` method to ensure that the binary is available on the system.
</p>

&nbsp;

### 🐾 How to use `b` as a binary

```bash
# Initialise a new project with b.yaml config and direnv
b init

# List all configured binaries
b list
b ls

# Install specific binaries
b install jq
b i kubectl helm

# Install and add binary to config
b install --add jq@1.7

# Update all binaries
b update
b u tilt

# Update specific binaries
b update jq kubectl

# Search for available binaries
b search terraform
b s kube

# Show version
b version
b v kind

# Request a new binary
b request
```

&nbsp;

### 🧾 Configuration, what to install

`b` needs one of three things defined to know where to install binaries:

- `PATH_BIN` env, set to the directory where you want to install binaries.
- `PATH_BASE` env, set to the project root directory. All binaries will be installed in the `.bin` directory.
- If you are in a git repository, `b` will install binaries in the `.bin` directory in the root of the repository.

If none of these are set, `b` will fail.

To properly use the `--all` flag, you should create a `b.yaml` file in the binary directory. This file should contain a list of binaries you want to manage. Here is an example:

```yaml
jq:
  # pin version
  version: jq-1.8.1
kind:
tilt:
```

This will ensure that `jq`, `kind`, and `tilt` are installed and at the correct version. If you don't specify a version, `b` will install the latest version.

&nbsp;

### 🏗️ Manual build

If you have Go installed, you can build and install the latest version of `b` with:

```bash
go install github.com/fentas/b/b@latest
```

> Binaries built in this way do not have the correct version embedded. Use our prebuilt binaries or check out [.goreleaser.yaml](./.goreleaser.yaml) to learn how to embed it yourself.

&nbsp;

### 📚 How to use `b` as go import 

To use this package, you need to import it into your Go project:

```go
import "github.com/fentas/b/pkg/binary"
```

The `Binary` struct represents a binary file, including its name, file path, version, and other related properties. You can create a `Binary` struct by providing the binary name and version:

```go
bin := binary.Binary{Name: "mybinary", Version: "1.0.0"}
bin.EnsureBinary(true)
```

Have a look at [pkg/binary](./pkg/binary/) for more details.

&nbsp;

### 📦 Prepackaged Binaries

Have a look at [pkg/binaries](./pkg/binaries/) for prepackaged binaries.

- [argsh](https://github.com/arg-sh/argsh) - Utilities for Bash script quality
- `b` - (Selfupdate) Manage and execute binary files
- [clusterctl](https://github.com/kubernetes-sigs/cluster-api) - Kubernetes cluster lifecycle management
- [docker-compose](https://github.com/docker/compose) - Define and run multi-container Docker applications
- [gh](https://github.com/cli/cli) - GitHub CLI wrapper
- [hcloud](https://github.com/hetznercloud/cli) - Hetzner Cloud CLI wrapper
- [jq](https://github.com/jqlang/jq) - Command-line JSON processor
- [k9s](https://github.com/derailed/k9s) - Kubernetes CLI to manage your clusters
- [kind](https://github.com/kubernetes-sigs/kind) - Kubernetes IN Docker
- [kubectl](https://github.com/kubernetes/kubectl) - Kubernetes CLI to manage your clusters
- [kubeseal](https://github.com/bitnami-labs/sealed-secrets) - A Kubernetes controller and tool for one-way encrypted Secrets
- [kustomize](https://github.com/kubernetes-sigs/kustomize) - Kubernetes native configuration management
- [mkcert](https://github.com/FiloSottile/mkcert) - Create locally-trusted development certificates
- [packer](https://github.com/hashicorp/packer) - Packer is a tool for creating machine and container images
- [sops](https://github.com/getsops/sops) - Secure processing of configuration files
- [stern](https://github.com/stern/stern) - Simultaneous log tailing for multiple Kubernetes pods and containers
- [tilt](https://github.com/tilt-dev/tilt) - Local Kubernetes development with no stress
- [yq](https://github.com/mikefarah/yq) - Command-line YAML processor

Feel free to extend this, PRs are welcome.

&nbsp;

### 🧙‍♂️ Magic, use direnv

Using [direnv](https://direnv.net/) allows you to load required binaries bound to a specific project.

```bash
#!/usr/bin/env bash
set -euo pipefail

: "${PATH_BASE:="$(git rev-parse --show-toplevel)"}"
: "${PATH_BIN:="${PATH_BASE}/.bin"}"
export PATH_BASE PATH_BIN
```

This is all you need or have a look [here](./.envrc).

&nbsp;

### 🎯 Short term goals

- [ ] Recognise the operating system and architecture and offer the correct binary
- [ ] Enforce min and max versions
- [ ] Create a logo
- [ ] Docs
- [ ] Tests

&nbsp;

### 📜 License

`b` is released under the MIT license, which grants the following permissions:

- Commercial use
- Distribution
- Modification
- Private use

For more convoluted language, see the [LICENSE](https://github.com/fentas/b/blob/main/LICENSE). Let's build a better Bash experience together.

&nbsp;

### ❤️ Gratitude

Thanks to all tools and projects that developing this project made possible.

&nbsp;

<p align="center">Copyright &copy; 2024-present <a href="https://github.com/fentas" target="_blank">Fentas</a>
<p align="center"><a href="https://github.com/fentas/b/blob/main/LICENSE"><img src="https://img.shields.io/static/v1.svg?style=for-the-badge&label=License&message=MIT&logoColor=d9e0ee&colorA=302d41&colorB=b7bdf8"/></a></p>
