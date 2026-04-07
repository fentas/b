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

// MarshalJSON ensures Plan.Rows is encoded as `[]` rather than `null`
// when the slice is nil. The plan-json contract advertised in
// docs/env-sync.mdx promises `rows: []` for up-to-date envs, and
// consumers that index into the array would break on `null`. Per
// copilot review on PR #128 round 4.
func (p Plan) MarshalJSON() ([]byte, error) {
	type planAlias Plan
	out := planAlias(p)
	if out.Rows == nil {
		out.Rows = make([]PlanRow, 0)
	}
	return json.Marshal(out)
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
// `prev` is the lock entry from the *previous* sync (nil if first
// sync). It's used to distinguish "new file" (PlanAdd) from "existing
// file changed" (PlanUpdate) — SyncEnv reports both as "replaced" and
// the previous-lock comparison is the only available signal here.
//
// The mapping intentionally collapses the dry-run-suffixed statuses
// ("replaced (dry-run)", "kept (dry-run)", etc.) so the renderer
// doesn't have to special-case them.
func PlanFromResult(result *SyncResult, prev *lock.EnvEntry) *Plan {
	if result == nil {
		return &Plan{}
	}
	p := &Plan{
		Ref:     result.Ref,
		Label:   result.Label,
		Version: result.Version,
		Commit:  result.Commit,
	}
	// Build a quick lookup of source paths that existed in the previous
	// lock entry, so we can identify NEW files (PlanAdd) vs UPDATED
	// files (PlanUpdate). Both come back from SyncEnv as Status:
	// "replaced" today, so without this comparison everything would
	// render as "update" and the "add" action would be unreachable —
	// flagged by copilot review on PR #128.
	prevPaths := make(map[string]bool)
	if prev != nil {
		for _, f := range prev.Files {
			prevPaths[f.Path] = true
		}
	}
	for _, f := range result.Files {
		p.Rows = append(p.Rows, planRowFromLockFile(f, prevPaths))
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
//
// `prevPaths` is the set of source paths that existed in the previous
// lock entry; it lets us tell apart "this file is new (Add)" from
// "this file existed and changed (Update)" — SyncEnv reports both as
// Status: "replaced".
func planRowFromLockFile(f lock.LockFile, prevPaths map[string]bool) PlanRow {
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
		// Extract the parenthetical reason for the user without the
		// surrounding parentheses; the renderer wraps notes in parens
		// itself, so leaving them in produces "((merge failed: ...))".
		// Per copilot review on PR #128.
		note := strings.TrimPrefix(status, "replaced ")
		note = strings.TrimPrefix(note, "(")
		note = strings.TrimSuffix(note, ")")
		row.Note = note
	case status == "replaced":
		// "replaced" alone means upstream content was written. Use
		// the previous lock entry to distinguish a newly tracked
		// file from an existing tracked file that was updated: if
		// the source path wasn't in prev, it's an Add; otherwise
		// it's an Update. Identical tracked files are reported as
		// "unchanged", not "replaced" (so they hit the PlanKeep
		// branch above, not this one). Without this check, PlanAdd
		// would be unreachable. Per copilot review on PR #128.
		if prevPaths != nil && !prevPaths[f.Path] {
			row.Action = PlanAdd
		} else {
			row.Action = PlanUpdate
		}
	default:
		row.Action = PlanUpdate
		row.Note = status
	}
	return row
}

// RenderPlanText writes a human-readable plan table to w.
//
// Format (one line per row, marker glyphs in single quotes to avoid
// godoc list-bullet eating them):
//
//	'+' add       path/to/file
//	'~' update    path/to/file
//	'=' keep      path/to/file       (local changes preserved)
//	'!' overwrite path/to/file
//	'⊕' merge     path/to/file
//	'✗' conflict  path/to/file
//
// A summary line is printed at the end with non-zero counts only:
//
//	→ 12 add, 3 update, 1 conflict
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
	// Summary line: only emit non-zero counts so the common case
	// reads "→ 3 update" instead of "→ 0 add, 3 update, 0 keep, 0
	// overwrite, 0 merge, 0 conflict". Per reviewer note on PR #128.
	counts := p.CountByAction()
	order := []PlanAction{PlanAdd, PlanUpdate, PlanKeep, PlanOverwrite, PlanMerge, PlanConflict}
	var parts []string
	for _, a := range order {
		if c := counts[a]; c > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", c, a))
		}
	}
	if len(parts) == 0 {
		fmt.Fprintln(w, "  → no changes")
	} else {
		fmt.Fprintf(w, "  → %s\n", strings.Join(parts, ", "))
	}
}

// RenderPlanJSON writes a single plan as a JSON document. Used by
// callers that operate on one plan at a time.
func RenderPlanJSON(w io.Writer, p *Plan) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(p)
}

// RenderPlansJSON writes a slice of plans as a single JSON array. This
// is the format the CLI emits for `--plan-json` so consumers can parse
// the entire run with one `jq .` invocation. Per copilot review on
// PR #128: emitting one JSON document per env produced concatenated
// docs that weren't valid JSON for standard parsers.
func RenderPlansJSON(w io.Writer, plans []*Plan) error {
	if plans == nil {
		plans = []*Plan{}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(plans)
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
