package gitcache

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// passwordRefs are refs without a scheme with a password in front of the
// host: with and without an @ in the password, a path and a version.
func passwordRefs() []string {
	var refs []string
	for _, user := range []string{"user:p@" + refPassword + "@", "user:" + refPassword + "@"} {
		for _, path := range []string{"", "/org/repo"} {
			for _, version := range []string{"", "@v1"} {
				refs = append(refs, user+"github.com"+path+version)
			}
		}
	}
	return refs
}

// checkRefused fails the test unless CheckRef refuses ref with the fixed
// text, ResolveGitURL gives refusedURL with no token, ls-remote, clone and
// fetch return the fixed text, and no git command runs. No error shows the
// password, in each format verb.
func checkRefused(t *testing.T, name, ref string) {
	t.Helper()
	cmds := recordGit(t)
	if err := CheckRef(ref); !errors.Is(err, errUserinfoAt) || err.Error() != refusedText {
		t.Errorf("%s: CheckRef does not give the fixed text (error: %v)", name, err != nil)
	}
	r := ResolveGitURL(ref, "")
	if r != (ResolvedRef{URL: refusedURL}) {
		t.Errorf("%s: ResolveGitURL = URL %q, local %v, ssh %v, token set %v; want refusedURL and no token",
			name, strings.ReplaceAll(r.URL, refPassword, "<password>"), r.IsLocal, r.IsSSH, r.AuthToken != "")
	}
	for op, err := range remoteErrors(t, ref, r) {
		if err == nil || err.Error() != refusedText {
			t.Errorf("%s %s: error %q, want the fixed text", name, op,
				strings.ReplaceAll(fmt.Sprint(err), refPassword, "<password>"))
		}
		if err != nil && formatsHold(err, refPassword) {
			t.Errorf("%s %s: the error shows the password", name, op)
		}
	}
	if len(*cmds) != 0 {
		t.Errorf("%s: %d git commands ran", name, len(*cmds))
	}
}

// A ref without a scheme with a password in front of the host
// ("user:password@host") is refused before its version is split, with and
// without an @ in the password, a path and a version, and also with
// B_GIT_SSH_AS_HTTPS=1. Before, a ref with no path cut the ref at an @ in
// its user part. RefBase and RefVersion do not cut it there either, so the
// user part of RefBase shows as "***" (RedactUserinfo), as it does for the
// ref.
func TestResolveGitURL_PasswordRefsRefused(t *testing.T) {
	for _, knob := range []string{"", "1"} {
		t.Setenv(SSHAsHTTPSEnv, knob)
		t.Setenv("GITHUB_TOKEN", refToken)
		for i, ref := range passwordRefs() {
			name := fmt.Sprintf("%s=%q ref %d", SSHAsHTTPSEnv, knob, i)
			checkRefused(t, name, ref)
			for what, s := range map[string]string{
				"RedactUserinfo(ref)":          RedactUserinfo(ref),
				"RedactUserinfo(RefBase(ref))": RedactUserinfo(RefBase(ref)),
				"RefVersion(ref)":              RefVersion(ref),
			} {
				if strings.Contains(s, refPassword) {
					t.Errorf("%s: %s shows the password", name, what)
				}
			}
		}
	}
}

// More than one @ in front of the host is refused in every form, also with
// B_GIT_SSH_AS_HTTPS=1 and with a version: an scp-like ref (a real one has
// one @, "git@github.com:org/repo") and an ssh:// ref. Before, both stayed
// SSH and reached git.
func TestResolveGitURL_MoreThanOneAtInFrontOfHost(t *testing.T) {
	refs := []string{
		"git@x@github.com:org/repo",
		"git@x@github.com:org/repo@v1",
		"git@" + refPassword + "@github.com:org/repo",
		"ssh://git@x@github.com/org/repo",
		"ssh://git@x@github.com/org/repo@v1",
		"ssh://git@" + refPassword + "@github.com/org/repo",
		"ssh://git:p@" + refPassword + "@github.com/org/repo",
		"SSH://git@" + refPassword + "@github.com/org/repo",
	}
	for _, knob := range []string{"", "1"} {
		t.Setenv(SSHAsHTTPSEnv, knob)
		t.Setenv("GITHUB_TOKEN", refToken)
		for i, ref := range refs {
			checkRefused(t, fmt.Sprintf("%s=%q ref %d", SSHAsHTTPSEnv, knob, i), ref)
		}
	}
}

// CheckRef refuses a password or more than one @ in front of the host, and
// lets every other form pass. The remote git commands refuse some of the
// forms that pass, with their own text (checkRemote).
func TestCheckRef_Forms(t *testing.T) {
	refused := []string{
		"user:pw@github.com",
		"user:pw@github.com/org/repo",
		"user:pw@github.com#lbl",
		"user:p@pw@github.com",
		"tok@pw@github.com/org/repo",
		"a@b@github.com",
		"github.com:443@evil.example/x",
		"user:pw?x@github.com/org/repo", // a "?" in the password: the first part ends at the "/"
		"user:pw#x@github.com/org/repo", // a "#" in the password
		"git@x@github.com:org/repo",
		"git://user:pw@github.com/org/repo:tool.sh",
		"git://a@b@github.com/org/repo",
		"https://a@b@github.com/org/repo",
		"ssh://git@x@github.com/org/repo",
		"http://a@b@host.example/x",
		"https://http://a@b@host.example/x",
		"git://http://a@b@host.example/x",
	}
	for _, ref := range refused {
		if err := CheckRef(ref); !errors.Is(err, errUserinfoAt) {
			t.Errorf("CheckRef(%q) = %v, want the fixed text", ref, err)
		}
	}
	passed := []string{
		"github.com/org/repo",
		"github.com/org/repo@v1",
		"github.com/org/repo#lbl@v1",
		"host.example:443/org/repo",
		"kubectl@v1.30",
		"git@github.com:org/repo",
		"git@host.example:org/repo@v1",
		"git@github.com:x@y:z",
		"deploy@host.example:repo@release/1.0",
		"ssh://git@github.com/org/repo",
		"ssh://git@github.com/org/repo@v1",
		"ssh://user:pw@host.example/org/repo", // reaches git: an ssh:// ref keeps its password
		"https://github.com/org/repo",
		"https://user:pw@github.com/org/repo", // the host check refuses it
		"tok@github.com/org/repo",             // the host check refuses it
		"http://user:pw@host.example/x",       // the scheme check refuses it
		"git://github.com/org/repo:path/file@v1",
		"git:///abs/repo:file",
		"git://./rel:file",
		"/home/me@work/repo",
		"./a:b@c",
		"../x@y:z",
		".",
		"..",
	}
	for _, ref := range passed {
		if err := CheckRef(ref); err != nil {
			t.Errorf("CheckRef(%q) = %v, want nil", ref, err)
		}
	}
}
