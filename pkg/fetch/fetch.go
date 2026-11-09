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
	"unicode/utf8"
)

// Policy bounds the retry loop and each try.
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
	// HeaderTimeout ends a try that gets no response headers in this time,
	// redirects included. Zero disables it.
	HeaderTimeout time.Duration
	// IdleTimeout ends a try whose body sends no byte for this time. A slow
	// body that keeps sending is not cut. Zero disables it.
	IdleTimeout time.Duration
}

// DefaultPolicy is the policy of Do, Get and Download: five tries, with
// waits of at most 5s, 10s, 20s and 40s, and 150s in total. A try ends
// after 30s without response headers or 60s without body data.
var DefaultPolicy = Policy{
	Attempts:      5,
	BaseDelay:     5 * time.Second,
	MaxDelay:      60 * time.Second,
	MaxWait:       150 * time.Second,
	HeaderTimeout: 30 * time.Second,
	IdleTimeout:   60 * time.Second,
}

// githubHosts receive GITHUB_TOKEN. A release download on github.com
// redirects to a storage host that is not in this list.
var githubHosts = map[string]bool{
	"github.com":     true,
	"api.github.com": true,
}

// credentialHeaders are removed from a redirect that keepCredentials
// rejects: GitHub and Gitea use Authorization, GitLab uses PRIVATE-TOKEN.
var credentialHeaders = []string{"Authorization", "Private-Token"}

// maxRetryAfter caps a Retry-After value before it becomes a Duration.
const maxRetryAfter = 24 * time.Hour

// drainTimeout caps the read of an error body before a retry. Tests
// replace it.
var drainTimeout = 2 * time.Second

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

// timeoutError ends a try that took too long. It is a net.Error with
// Timeout true, so the try is retried.
type timeoutError struct{ msg string }

func (e *timeoutError) Error() string   { return e.msg }
func (e *timeoutError) Timeout() bool   { return true }
func (e *timeoutError) Temporary() bool { return true }

