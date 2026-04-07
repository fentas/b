package env

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/fentas/b/pkg/lock"
)

// PlanAction is the canonical, user-facing classification of what a sync
// will do (or did) to a single file. The set is small so it can be
// rendered uniformly in tables, JSON, and CI summaries.
type PlanAction string

const (
	PlanAdd       PlanAction = "add"       // file is new in upstream and will be created locally
	PlanUpdate    PlanAction = "update"    // file exists, content will change
	PlanKeep      PlanAction = "keep"      // file unchanged (or local-only changes preserved by client strategy)
	PlanOverwrite PlanAction = "overwrite" // local changes will be replaced by upstream
	PlanMerge     PlanAction = "merge"     // 3-way merge applied (or would be), no conflict
	PlanConflict  PlanAction = "conflict"  // 3-way merge produced conflict markers; needs manual resolution
)

// IsDestructive reports whether an action would lose user-owned content.
// Strict safety mode refuses to apply a plan whose actions are destructive.
func (a PlanAction) IsDestructive() bool {
	switch a {
	case PlanOverwrite, PlanConflict:
		return true
	default:
		return false
	}
}

// PlanRow is a single row in a sync plan: one file, one action, plus the
// metadata needed to render it.
type PlanRow struct {
	Action PlanAction `json:"action"`
	Source string     `json:"source"` // path inside the upstream repo
	Dest   string     `json:"dest"`   // path on the consumer's disk (relative to projectRoot)
	// Note string is rendered alongside the row when non-empty. Currently
	// used to surface inline messages like "(merge failed: ...)".
	Note string `json:"note,omitempty"`
}

// Plan is the full per-env plan.
type Plan struct {
	Ref     string    `json:"ref"`
	Label   string    `json:"label,omitempty"`
	Version string    `json:"version,omitempty"`
	Commit  string    `json:"commit,omitempty"`
	Rows    []PlanRow `json:"rows"`
}

// HasDestructive reports whether the plan contains any destructive row.
func (p *Plan) HasDestructive() bool {
	for _, r := range p.Rows {
		if r.Action.IsDestructive() {
			return true
		}
	}
	return false
}

// CountByAction returns a map[action]count over the plan's rows. Useful
// for summary lines like "12 add, 3 update, 1 conflict".
func (p *Plan) CountByAction() map[PlanAction]int {
	out := make(map[PlanAction]int, 6)
	for _, r := range p.Rows {
		out[r.Action]++
	}
	return out
}

// PlanFromResult builds a Plan from a SyncResult. The result must come
// from a SyncEnv invocation (dry-run or real); the lock files' Status
// field is the ground truth and is mapped to a PlanAction.
//
// The mapping intentionally collapses the dry-run-suffixed statuses
// ("replaced (dry-run)", "kept (dry-run)", etc.) so the renderer doesn't
// have to special-case them.
func PlanFromResult(result *SyncResult) *Plan {
	if result == nil {
		return &Plan{}
	}
	p := &Plan{
		Ref:     result.Ref,
		Label:   result.Label,
		Version: result.Version,
		Commit:  result.Commit,
	}
	for _, f := range result.Files {
		p.Rows = append(p.Rows, planRowFromLockFile(f))
	}
	// Stable, deterministic ordering by destination path so callers can
	// snapshot-test the rendering.
	sort.Slice(p.Rows, func(i, j int) bool {
		return p.Rows[i].Dest < p.Rows[j].Dest
	})
	return p
}

// planRowFromLockFile maps a lock.LockFile (which carries a free-form
// status string) into a structured PlanRow.
func planRowFromLockFile(f lock.LockFile) PlanRow {
	status := strings.TrimSuffix(f.Status, " (dry-run)")
	row := PlanRow{
		Source: f.Path,
		Dest:   f.Dest,
	}
	switch {
	case status == "unchanged":
		row.Action = PlanKeep
	case status == "kept":
		row.Action = PlanKeep
		row.Note = "local changes preserved"
	case status == "merged":
		row.Action = PlanMerge
	case status == "conflict":
		row.Action = PlanConflict
	case strings.Contains(status, "local changes overwritten"):
		row.Action = PlanOverwrite
	case strings.HasPrefix(status, "replaced (merge failed"):
		row.Action = PlanOverwrite
		// Extract the parenthetical reason for the user.
		row.Note = strings.TrimSuffix(strings.TrimPrefix(status, "replaced "), "")
	case status == "replaced":
		// "replaced" alone (no "local changes overwritten") means the
		// file was new or identical-on-disk; treat as add/update rather
		// than destructive.
		row.Action = PlanUpdate
	default:
		row.Action = PlanUpdate
		row.Note = status
	}
	return row
}

// RenderPlanText writes a human-readable plan table to w.
//
// Format (one line per row):
//
//   - add       path/to/file
//     ~ update    path/to/file
//     = keep      path/to/file       (local changes preserved)
//     ! overwrite path/to/file
//     ⊕ merge     path/to/file
//     ✗ conflict  path/to/file
//
// A summary line is printed at the end:
//
//	→ 12 add, 3 update, 0 keep, 1 overwrite, 0 merge, 0 conflict
func RenderPlanText(w io.Writer, p *Plan) {
	if p == nil || len(p.Rows) == 0 {
		fmt.Fprintln(w, "  (no files)")
		return
	}
	for _, r := range p.Rows {
		marker, label := planMarker(r.Action)
		if r.Note != "" {
			fmt.Fprintf(w, "  %s %-9s %-40s  (%s)\n", marker, label, r.Dest, r.Note)
		} else {
			fmt.Fprintf(w, "  %s %-9s %s\n", marker, label, r.Dest)
		}
	}
	counts := p.CountByAction()
	fmt.Fprintf(w, "  → %d add, %d update, %d keep, %d overwrite, %d merge, %d conflict\n",
		counts[PlanAdd], counts[PlanUpdate], counts[PlanKeep],
		counts[PlanOverwrite], counts[PlanMerge], counts[PlanConflict])
}

// RenderPlanJSON writes the plan as JSON for machine consumers (PR
// comment bots, CI summary jobs, etc).
func RenderPlanJSON(w io.Writer, p *Plan) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(p)
}

// planMarker returns the (glyph, label) pair for a plan action. The glyph
// is single-width to keep the table aligned in the common case.
func planMarker(a PlanAction) (string, string) {
	switch a {
	case PlanAdd:
		return "+", "add"
	case PlanUpdate:
		return "~", "update"
	case PlanKeep:
		return "=", "keep"
	case PlanOverwrite:
		return "!", "overwrite"
	case PlanMerge:
		return "⊕", "merge"
	case PlanConflict:
		return "✗", "conflict"
	default:
		return "?", string(a)
	}
}
