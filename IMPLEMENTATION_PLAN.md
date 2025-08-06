# B CLI Refactor Implementation Plan

## Overview

This document outlines the detailed implementation plan for refactoring the `b` CLI from a flag-driven interface to a modern subcommand-based interface as specified in `refactor.md`.

## Current Architecture Analysis

### Existing Structure
- **Entry Point**: `cmd/b/main.go` - Uses cobra CLI framework
- **Main CLI Logic**: `pkg/cli/cli.go` - Contains `CmdBinaryOptions` and `NewCmdBinary()`
- **Helper Functions**: `pkg/cli/helper.go` - Binary installation and lookup logic
- **Binary Management**: `pkg/binary/` - Core binary operations
- **Binary Definitions**: `pkg/binaries/` - Individual binary implementations
- **Configuration**: `pkg/state/` - Handles `b.yaml` loading and management

### Current Command Structure
```bash
b [flags] [...binaries]
```

Key flags: `--all/-a`, `--install/-i`, `--upgrade/-u`, `--force/-f`, `--list`, `--check/-c`, `--quiet/-q`

## Target Architecture

### New Command Structure
```bash
b <subcommand> [flags] [args]
```

### Subcommands to Implement

| Command | Aliases | Description | Implementation Priority |
|---------|---------|-------------|------------------------|
| `install` | `i` | Install binaries (with/without args) | High |
| `add` | - | Add binary to b.yaml without installing | High |
| `update` | `u` | Update binaries | High |
| `list` | `ls`, `l` | List project binaries and status | High |
| `search` | `s` | Search available binaries | Medium |
| `init` | - | Create new b.yaml config | Medium |
| `version` | `v` | Show version information | Low |
| `request` |  | Request a binary. Github issues template, prefilled and labeld. | Low |

### Global Flags
- `--config/-c <path>` - Custom config file path
- `--force` - Force operations
- `--output <format>` - Output format (json, yaml)
- `--help/-h` - Help information
- `--quiet/-q` - Quiet mode

## Implementation Strategy

### Phase 1: Core Infrastructure (High Priority)

#### 1.1 Create New Subcommand Structure
- **File**: `pkg/cli/root.go`
- **Purpose**: New root command with subcommand registration
- **Tasks**:
  - Create `NewRootCmd()` function
  - Register all subcommands
  - Handle global flags
  - Maintain version handling logic

#### 1.2 Refactor Existing CLI Logic
- **File**: `pkg/cli/cli.go` → Split into multiple files
- **Tasks**:
  - Extract reusable logic from `CmdBinaryOptions`
  - Create shared options struct for common functionality
  - Preserve existing binary operations logic

#### 1.3 Context-Aware Configuration
- **File**: `pkg/state/config.go` (new)
- **Tasks**:
  - Implement automatic `b.yaml` discovery (walk up directory tree)
  - Add support for `--config` flag override
  - Enhance error handling for missing configs

### Phase 2: Core Subcommands (High Priority)

#### 2.1 Install Command (`b install` / `b i`)
- **File**: `pkg/cli/install.go`
- **Functionality**:
  - `b install` - Install all from b.yaml
  - `b install <binary>` - Install specific binary
  - `--add` flag - Add to b.yaml during install
  - Support version specification: `b install jq@1.7`
- **Reuse**: Existing `installBinaries()` logic from helper.go

#### 2.2 Add Command (`b add`)
- **File**: `pkg/cli/add.go`
- **Functionality**:
  - `b add <binary>` - Add to b.yaml without installing
  - `b add <binary>@<version>` - Add specific version
  - `--fix` flag - Pin version in b.yaml
  - `--install/-i` flag - Install after adding

#### 2.3 Update Command (`b update` / `b u`)
- **File**: `pkg/cli/update.go`
- **Functionality**:
  - `b update` - Update all from b.yaml
  - `b update <binary>` - Update specific binary
- **Reuse**: Existing update logic with `o.update = true`

#### 2.4 List Command (`b list` / `b ls` / `b l`)
- **File**: `pkg/cli/list.go`
- **Functionality**:
  - Show binaries from b.yaml with installation status
  - Support `--output` flag for JSON/YAML
- **Reuse**: Existing `lookupLocals()` logic

### Phase 3: Additional Subcommands (Medium Priority)

#### 3.1 Search Command (`b search` / `b s`)
- **File**: `pkg/cli/search.go`
- **Functionality**:
  - `b search` - List all available binaries
  - `b search <query>` - Filter by query
