// Package fetch sends the HTTP GET requests b makes to release hosts. It
// retries transient failures with a bounded backoff, and it sends
// GITHUB_TOKEN to GitHub hosts.
package fetch

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Policy bounds the retry loop.
type Policy struct {
	// Attempts is the total number of tries, the first one included.
	Attempts int
	// BaseDelay is the wait before the second try. It doubles for each
	// further try; a random jitter takes up to half of it off.
	BaseDelay time.Duration
	// MaxDelay caps one wait, a Retry-After value included.
	MaxDelay time.Duration
	// MaxWait caps the sum of all waits. A try whose wait would go past it
	// is not made.
	MaxWait time.Duration
}

// DefaultPolicy is the policy of Do, Get and Download: five tries, with
// waits of at most 5s, 10s, 20s and 40s, and 150s in total.
var DefaultPolicy = Policy{
	Attempts:  5,
	BaseDelay: 5 * time.Second,
	MaxDelay:  60 * time.Second,
	MaxWait:   150 * time.Second,
}

// githubHosts receive GITHUB_TOKEN. A release download on github.com
// redirects to a storage host that is not in this list.
var githubHosts = map[string]bool{
	"github.com":     true,
	"api.github.com": true,
}

// sleep waits for d, or until ctx is done. Tests replace it.
var sleep = func(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Error reports a request that still failed after its last try.
type Error struct {
	URL      string
	Attempts int
	// Status is the HTTP status of the last try, 0 if it got no response.
	Status int
	// Err is the error of the last try, nil if it got a response.
	Err  error
	hint string
}

func (e *Error) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("GET %s: HTTP %d after %d attempts%s", e.URL, e.Status, e.Attempts, e.hint)
	}
	return fmt.Sprintf("GET %s: %v (after %d attempts)", e.URL, e.Err, e.Attempts)
}

func (e *Error) Unwrap() error { return e.Err }

// Do sends req with DefaultPolicy. It retries a network error, HTTP 429 and
// HTTP 5xx other than 501. It returns any other response at once, 4xx
// included, and the caller then owns its body. When the tries run out, the
// error is an *Error that names the URL and the last status or error.
//
// For an https request to a GitHub host, Do sends GITHUB_TOKEN (if set) as a
// Bearer token. The http.Client drops the header when a redirect goes to
// another domain, so the token does not reach the storage host that GitHub
// redirects release downloads to.
func Do(req *http.Request) (*http.Response, error) {
	return DefaultPolicy.do(req, false, nil)
}

// Get is Do for a GET of rawURL.
func Get(ctx context.Context, rawURL string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	return Do(req)
}

// Download is Get for a release asset. It reads a 200 body to the end into a
// temp file before it returns, so a body that the network cuts short is
// retried from byte zero, and a partial body never reaches the caller. The
// Body of the returned 200 response reads that file, and Close removes it;
// ContentLength is the file size.
//
// wrap, if not nil, is called for each try that gets a 200 and returns the
// reader to copy the body from (for example, to show progress).
func Download(ctx context.Context, rawURL string, wrap func(*http.Response) io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	return DefaultPolicy.do(req, true, wrap)
}

func (p Policy) do(req *http.Request, spoolBody bool, wrap func(*http.Response) io.Reader) (*http.Response, error) {
	ctx := req.Context()
	var waited time.Duration
	for attempt := 1; ; attempt++ {
		r := req.Clone(ctx)
		setGitHubAuth(r)
		resp, err := http.DefaultClient.Do(r)

		status := 0
		var after time.Duration
		switch {
		case err != nil:
			if ctx.Err() != nil || !retryableError(err) {
				return nil, err
			}
		case retryableStatus(resp.StatusCode):
			status = resp.StatusCode
			after = retryAfter(resp.Header.Get("Retry-After"), time.Now())
			// Read a little of the body so the connection can be reused.
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
			resp.Body.Close()
		case spoolBody && resp.StatusCode == http.StatusOK:
			if err = spool(resp, wrap); err == nil {
				return resp, nil
			}
			if ctx.Err() != nil || !retryableError(err) {
				return nil, err
			}
		default:
			return resp, nil
		}

		delay := p.delay(attempt, after)
		if attempt >= p.Attempts || waited+delay > p.MaxWait {
			e := &Error{URL: req.URL.Redacted(), Attempts: attempt, Status: status, Err: err}
			if status == http.StatusTooManyRequests && isGitHub(req.URL) && os.Getenv("GITHUB_TOKEN") == "" {
				e.hint = "; rate limited, set GITHUB_TOKEN to authenticate"
			}
			return nil, e
		}
		if err := sleep(ctx, delay); err != nil {
			return nil, fmt.Errorf("GET %s: %w", req.URL.Redacted(), err)
		}
		waited += delay
	}
}

// delay returns the wait after try number attempt: the backoff with jitter,
// raised to after, capped at MaxDelay.
func (p Policy) delay(attempt int, after time.Duration) time.Duration {
	d := p.BaseDelay
	for i := 1; i < attempt && d < p.MaxDelay; i++ {
		d *= 2
	}
	if d > p.MaxDelay {
		d = p.MaxDelay
	}
	if half := int64(d / 2); half > 0 {
		d -= time.Duration(rand.Int64N(half))
	}
	if after > d {
		d = after
	}
	if d > p.MaxDelay {
		d = p.MaxDelay
	}
	return d
}

// retryableStatus reports whether a status can go away on its own: 429, and
// 5xx other than 501 Not Implemented.
func retryableStatus(code int) bool {
	return code == http.StatusTooManyRequests ||
		code >= 500 && code <= 599 && code != http.StatusNotImplemented
}

// retryableError reports whether err is a network failure that a new try
// can fix. A host that does not resolve and a bad certificate are not.
func retryableError(err error) bool {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		err = urlErr.Err
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return !dnsErr.IsNotFound
	}
	var certErr *tls.CertificateVerificationError
	if errors.As(err, &certErr) {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}

// retryAfter parses a Retry-After value: delay seconds or an HTTP date.
func retryAfter(v string, now time.Time) time.Duration {
	if v == "" {
		return 0
	}
	if s, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
		if s < 0 {
			return 0
		}
		return time.Duration(s) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil && t.After(now) {
		return t.Sub(now)
	}
	return 0
}

// spool reads the body of resp into a temp file and puts the file in
// place of the body. On error, it removes the file and closes the body.
func spool(resp *http.Response, wrap func(*http.Response) io.Reader) error {
	body := resp.Body
	defer body.Close()
	var src io.Reader = body
	if wrap != nil {
		src = wrap(resp)
	}
	f, err := os.CreateTemp("", "b-download-*")
	if err != nil {
		return err
	}
	n, err := io.Copy(f, src)
	if err == nil {
		_, err = f.Seek(0, io.SeekStart)
	}
	if err != nil {
		f.Close()
		os.Remove(f.Name())
		return err
	}
	resp.Body = tempFile{f}
	resp.ContentLength = n
	return nil
}

// tempFile is a spooled body; Close also removes the file.
type tempFile struct{ *os.File }

func (f tempFile) Close() error {
	err := f.File.Close()
	if rmErr := os.Remove(f.Name()); err == nil {
		err = rmErr
	}
	return err
}

func isGitHub(u *url.URL) bool {
	return u.Scheme == "https" && githubHosts[strings.ToLower(u.Hostname())]
}

// setGitHubAuth adds GITHUB_TOKEN to an https request for a GitHub host,
// unless the request already has an Authorization header.
func setGitHubAuth(req *http.Request) {
	if !isGitHub(req.URL) || req.Header.Get("Authorization") != "" {
		return
	}
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
}
