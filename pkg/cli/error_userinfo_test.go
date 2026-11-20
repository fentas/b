package cli

import (
	"strings"
	"testing"

	"github.com/fentas/b/pkg/gitcache"
)

// fakePassword is the password in the refs of these tests. It is not a real
// value.
const fakePassword = "fake-pw-value-for-tests"

// For a ref with a user part, b env match, b env profiles and b env add
// show "***@" in place of the user part in the resolve error (env.go,
// "resolving"). b refuses the URL, so no remote git command runs.
func TestEnvCommands_ResolveErrorHidesTheUserPart(t *testing.T) {
	offlineUpstream(t)
	ref := "user:" + fakePassword + "@github.com/org/repo"
	io, _, _ := bufIO()
	for _, tt := range []struct {
		name string
		run  func() error
	}{
		{"env match", func() error {
			return (&EnvMatchOptions{SharedOptions: NewSharedOptions(io, nil)}).Run([]string{ref, "manifests/*.yaml"})
		}},
		{"env profiles", func() error {
			return (&EnvProfilesOptions{SharedOptions: NewSharedOptions(io, nil)}).Run(ref)
		}},
		{"env add", func() error {
			_, err := (&EnvAddOptions{SharedOptions: NewSharedOptions(io, nil)}).fetchUpstream(gitcache.RefBase(ref), "", ref)
			return err
		}},
	} {
		err := tt.run()
		if err == nil {
			t.Fatalf("%s: no error for a ref with a password", tt.name)
		}
		msg := err.Error()
		if strings.Contains(msg, fakePassword) {
			t.Errorf("%s: the error shows the password", tt.name)
		}
		if !strings.HasPrefix(msg, "resolving ***@github.com/org/repo: refusing https://***@github.com/org/repo.git") {
			t.Errorf("%s: error %q, want the resolve prefix and the refused URL with ***@", tt.name, msg)
		}
	}
}
