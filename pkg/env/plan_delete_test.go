package env

import (
	"strings"
	"testing"

	"github.com/fentas/b/pkg/lock"
)

// TestPlanDeleteIsDestructive: Phase 3 of #125 adds `delete` to the
// destructive set so strict mode refuses sync plans that would
// remove files.
func TestPlanDeleteIsDestructive(t *testing.T) {
	if !PlanDelete.IsDestructive() {
		t.Error("PlanDelete should be destructive")
	}
}

// TestPlanFromResult_IncludesDeleteRows: orphan files on
// result.Deleted must surface in the plan as `delete` rows so the
// user sees them and the gate can refuse them.
func TestPlanFromResult_IncludesDeleteRows(t *testing.T) {
	result := &SyncResult{
		Ref: "github.com/x/y",
		Files: []lock.LockFile{
			{Path: "kept.yaml", Dest: "kept.yaml", Status: "unchanged"},
		},
		Deleted: []lock.LockFile{
			{Path: "old.yaml", Dest: "old.yaml", Status: "deleted"},
		},
	}
	prev := &lock.EnvEntry{
		Files: []lock.LockFile{
			{Path: "kept.yaml"},
			{Path: "old.yaml"},
		},
	}
	p := PlanFromResult(result, prev)

	var sawDelete bool
	for _, r := range p.Rows {
		if r.Action == PlanDelete && r.Dest == "old.yaml" {
			sawDelete = true
		}
	}
	if !sawDelete {
		t.Errorf("expected a delete row for old.yaml, got rows: %+v", p.Rows)
	}
	if !p.HasDestructive() {
		t.Errorf("plan with a delete row should be destructive")
	}
}

// TestPlanFromResult_DeleteSkippedRendersAsKeep: when local has
// modified the file, the delete is skipped. The plan should NOT show
// a destructive row for it — it renders as keep with a note so the
// user understands what happened.
func TestPlanFromResult_DeleteSkippedRendersAsKeep(t *testing.T) {
	result := &SyncResult{
		Ref: "github.com/x/y",
		Deleted: []lock.LockFile{
			{Path: "modified.yaml", Dest: "modified.yaml", Status: "delete-skipped (local modified)"},
		},
	}
	p := PlanFromResult(result, nil)

	if len(p.Rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(p.Rows))
	}
	row := p.Rows[0]
	if row.Action != PlanKeep {
		t.Errorf("want PlanKeep, got %v", row.Action)
	}
	if !strings.Contains(row.Note, "local modified") {
		t.Errorf("note should mention local modified, got %q", row.Note)
	}
	if p.HasDestructive() {
		t.Errorf("a delete-skipped row must not be destructive")
	}
}

// TestPlanRenderText_DeleteRow renders the new glyph + label.
func TestPlanRenderText_DeleteRow(t *testing.T) {
	p := &Plan{
		Rows: []PlanRow{
			{Action: PlanDelete, Dest: "gone.yaml"},
		},
	}
	var sb strings.Builder
	RenderPlanText(&sb, p)
	out := sb.String()
	if !strings.Contains(out, "delete") || !strings.Contains(out, "gone.yaml") {
		t.Errorf("delete row missing in render:\n%s", out)
	}
	if !strings.Contains(out, "1 delete") {
		t.Errorf("summary missing delete count:\n%s", out)
	}
}
