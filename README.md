<h3 align="center">
	<img width="130" alt="Stargazers" src="./logo.svg">
</h3>

<p align="center">
	<a href="https://github.com/fentas/b/stargazers">
		<img alt="Stargazers" src="https://img.shields.io/github/stars/fentas/b?style=for-the-badge&logo=starship&color=C9CBFF&logoColor=D9E0EE&labelColor=302D41"></a>
	<a href="https://github.com/fentas/b/releases/latest">
		<img alt="Releases" src="https://img.shields.io/github/release/fentas/b.svg?style=for-the-badge&logo=github&color=F2CDCD&logoColor=D9E0EE&labelColor=302D41"/></a>
	<a href="https://github.com/fentas/b/issues">
		<img alt="Issues" src="https://img.shields.io/github/issues/fentas/b?style=for-the-badge&logo=gitbook&color=B5E8E0&logoColor=D9E0EE&labelColor=302D41"></a>
	<a href="https://codecov.io/gh/fentas/b">
		<img alt="Coverage" src="https://img.shields.io/codecov/c/github/fentas/b?style=for-the-badge&logo=codecov&color=DDB6F2&logoColor=D9E0EE&labelColor=302D41"></a>
	<a href="https://pkg.go.dev/github.com/fentas/b">
		<img alt="Go Reference" src="https://img.shields.io/badge/go-reference-blue?style=for-the-badge&logo=go&color=89DCEB&logoColor=D9E0EE&labelColor=302D41"></a>
</p>

&nbsp;

<p align="left">
	
`b` is a binary manager and environment file syncer for development projects. It manages binary installations from GitHub/GitLab releases and syncs configuration files from upstream git repositories.

Features:

- **30+ pre-packaged binaries** (kubectl, k9s, jq, helm, etc.) with auto-detection
- **Install any GitHub/GitLab release** via `b install github.com/org/repo`
- **Sync env files** from git repos with glob matching and three-way merge
- **Lockfile** (`b.lock`) for reproducible installations with SHA256 verification
- **direnv integration** for per-project binary management

</p>

&nbsp;

### 🐾 How to use `b` as a binary

```bash
# Initialise a new project with b.yaml config and direnv
b init

# List all configured binaries and envs
b list

# Install pre-packaged binaries
b install jq kubectl helm

# Install any GitHub/GitLab release (auto-detected)
b install github.com/derailed/k9s
b install github.com/sharkdp/bat@v0.24.0

# Install and add to b.yaml
b install --add jq@1.7

# Sync env files from a git repository (SCP-style)
b install github.com/org/infra:/manifests/hetzner/** /hetzner
b install github.com/org/infra@v2.0:/manifests/base/** .

# Update all binaries and envs
b update

# Update with merge strategy (three-way merge on local changes)
b update --strategy=merge

# Update keeping local changes
b update --strategy=client

# Search for available binaries
b search terraform

# Show versions (with remote update check for envs)
b version
b version --local  # skip remote checks

# Verify installed artifacts against b.lock checksums
b verify

# Manage git cache
b cache clean  # remove all cached repos

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

Create a `b.yaml` file in the binary directory to declare what to install. Here is an example:

```yaml
binaries:
  jq:
    version: jq-1.8.1    # pin version
  kind:
  tilt:
  envsubst:
    alias: renvsubst      # alias to renvsubst
  kubectl:
    file: ../kc           # custom path (relative to config)
  # Install any GitHub release by ref
  github.com/sharkdp/bat:
    version: v0.24.0

