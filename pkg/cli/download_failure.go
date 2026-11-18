package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/fentas/b/pkg/atomicfile"
	"github.com/fentas/b/pkg/binary"
)

// downloadFailure is the error of a command whose downloads failed: one line
// per binary, with the cause. b install, b update and the -i flag of
// NewCmdBinary return it after they did the rest of their work.
type downloadFailure struct{ msg string }

func (e *downloadFailure) Error() string { return e.msg }

// isDownloadFailure reports whether err is a downloadFailure and nothing
// else. An error that joins a downloadFailure with another error (a b.lock
// that cannot be read) is not: the command stops on it at once.
func isDownloadFailure(err error) bool {
	_, ok := err.(*downloadFailure)
	return ok
}

// withDownloadErr is err, after the error dl of the failed downloads when
// there is one. Without dl it is err as it is, and without err it is dl as
// it is.
func withDownloadErr(dl, err error) error {
	if dl == nil {
		return err
	}
	if err == nil {
		return dl
	}
	return errors.Join(dl, err)
}

// failedDownloads is the error of the downloads of binaries that failed
// (failed holds the error of each binary), nil when none failed. hint, when
// not empty, is the last line.
func failedDownloads(binaries []*binary.Binary, failed map[*binary.Binary]error, hint string) error {
	var lines []string
	for _, b := range binaries {
		if err := failed[b]; err != nil {
			lines = append(lines, fmt.Sprintf("  %s: %v", displayName(b), err))
		}
	}
	if len(lines) == 0 {
		return nil
	}
	msg := "download failed:\n" + strings.Join(lines, "\n")
	if hint != "" {
		msg += "\n  Hint: " + hint
	}
	return &downloadFailure{msg: msg}
}

// removeStaleTemps removes the temp files that a stopped install or update
// (Ctrl-C, a kill, a crash) left beside the file of each binary
// (atomicfile.RemoveStale: only the temp file names of that file, only
// regular files, only when no process uses them). It names each file that
// it removed. A failure is a warning: the command goes on. It does not set
// the File of a binary (FilePath, not BinaryPath): the update reads an
// empty File as "not known yet".
func (o *SharedOptions) removeStaleTemps(binaries []*binary.Binary) {
	for _, b := range binaries {
		removed, err := atomicfile.RemoveStale(b.FilePath())
		for _, p := range removed {
			fmt.Fprintf(o.IO.ErrOut, "Removed %s: a stopped install or update of %s left this temp file\n", p, displayName(b))
		}
		if err != nil {
			fmt.Fprintf(o.IO.ErrOut, "Warning: cannot remove the temp files of %s: %v\n", displayName(b), err)
		}
	}
}
