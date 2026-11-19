package fetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"testing/iotest"
	"unicode/utf8"
)

// TestScrubURLs: each URL in a text loses its user info, its query and its
// fragment, also when it does not parse; the text around it stays.
func TestScrubURLs(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"GET https://u:p@example.org/dl/tool?token=abc#top: EOF", "GET https://example.org/dl/tool: EOF"},
		{`header "https://alice:hunter2@example.org/%zz?token=s3cr3t#frag9": bad`, `header "https://example.org/%zz": bad`},
		{`quoted \"https://u:p@example.org/a?x=1\" too`, `quoted \"https://example.org/a\" too`},
		{"two: http://a@x.test/1?q=1 and ftp://b@y.test/2#f", "two: http://x.test/1 and ftp://y.test/2"},
		{"no path: https://user:pw@host.test?sig=1", "no path: https://host.test"},
		{"an @ in the path stays: https://host.test/a@b?q=1", "an @ in the path stays: https://host.test/a@b"},
		{"the last @ of the user info: https://a@b@host.test/x", "the last @ of the user info: https://host.test/x"},
		{"a port: https://u@host.test:8443/x?t=1", "a port: https://host.test:8443/x"},
		{"<https://u@host.test/x?t=1>", "<https://host.test/x>"},
		{"a dot after the query: https://host.test/x?sig=abc.", "a dot after the query: https://host.test/x."},
		{`an escape in the query: "https://host.test/x?t=a\x00b-s3cr3t"`, `an escape in the query: "https://host.test/x"`},
		{"no URL here: a@b?c#d", "no URL here: a@b?c#d"},
		{"", ""},
	} {
		if got := ScrubURLs(tc.in); got != tc.want {
			t.Errorf("ScrubURLs(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestScrubError: scrubError keeps nil and an error with nothing to cut as
// they are; otherwise its text is cut and errors.Is and errors.As still
// reach the cause.
func TestScrubError(t *testing.T) {
	if scrubError(nil) != nil {
		t.Error("scrubError(nil) is not nil")
	}
	plain := errors.New("no URL in this text")
	if got := scrubError(plain); got != plain {
		t.Errorf("scrubError of an error with nothing to cut = %#v, want the error itself", got)
	}

	cause := &url.Error{Op: "Get", URL: "https://example.org/dl", Err: fmt.Errorf("read https://u:p@cdn.test/x?sig=s3cr3t: %w", io.EOF)}
	err := scrubError(cause)
	want := "Get \"https://example.org/dl\": read https://cdn.test/x: EOF"
	if err.Error() != want {
		t.Errorf("text %q, want %q", err, want)
	}
	if !errors.Is(err, io.EOF) {
		t.Errorf("%q does not reach io.EOF", err)
	}
	var ue *url.Error
	if !errors.As(err, &ue) || ue != cause {
		t.Errorf("%q does not reach the *url.Error", err)
	}
}

// TestGet_MalformedLocationHidesCredentials: net/http quotes a Location
// header that does not parse in the text of its error, with the user info
// and the query. Get and Download cut them, and the *url.Error is still in
// the chain. The request is not sent again.
func TestGet_MalformedLocationHidesCredentials(t *testing.T) {
	const location = "https://alice:hunter2@example.org/%zz?token=s3cr3t#frag9"
	for name, get := range map[string]func(string) (*http.Response, error){
		"Get":      func(u string) (*http.Response, error) { return Get(context.Background(), u) },
		"Download": func(u string) (*http.Response, error) { return Download(context.Background(), u, nil) },
	} {
		t.Run(name, func(t *testing.T) {
			withTestPolicy(t)
			var n int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&n, 1)
				w.Header().Set("Location", location)
				w.WriteHeader(http.StatusFound)
			}))
			t.Cleanup(srv.Close)
			resp, err := get(srv.URL + "/dl/tool")
			if err == nil {
				resp.Body.Close()
				t.Fatal("no error for a Location that does not parse")
			}
			for _, secret := range []string{"alice", "hunter2", "s3cr3t", "frag9"} {
				if strings.Contains(err.Error(), secret) {
					t.Errorf("error %q holds %q", err, secret)
				}
			}
			if !strings.Contains(err.Error(), `"https://example.org/%zz"`) {
				t.Errorf("error %q does not name the scrubbed Location", err)
			}
			var ue *url.Error
			if !errors.As(err, &ue) {
				t.Errorf("error %q does not reach the *url.Error", err)
			}
			if got := atomic.LoadInt32(&n); got != 1 {
				t.Errorf("%d requests, want 1", got)
			}
		})
	}
}

