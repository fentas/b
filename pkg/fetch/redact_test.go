package fetch

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestRedactURL: the redacted form of a URL has no user info, no query and
// no fragment; the scheme, the host, the port and the path stay.
func TestRedactURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://user:pass@example.com:8443/dl/tool.tar.gz?token=abc&x=1#top": "https://example.com:8443/dl/tool.tar.gz",
		"https://user@example.com/dl/tool.zip":                                "https://example.com/dl/tool.zip",
		"https://example.com/dl/tool?":                                        "https://example.com/dl/tool",
		"https://example.com/dl/tool#":                                        "https://example.com/dl/tool",
		"https://example.com/dl/a%2Fb?sig=abc":                                "https://example.com/dl/a%2Fb",
		"https://example.com":                                                 "https://example.com",
	} {
		u, err := url.Parse(in)
		if err != nil {
			t.Fatal(err)
		}
		if got := RedactURL(u); got != want {
			t.Errorf("RedactURL(%q) = %q, want %q", in, got, want)
		}
		if got := RedactRawURL(in); got != want {
			t.Errorf("RedactRawURL(%q) = %q, want %q", in, got, want)
		}
	}
	if got := RedactURL(nil); got != "" {
		t.Errorf("RedactURL(nil) = %q, want \"\"", got)
	}
	if got := RedactRawURL("http://u-sekrit:p@127.0.0.1:%zz/?q=sekrit"); got != InvalidURL {
		t.Errorf("RedactRawURL of a URL that does not parse = %q, want %q", got, InvalidURL)
	}
}

// secretURL is http://host/path with user info, a token in the query and a
// fragment. Each secret part holds "sekrit".
func secretURL(hostPort, path string) string {
	return "http://u-sekrit-name:p-sekrit-pass@" + hostPort + path + "?access_token=q-sekrit-token#f-sekrit-frag"
}

// noSecret fails the test when the text of err holds a part of the user
// info, the query or the fragment of secretURL.
func noSecret(t *testing.T, err error) {
	t.Helper()
	if strings.Contains(err.Error(), "sekrit") {
		t.Errorf("error %q holds a part of the user info, the query or the fragment", err)
	}
}

// refusedHost is an address on 127.0.0.1 where no one listens.
func refusedHost(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}

// TestErrors_HideCredentials: each error of Get and Download names the URL
// without its user info, query and fragment, also in the *url.Error that
// net/http returns, and it still wraps its cause.
func TestErrors_HideCredentials(t *testing.T) {
	for name, get := range map[string]func(string) (*http.Response, error){
		"Get":      func(u string) (*http.Response, error) { return Get(context.Background(), u) },
		"Download": func(u string) (*http.Response, error) { return Download(context.Background(), u, nil) },
	} {
		t.Run(name+", the tries run out on a 503", func(t *testing.T) {
			withTestPolicy(t)
			srv, _ := stepServer(t, step{status: http.StatusServiceUnavailable})
			host := strings.TrimPrefix(srv.URL, "http://")
			_, err := get(secretURL(host, "/dl/tool"))
			want := "GET http://" + host + "/dl/tool: HTTP 503 after 5 attempts"
			if err == nil || err.Error() != want {
				t.Fatalf("error %v, want %q", err, want)
			}
		})
		t.Run(name+", a refused connection", func(t *testing.T) {
			withTestPolicy(t)
			host := refusedHost(t)
			_, err := get(secretURL(host, "/dl/tool"))
			if err == nil || !strings.HasPrefix(err.Error(), "GET http://"+host+"/dl/tool: ") {
				t.Fatalf("error %v, want one that starts with GET and the URL", err)
			}
			noSecret(t, err)
			if !errors.Is(err, syscall.ECONNREFUSED) {
				t.Errorf("error %q does not wrap ECONNREFUSED", err)
			}
			var ue *url.Error
			switch {
			case !errors.As(err, &ue):
				t.Errorf("error %q does not wrap a *url.Error", err)
			case ue.URL != "http://"+host+"/dl/tool":
				t.Errorf("the *url.Error in %q has the URL %q, want the redacted URL", err, ue.URL)
			}
		})
		t.Run(name+", an error that is not retried", func(t *testing.T) {
			withTestPolicy(t)
			// net/http refuses the scheme at once: one try, the *url.Error itself.
			_, err := get("ftp://u-sekrit-name:p-sekrit-pass@127.0.0.1/dl/tool?access_token=q-sekrit-token#f-sekrit-frag")
			var ue *url.Error
			if err == nil || !errors.As(err, &ue) {
				t.Fatalf("error %v, want a *url.Error", err)
			}
			noSecret(t, err)
			if ue.URL != "ftp://127.0.0.1/dl/tool" {
				t.Errorf("the *url.Error has the URL %q, want %q", ue.URL, "ftp://127.0.0.1/dl/tool")
			}
		})
		t.Run(name+", a URL that does not parse", func(t *testing.T) {
			withTestPolicy(t)
			_, err := get("http://u-sekrit-name:p-sekrit-pass@127.0.0.1:%zz/dl/tool?access_token=q-sekrit-token")
			var ue *url.Error
			if err == nil || !errors.As(err, &ue) || ue.URL != InvalidURL {
				t.Fatalf("error %v, want a *url.Error with the URL %q", err, InvalidURL)
			}
			noSecret(t, err)
		})
		t.Run(name+", a wait that fails", func(t *testing.T) {
			withTestPolicy(t)
			stop := errors.New("stop")
			sleep = func(context.Context, time.Duration) error { return stop }
			srv, _ := stepServer(t, step{status: http.StatusServiceUnavailable})
			host := strings.TrimPrefix(srv.URL, "http://")
			_, err := get(secretURL(host, "/dl/tool"))
			want := "GET http://" + host + "/dl/tool: stop"
			if err == nil || err.Error() != want || !errors.Is(err, stop) {
				t.Fatalf("error %v, want %q that wraps the error of the wait", err, want)
			}
		})
	}
}
