package env

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fentas/b/pkg/lock"
)

func TestPlanAction_IsDestructive(t *testing.T) {
	cases := map[PlanAction]bool{
		PlanAdd:       false,
		PlanUpdate:    false,
		PlanKeep:      false,
		PlanMerge:     false,
		PlanOverwrite: true,
		PlanConflict:  true,
	}
	for a, want := range cases {
		if got := a.IsDestructive(); got != want {
			t.Errorf("%q.IsDestructive() = %v, want %v", a, got, want)
		}
	}
}

func TestPlanFromResult_StatusMapping(t *testing.T) {
	result := &SyncResult{
		Ref: "github.com/org/repo",
		Files: []lock.LockFile{
			{Path: "a.yaml", Dest: "a.yaml", Status: "replaced"},
			{Path: "b.yaml", Dest: "b.yaml", Status: "kept"},
			{Path: "c.yaml", Dest: "c.yaml", Status: "merged"},
			{Path: "d.yaml", Dest: "d.yaml", Status: "conflict"},
			{Path: "e.yaml", Dest: "e.yaml", Status: "replaced (local changes overwritten)"},
			{Path: "f.yaml", Dest: "f.yaml", Status: "unchanged"},
			{Path: "g.yaml", Dest: "g.yaml", Status: "replaced (dry-run)"},
		},
	}
	plan := PlanFromResult(result)
	if len(plan.Rows) != 7 {
		t.Fatalf("rows = %d, want 7", len(plan.Rows))
	}
	want := map[string]PlanAction{
		"a.yaml": PlanUpdate,
		"b.yaml": PlanKeep,
		"c.yaml": PlanMerge,
		"d.yaml": PlanConflict,
		"e.yaml": PlanOverwrite,
		"f.yaml": PlanKeep,
		"g.yaml": PlanUpdate, // dry-run suffix stripped
	}
	for _, r := range plan.Rows {
		if want[r.Dest] != r.Action {
			t.Errorf("%s: action = %q, want %q", r.Dest, r.Action, want[r.Dest])
		}
	}
}

func TestPlanFromResult_DestructiveAndCounts(t *testing.T) {
	result := &SyncResult{
		Files: []lock.LockFile{
			{Dest: "a", Status: "replaced"},
			{Dest: "b", Status: "replaced (local changes overwritten)"},
			{Dest: "c", Status: "kept"},
			{Dest: "d", Status: "conflict"},
		},
	}
	p := PlanFromResult(result)
	if !p.HasDestructive() {
		t.Error("plan with overwrite + conflict should be destructive")
	}
	c := p.CountByAction()
	if c[PlanUpdate] != 1 || c[PlanOverwrite] != 1 || c[PlanKeep] != 1 || c[PlanConflict] != 1 {
		t.Errorf("counts = %v", c)
	}
}

func TestPlanFromResult_NonDestructive(t *testing.T) {
	result := &SyncResult{
		Files: []lock.LockFile{
			{Dest: "a", Status: "replaced"},
			{Dest: "b", Status: "kept"},
		},
	}
	if PlanFromResult(result).HasDestructive() {
		t.Error("non-destructive plan flagged as destructive")
	}
}

func TestRenderPlanText_Empty(t *testing.T) {
	var buf bytes.Buffer
	RenderPlanText(&buf, &Plan{})
	if !strings.Contains(buf.String(), "(no files)") {
		t.Errorf("empty plan should say (no files), got: %q", buf.String())
	}
}

func TestRenderPlanText_FormatsRowsAndSummary(t *testing.T) {
	plan := &Plan{
		Rows: []PlanRow{
			{Action: PlanAdd, Dest: "a.yaml"},
			{Action: PlanOverwrite, Dest: "b.yaml"},
			{Action: PlanKeep, Dest: "c.yaml", Note: "local changes preserved"},
		},
	}
	var buf bytes.Buffer
	RenderPlanText(&buf, plan)
	out := buf.String()
	if !strings.Contains(out, "a.yaml") {
		t.Error("missing a.yaml")
	}
	if !strings.Contains(out, "b.yaml") {
		t.Error("missing b.yaml")
	}
	if !strings.Contains(out, "(local changes preserved)") {
		t.Error("note not rendered")
	}
	if !strings.Contains(out, "1 add") || !strings.Contains(out, "1 overwrite") || !strings.Contains(out, "1 keep") {
		t.Errorf("summary missing counts, got: %s", out)
	}
}

func TestRenderPlanJSON_RoundTrip(t *testing.T) {
	plan := &Plan{
		Ref:    "github.com/org/repo",
		Commit: "abc123",
		Rows: []PlanRow{
			{Action: PlanUpdate, Source: "a.yaml", Dest: "a.yaml"},
			{Action: PlanConflict, Source: "b.yaml", Dest: "b.yaml", Note: "see markers"},
		},
	}
	var buf bytes.Buffer
	if err := RenderPlanJSON(&buf, plan); err != nil {
		t.Fatal(err)
	}
	var got Plan
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, buf.String())
	}
	if got.Ref != plan.Ref || got.Commit != plan.Commit {
		t.Errorf("metadata lost: %+v", got)
	}
	if len(got.Rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(got.Rows))
	}
	if got.Rows[1].Action != PlanConflict || got.Rows[1].Note != "see markers" {
		t.Errorf("row[1] = %+v", got.Rows[1])
	}
}

func TestPlanFromResult_DeterministicOrdering(t *testing.T) {
	// Rows should be sorted by Dest so two equivalent inputs produce
	// byte-identical text output (snapshot tests rely on this).
	result := &SyncResult{
		Files: []lock.LockFile{
			{Dest: "z.yaml", Status: "replaced"},
			{Dest: "a.yaml", Status: "replaced"},
			{Dest: "m.yaml", Status: "replaced"},
		},
	}
	p := PlanFromResult(result)
	if p.Rows[0].Dest != "a.yaml" || p.Rows[1].Dest != "m.yaml" || p.Rows[2].Dest != "z.yaml" {
		t.Errorf("rows not sorted: %+v", p.Rows)
	}
}