envs:
  # Sync files from upstream git repos
  github.com/org/infra:
    version: v2.0          # pin to tag/branch (default: HEAD)
    strategy: merge         # replace (default) | client | merge
    ignore:
      - "*.md"
    files:
      manifests/base/**:   # glob pattern
        dest: base/         # local destination
      manifests/hetzner/**:
        dest: hetzner/
  # Minimal env entry (sync all files, default settings)
  github.com/org/shared-config:
```

**Binaries:** If you don't specify a version, `b` will install the latest. Custom file paths can be relative (resolved from config location) or absolute.

**Envs:** Sync configuration files from upstream git repositories. Strategy controls how local changes are handled during updates:

- **replace** (default): Overwrite with upstream. Interactive prompt on TTY when local changes detected.
- **client**: Keep local files when modified, skip upstream.
- **merge**: Three-way merge via `git merge-file`. Conflict markers inserted on failure.

&nbsp;

### 🐳 Using Docker

You can run `b` using Docker without installing it locally:

```bash
# Run b with volume mount to access .bin directory
docker run --rm -v ./.bin:/.bin ghcr.io/fentas/b list

# Install binaries using Docker
docker run --rm -v ./.bin:/.bin ghcr.io/fentas/b install b jq kubectl
```

#### Using in your own Dockerfile

You can also copy the `b` binary into your own Docker images:

```dockerfile
FROM alpine:latest

# Copy the b binary from the official image
COPY --from=ghcr.io/fentas/b:latest /b /usr/local/bin/b

# Install binaries during build
ENV PATH_BIN=/usr/local/bin
RUN b install curl jq

# Your application code
COPY . /app
WORKDIR /app
```

&nbsp;

### 📚 Documentation

All related documentation, including the source for the website, is located on the [`docs` branch](https://github.com/fentas/b/tree/docs).

&nbsp;

### 🏗️ Manual build

If you have Go installed, you can build and install the latest version of `b` with:

```bash
go install github.com/fentas/b/b@latest
```

> Binaries built in this way do not have the correct version embedded. Use our prebuilt binaries or check out [.goreleaser.yaml](./.goreleaser.yaml) to learn how to embed it yourself.

&nbsp;

### 🧬 How to use `b` as go import 

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

- [argocd](https://github.com/argoproj/argo-cd) - Declarative Continuous Deployment for Kubernetes
- [argsh](https://github.com/arg-sh/argsh) - Utilities for Bash script quality
- `b` - (Selfupdate) Manage and execute binary files
- [cilium](https://github.com/cilium/cilium-cli) - Providing, securing, and observing network connectivity between workloads
- [clusterctl](https://github.com/kubernetes-sigs/cluster-api) - Kubernetes cluster lifecycle management
- [curl](https://github.com/stunnel/static-curl) - Command-line tool for transferring data with URL syntax
- [docker-compose](https://github.com/docker/compose) - Define and run multi-container Docker applications
- [gh](https://github.com/cli/cli) - GitHub CLI wrapper
- [hcloud](https://github.com/hetznercloud/cli) - Hetzner Cloud CLI wrapper
- [hubble](https://github.com/cilium/hubble) - Fully distributed networking and security observability platform
- [jq](https://github.com/jqlang/jq) - Command-line JSON processor
- [k9s](https://github.com/derailed/k9s) - Kubernetes CLI to manage your clusters
- [khelm](https://github.com/mgoltzsche/khelm) - A Helm chart templating CLI, kustomize plugin and containerized kustomize/kpt KRM function
- [kind](https://github.com/kubernetes-sigs/kind) - Kubernetes IN Docker
- [kubectl](https://github.com/kubernetes/kubectl) - Kubernetes CLI to manage your clusters
- [kubectl-cnpg](https://github.com/cloudnative-pg/cloudnative-pg) - CloudNativePG kubectl plugin to manage PostgreSQL databases
- [kubelogin - int128](https://github.com/int128/kubelogin) - kubectl plugin for Kubernetes OpenID Connect authentication (kubectl oidc-login)
- [kubeseal](https://github.com/bitnami-labs/sealed-secrets) - A Kubernetes controller and tool for one-way encrypted Secrets
- [kustomize](https://github.com/kubernetes-sigs/kustomize) - Kubernetes native configuration management
- [mkcert](https://github.com/FiloSottile/mkcert) - Create locally-trusted development certificates
- [packer](https://github.com/hashicorp/packer) - Packer is a tool for creating machine and container images
- [renvsubst](https://github.com/containeroo/renvsubst) - envsubst with some extra features written in Rust
- [sops](https://github.com/getsops/sops) - Secure processing of configuration files
- [ssh-to-age](https://github.com/Mic92/ssh-to-age) - Convert SSH Ed25519 keys to age keys
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

This is all you need; alternatively, you can refer to [here](./.envrc).

&nbsp;

### 🎯 Short term goals

- [ ] Windows support (OS/arch detection improvements)
- [ ] Advanced configurations (proxy, custom registries)
- [ ] Upstream `b.yaml` discovery (auto-detect file groups from repos)

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

<p align="center">Copyright &copy; 2024-present <a href="https://github.com/fentas" target="_blank">fentas</a>
<p align="center"><a href="https://github.com/fentas/b/blob/main/LICENSE"><img src="https://img.shields.io/static/v1.svg?style=for-the-badge&label=License&message=MIT&logoColor=d9e0ee&colorA=302d41&colorB=b7bdf8"/></a></p>
