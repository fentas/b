# Proposal: A Redesigned, Streamlined CLI for `b`

## 1. Introduction

This proposal outlines a plan to refactor and streamline the command-line interface (CLI) for `b`. The current flag-driven system is functional but can be confusing for new users and is less scalable for future development.

The goal is to evolve the CLI to a more intuitive, powerful, and predictable interface by adopting modern CLI design patterns. This includes introducing verb-based subcommands, making the tool context-aware, and providing short aliases for power users.

## 2. Core Principles

The new design is guided by three core principles:

1.  **Command-Driven, Not Flag-Driven**: Actions will be initiated via clear subcommands (e.g., `b install`). This makes user intent explicit and the tool easier to learn.
2.  **Context-Aware by Default**: Commands will automatically detect and operate on a `b.yaml` file in the current directory tree. This removes the need for the `--all` flag and simplifies the most common use cases.
3.  **Brevity for Power Users**: Short, memorable aliases (e.g., `b i` for `b install`) will be available for frequent, interactive use.

## 3. Proposed Command Structure

### Main Commands & Aliases

| Full Command | Alias(es) | Description                                                                                                          |
| :----------- | :-------- | :------------------------------------------------------------------------------------------------------------------- |
| `install`    | `i`       | If an argument is given, installs that specific binary. If no argument is given, it installs all binaries from `b.yaml`. `--add` flag adds it to b.yaml. |
| `add` |  | Adds a binary to b.yaml without installing it. You can specify a version with `@`, e.g. `jq@1.7` or `jq@latest` (default). `--fix` adds the given version to b.yaml. `-i` or `--install` will directly install it. |
| `update`     | `u`       | If an argument is given, updates that specific binary. If no argument is given, updates all binaries from `b.yaml`. |
| `list`       | `ls`, `l` | Lists all binaries defined in the project's `b.yaml` and their installation status.                                  |
| `search`     | `s`       | Discovers all binaries available for installation. Can be filtered with a query.                                     |
| `init`       |           | Creates a new `.bin/b.yaml` configuration file in the current directory (ENV Variables have precedence).                                                  |
| `version` | `v` | List all versions. If an argument is given, it just shows the version of the binary. With `--local` it will only show the local version, no lookup for new version. `-q` or `--quite` will lookup versions and fail (exit code) if not all, or the specified binary, is not up to date. Note, if a version is pinned (fixed), it will not fail if a newer version is available. |

### Global Flags

| Flag                            | Description                                                                                  |
| :------------------------------ | :------------------------------------------------------------------------------------------- |
| `--config <path>`, `-c <path>`  | Specifies a path to a configuration file, overriding the automatic `b.yaml` discovery. |
| `--force`                       | Forces an action, such as overwriting an existing binary during installation.                |
| `--output <format>`             | Specifies an output format (e.g., `json`) for commands like `list` and `search`.             |
| `--help`, `-h`                  | Displays help information for the main command or any subcommand.                            |
| `--quite`, `-q` | No output. Quite mode. |

## 4. Comparison: Old vs. New

| Action                       | Old Command        | **New Command** | **Alias** |
| :--------------------------- | :----------------- | :---------------------------- | :---------------------- |
| Install/Update one binary    | `b -iu <binary>`   | `b install <binary>`          | `b i <binary>`          |
| Force install one binary     | `b -fi <binary>`   | `b install --force <binary>`  | `b i --force <binary>`  |
| Install all from config      | `b -a --install`   | `b install`                   | `b i`                   |
| Update all from config       | `b -aiu`           | `b update` [binary]                   | `b u`                   |
| List project binaries        | `b --all`          | `b list`                      | `b ls`                  |
| List all available binaries  | `b --list`         | `b search`                    | `b s`                   |

## 5. Example User Workflow

Here’s how a user might interact with the new CLI:

1.  **Initialise a project:**
    ```sh
    mkdir my-new-project && cd my-new-project
    b init
    # -> Creates a b.yaml file
    ```

2.  **Add tools to the project's config file and install everything:**
    *User manually adds `jq` and `shfmt` to `b.yaml`.*
    ```sh
    b i
    # -> Reads b.yaml and installs jq and shfmt
    ```

3.  **List the project's managed binaries:**
    ```sh
    b ls
    # -> Shows that jq and shfmt are installed for this project
    ```

4.  **Discover a new tool and install it without adding to `b.yaml`:**
    ```sh
    b s fzf
    # -> Confirms that 'fzf' is an available binary
    b i fzf
    # -> Installs fzf without modifying the project's b.yaml
    ```

5.  **Update all project dependencies:**
    ```sh
    b u
    # -> Checks for new versions of jq and shfmt
    ```

## 6. Recommendation for Documentation and Scripts

To ensure clarity for all users, a clear guideline should be established:

**Official documentation and shared scripts should always use the full command names** (e.g., `b install`).

Aliases are an enhancement intended for personal, interactive use at the command line. This practice ensures that scripts and tutorials remain readable and easy to understand for everyone, regardless of their familiarity with the aliases.