// TestErrorBody: ErrorBody cuts the URLs in a body, then cuts the text at
// maxErrorBody bytes, on a UTF-8 boundary.
func TestErrorBody(t *testing.T) {
	echo := `{"message":"bad request","url":"https://api.test/repos/o/r?access_token=s3cr3t"}`
	if got, want := ErrorBody(strings.NewReader(echo)), `{"message":"bad request","url":"https://api.test/repos/o/r"}`; got != want {
		t.Errorf("ErrorBody = %q, want %q", got, want)
	}

	long := ErrorBody(strings.NewReader(strings.Repeat("x", 600)))
	if want := strings.Repeat("x", maxErrorBody) + "..."; long != want {
		t.Errorf("ErrorBody of 600 bytes has %d bytes, want %d", len(long), len(want))
	}

	// A URL across the cut, which falls before the "@" of the user info: the
	// scrub comes first, so it still sees the "@", and no part of the user
	// info is left.
	userInfo := " https://u-name:p-pass"
	straddle := strings.Repeat("x", maxErrorBody-len(userInfo)) + userInfo + "@api.test/x?access_token=s3cr3t" + strings.Repeat("y", 100)
	for _, secret := range []string{"u-name", "p-pass", "s3cr3t"} {
		if got := ErrorBody(strings.NewReader(straddle)); strings.Contains(got, secret) {
			t.Errorf("ErrorBody kept %q: %q", secret, got)
		}
	}

	// A cut inside a multi-byte rune drops the part of the rune.
	runes := ErrorBody(strings.NewReader(strings.Repeat("x", maxErrorBody-1) + "€€"))
	if !utf8.ValidString(runes) {
		t.Errorf("ErrorBody gave text that is not valid UTF-8: %q", runes)
	}
	if want := strings.Repeat("x", maxErrorBody-1) + "..."; runes != want {
		t.Errorf("ErrorBody = %q, want %q", runes, want)
	}
}

// TestDownload_SpoolErrorHidesCredentials: an error while Download reads a
// 200 body to its file can name a URL in its text (here, from the reader
// that wrap gives). Download cuts the user info and the query.
func TestDownload_SpoolErrorHidesCredentials(t *testing.T) {
	withTestPolicy(t)
	srv, _ := stepServer(t, step{status: http.StatusOK, body: "0123456789"})
	broken := errors.New("read https://u-name:p-pass@cdn.test/x?sig=s3cr3t: broken")
	_, err := Download(context.Background(), srv.URL+"/dl/tool", func(*http.Response) io.Reader { return iotest.ErrReader(broken) })
	if want := "read https://cdn.test/x: broken"; err == nil || err.Error() != want {
		t.Fatalf("error %v, want %q", err, want)
	}
	if !errors.Is(err, broken) {
		t.Errorf("error %q does not wrap the read error", err)
	}
}

// TestDownload_BodyIsAFile: the Body of a 200 from Download is the spooled
// file. It implements io.ReaderAt and io.Seeker, so a caller can open a zip
// from it without a copy in memory.
func TestDownload_BodyIsAFile(t *testing.T) {
	withTestPolicy(t)
	srv, _ := stepServer(t, step{status: http.StatusOK, body: "0123456789"})
	resp, err := Download(context.Background(), srv.URL+"/dl/tool", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	at, ok := resp.Body.(io.ReaderAt)
	if !ok {
		t.Fatalf("the Body %T does not implement io.ReaderAt", resp.Body)
	}
	if _, ok := resp.Body.(io.Seeker); !ok {
		t.Fatalf("the Body %T does not implement io.Seeker", resp.Body)
	}
	buf := make([]byte, 3)
	if _, err := at.ReadAt(buf, 4); err != nil || string(buf) != "456" {
		t.Errorf("ReadAt(4) = %q, %v, want \"456\"", buf, err)
	}
}
