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

	// Normalize each filter arg to its canonical "ref" or "ref#label"
	// form so labeled envs can be targeted unambiguously when multiple
	// lock entries share the same ref.
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
			totalConflicts++
			fmt.Fprintf(o.IO.Out, "  %s → %s\n", key, f.Dest)

			if !o.Ours && !o.Theirs {
				continue
			}
			resolved, n, rerr := resolveConflictMarkers(data, o.Ours)
			if rerr != nil {
				return fmt.Errorf("resolving %s: %w", f.Dest, rerr)
			}
			if err := os.WriteFile(absDest, resolved, 0644); err != nil {
				return fmt.Errorf("writing %s: %w", f.Dest, err)
			}
			// Update the lock entry's SHA so the next sync /
			// `b verify` doesn't treat the resolved file as
			// drifted. Without this step, `b env resolve` would
			// produce a state where every status check still
			// flagged the file as locally modified.
			newHash, hashErr := lock.SHA256File(absDest)
			if hashErr == nil && newHash != "" {
				f.SHA256 = newHash
				lockChanged = true
			}
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

// envKey returns the canonical "ref" or "ref#label" form for a user-
// supplied env key. It's a no-op for keys that already include a label
// or that don't need normalization; it exists so the filter matching
// in Run can compare against the same canonical form the lock entries
// produce.
func envKey(s string) string {
	return strings.TrimSpace(s)
}

// hasConflictMarkers reports whether the byte slice contains the
// minimum signature of a git merge-file conflict region. We require
// all three markers because partial matches can be legitimate file
// content (e.g. a markdown rule like `=======`).
func hasConflictMarkers(b []byte) bool {
	return bytes.Contains(b, []byte("<<<<<<<")) &&
		bytes.Contains(b, []byte("=======")) &&
		bytes.Contains(b, []byte(">>>>>>>"))
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