// Do sends req with DefaultPolicy. For GET and HEAD without a body, it
// retries a network error, a timeout, HTTP 429 and HTTP 5xx other than 501.
// It returns any other response at once, 4xx included, and the caller then
// owns its body; the caller must read it with no pause of IdleTimeout or
// more. When the tries run out, the error is an *Error that names the URL
// and the last status or error. Any other request gets one try.
//
// For an https request to a GitHub host, Do sends GITHUB_TOKEN (if set) as a
// Bearer token. On each redirect, Do keeps the Authorization and
// PRIVATE-TOKEN headers only if the next URL is an https GitHub host (for a
// request to GitHub) or the same origin (for any other request). So the
// token does not reach the storage host that GitHub redirects release
// downloads to, a subdomain, or an http URL.
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
// temp file before it returns, so a body that the network cuts short or
// stops is retried from byte zero, and a partial body never reaches the
// caller. The Body of the returned 200 response reads that file, and Close
// removes it; ContentLength is the file size.
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
	// A copy, so that the redirect check also wraps a CheckRedirect of the
	// caller and runs after it.
	client := *http.DefaultClient
	client.CheckRedirect = checkRedirect(client.CheckRedirect)

	if !replayable(req) {
		return p.try(&client, req)
	}

	ctx := req.Context()
	var waited time.Duration
	for attempt := 1; ; attempt++ {
		resp, err := p.try(&client, req)

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
			discard(resp.Body)
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

// replayable reports whether req can be sent again: a GET or HEAD without
// a body.
func replayable(req *http.Request) bool {
	switch req.Method {
	case "", http.MethodGet, http.MethodHead:
		return req.Body == nil || req.Body == http.NoBody
	}
	return false
}

// try sends req once. The try has its own context: it ends when no
// response headers come in HeaderTimeout, when the body sends no byte for
// IdleTimeout, and when the body is closed.
func (p Policy) try(client *http.Client, req *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithCancelCause(req.Context())
	r := req.Clone(ctx)
	setGitHubAuth(r)

	var headers *time.Timer
	if p.HeaderTimeout > 0 {
		headers = time.AfterFunc(p.HeaderTimeout, func() {
			cancel(&timeoutError{fmt.Sprintf("no response headers after %v", p.HeaderTimeout)})
		})
	}
	// When a timer ends the try, the transport returns its cause (a
	// timeoutError, which is retried) from Do and from body reads.
	resp, err := client.Do(r)
	if headers != nil {
		headers.Stop()
	}
	if err != nil {
		cancel(nil)
		return nil, err
	}
	resp.Body = watchBody(resp.Body, p.IdleTimeout, cancel)
	return resp, nil
}

// watchedBody is the body of one try. It ends the try when no byte comes
// for idle, and when it is closed.
type watchedBody struct {
	io.ReadCloser
	idle   time.Duration
	timer  *time.Timer
	cancel context.CancelCauseFunc
}

func watchBody(body io.ReadCloser, idle time.Duration, cancel context.CancelCauseFunc) *watchedBody {
	w := &watchedBody{ReadCloser: body, idle: idle, cancel: cancel}
	if idle > 0 {
		w.timer = time.AfterFunc(idle, func() {
			cancel(&timeoutError{fmt.Sprintf("no body data for %v", idle)})
		})
	}
	return w
}

func (w *watchedBody) Read(p []byte) (int, error) {
	n, err := w.ReadCloser.Read(p)
	if n > 0 && w.timer != nil {
		w.timer.Reset(w.idle)
	}
	return n, err
}

func (w *watchedBody) Close() error {
	if w.timer != nil {
		w.timer.Stop()
	}
	err := w.ReadCloser.Close()
	w.cancel(nil)
	return err
}

// discard reads up to 64 KiB of an error body, for at most drainTimeout,
// so the connection can be reused, and closes the body.
func discard(body io.ReadCloser) {
	if w, ok := body.(*watchedBody); ok {
		t := time.AfterFunc(drainTimeout, func() { w.cancel(errors.New("drain time is over")) })
		defer t.Stop()
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(body, 64<<10))
	body.Close()
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

// retryableError reports whether err is a network failure or a timeout
// that a new try can fix. A host that does not resolve and a bad
// certificate are not.
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
// It caps the value at maxRetryAfter.
func retryAfter(v string, now time.Time) time.Duration {
	if v == "" {
		return 0
	}
	if s, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil || errors.Is(err, strconv.ErrRange) {
		// On ErrRange, s is the largest or smallest int64.
		switch {
		case s < 0:
			return 0
		case s > int64(maxRetryAfter/time.Second):
			return maxRetryAfter
		}
		return time.Duration(s) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil && t.After(now) {
		return min(t.Sub(now), maxRetryAfter)
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

// checkRedirect runs next (or the default limit of 10 redirects), then
// removes the credential headers from the next request if keepCredentials
// rejects it. It runs last, so next cannot put a credential back.
func checkRedirect(next func(*http.Request, []*http.Request) error) func(*http.Request, []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		var err error
		if next != nil {
			err = next(req, via)
		} else if len(via) >= 10 {
			err = errors.New("stopped after 10 redirects")
		}
		if !keepCredentials(via[0].URL, req.URL) {
			for _, h := range credentialHeaders {
				req.Header.Del(h)
			}
		}
		return err
	}
}

// keepCredentials reports whether a redirect to next may carry the
// credentials of a request to first: for a GitHub request, only to a
// GitHub host over https; for any other request, only to its own origin.
func keepCredentials(first, next *url.URL) bool {
	if isGitHub(first) {
		return isGitHub(next)
	}
	return sameOrigin(first, next)
}

// isGitHub reports whether u is an https URL for a host in githubHosts, on
// the default port, without user info. The host must be ASCII: the
// transport converts a non-ASCII host with IDNA to a different domain,
// even when Unicode case folding makes it look like a GitHub host (U+0130
// lowercases to "i").
func isGitHub(u *url.URL) bool {
	if u.Scheme != "https" || u.User != nil {
		return false
	}
	if port := u.Port(); port != "" && port != "443" {
		return false
	}
	host, ok := asciiHost(u)
	return ok && githubHosts[host]
}

// sameOrigin reports whether a and b have the same scheme, ASCII host and
// port.
func sameOrigin(a, b *url.URL) bool {
	ah, aok := asciiHost(a)
	bh, bok := asciiHost(b)
	return aok && bok && a.Scheme == b.Scheme && ah == bh && portOf(a) == portOf(b)
}

// asciiHost returns the host of u in ASCII lowercase, without one trailing
// dot. It reports false for a host with a byte outside ASCII.
func asciiHost(u *url.URL) (string, bool) {
	host := []byte(u.Hostname())
	for i, c := range host {
		if c >= utf8.RuneSelf {
			return "", false
		}
		if 'A' <= c && c <= 'Z' {
			host[i] = c + ('a' - 'A')
		}
	}
	return strings.TrimSuffix(string(host), "."), true
}

func portOf(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	switch u.Scheme {
	case "https":
		return "443"
	case "http":
		return "80"
	}
	return ""
}

// setGitHubAuth adds GITHUB_TOKEN to a request for which isGitHub is true,
// unless the request already has an Authorization header.
func setGitHubAuth(req *http.Request) {
	if !isGitHub(req.URL) || req.Header.Get("Authorization") != "" {
		return
	}
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
}
