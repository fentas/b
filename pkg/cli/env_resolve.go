package cli

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fentas/goodies/templates"
	"github.com/spf13/cobra"

	"github.com/fentas/b/pkg/env"
	"github.com/fentas/b/pkg/lock"
)

// EnvResolveOptions holds options for `b env resolve`.
//
// The command lists files that contain unresolved git-style conflict
// markers (left there by `b env update` after a 3-way merge couldn't
// auto-merge a hunk) and optionally rewrites them in bulk by picking
// one side of every conflict region.
//
// Conflict marker shape (the `git merge-file --diff3` output b uses):
//
//	<<<<<<< local
//	  consumer's version
//	||||||| base
//	  base version
//	=======
//	  upstream version
//	>>>>>>> upstream
//
// `--ours` keeps the local block, `--theirs` keeps the upstream block.
// With neither flag set, the command just enumerates the affected
// files so the user can decide. Phase 4 of #125 also covers in-place
// YAML comment markers; that conversion is a separate PR — this
// command speaks the marker format that's actually written today.
type EnvResolveOptions struct {
	*SharedOptions
	Ours   bool
	Theirs bool
}

// NewEnvResolveCmd creates the env resolve subcommand.
func NewEnvResolveCmd(shared *SharedOptions) *cobra.Command {
	o := &EnvResolveOptions{SharedOptions: shared}
	cmd := &cobra.Command{
		Use:   "resolve [env...]",
		Short: "List or auto-resolve merge conflicts left by `b env update`",
		Long: `Inspect synced env files for unresolved git-style merge conflict markers
and optionally pick a side in bulk.

Without --ours or --theirs the command lists every conflicted file. With
either flag, the listed files are rewritten in place by keeping that
side of every conflict region. Pass one or more env keys to limit the
scope to those envs; with no args all envs in the lock are checked.`,
		Example: templates.Examples(`
			# list conflicts across all envs
			b env resolve

			# accept upstream for every conflict in one env
			b env resolve --theirs github.com/org/infra

			# accept local everywhere
			b env resolve --ours
		`),
		RunE: func(cmd *cobra.Command, args []string) error {
			return o.Run(args)
		},
	}
	cmd.Flags().BoolVar(&o.Ours, "ours", false, "rewrite each conflict by keeping the local side")
	cmd.Flags().BoolVar(&o.Theirs, "theirs", false, "rewrite each conflict by keeping the upstream side")
	return cmd
}

