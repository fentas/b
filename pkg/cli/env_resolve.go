package cli

import (
	"bytes"
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
				return fmt.Errorf("path traversal rejected for %s: %w", f.Dest, err)
			}
			data, err := os.ReadFile(absDest)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return fmt.Errorf("reading %s: %w", f.Dest, err)
			}
			if !hasConflictMarkers(data) {
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
				return fmt.Errorf("resolving %s: %w", f.Dest, rerr)
			}
			if n == 0 {
				continue
			}
			totalConflicts++
			fmt.Fprintf(o.IO.Out, "  %s → %s\n", key, f.Dest)
			if err := os.WriteFile(absDest, resolved, 0644); err != nil {
				return fmt.Errorf("writing %s: %w", f.Dest, err)
			}
			// Update the lock entry's SHA so the next sync /
			// `b verify` doesn't treat the resolved file as
			// drifted. Without this step, `b env resolve` would
			// produce a state where every status check still
			// flagged the file as locally modified.
			newHash, hashErr := lock.SHA256File(absDest)
			if hashErr != nil {
				return fmt.Errorf("rehashing %s after resolve: %w", f.Dest, hashErr)
			}
			if newHash == "" {
				return fmt.Errorf("rehashing %s after resolve: empty digest", f.Dest)
			}
			f.SHA256 = newHash
			lockChanged = true
			totalResolved += n
			side := "upstream"
			if o.Ours {
				side = "local"
			}
			fmt.Fprintf(o.IO.Out, "    → resolved %d region(s) in favor of %s\n", n, side)
		}
	}

	if lockChanged {
		if err := lock.WriteLock(lockDir, lk, o.bVersion); err != nil {
			return fmt.Errorf("updating b.lock after resolve: %w", err)
		}
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

// hasConflictMarkers reports whether the byte slice contains a git
// merge-file conflict region. The check is line-anchored (and
// tolerates a trailing CR for CRLF files) so a marker substring
// inside a YAML string literal or a markdown rule like `=======`
// can't trigger a false positive — Run() would otherwise rewrite
// a file that has no real conflicts.
//
// Implementation note: a tiny state machine instead of bufio.Scanner.
// Scanner has a max-token-size cap that would silently fail on very
// long lines (SOPS blobs, minified JSON, lockfiles). The state
// machine bounds per-line state at conflictPendingMax bytes — the
// markers we care about are at most 16 bytes — so a degenerate
// single long line is harmless regardless of file size.
const conflictPendingMax = 64

func hasConflictMarkers(b []byte) bool {
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
// Returns (resolved bytes, number of regions resolved, error).
func resolveConflictMarkers(data []byte, keepOurs bool) ([]byte, int, error) {
	lines := strings.Split(string(data), "\n")
	var out []string
	var count int
	i := 0
	for i < len(lines) {
		line := lines[i]
		if !strings.HasPrefix(line, "<<<<<<<") {
			out = append(out, line)
			i++
			continue
		}
		// Found a conflict opening. Scan forward for the middle (|||||||
		// or =======) and end (>>>>>>>) markers.
		startOurs := i + 1
		var endOurs, startTheirs int
		var endTheirs int
		j := startOurs
		// First search for `|||||||` (diff3 base) or `=======`.
		for j < len(lines) {
			if strings.HasPrefix(lines[j], "|||||||") {
				endOurs = j
				// Skip the base section to the `=======`.
				k := j + 1
				for k < len(lines) && !strings.HasPrefix(lines[k], "=======") {
					k++
				}
				if k >= len(lines) {
					return nil, 0, fmt.Errorf("unterminated conflict region at line %d", i+1)
				}
				startTheirs = k + 1
				break
			}
			if strings.HasPrefix(lines[j], "=======") {
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
		for k < len(lines) && !strings.HasPrefix(lines[k], ">>>>>>>") {
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
	return []byte(strings.Join(out, "\n")), count, nil
}
