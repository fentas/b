# Migration Guide: B CLI v2.0

## Overview

The `b` CLI has been completely refactored from a flag-driven interface to a modern subcommand-based interface. This is a **breaking change** requiring a major version upgrade.

## What Changed

### Old CLI Structure (v1.x)
```bash
b [flags] [...binaries]
```

### New CLI Structure (v2.0)
```bash
b <subcommand> [flags] [args]
```

## Command Migration Reference

| Old Command | New Command | Alias | Description |
|-------------|-------------|-------|-------------|
| `b -iu <binary>` | `b install <binary>` | `b i <binary>` | Install specific binary |
| `b -fi <binary>` | `b install --force <binary>` | `b i --force <binary>` | Force install binary |
| `b -a --install` | `b install` | `b i` | Install all from config |
| `b -aiu` | `b update` | `b u` | Update all from config |
| `b --all` | `b list` | `b ls` | List project binaries |
| `b --list` | `b search` | `b s` | List all available binaries |
| `b -acq` | `b version --quiet --check` | `b v -q --check` | Check if up to date |

## New Features

### 1. Enhanced Commands
- **`b add`**: Add binaries to b.yaml without installing
- **`b init`**: Create new b.yaml configuration files
- **`b request`**: Request new binaries via GitHub issues

### 2. Context-Aware Configuration
- Automatic `b.yaml` discovery (walks up directory tree)
- Support for custom config paths with `--config`
- No more need for `--all` flag

### 3. Version Management
- Support for version pinning: `b add jq@1.7 --fix`
- Version specification during install: `b install kubectl@1.28.0`
- Better version checking and reporting

## Migration Examples

### Installing Binaries

**Old:**
```bash
# Install specific binary
b -iu jq

# Install all from config
b -a --install

# Force install
b -fi kubectl
```

**New:**
```bash
# Install specific binary
b install jq
# or
b i jq

# Install all from config
b install
# or
b i

# Force install
b install --force kubectl
# or
b i --force kubectl
```

### Managing Configuration

**Old:**
```bash
# List configured binaries
b --all

# List available binaries
b --list
```

**New:**
```bash
# List configured binaries
b list
# or
b ls

# List available binaries
b search
# or
b s

# Add binary to config
b add jq@1.7

# Initialize new config
b init
```

### Version Management

**Old:**
```bash
# Check if up to date (quiet)
b -acq
```

**New:**
```bash
# Check if up to date (quiet)
b version --quiet --check
# or
b v -q --check

# Show versions
b version
# or
b v

# Show specific binary version
b version jq
```

## Global Flags

All subcommands support these global flags:

| Flag | Description |
|------|-------------|
| `--config/-c <path>` | Custom configuration file path |
| `--force` | Force operations, overwriting existing binaries |
| `--quiet/-q` | Quiet mode |
| `--output <format>` | Output format (json, yaml) |
| `--help/-h` | Help information |

## Configuration Changes

### Enhanced b.yaml Discovery
The new CLI automatically searches for `b.yaml` files:
1. Current directory: `./b.yaml` or `./.bin/b.yaml`
2. Parent directories (walks up the tree)
3. Custom path via `--config` flag

### Version Pinning
You can now pin specific versions in your `b.yaml`:
```yaml
jq:
  version: "1.7"
  enforced: "1.7"  # Pins this version
kubectl:
  version: "latest"
  enforced: ""     # Allows updates
```

## Breaking Changes Summary

1. **Command Structure**: All operations now use subcommands instead of flags
2. **Flag Names**: Some flags have changed (e.g., `--all` is now implicit in `b list`)
3. **Configuration**: Enhanced config discovery may find different config files
4. **Exit Codes**: Some exit codes may have changed for consistency
5. **Output Format**: Default output format may differ slightly

## Recommended Migration Steps

1. **Update Scripts**: Replace all old commands with new subcommand equivalents
2. **Test Configuration**: Verify `b.yaml` files are discovered correctly
3. **Update Documentation**: Update any documentation to use new command syntax
4. **Training**: Familiarize team with new command structure and aliases

## Backward Compatibility

**There is no backward compatibility.** This is a breaking change requiring immediate migration of all scripts and workflows.

## Getting Help

- Use `b --help` for general help
- Use `b <subcommand> --help` for specific command help
- All commands support the `--help` flag

## Examples of Common Workflows

### Project Setup
```bash
# Initialize new project
mkdir my-project && cd my-project
b init

# Add tools to project
b add jq kubectl helm

# Install all project tools
b install
```

### Daily Development
```bash
# Check what's installed
b list

# Update everything
b update

# Install specific tool
b install --add terraform@1.5.0

# Search for tools
b search docker
```

### CI/CD Integration
```bash
# Install all project dependencies (quiet)
b install --quiet

# Check if everything is up to date
b version --quiet --check || exit 1
```

This migration guide should help you transition smoothly to the new CLI interface. The new structure is more intuitive, powerful, and follows modern CLI design patterns.