// Run executes env resolve.
func (o *EnvResolveOptions) Run(envFilter []string) error {
	if o.Ours && o.Theirs {
		return fmt.Errorf("--ours and --theirs are mutually exclusive")
	}

	lockDir := o.LockDir()
	lk, err := lock.ReadLock(lockDir)
	if err != nil {
		return err
	}
	if lk == nil || len(lk.Envs) == 0 {
		fmt.Fprintln(o.IO.Out, "No envs in lock.")
		return nil
	}

	// Trim whitespace off each filter arg so the matching loop
	// below can compare against the canonical "ref" / "ref#label"
	// form built from the lock entry side. envKey is intentionally
	// just TrimSpace; the canonical form is built per-entry in the
	// loop, not here.
	filter := make(map[string]bool, len(envFilter))
	for _, k := range envFilter {
		filter[envKey(k)] = true
	}

	projectRoot := o.ProjectRoot()
	var totalConflicts, totalResolved int
	lockChanged := false
	// Collect per-file errors instead of returning early. Bulk
	// resolve mutates files on disk and the lock in memory; bailing
	// halfway through would leave the working tree partially
	// resolved while the lock still pointed at stale SHAs. Walk the
	// whole list, persist the lock once at the end, then surface
	// the aggregate error.
	var resolveErrors []string
	for ei := range lk.Envs {
		entry := &lk.Envs[ei]
		key := envKey(entry.Ref)
		if entry.Label != "" {
			key = entry.Ref + "#" + entry.Label
		}
		if len(filter) > 0 && !filter[key] {
			continue
		}
		for fi := range entry.Files {
			f := &entry.Files[fi]
			absDest := f.Dest
			if !filepath.IsAbs(absDest) {
				absDest = filepath.Join(projectRoot, absDest)
			}
			absDest = filepath.Clean(absDest)
			// Path-traversal check against projectRoot. A
			// malicious or hand-edited lockfile must not let
			// `b env resolve` read or write files outside the
			// project root.
			if err := env.ValidatePathUnderRoot(projectRoot, absDest); err != nil {
				resolveErrors = append(resolveErrors, fmt.Sprintf("path traversal rejected for %s: %v", f.Dest, err))
				continue
			}
			data, err := os.ReadFile(absDest)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				resolveErrors = append(resolveErrors, fmt.Sprintf("reading %s: %v", f.Dest, err))
				continue
			}
			if !hasResolvableConflictMarkers(data) {
				continue
			}

			// In list-only mode (no --ours/--theirs) we trust
			// the line-anchored marker scan and just print the
			// file. In rewrite mode we need the parser to find
			// at least one real region; otherwise the marker
			// scan was a false positive on a pathological
			// input and we leave the file alone WITHOUT
			// counting it as a conflict (so the summary line
			// reflects what was actually actionable).
			if !o.Ours && !o.Theirs {
				totalConflicts++
				fmt.Fprintf(o.IO.Out, "  %s → %s\n", key, f.Dest)
				continue
			}
			resolved, n, rerr := resolveConflictMarkers(data, o.Ours)
			if rerr != nil {
				resolveErrors = append(resolveErrors, fmt.Sprintf("resolving %s: %v", f.Dest, rerr))
				continue
			}
			if n == 0 {
				continue
			}
			totalConflicts++
			fmt.Fprintf(o.IO.Out, "  %s → %s\n", key, f.Dest)
			if err := os.WriteFile(absDest, resolved, 0644); err != nil {
				resolveErrors = append(resolveErrors, fmt.Sprintf("writing %s: %v", f.Dest, err))
				continue
			}
			// Update the lock entry's SHA so the next sync /
			// `b verify` doesn't treat the resolved file as
			// drifted. Hash the in-memory `resolved` bytes
			// rather than re-reading the file: avoids the
			// extra filesystem read AND closes a TOCTOU
			// window where the file could be modified between
			// the WriteFile and the rehash.
			f.SHA256 = fmt.Sprintf("%x", sha256.Sum256(resolved))
			lockChanged = true
			totalResolved += n
			side := "upstream"
			if o.Ours {
				side = "local"
			}
			fmt.Fprintf(o.IO.Out, "    → resolved %d region(s) in favor of %s\n", n, side)
		}
	}

	// Persist the lock BEFORE returning any error. If we got
	// halfway through a bulk resolve and then hit a write failure,
	// the working tree already has resolved files; the lock must
	// reflect that to avoid leaving stale SHAs that would make
	// every future status check report drift.
	if lockChanged {
		if err := lock.WriteLock(lockDir, lk, o.bVersion); err != nil {
			resolveErrors = append(resolveErrors, fmt.Sprintf("updating b.lock: %v", err))
		}
	}
	if len(resolveErrors) > 0 {
		return fmt.Errorf("env resolve: %d error(s):\n  - %s", len(resolveErrors), strings.Join(resolveErrors, "\n  - "))
	}

	if totalConflicts == 0 {
		fmt.Fprintln(o.IO.Out, "No conflicts found.")
		return nil
	}
	if !o.Ours && !o.Theirs {
		fmt.Fprintf(o.IO.Out, "\n%d conflicted file(s). Re-run with --ours or --theirs to resolve in bulk, or edit them manually.\n", totalConflicts)
		return nil
	}
	fmt.Fprintf(o.IO.Out, "\nResolved %d region(s) across %d file(s).\n", totalResolved, totalConflicts)
	return nil
}

// envKey trims surrounding whitespace from a user-supplied env key.
// The matching loop in Run already builds the canonical "ref" /
// "ref#label" form from the lock entry side, so this helper only
// needs to neutralize stray whitespace before comparing.
func envKey(s string) string {
	return strings.TrimSpace(s)
}

