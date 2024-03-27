package binary

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

func (b *Binary) Env() []string {
	env := []string{}
	for k, v := range b.Envs {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
	return env
}

func (b *Binary) Cmd(args ...string) *exec.Cmd {
	if !b.BinaryExists() {
		return nil
	}
	if b.Context == nil {
		b.Context = context.Background()
	}

	cmd := exec.CommandContext(b.Context, b.BinaryPath(), args...)
	// alsways set the environment
	// if env is nil, it will use the user environment
	cmd.Env = b.Env()
	return cmd
}

func (b *Binary) Exec(args ...string) (string, error) {
	cmd := b.Cmd(args...)
	if cmd == nil {
		return "", fmt.Errorf("binary %s does not exist", b.Name)
	}

	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}