- **Reuse**: Existing `--list` functionality

#### 3.2 Init Command (`b init`)
- **File**: `pkg/cli/init.go`
- **Functionality**:
  - Create new `.bin/b.yaml` in current directory
  - Handle existing file scenarios
  - Template generation

#### 3.3 Version Command (`b version` / `b v`)
- **File**: `pkg/cli/version.go`
- **Functionality**:
  - `b version` - Show all versions
  - `b version <binary>` - Show specific binary version
  - `--local` flag - Local versions only
  - `--quiet/-q` flag - Exit code based checking

### Phase 4: Migration and Compatibility (Low Priority)

#### 4.1 Backward Compatibility Layer
- **File**: `pkg/cli/legacy.go`
- **Purpose**: Support old flag-based commands during transition
- **Implementation**:
  - Detect old flag usage patterns
  - Map to new subcommands
  - Show deprecation warnings

#### 4.2 Migration Guide
- **File**: `MIGRATION.md`
- **Content**: Command mapping table, examples, transition timeline

## File Structure Changes

### New Files to Create
```
pkg/cli/
├── root.go          # New root command
├── install.go       # Install subcommand
├── add.go          # Add subcommand  
├── update.go       # Update subcommand
├── list.go         # List subcommand
├── search.go       # Search subcommand
├── init.go         # Init subcommand
├── version.go      # Version subcommand
├── shared.go       # Shared options and utilities
└── legacy.go       # Backward compatibility (optional)

pkg/state/
└── config.go       # Enhanced config discovery
```

### Files to Modify
```
cmd/b/main.go       # Update to use new root command
pkg/cli/cli.go      # Refactor existing logic
pkg/cli/helper.go   # Extract reusable functions
```

## Command Mapping Reference

| Old Command | New Command | Alias |
|-------------|-------------|-------|
| `b -iu <binary>` | `b install <binary>` | `b i <binary>` |
| `b -fi <binary>` | `b install --force <binary>` | `b i --force <binary>` |
| `b -a --install` | `b install` | `b i` |
| `b -aiu` | `b update` | `b u` |
| `b --all` | `b list` | `b ls` |
| `b --list` | `b search` | `b s` |
| `b -acq` | `b version --quiet` | `b v -q` |

## Implementation Timeline

### Week 1: Infrastructure
- [ ] Create new root command structure
- [ ] Implement context-aware config discovery
- [ ] Set up subcommand registration framework

### Week 2: Core Commands
- [ ] Implement `install` command
- [ ] Implement `list` command
- [ ] Implement `update` command

### Week 3: Additional Commands
- [ ] Implement `add` command
- [ ] Implement `search` command
- [ ] Implement `init` command

### Week 4: Polish and Testing
- [ ] Implement `version` command
- [ ] Add comprehensive tests
- [ ] Update documentation
- [ ] Optional: Add backward compatibility layer

## Testing Strategy

### Unit Tests
- Test each subcommand independently
- Mock binary operations for faster tests
- Test flag parsing and validation

### Integration Tests
- Test complete workflows
- Test config file interactions
- Test binary installation flows

### Backward Compatibility Tests
- Ensure existing scripts continue to work
- Test migration scenarios

## Documentation Updates

### Files to Update
- `README.md` - Update examples and usage
- `CHANGELOG.md` - Document breaking changes
- Create `MIGRATION.md` - Migration guide

### Documentation Standards
- Use full command names in official docs
- Provide alias examples for interactive use
- Include before/after command comparisons

## Risk Mitigation

### Breaking Changes
- This is a breaking change, major version upgrade.
- No transition period required.

### Performance
- Reuse existing binary management logic
- Optimize config file discovery
- Maintain parallel processing for installations

### User Experience
- Preserve existing behavior where possible
- Improve error messages and help text
- Add command suggestions for typos

## Success Criteria

- [ ] All new subcommands implemented and functional
- [ ] Existing functionality preserved
- [ ] Performance maintained or improved
- [ ] Comprehensive test coverage (>80%)
- [ ] Updated documentation
- [ ] Keep binary small, whenever possible

## Notes

- Maintain existing binary definitions in `pkg/binaries/`
- Preserve all current binary management functionality
- Focus on CLI interface changes, not core logic changes
- Ensure the refactor improves usability without sacrificing power
