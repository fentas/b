package cli

import (
	"testing"

	"github.com/fentas/b/pkg/env"
)

func TestUpdateOptions_Validate(t *testing.T) {
	tests := []struct {
		strategy string
		wantErr  bool
	}{
		{"", false},
		{"replace", false},
		{"client", false},
		{"merge", false},
		{"invalid", true},
		{"REPLACE", true}, // case-sensitive
	}

	for _, tt := range tests {
		o := &UpdateOptions{Strategy: tt.strategy}
		err := o.Validate()
		if (err != nil) != tt.wantErr {
			t.Errorf("Validate(strategy=%q) error = %v, wantErr %v", tt.strategy, err, tt.wantErr)
		}
	}
}

func TestIsTTY(t *testing.T) {
	// In test environment, stdout is typically not a TTY
	got := isTTY()
	// We can't assert true/false since it depends on the test runner,
	// but we can verify it doesn't panic
	_ = got
}

func TestStrategyConstants(t *testing.T) {
	// Verify the constants match what the update command accepts
	if env.StrategyReplace != "replace" {
		t.Errorf("StrategyReplace = %q", env.StrategyReplace)
	}
	if env.StrategyClient != "client" {
		t.Errorf("StrategyClient = %q", env.StrategyClient)
	}
	if env.StrategyMerge != "merge" {
		t.Errorf("StrategyMerge = %q", env.StrategyMerge)
	}
}
