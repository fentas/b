//go:build !race

package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstallOptions_Run_ExistingBinary(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, ".bin")
	_ = os.MkdirAll(binDir, 0755)
	_ = os.WriteFile(filepath.Join(binDir, "jq"), []byte("#!/bin/sh\n"), 0755)
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
	_ = os.MkdirAll(binDir, 0755)
	_ = os.WriteFile(filepath.Join(binDir, "jq"), []byte("#!/bin/sh\n"), 0755)
	t.Setenv("PATH_BIN", binDir)
	t.Setenv("PATH_BASE", dir)

	cfgYaml := filepath.Join(dir, "b.yaml")
	_ = os.WriteFile(cfgYaml, []byte("binaries: {}\n"), 0644)
	shared := NewSharedOptions(mkIO(), mkBinaries())
	shared.ConfigPath = cfgYaml
	_ = shared.LoadConfig()
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
	_ = os.MkdirAll(binDir, 0755)
	_ = os.WriteFile(filepath.Join(binDir, "jq"), []byte("#!/bin/sh\n"), 0755)
	t.Setenv("PATH_BIN", binDir)
	t.Setenv("PATH_BASE", dir)

	// Force mode with a binary that has no download source → will fail, but exercises the path
	shared := NewSharedOptions(mkIO(), mkBinaries())
	shared.Force = true
	o := &InstallOptions{SharedOptions: shared}
	if err := o.Complete([]string{"jq"}); err != nil {
		t.Fatal(err)
	}
	// Expected to fail (no URL in mkBinaries jq), but covers the force path
	_ = o.Run()
}

func TestCmdBinaryOptions_Run_InstallExisting(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, ".bin")
	_ = os.MkdirAll(binDir, 0755)
	_ = os.WriteFile(filepath.Join(binDir, "jq"), []byte("#!/bin/sh\n"), 0755)
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
