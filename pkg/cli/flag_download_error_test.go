package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/fentas/goodies/cmdutil"
	"github.com/fentas/goodies/streams"

	"github.com/fentas/b/pkg/binary"
)

// fatalExit is what the command gave to the fatal error handler of
// cmdutil.CheckErr: the message and the exit code. The handler calls
// os.Exit(code) outside of the tests.
type fatalExit struct {
	msg  string
	code int
}

// flagFixture is a .bin directory and a release server for the presets tool
// and other. GET /<name>/... answers "<name> v2.0.0\n", or what fail[name]
// says: "403" is a 403 Forbidden, "empty" is a 200 with an empty body.
// hits counts the requests of each name.
type flagFixture struct {
	binDir string
	srv    *httptest.Server
	fail   map[string]string
	mu     sync.Mutex
	hits   map[string]int
}

func newFlagFixture(t *testing.T, fail map[string]string) *flagFixture {
	t.Helper()
	dir := t.TempDir()
	f := &flagFixture{binDir: filepath.Join(dir, ".bin"), fail: fail, hits: map[string]int{}}
	mustMkdir(t, f.binDir)
	t.Setenv("PATH_BIN", f.binDir)
	t.Setenv("PATH_BASE", dir)
	t.Chdir(dir)
	fastRetries(t)
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name, _, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
		f.mu.Lock()
		f.hits[name]++
		f.mu.Unlock()
		switch f.fail[name] {
		case "403":
			w.WriteHeader(http.StatusForbidden)
		case "empty":
		default:
			_, _ = w.Write([]byte(name + " v2.0.0\n"))
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// presets are tool and other. The latest version is v2.0.0. The version of
// a file is the second word of its content.
func (f *flagFixture) presets() []*binary.Binary {
	var out []*binary.Binary
	for _, name := range []string{"tool", "other"} {
		out = append(out, &binary.Binary{
			Name:       name,
			GitHubRepo: "test/" + name,
			Context:    context.Background(),
			URLF:       func(b *binary.Binary) (string, error) { return f.srv.URL + "/" + b.Name + "/" + b.Version, nil },
			VersionF:   func(*binary.Binary) (string, error) { return "v2.0.0", nil },
			VersionLocalF: func(b *binary.Binary) (string, error) {
				data, err := os.ReadFile(b.BinaryPath())
				if err != nil {
					return "", err
				}
				return strings.TrimPrefix(strings.TrimSpace(string(data)), b.Name+" "), nil
			},
		})
	}
	return out
}

// run runs `b <args>` through the flag command (NewCmdBinary), as a program
// that uses it runs it. It returns what the command gave to the fatal error
// handler, or a zero fatalExit when the command did not fail.
func (f *flagFixture) run(t *testing.T, args ...string) (exit fatalExit) {
	t.Helper()
	// The handler stops the command at the first fatal error, as os.Exit
	// does.
	cmdutil.BehaviorOnFatal(func(msg string, code int) { panic(fatalExit{msg: msg, code: code}) })
	t.Cleanup(cmdutil.DefaultBehaviorOnFatal)
	// The goroutines of installBinaries and the progress output write
	// concurrently; a bytes.Buffer does not take concurrent writes.
	io := &streams.IO{
		In:     strings.NewReader(""),
		Out:    &syncWriter{w: &bytes.Buffer{}},
		ErrOut: &syncWriter{w: &bytes.Buffer{}},
	}
	cmd := NewCmdBinary(&CmdBinaryOptions{IO: io, Binaries: f.presets(), NoConfig: true})
	cmd.SetArgs(args)
	defer func() {
		if r := recover(); r != nil {
			e, ok := r.(fatalExit)
			if !ok {
				panic(r)
			}
			exit = e
		}
	}()
	if err := cmd.Execute(); err != nil {
		t.Fatalf("b %s: %v", strings.Join(args, " "), err)
	}
	return exit
}

// file is the content of .bin/<name>, "" when there is no file.
func (f *flagFixture) file(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.binDir, name))
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// failedLine is a line of the error of failed downloads: the name of the
// binary, and a part of the cause.
type failedLine struct{ name, cause string }

// TestFlagInstall_FailedDownloadExitsNonZero: `b -i` (also with -u and -f)
// exits 1 when a download fails, as b install and b update do. The error
// names each binary whose download failed, in the order of the presets, and
// only those. A 403 and an empty body are failed downloads. A failed
// download stops nothing else: the other binaries install. A binary that
// failed keeps its old file.
func TestFlagInstall_FailedDownloadExitsNonZero(t *testing.T) {
	const forbidden = ": HTTP 403 for "
	const empty = " wrote 0 bytes: the server sent an empty body"
	for _, tc := range []struct {
		name    string
		args    []string
		present bool // .bin/tool is v1.0.0 before the command
		fail    map[string]string
		want    []failedLine // nil: no error
		// installed are the binaries that are v2.0.0 after the command.
		installed []string
	}{
		{name: "-i, a 403", args: []string{"-i", "tool"}, fail: map[string]string{"tool": "403"},
			want: []failedLine{{"tool", forbidden}}},
		{name: "-i, an empty body", args: []string{"-i", "tool"}, fail: map[string]string{"tool": "empty"},
			want: []failedLine{{"tool", empty}}},
		{name: "-i -u, an empty body", args: []string{"-i", "-u", "tool"}, present: true, fail: map[string]string{"tool": "empty"},
			want: []failedLine{{"tool", empty}}},
		{name: "-i -f, a 403", args: []string{"-i", "-f", "tool"}, present: true, fail: map[string]string{"tool": "403"},
			want: []failedLine{{"tool", forbidden}}},
		{name: "-i, one of two fails", args: []string{"-i", "other", "tool"}, fail: map[string]string{"tool": "403"},
			want: []failedLine{{"tool", forbidden}}, installed: []string{"other"}},
		{name: "-iu, both fail", args: []string{"-iu", "other", "tool"}, fail: map[string]string{"tool": "403", "other": "empty"},
			want: []failedLine{{"tool", forbidden}, {"other", empty}}},
		{name: "-if, both fail", args: []string{"-if", "other", "tool"}, present: true, fail: map[string]string{"tool": "empty", "other": "403"},
			want: []failedLine{{"tool", empty}, {"other", forbidden}}},
		{name: "-i, all download", args: []string{"-i", "tool", "other"}, installed: []string{"tool", "other"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFlagFixture(t, tc.fail)
			if tc.present {
				mustWriteExec(t, filepath.Join(f.binDir, "tool"), []byte("tool v1.0.0\n"))
			}
			exit := f.run(t, tc.args...)
			cmd := "b " + strings.Join(tc.args, " ")

			if tc.want == nil {
				if exit != (fatalExit{}) {
					t.Errorf("%s: exit %d with %q, want no error", cmd, exit.code, exit.msg)
				}
			} else {
				if exit.code != 1 {
					t.Errorf("%s: exit code %d (%q), want 1", cmd, exit.code, exit.msg)
				}
				lines := strings.Split(strings.TrimSuffix(exit.msg, "\n"), "\n")
				if lines[0] != "error: download failed:" || len(lines) != len(tc.want)+1 {
					t.Fatalf("%s: error %q, want \"download failed:\" and %d binary lines", cmd, exit.msg, len(tc.want))
				}
				for i, w := range tc.want {
					if line := lines[i+1]; !strings.HasPrefix(line, "  "+w.name+": ") || !strings.Contains(line, w.cause) {
						t.Errorf("%s: line %d of the error is %q, want %s with %q", cmd, i+1, line, w.name, w.cause)
					}
				}
			}

			f.mu.Lock()
			for name := range tc.fail {
				if f.hits[name] == 0 {
					t.Errorf("%s: no download of %s: the test checks nothing", cmd, name)
				}
			}
			f.mu.Unlock()
			for _, name := range tc.installed {
				if got := f.file(t, name); got != name+" v2.0.0\n" {
					t.Errorf("%s: .bin/%s is %q, want %s v2.0.0", cmd, name, got, name)
				}
			}
			if _, failed := tc.fail["tool"]; failed {
				want := ""
				if tc.present {
					want = "tool v1.0.0\n"
				}
				if got := f.file(t, "tool"); got != want {
					t.Errorf("%s: .bin/tool is %q after its failed download, want %q", cmd, got, want)
				}
			}
		})
	}
}
