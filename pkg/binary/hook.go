package binary

import (
	"fmt"
	"io"
	"os"
	"os/exec"
)

// RunHook executes a shell command with B_EVENT, B_NAME, B_VERSION, B_FILE
// env vars set. dir is the working directory (project root). stdout/stderr
// accept io.Writer so callers can route through the CLI's IO streams
// (respecting --quiet / output capture). nil writers default to io.Discard.
// Returns nil on success, the exec error on failure. Callers decide whether
// to treat the error as fatal or as a warning.
//
// The command runs via "sh -c" — hooks are POSIX-only. This is consistent
// with the existing env onPreSync/onPostSync hooks in pkg/env/env.go.
func RunHook(command, dir, event, name, version, file string, stdout, stderr io.Writer) error {
	if command == "" {
		return nil
	}
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	cmd := exec.Command("sh", "-c", command)
	cmd.Dir = dir
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	// Build env from parent process, filtering out the four hook-specific
	// variables (B_EVENT, B_NAME, B_VERSION, B_FILE) so our values take
	// guaranteed precedence regardless of platform.
	hookVars := map[string]bool{
		"B_EVENT=": true, "B_NAME=": true, "B_VERSION=": true, "B_FILE=": true,
	}
	env := make([]string, 0, len(os.Environ())+4)
	for _, e := range os.Environ() {
		skip := false
		for prefix := range hookVars {
			if len(e) >= len(prefix) && e[:len(prefix)] == prefix {
				skip = true
				break
			}
		}
		if !skip {
			env = append(env, e)
		}
	}
	env = append(env,
		fmt.Sprintf("B_EVENT=%s", event),
		fmt.Sprintf("B_NAME=%s", name),
		fmt.Sprintf("B_VERSION=%s", version),
		fmt.Sprintf("B_FILE=%s", file),
	)
	cmd.Env = env
	return cmd.Run()
}
