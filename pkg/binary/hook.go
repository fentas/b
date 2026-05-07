package binary

import (
	"fmt"
	"os"
	"os/exec"
)

// RunHook executes a shell command with B_EVENT, B_NAME, B_VERSION, B_FILE
// env vars set. dir is the working directory (project root). Returns nil
// on success, the exec error on failure. Callers decide whether to treat
// the error as fatal or as a warning.
func RunHook(command, dir, event, name, version, file string, stdout, stderr *os.File) error {
	if command == "" {
		return nil
	}
	cmd := exec.Command("sh", "-c", command)
	cmd.Dir = dir
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("B_EVENT=%s", event),
		fmt.Sprintf("B_NAME=%s", name),
		fmt.Sprintf("B_VERSION=%s", version),
		fmt.Sprintf("B_FILE=%s", file),
	)
	return cmd.Run()
}
