# Testing Plan for b CLI Tool

## Overview

This document outlines a comprehensive testing strategy for the `b` CLI tool, covering unit tests, integration tests, and end-to-end testing. The goal is to ensure reliability, maintainability, and correctness of all CLI commands and core functionality.

## Current State

- **No existing tests**: The codebase currently has no test files (`*_test.go`)
- **Complex CLI structure**: Multiple subcommands with shared options and state management
- **External dependencies**: GitHub releases, file system operations, network requests
- **Concurrent operations**: Progress tracking, parallel downloads, and installations

## Testing Strategy

### 1. Unit Testing

#### 1.1 Core Package Tests

**`pkg/state/` - Configuration Management**
- `config_test.go`: Test config loading, saving, discovery, and validation
- `types_test.go`: Test YAML marshaling/unmarshaling, BinaryList operations
- Test cases:
  - Config file discovery in parent directories
  - YAML serialization with minimal format
  - Default config creation
  - Error handling for invalid YAML

**`pkg/binary/` - Binary Management**
- `binary_test.go`: Test binary operations, path resolution, version handling
- `download_test.go`: Test download logic with mocked HTTP responses
- Test cases:
  - Binary path resolution
  - Version parsing and validation
  - Download progress tracking
  - File extraction and permissions

**`pkg/cli/shared.go` - Shared Utilities**
- `shared_test.go`: Test shared options, binary lookup, config loading
- Test cases:
  - Binary lookup map creation
  - Config loading with different paths
  - Binary filtering from config

#### 1.2 CLI Command Tests

**Command Structure Tests**
- `pkg/cli/install_test.go`: Test install command logic
- `pkg/cli/list_test.go`: Test list command output formatting
- `pkg/cli/search_test.go`: Test search filtering and results
- `pkg/cli/update_test.go`: Test update operations
- `pkg/cli/init_test.go`: Test initialization and file creation
- `pkg/cli/version_test.go`: Test version checking and comparison
- `pkg/cli/request_test.go`: Test GitHub issue URL generation

### 2. Integration Testing

#### 2.1 CLI Integration Tests

**`test/integration/` Directory Structure**
```
test/
├── integration/
│   ├── cli_test.go           # Full CLI command testing
│   ├── config_test.go        # Config file operations
│   ├── install_test.go       # Installation workflows
│   └── fixtures/             # Test data and configs
│       ├── configs/
│       │   ├── basic.yaml
│       │   ├── versioned.yaml
│       │   └── invalid.yaml
│       └── binaries/
│           └── mock-releases/
```

**Test Scenarios**
- Complete install workflows (with mocked downloads)
- Config file creation and modification
- Multi-binary operations
- Error handling and recovery
- Cross-platform compatibility

#### 2.2 File System Integration

**Temporary Directory Testing**
- Test file creation in isolated environments
- Test permission handling
- Test cleanup operations
- Test concurrent file access

### 3. End-to-End Testing

#### 3.1 CLI E2E Tests

**`test/e2e/` Directory Structure**
```
test/
├── e2e/
│   ├── main_test.go          # Full CLI binary testing
│   ├── scenarios_test.go     # Real-world usage scenarios
│   └── helpers.go            # Test utilities and setup
```

**Test Scenarios**
- Fresh project initialization
- Adding and installing binaries
- Version management workflows
- Configuration migration
- Error scenarios and recovery

#### 3.2 GitHub Integration Tests

**Mock GitHub API**
- Test request command URL generation
- Test issue template integration
- Test release API interactions (mocked)

### 4. Test Infrastructure

#### 4.1 Test Utilities

**`test/testutil/` Package**
```go
// testutil/helpers.go
package testutil

import (
    "io/ioutil"
    "os"
    "path/filepath"
    "testing"
)

// TempDir creates a temporary directory for testing
func TempDir(t *testing.T) string

// MockBinary creates a mock binary for testing
func MockBinary(name, version string) *binary.Binary

// MockConfig creates a test configuration
func MockConfig(binaries ...string) *state.BinaryList

// AssertFileExists checks if a file exists
func AssertFileExists(t *testing.T, path string)

// AssertFileContent validates file content
func AssertFileContent(t *testing.T, path, expected string)
```

#### 4.2 Mock Implementations

**HTTP Client Mocking**
- Mock GitHub API responses
- Mock binary download responses
- Test network failure scenarios

**File System Mocking**
- Mock file operations for error testing
- Test permission scenarios
- Test concurrent access

#### 4.3 Test Data Management