// hasResolvableConflictMarkers reports whether the byte slice
// contains a git merge-file conflict region that env resolve can
// rewrite. The check is line-anchored (and tolerates trailing CR for
// CRLF files) so a marker substring inside a YAML string literal or
// a markdown rule like `=======` can't trigger a false positive —
// Run() would otherwise rewrite a file that has no real conflicts.
//
// This is intentionally distinct from env.go's hasConflictMarkers,
// which is the strict env-status detector. env resolve needs a
// looser detector that:
//   - matches bare `<<<<<<<` / `>>>>>>>` lines (no label suffix)
//     because resolveConflictMarkers' parser uses the same loose
//     prefix
//   - reports an unterminated region (start marker but no closing
//     line) as conflicted, so the cleanup command surfaces files
//     left in a half-merged state by a partial manual edit
//
// env status uses the strict variant so partial files (e.g. a
// document that legitimately starts with a `<<<<<<<` literal in a
// YAML string) don't show up as drifted.
func hasResolvableConflictMarkers(b []byte) bool {
	var hasStart, hasSep, hasEnd bool
	pending := make([]byte, 0, conflictPendingMax)
	consume := func() {
		line := pending
		if n := len(line); n > 0 && line[n-1] == '\r' {
			line = line[:n-1]
		}
		// Match the same loose prefix that resolveConflictMarkers
		// uses: a bare `<<<<<<<` line (no label suffix) is valid
		// hand-edited conflict and the resolver will rewrite it,
		// so the detector must agree.
		switch {
		case bytes.HasPrefix(line, []byte("<<<<<<<")):
			hasStart = true
		case bytes.Equal(line, []byte("=======")):
			hasSep = true
		case bytes.HasPrefix(line, []byte(">>>>>>>")):
			hasEnd = true
		}
	}
	for _, c := range b {
		if c == '\n' {
			consume()
			pending = pending[:0]
			if hasStart && hasSep && hasEnd {
				return true
			}
			continue
		}
		if len(pending) < conflictPendingMax {
			pending = append(pending, c)
		}
	}
	consume()
	// Treat the presence of a start marker as conflicted even
	// without a closing >>>>>>> line. The point of `b env resolve`
	// is to surface and clean up malformed merge state, so an
	// unterminated region — which a partial manual edit can leave
	// behind — needs to show up rather than be silently ignored.
	// resolveConflictMarkers below will fail to find a complete
	// region and either error out or return n == 0, which the Run
	// loop already handles.
	return hasStart
}

// resolveConflictMarkers walks a file containing git-style conflict
// regions and rewrites it by keeping one side of every region. It
// supports both 2-way (`<<<<<<< / ======= / >>>>>>>`) and diff3
// (`<<<<<<< / ||||||| / ======= / >>>>>>>`) marker forms — `b env
// update` writes the diff3 form, but the parser tolerates either so
// hand-edited files still resolve cleanly.
//
// keepOurs picks the block between `<<<<<<<` and the base or middle
// marker; otherwise the block between `=======` and `>>>>>>>` wins.
//
// The implementation operates on []byte throughout (not a Go string)
// so files containing non-UTF-8 bytes round-trip without corruption.
// The separator marker is matched as the EXACT line `=======` (with
// optional trailing `\r` for CRLF files) so a content line that
// happens to start with `=======...` isn't misinterpreted as the
// separator.
//
// Returns (resolved bytes, number of regions resolved, error).
func resolveConflictMarkers(data []byte, keepOurs bool) ([]byte, int, error) {
	lines := bytes.Split(data, []byte("\n"))
	var out [][]byte
	var count int
	startPrefix := []byte("<<<<<<<")
	basePrefix := []byte("|||||||")
	endPrefix := []byte(">>>>>>>")
	i := 0
	for i < len(lines) {
		line := lines[i]
		if !bytes.HasPrefix(line, startPrefix) {
			out = append(out, line)
			i++
			continue
		}
		// Found a conflict opening. Scan forward for the middle
		// (||||||| or =======) and end (>>>>>>>) markers.
		startOurs := i + 1
		var endOurs, startTheirs int
		var endTheirs int
		j := startOurs
		for j < len(lines) {
			if bytes.HasPrefix(lines[j], basePrefix) {
				endOurs = j
				// Skip the base section to the `=======`.
				k := j + 1
				for k < len(lines) && !isSeparatorLine(lines[k]) {
					k++
				}
				if k >= len(lines) {
					return nil, 0, fmt.Errorf("unterminated conflict region at line %d", i+1)
				}
				startTheirs = k + 1
				break
			}
			if isSeparatorLine(lines[j]) {
				endOurs = j
				startTheirs = j + 1
				break
			}
			j++
		}
		if j >= len(lines) {
			return nil, 0, fmt.Errorf("unterminated conflict region at line %d", i+1)
		}
		// Find the closing marker.
		k := startTheirs
		for k < len(lines) && !bytes.HasPrefix(lines[k], endPrefix) {
			k++
		}
		if k >= len(lines) {
			return nil, 0, fmt.Errorf("missing closing marker for conflict at line %d", i+1)
		}
		endTheirs = k
		if keepOurs {
			out = append(out, lines[startOurs:endOurs]...)
		} else {
			out = append(out, lines[startTheirs:endTheirs]...)
		}
		count++
		i = endTheirs + 1
	}
	return bytes.Join(out, []byte("\n")), count, nil
}

// isSeparatorLine matches the exact `=======` separator line, with
// optional trailing `\r` for CRLF files. We deliberately do NOT use
// HasPrefix here so a content line that starts with `=======...`
// (e.g. a markdown rule that happens to be inside a conflict
// region's "ours" side) isn't misinterpreted.
func isSeparatorLine(line []byte) bool {
	return bytes.Equal(line, []byte("=======")) || bytes.Equal(line, []byte("=======\r"))
}
