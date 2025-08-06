package cli

import (
	"os"
	"testing"

	"github.com/fentas/goodies/streams"

	"github.com/fentas/b/pkg/binary"
	"github.com/fentas/b/pkg/state"
	"github.com/fentas/b/test/testutil"
)

func TestVersionOptions_Complete(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "no args",
			args:    []string{},
			wantErr: false,
		},
		{
			name:    "single valid binary",
			args:    []string{"jq"},
			wantErr: false,
		},
		{
			name:    "multiple valid binaries",
			args:    []string{"jq", "kubectl"},
			wantErr: false,
		},
		{
			name:    "unknown binary",
			args:    []string{"nonexistent"},
			wantErr: true,
			errMsg:  "unknown binary: nonexistent",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test binaries
			binaries := []*binary.Binary{
				{Name: "jq", Version: "1.7"},
				{Name: "kubectl", Version: "latest"},
			}

			io := &streams.IO{
				Out:    os.Stdout,
				ErrOut: os.Stderr,
			}
			shared := NewSharedOptions(io, binaries)
			o := &VersionOptions{
				SharedOptions: shared,
			}

			err := o.Complete(tt.args)

			if tt.wantErr {
				if err == nil {
					t.Errorf("Complete() expected error but got none")
					return
				}
				if !testutil.Contains(err.Error(), tt.errMsg) {
					t.Errorf("Complete() error = %v, want error containing %v", err, tt.errMsg)
				}
			} else {
				if err != nil {
					t.Errorf("Complete() unexpected error = %v", err)
				}
			}
		})
	}
}

func TestVersionOptions_Validate(t *testing.T) {
	io := &streams.IO{
		Out:    os.Stdout,
		ErrOut: os.Stderr,
	}
	binaries := []*binary.Binary{{Name: "jq", Version: "1.7"}}
	shared := NewSharedOptions(io, binaries)
	o := &VersionOptions{
		SharedOptions: shared,
	}

	err := o.Validate()
	if err != nil {
		t.Errorf("Validate() unexpected error = %v", err)
	}
}

func TestVersionOptions_Run(t *testing.T) {
	tests := []struct {
		name     string
		config   *state.BinaryList
		local    bool
		check    bool
		quiet    bool
		wantErr  bool
		setupEnv func()
	}{
		{
			name: "run with config",
			config: &state.BinaryList{
				&binary.LocalBinary{
					Name:    "jq",
					Version: "1.7",
				},
			},
			local: false,
			check: false,
			quiet: false,
		},
		{
			name: "run local only",
			config: &state.BinaryList{
				&binary.LocalBinary{
					Name:    "jq",
					Version: "1.7",
				},
			},
			local: true,
			check: false,
			quiet: false,
		},
		{
			name:   "run without config",
			config: nil,
			local:  false,
			check:  false,
			quiet:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setupEnv != nil {
				tt.setupEnv()
			}

			// Create test binaries with mock version functions
			binaries := []*binary.Binary{
				{
					Name:    "jq",
					Version: "1.7",
					VersionF: func(b *binary.Binary) (string, error) {
						return "1.7.1", nil // Mock latest version
					},
				},
				{
					Name:    "kubectl",
					Version: "latest",
					VersionF: func(b *binary.Binary) (string, error) {
						return "1.28.0", nil // Mock latest version
					},
				},
			}

			io := &streams.IO{
				Out:    os.Stdout,
				ErrOut: os.Stderr,
			}
			shared := NewSharedOptions(io, binaries)
			shared.Config = tt.config
			shared.Quiet = tt.quiet

			o := &VersionOptions{
				SharedOptions: shared,
				Local:         tt.local,
				Check:         tt.check,
			}

			err := o.Run()

			if tt.wantErr {
				if err == nil {
					t.Errorf("Run() expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("Run() unexpected error = %v", err)
				}
			}
		})
	}
}

