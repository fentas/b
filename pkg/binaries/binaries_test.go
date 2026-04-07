package binaries

import "testing"

func TestArch(t *testing.T) {
	if got := Arch("amd64"); got != "x86_64" {
		t.Errorf("Arch(amd64) = %q, want x86_64", got)
	}
	if got := Arch("arm64"); got != "arm64" {
		t.Errorf("Arch(arm64) = %q, want arm64", got)
	}
}
