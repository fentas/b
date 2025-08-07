---
sidebar_label: "b contribution"
sidebar_position: 2
---

# Contribute by improving b

In this document, you'll learn how you can contribute to b by improving the codebase.

## Overview

b is a binary manager for developers that simplifies installation, versioning, and management of command-line tools. It is written in Go and is used to build the core functionality of b.

### Binaries

Binaries are the actual command-line tools that are managed by b. They are stored in the `binaries` directory in the b repository and registered in `cmd/b/main.go`.

```
├── cmd
│   └── b
│       └── # All supported binaries are added here
├── pkg
│   ├── binaries
│   │   └── # Binary definitions
│   ├── binary
│   │   └── # Binary management and download logic
│   ├── cli
│   │   └── # CLI subcommands
│   ├── path
│   │   └── # PATH management
│   └── state
│       └── # State management
└── test
    ├── e2e
    │   └── # End-to-end tests
    └── testutil
        └── # Test utilities
```

## How to contribute

If you’re adding a new library or contributing to the codebase, you need to fork the repository, create a new branch, and make all changes necessary in your repository. Then, once you’re done, create a PR in the b repository.

### Base Branch

When you make an edit to an existing documentation page or fork the repository to make changes to the documentation, you have to create a new branch.

Documentation contributions always use `main` as the base branch. Make sure to also open your PR against the `main` branch.

### Pull Request Conventions

When you create a pull request, prefix the title with `fix:`, `feat:` or `docs:`.

<!-- vale off -->

In the body of the PR, explain clearly what the PR does. If the PR solves an issue, use [closing keywords](https://docs.github.com/en/issues/tracking-your-work-with-issues/linking-a-pull-request-to-an-issue#linking-a-pull-request-to-an-issue-using-a-keyword) with the issue number. For example, “Closes #1333”.

<!-- vale on -->


## Testing

Feel free to add tests if you think it's necessary.

### Coverage

We strive to have 100% test coverage. When you add a new library or make changes to an existing library, make sure to add tests that cover all functionality.

## Linting

To lint the code, run the following command:

```bash
gofmt -w .
```