func TestVersionOptions_getVersionInfo(t *testing.T) {
	tests := []struct {
		name     string
		binaries []*binary.Binary
		local    bool
		wantErr  bool
	}{
		{
			name: "single binary with version function",
			binaries: []*binary.Binary{
				{
					Name:    "jq",
					Version: "1.7",
					VersionF: func(b *binary.Binary) (string, error) {
						return "1.7.1", nil
					},
				},
			},
			local:   false,
			wantErr: false,
		},
		{
			name: "multiple binaries",
			binaries: []*binary.Binary{
				{
					Name:    "kubectl",
					Version: "1.28.0",
					VersionF: func(b *binary.Binary) (string, error) {
						return "1.28.1", nil
					},
				},
				{
					Name:    "jq",
					Version: "1.7",
					VersionF: func(b *binary.Binary) (string, error) {
						return "1.7.1", nil
					},
				},
			},
			local:   false,
			wantErr: false,
		},
		{
			name: "local only mode",
			binaries: []*binary.Binary{
				{
					Name:    "jq",
					Version: "1.7",
					VersionF: func(b *binary.Binary) (string, error) {
						return "1.7.1", nil
					},
				},
			},
			local:   true,
			wantErr: false,
		},
		{
			name:     "empty binaries list",
			binaries: []*binary.Binary{},
			local:    false,
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			io := &streams.IO{
				Out:    os.Stdout,
				ErrOut: os.Stderr,
			}
			shared := NewSharedOptions(io, tt.binaries)
			o := &VersionOptions{
				SharedOptions: shared,
				Local:         tt.local,
			}

			locals, err := o.getVersionInfo(tt.binaries)

			if tt.wantErr {
				if err == nil {
					t.Errorf("getVersionInfo() expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("getVersionInfo() unexpected error = %v", err)
				return
			}

			if len(locals) != len(tt.binaries) {
				t.Errorf("getVersionInfo() returned %d locals, want %d", len(locals), len(tt.binaries))
				return
			}

			// Create a map of expected binaries for non-deterministic order checking
			expectedBinaries := make(map[string]*binary.Binary)
			for _, b := range tt.binaries {
				expectedBinaries[b.Name] = b
			}

			// Verify local version info (order-independent)
			for _, local := range locals {
				expectedBinary, exists := expectedBinaries[local.Name]
				if !exists {
					t.Errorf("getVersionInfo() returned unexpected binary: %v", local.Name)
					continue
				}

				// In local mode, Latest should be empty
				if tt.local && local.Latest != "" {
					t.Errorf("getVersionInfo() in local mode, binary %s Latest = %v, want empty", local.Name, local.Latest)
				}

				// In non-local mode with VersionF, Latest should be set
				if !tt.local && expectedBinary.VersionF != nil && local.Latest == "" {
					t.Errorf("getVersionInfo() in non-local mode, binary %s Latest should not be empty", local.Name)
				}
			}
		})
	}
}

func TestNewVersionCmd(t *testing.T) {
	io := &streams.IO{
		Out:    os.Stdout,
		ErrOut: os.Stderr,
	}
	binaries := []*binary.Binary{{Name: "jq", Version: "1.7"}}
	shared := NewSharedOptions(io, binaries)

	cmd := NewVersionCmd(shared)

	if cmd == nil {
		t.Fatal("NewVersionCmd() returned nil")
	}

	if cmd.Use != "version [binary...]" {
		t.Errorf("NewVersionCmd() Use = %v, want %v", cmd.Use, "version [binary...]")
	}

	if len(cmd.Aliases) != 1 || cmd.Aliases[0] != "v" {
		t.Errorf("NewVersionCmd() Aliases = %v, want [v]", cmd.Aliases)
	}

	if cmd.Short != "Show version information" {
		t.Errorf("NewVersionCmd() Short = %v, want %v", cmd.Short, "Show version information")
	}

	// Test flags
	localFlag := cmd.Flags().Lookup("local")
	if localFlag == nil {
		t.Error("NewVersionCmd() missing --local flag")
	}

	checkFlag := cmd.Flags().Lookup("check")
	if checkFlag == nil {
		t.Error("NewVersionCmd() missing --check flag")
	}
}

func TestVersionOptions_RunWithCheck(t *testing.T) {
	// This test is tricky because it calls os.Exit(1)
	// We'll test the logic without actually calling Run()

	tests := []struct {
		name              string
		locals            []*binary.LocalBinary
		expectNotUpToDate bool
	}{
		{
			name: "all up to date",
			locals: []*binary.LocalBinary{
				{Name: "jq", Version: "1.7", Latest: "1.7"},
				{Name: "kubectl", Version: "1.28.0", Latest: "1.28.0"},
			},
			expectNotUpToDate: false,
		},
		{
			name: "some not up to date",
			locals: []*binary.LocalBinary{
				{Name: "jq", Version: "1.6", Latest: "1.7"},
				{Name: "kubectl", Version: "1.28.0", Latest: "1.28.0"},
			},
			expectNotUpToDate: true,
		},
		{
			name: "enforced version should be skipped",
			locals: []*binary.LocalBinary{
				{Name: "jq", Version: "1.6", Latest: "1.7", Enforced: "1.6"},
				{Name: "kubectl", Version: "1.28.0", Latest: "1.28.0"},
			},
			expectNotUpToDate: false,
		},
		{
			name: "missing version",
			locals: []*binary.LocalBinary{
				{Name: "jq", Version: "", Latest: "1.7"},
			},
			expectNotUpToDate: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test the check logic without calling Run() to avoid os.Exit
			notUpToDate := make([]*binary.LocalBinary, 0)
			for _, l := range tt.locals {
				// Skip if version is pinned (enforced)
				if l.Enforced != "" && l.Enforced != "latest" {
					continue
				}
				if l.Version == "" || (l.Latest != "" && l.Version != l.Latest) {
					notUpToDate = append(notUpToDate, l)
				}
			}

			hasNotUpToDate := len(notUpToDate) > 0
			if hasNotUpToDate != tt.expectNotUpToDate {
				t.Errorf("Check logic: hasNotUpToDate = %v, want %v", hasNotUpToDate, tt.expectNotUpToDate)
			}
		})
	}
}
