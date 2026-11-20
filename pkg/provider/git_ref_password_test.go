package provider

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// canaryPassword and canaryToken are the password and the provider token of
// these tests. They are not real values.
const (
	canaryPassword = "pw-canary-q7z"
	canaryToken    = "FAKE-TOKEN-x"
)

// Git.Install and Git.LatestVersion refuse a git ref with a password or more
// than one @ in front of the host before they split it at its first ":":
// the error is the fixed text alone, the directory of dest is not made, and
// no git command runs (a fake git on the PATH records each call).
func TestGitRef_PasswordRefRefusedFirst(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake git is a shell script")
	}
	home := t.TempDir()
	t.Setenv("HOME", home) // gitcache.DefaultCacheRoot is under HOME
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GITHUB_TOKEN", canaryToken)
	bin := t.TempDir()
	calls := filepath.Join(bin, "calls")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + calls + "'\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)

	const refusal = "refusing a git ref with a password or more than one @ before its path: remove the user part"
	refs := []string{
		"git://user:" + canaryPassword + "@github.com/org/repo:tool.sh",
		"git://user:" + canaryPassword + "@github.com:tool.sh",
		"git://user:p@" + canaryPassword + "@github.com/org/repo:tool.sh@v1",
		"git@" + canaryPassword + "@github.com:org/repo:tool.sh",
		"ssh://git@" + canaryPassword + "@github.com/org/repo:tool.sh",
	}
	for i, ref := range refs {
		dest := filepath.Join(t.TempDir(), "bin", "tool")
		_, latestErr := (&Git{}).LatestVersion(ref)
		for op, err := range map[string]error{
			"Install":       (&Git{}).Install(ref, "", dest),
			"LatestVersion": latestErr,
		} {
			name := fmt.Sprintf("ref %d %s", i, op)
			if err == nil || err.Error() != refusal {
				t.Errorf("%s: error %q, want the fixed text alone", name,
					strings.ReplaceAll(fmt.Sprint(err), canaryPassword, "<password>"))
			}
			for _, verb := range []string{"%s", "%v", "%+v", "%q", "%#v"} {
				if text := fmt.Sprintf(verb, err); strings.Contains(text, canaryPassword) || strings.Contains(text, canaryToken) {
					t.Errorf("%s: the %s text shows the password or the token", name, verb)
				}
			}
		}
		if _, err := os.Stat(filepath.Dir(dest)); err == nil {
			t.Errorf("ref %d: Install made the directory of dest", i)
		}
	}
	if b, err := os.ReadFile(calls); err == nil {
		t.Errorf("git ran:\n%s", strings.ReplaceAll(string(b), canaryPassword, "<password>"))
	}
}
