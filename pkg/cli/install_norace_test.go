//go:build !race

package cli

import (
	"path/filepath"
	"testing"
)

func TestInstallOptions_Run_ExistingBinary(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, ".bin")
	mustMkdir(t, binDir)
	mustWriteExec(t, filepath.Join(binDir, "jq"), []byte("#!/bin/sh\n"))
	t.Setenv("PATH_BIN", binDir)
	t.Setenv("PATH_BASE", dir)

	shared := NewSharedOptions(mkIO(), mkBinaries())
	o := &InstallOptions{SharedOptions: shared}
	if err := o.Complete([]string{"jq"}); err != nil {
		t.Fatal(err)
	}
	if err := o.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := o.Run(); err != nil {
		t.Errorf("Run: %v", err)
	}
}

func TestInstallOptions_Run_AddBinaryToConfig(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, ".bin")
	mustMkdir(t, binDir)
	mustWriteExec(t, filepath.Join(binDir, "jq"), []byte("#!/bin/sh\n"))
	t.Setenv("PATH_BIN", binDir)
	t.Setenv("PATH_BASE", dir)

	cfgYaml := filepath.Join(dir, "b.yaml")
	mustWrite(t, cfgYaml, []byte("binaries: {}\n"))
	shared := NewSharedOptions(mkIO(), mkBinaries())
	shared.ConfigPath = cfgYaml
	if err := shared.LoadConfig(); err != nil {
		t.Fatal(err)
	}
	o := &InstallOptions{SharedOptions: shared, Add: true}
	if err := o.Complete([]string{"jq"}); err != nil {
		t.Fatal(err)
	}
	if err := o.Run(); err != nil {
		t.Errorf("%v", err)
	}
}

func TestInstallOptions_Run_ForceMode(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, ".bin")
	mustMkdir(t, binDir)
	mustWriteExec(t, filepath.Join(binDir, "jq"), []byte("#!/bin/sh\n"))
	t.Setenv("PATH_BIN", binDir)
	t.Setenv("PATH_BASE", dir)

	// Force mode exercises the DownloadBinary path. installBinaries reports
	// per-binary errors via the progress writer, not the Run() return value,
	// so Run() is expected to succeed even when the underlying download fails
	// (mkBinaries() jq has no URL). The smoke test is that it doesn't panic.
	shared := NewSharedOptions(mkIO(), mkBinaries())
	shared.Force = true
	o := &InstallOptions{SharedOptions: shared}
	if err := o.Complete([]string{"jq"}); err != nil {
		t.Fatal(err)
	}
	if err := o.Run(); err != nil {
		t.Errorf("Run: %v", err)
	}
}

func TestCmdBinaryOptions_Run_InstallExisting(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, ".bin")
	mustMkdir(t, binDir)
	mustWriteExec(t, filepath.Join(binDir, "jq"), []byte("#!/bin/sh\n"))
	t.Setenv("PATH_BIN", binDir)

	opts := &CmdBinaryOptions{IO: mkIO(), Binaries: mkBinaries()}
	c := NewCmdBinary(opts)
	opts.install = true
	opts.all = true
	if err := opts.Complete(c, nil); err != nil {
		t.Fatal(err)
	}
	if err := opts.Run(); err != nil {
		t.Errorf("%v", err)
	}
}
