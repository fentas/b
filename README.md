<h3 align="center">
	b
</h3>

<p align="center">
	<a href="https://github.com/buyoio/b/stargazers">
		<img alt="Stargazers" src="https://img.shields.io/github/stars/buyoio/b?style=for-the-badge&logo=starship&color=C9CBFF&logoColor=D9E0EE&labelColor=302D41"></a>
	<a href="https://github.com/buyoio/b/releases/latest">
		<img alt="Releases" src="https://img.shields.io/github/release/buyoio/b.svg?style=for-the-badge&logo=github&color=F2CDCD&logoColor=D9E0EE&labelColor=302D41"/></a>
	<a href="https://github.com/buyoio/b/issues">
		<img alt="Issues" src="https://img.shields.io/github/issues/buyoio/b?style=for-the-badge&logo=gitbook&color=B5E8E0&logoColor=D9E0EE&labelColor=302D41"></a>
</p>

&nbsp;

<p align="left">
`b` is binary or a go package that provides a set of utilities for managing and executing binary files. It is particularly useful for binaries hosted on GitHub.

The package includes a `Binary` struct that represents a binary file, including its name, file path, version, and other related properties. You can create a `Binary` struct by providing the binary name and version, and then use the `EnsureBinary` method to ensure that the binary is available on the system.
</p>

&nbsp;

### 🐾 How to use `b` as binary

```bash
# List all installed binaries and/or defined in b.yaml
b --all

# Print as JSON
b -ao json

# Install all binaries defined in b.yaml
b -a --install

# Install or update jq
b -iu jq

# Force install jq, overwriting existing binary
b -fi jq

# Upgrade all binaries
b -aiu

# List all available binaries
b --list

# Checks (silent) if all binaries are up to date
b -acq || echo "Some binaries are not up to date"
```

&nbsp;

### 🧾 Configuration, what to install

`b` needs one of three things defined to know where to install binaries to:

- `PATH_BIN` env, set to the directory where you want to install binaries.
- `PATH_BASE` env, set to the project root directory. All binaries will be installed in the `.bin` directory.
- If you are in a git repository, `b` will install binaries in the `.bin` directory in the root of the repository.

If none of these are set, `b` will fail.

To properly use the `--all` flag, you should create a `b.yaml` file in the binary directory. This file should contain a list of binaries you want to manage. Here is an example:

```yaml
jq:
  version: 1.7
kind:
tilt:
```

This will ensure that `jq`, `kind`, and `tilt` are installed and at the correct version. If you don't specify a version, `b` will install the latest version.

&nbsp;

### 🏗️ Manuell build

If you have Go installed, you can build and install the latest version of `b` with:

```bash
go install github.com/buyoio/b/b@latest
```

> Binaries built in this way do not have the correct version embedded. Use our prebuilt binaries or check out [.goreleaser.yaml](./.goreleaser.yaml) to learn how to embed it yourself.

&nbsp;

### 📚 How to use `b` as go import 

To use this package, you need to import it in your Go project:

```go
import "github.com/buyoio/b/pkg/binary"
```

The `Binary` struct represents a binary file, including its name, file path, version, and other related properties. You can create a `Binary` struct by providing the binary name and version:

```go
bin := binary.Binary{Name: "mybinary", Version: "1.0.0"}
bin.EnsureBinary(true)
```

Have a look into [pkg/binary](./pkg/binary/) for more details.

&nbsp;

### 📦 Prepackaged Binaries

Have a look into [pkg/binaries](./pkg/binaries/) for prepackaged binaries.

- `hcloud` - Hetzner Cloud CLI
- `jq` - Command-line JSON processor
- `k9s` - Kubernetes CLI to manage your clusters
- `kind` - Kubernetes IN Docker
- `kubectl` - Kubernetes CLI to manage your clusters
- `mkcert` - Create locally-trusted development certificates
- `tilt` - Local Kubernetes development with no stress
- `yq` - Command-line YAML processor

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

- [ ] Recognize the operating system and architecture and offer the correct binary
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

For more convoluted language, see the [LICENSE](https://github.com/buyoio/b/blob/main/LICENSE). Let's build a better Bash experience together.

&nbsp;

### ❤️ Gratitude

Thanks to all tools and projects that developing this project made possible.

&nbsp;

<p align="center">Copyright &copy; 2024-present <a href="https://github.com/buyoio" target="_blank">Buyo</a>
<p align="center"><a href="https://github.com/buyio/b/blob/main/LICENSE"><img src="https://img.shields.io/static/v1.svg?style=for-the-badge&label=License&message=MIT&logoColor=d9e0ee&colorA=302d41&colorB=b7bdf8"/></a></p>
