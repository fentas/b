package binary

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestRunHook_SetsEnvVars(t *testing.T) {
	tmp := t.TempDir()
	out := filepath.Join(tmp, "env.txt")

	cmd := `printf "%s\n%s\n%s\n%s" "$B_EVENT" "$B_NAME" "$B_VERSION" "$B_FILE" > ` + out
	if err := RunHook(cmd, tmp, "install", "kubectl", "v1.28.0", "/usr/local/bin/kubectl", os.Stdout, os.Stderr); err != nil {
		t.Fatalf("RunHook: %v", err)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	want := "install\nkubectl\nv1.28.0\n/usr/local/bin/kubectl"
	if string(data) != want {
		t.Errorf("env vars:\ngot:  %q\nwant: %q", data, want)
	}
}

func TestRunHook_EmptyIsNoOp(t *testing.T) {
	if err := RunHook("", t.TempDir(), "install", "x", "v1", "/x", nil, nil); err != nil {
		t.Errorf("empty hook should be no-op, got: %v", err)
	}
}

func TestRunHook_NonZeroExitReturnsError(t *testing.T) {
	if err := RunHook("exit 1", t.TempDir(), "update", "x", "v1", "/x", nil, nil); err == nil {
		t.Error("expected error on non-zero exit")
	}
}

func TestRunHook_RunsInDir(t *testing.T) {
	tmp := t.TempDir()
	out := filepath.Join(tmp, "pwd.txt")
	cmd := "pwd > " + out
	if err := RunHook(cmd, tmp, "install", "x", "v1", "/x", nil, nil); err != nil {
		t.Fatalf("RunHook: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if got[:len(got)-1] != tmp {
		t.Errorf("hook ran in %q, want %q", got, tmp)
	}
}

func TestRunHook_WritesToProvidedStreams(t *testing.T) {
	var out bytes.Buffer
	if err := RunHook("echo hello", t.TempDir(), "install", "x", "v1", "/x", &out, nil); err != nil {
		t.Fatalf("RunHook: %v", err)
	}
	if out.String() != "hello\n" {
		t.Errorf("stdout = %q, want %q", out.String(), "hello\n")
	}
}

func TestRunHook_NilWritersDefaultToDiscard(t *testing.T) {
	// Should not panic with nil writers.
	if err := RunHook("echo ok", t.TempDir(), "install", "x", "v1", "/x", nil, nil); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}
