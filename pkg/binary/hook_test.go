package binary

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunHook_SetsEnvVars(t *testing.T) {
	tmp := t.TempDir()
	out := filepath.Join(tmp, "env.txt")

	// The hook writes the four B_ env vars to a file so we can inspect them.
	cmd := `printf "%s\n%s\n%s\n%s" "$B_EVENT" "$B_NAME" "$B_VERSION" "$B_FILE" > ` + out
	if err := RunHook(cmd, tmp, "install", "kubectl", "v1.28.0", "/usr/local/bin/kubectl", os.Stdout, os.Stderr); err != nil {
		t.Fatalf("RunHook: %v", err)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	lines := string(data)
	want := "install\nkubectl\nv1.28.0\n/usr/local/bin/kubectl"
	if lines != want {
		t.Errorf("env vars:\ngot:  %q\nwant: %q", lines, want)
	}
}

func TestRunHook_EmptyIsNoOp(t *testing.T) {
	if err := RunHook("", t.TempDir(), "install", "x", "v1", "/x", os.Stdout, os.Stderr); err != nil {
		t.Errorf("empty hook should be no-op, got: %v", err)
	}
}

func TestRunHook_NonZeroExitReturnsError(t *testing.T) {
	if err := RunHook("exit 1", t.TempDir(), "update", "x", "v1", "/x", os.Stdout, os.Stderr); err == nil {
		t.Error("expected error on non-zero exit")
	}
}

func TestRunHook_RunsInDir(t *testing.T) {
	tmp := t.TempDir()
	out := filepath.Join(tmp, "pwd.txt")
	cmd := "pwd > " + out
	if err := RunHook(cmd, tmp, "install", "x", "v1", "/x", os.Stdout, os.Stderr); err != nil {
		t.Fatalf("RunHook: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if got[:len(got)-1] != tmp { // trim trailing newline
		t.Errorf("hook ran in %q, want %q", got, tmp)
	}
}
