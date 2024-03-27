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

### 🚀 Quickstart

If you have Go installed, you can build and install the latest version of `b` with:

```bash
go install github.com/buyoio/b/b@latest
```

> Binaries built in this way do not have the correct version embedded. Use our prebuilt binaries or check out [.goreleaser.yaml](./.goreleaser.yaml) to learn how to embed it yourself.

&nbsp;

### 📚 Import 

To use this package, you need to import it in your Go project:

```go
import "github.com/buyoio/b/pkg/binary"
```

The `Binary` struct represents a binary file, including its name, file path, version, and other related properties. You can create a `Binary` struct by providing the binary name and version:

```go
bin := binary.Binary{Name: "mybinary", Version: "1.0.0"}
bin.EnsureBinary()
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

### 🎯 Short term goals

- [ ] Recognize the operating system and architecture and offer the correct binary
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