**Fixtures and Test Data**
- Sample `b.yaml` configurations
- Mock binary releases
- Test project structures
- Error scenario data

### 5. Testing Tools and Dependencies

#### 5.1 Required Dependencies

```go
// go.mod additions for testing
require (
    github.com/stretchr/testify v1.8.4
    github.com/golang/mock v1.6.0
    github.com/jarcoal/httpmock v1.3.1
)
```

#### 5.2 Testing Commands

```bash
# Run all tests
go test ./...

# Run with coverage
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out

# Run specific test suites
go test ./pkg/cli/...
go test ./test/integration/...
go test ./test/e2e/...

# Run with race detection
go test -race ./...

# Benchmark tests
go test -bench=. ./...
```

### 6. Continuous Integration

#### 6.1 GitHub Actions Workflow

**`.github/workflows/test.yml`**
```yaml
name: Tests
on: [push, pull_request]
jobs:
  test:
    strategy:
      matrix:
        os: [ubuntu-latest, macos-latest, windows-latest]
        go-version: [1.21, 1.22]
    runs-on: ${{ matrix.os }}
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v4
        with:
          go-version: ${{ matrix.go-version }}
      - run: go test -race -coverprofile=coverage.out ./...
      - run: go tool cover -func=coverage.out
```

#### 6.2 Coverage Requirements

- **Minimum coverage**: 80% overall
- **Critical paths**: 95% coverage for CLI commands
- **Core packages**: 90% coverage for state and binary management

### 7. Test Organization

#### 7.1 Test Categories

**Fast Tests** (< 100ms each)
- Unit tests for pure functions
- Configuration parsing
- URL generation
- Data structure operations

**Medium Tests** (< 1s each)
- File system operations
- Mock HTTP requests
- CLI command parsing

**Slow Tests** (< 10s each)
- Integration tests
- End-to-end scenarios
- Network operations (mocked)

#### 7.2 Test Naming Conventions

```go
// Function: TestPackage_Function_Scenario
func TestConfig_LoadConfigFromPath_ValidFile(t *testing.T)
func TestConfig_LoadConfigFromPath_InvalidYAML(t *testing.T)
func TestInstall_Run_SingleBinary(t *testing.T)
func TestInstall_Run_MultipleBinaries(t *testing.T)

// Table-driven tests
func TestBinaryArg_Parse(t *testing.T) {
    tests := []struct {
        name     string
        input    string
        wantName string
        wantVer  string
    }{
        {"simple name", "jq", "jq", ""},
        {"with version", "jq@1.7", "jq", "1.7"},
    }
    // ...
}
```

### 8. Implementation Phases

#### Phase 1: Foundation (Week 1)
- [ ] Set up test infrastructure and utilities
- [ ] Implement core package unit tests (`pkg/state`, `pkg/binary`)
- [ ] Create mock implementations and test fixtures
- [ ] Set up CI/CD pipeline

#### Phase 2: CLI Commands (Week 2)
- [ ] Unit tests for all CLI commands
- [ ] Integration tests for command workflows
- [ ] Error handling and edge case testing
- [ ] Cross-platform compatibility tests

#### Phase 3: End-to-End (Week 3)
- [ ] Complete E2E test scenarios
- [ ] Performance and stress testing
- [ ] Documentation and examples
- [ ] Coverage analysis and optimization

#### Phase 4: Advanced Testing (Week 4)
- [ ] Benchmark tests for performance
- [ ] Fuzz testing for input validation
- [ ] Security testing for file operations
- [ ] Load testing for concurrent operations

### 9. Success Criteria

- **Coverage**: Minimum 80% test coverage across all packages
- **Reliability**: All tests pass consistently across platforms
- **Performance**: Test suite completes in under 2 minutes
- **Maintainability**: Tests are easy to understand and modify
- **Documentation**: Clear testing guidelines and examples

### 10. Maintenance

#### 10.1 Test Maintenance
- Regular review of test coverage reports
- Update tests when adding new features
- Refactor tests when code structure changes
- Monitor test execution time and optimize slow tests

#### 10.2 Quality Gates
- All new code must include tests
- PRs require passing tests and coverage checks
- Regular dependency updates for test libraries
- Periodic review of test effectiveness

## Conclusion

This comprehensive testing plan ensures the `b` CLI tool is robust, reliable, and maintainable. The phased approach allows for incremental implementation while providing immediate value. The combination of unit, integration, and end-to-end tests provides confidence in the tool's functionality across different environments and use cases.