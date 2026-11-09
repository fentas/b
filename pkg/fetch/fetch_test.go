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
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// testPolicy keeps the waits short; the tests record them through sleep.
var testPolicy = Policy{
	Attempts:  5,
	BaseDelay: time.Millisecond,
	MaxDelay:  10 * time.Second,
	MaxWait:   30 * time.Second,
}

// fakeToken is not a real credential.
const fakeToken = "test-token-not-a-secret"

// withTestPolicy installs testPolicy and a sleep that records each wait
// without sleeping. It returns the recorded waits.
func withTestPolicy(t *testing.T) *[]time.Duration {
	t.Helper()
	oldPolicy, oldSleep := DefaultPolicy, sleep
	var waits []time.Duration
	DefaultPolicy = testPolicy
	sleep = func(ctx context.Context, d time.Duration) error {
		waits = append(waits, d)
		return ctx.Err()
	}
	t.Cleanup(func() { DefaultPolicy, sleep = oldPolicy, oldSleep })
	return &waits
}

// hostRouter sends each request to the test server for its host, so the
// http.Client sees real host names (and applies its redirect rules to them)
// while the servers run on 127.0.0.1.
type hostRouter map[string]*httptest.Server

func (h hostRouter) RoundTrip(req *http.Request) (*http.Response, error) {
	srv, ok := h[req.URL.Hostname()]
	if !ok {
		return nil, fmt.Errorf("no test server for host %q", req.URL.Hostname())
	}
	target, err := url.Parse(srv.URL)
	if err != nil {
		return nil, err
	}
	out := req.Clone(req.Context())
	out.URL.Scheme = target.Scheme
	out.URL.Host = target.Host
	out.Host = target.Host
	return http.DefaultTransport.RoundTrip(out)
}

// withHosts makes http.DefaultClient route the given hosts to the servers.
func withHosts(t *testing.T, hosts hostRouter) {
	t.Helper()
	old := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: hosts}
	t.Cleanup(func() { http.DefaultClient = old })
}

// step is one canned response of a test server.
type step struct {
	status     int
	retryAfter string
	body       string
}

// stepServer answers the steps in order and repeats the last one.
func stepServer(t *testing.T, steps ...step) (*httptest.Server, *int32) {
	t.Helper()
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := int(atomic.AddInt32(&n, 1)) - 1
		if i >= len(steps) {
			i = len(steps) - 1
		}
		s := steps[i]
		if s.retryAfter != "" {
			w.Header().Set("Retry-After", s.retryAfter)
		}
		w.WriteHeader(s.status)
		_, _ = io.WriteString(w, s.body)
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

func TestGet(t *testing.T) {
	tests := []struct {
		name       string
		steps      []step
		wantStatus int    // 0: want an error
		wantBody   string // checked when wantStatus is 200
		wantErr    []string
		wantTries  int32
		wantWaits  []time.Duration // nil: not checked
	}{
		{
			name:       "504 then 200 succeeds",
			steps:      []step{{status: 504}, {status: 200, body: "binary"}},
			wantStatus: 200,
			wantBody:   "binary",
			wantTries:  2,
		},
		{
			name:       "404 fails at once",
			steps:      []step{{status: 404}, {status: 200}},
			wantStatus: 404,
			wantTries:  1,
		},
		{
			name:       "403 fails at once",
			steps:      []step{{status: 403}, {status: 200}},
			wantStatus: 403,
			wantTries:  1,
		},
		{
			name:       "501 fails at once",
			steps:      []step{{status: 501}, {status: 200}},
			wantStatus: 501,
			wantTries:  1,
		},
		{
			name:       "429 waits for Retry-After",
			steps:      []step{{status: 429, retryAfter: "7"}, {status: 200, body: "ok"}},
			wantStatus: 200,
			wantBody:   "ok",
			wantTries:  2,
			wantWaits:  []time.Duration{7 * time.Second},
		},
		{
			name:       "Retry-After is capped at MaxDelay",
			steps:      []step{{status: 503, retryAfter: "3600"}, {status: 200}},
			wantStatus: 200,
			wantTries:  2,
			wantWaits:  []time.Duration{testPolicy.MaxDelay},
		},
		{
			name:      "retries exhausted names the URL and the last status",
			steps:     []step{{status: 502}, {status: 503}},
			wantErr:   []string{"/asset", "HTTP 503", "5 attempts"},
			wantTries: 5,
		},
		{
			name:  "total wait is capped at MaxWait",
			steps: []step{{status: 503, retryAfter: "10"}},
			// 10s + 10s + 10s; a fourth wait would pass MaxWait (30s).
			wantErr:   []string{"HTTP 503", "4 attempts"},
			wantTries: 4,
			wantWaits: []time.Duration{10 * time.Second, 10 * time.Second, 10 * time.Second},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			waits := withTestPolicy(t)
			srv, tries := stepServer(t, tt.steps...)

			resp, err := Get(context.Background(), srv.URL+"/asset")
			if tt.wantStatus == 0 {
				if err == nil {
					resp.Body.Close()
					t.Fatalf("want an error, got HTTP %d", resp.StatusCode)
				}
				var fe *Error
				if !errors.As(err, &fe) {
					t.Errorf("want *Error, got %T: %v", err, err)
				}
				for _, s := range tt.wantErr {
					if !strings.Contains(err.Error(), s) {
						t.Errorf("error %q does not contain %q", err, s)
					}
				}
			} else {
				if err != nil {
					t.Fatalf("Get: %v", err)
				}
				defer resp.Body.Close()
				if resp.StatusCode != tt.wantStatus {
					t.Errorf("status = %d, want %d", resp.StatusCode, tt.wantStatus)
				}
				if tt.wantStatus == http.StatusOK {
					body, _ := io.ReadAll(resp.Body)
					if string(body) != tt.wantBody {
						t.Errorf("body = %q, want %q", body, tt.wantBody)
					}
				}
			}
			if got := atomic.LoadInt32(tries); got != tt.wantTries {
				t.Errorf("server got %d requests, want %d", got, tt.wantTries)
			}
			if tt.wantWaits != nil && fmt.Sprint(*waits) != fmt.Sprint(tt.wantWaits) {
				t.Errorf("waits = %v, want %v", *waits, tt.wantWaits)
			}
		})
	}
}

func TestGet_NetworkErrorIsRetried(t *testing.T) {
	withTestPolicy(t)
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&n, 1) == 1 {
			// Close the connection without a response.
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				conn.Close()
			}
			return
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()

	resp, err := Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	resp.Body.Close()
	if got := atomic.LoadInt32(&n); got != 2 {
		t.Errorf("server got %d requests, want 2", got)
	}
}

func TestGet_ContextCanceledStopsRetry(t *testing.T) {
	withTestPolicy(t)
	ctx, cancel := context.WithCancel(context.Background())
	sleep = func(ctx context.Context, d time.Duration) error {
		cancel()
		<-ctx.Done()
		return ctx.Err()
	}
	srv, tries := stepServer(t, step{status: 504})

	_, err := Get(ctx, srv.URL)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if got := atomic.LoadInt32(tries); got != 1 {
		t.Errorf("server got %d requests, want 1", got)
	}
}

// TestDownload_CutBodyRestartsClean serves a body that stops short of its
// Content-Length first. The retry must start from byte zero: the result is
// the full body once, not the partial body plus the full body.
func TestDownload_CutBodyRestartsClean(t *testing.T) {
	withTestPolicy(t)
	full := strings.Repeat("0123456789", 100)
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(len(full)))
		if atomic.AddInt32(&n, 1) == 1 {
			_, _ = io.WriteString(w, full[:300])
			return // the server closes the connection 700 bytes short
		}
		_, _ = io.WriteString(w, full)
	}))
	defer srv.Close()

	var wrapped int
	resp, err := Download(context.Background(), srv.URL, func(r *http.Response) io.Reader {
		wrapped++
		return r.Body
	})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(body) != full {
		t.Errorf("body has %d bytes, want the %d bytes of the full body", len(body), len(full))
	}
	if resp.ContentLength != int64(len(full)) {
		t.Errorf("ContentLength = %d, want %d", resp.ContentLength, len(full))
	}
	if wrapped != 2 {
		t.Errorf("wrap called %d times, want 2 (once per try with a 200)", wrapped)
	}
}

func TestGitHubAuth(t *testing.T) {
	tests := []struct {
		name     string
		token    string
		url      string
		wantAuth string
	}{
		{"token sent to github.com", fakeToken, "https://github.com/o/r/releases/download/v1/a", "Bearer " + fakeToken},
		{"token sent to api.github.com", fakeToken, "https://api.github.com/repos/o/r/releases/tags/v1", "Bearer " + fakeToken},
		{"no header without a token", "", "https://github.com/o/r/releases/download/v1/a", ""},
		{"no token to another host", fakeToken, "https://example.test/a", ""},
		{"no token to a look-alike host", fakeToken, "https://github.com.example.test/a", ""},
		{"no token to a suffix look-alike host", fakeToken, "https://notgithub.com/a", ""},
		{"no token to a GitHub storage host", fakeToken, "https://release-assets.githubusercontent.com/a", ""},
		{"no token over plain http", fakeToken, "http://github.com/o/r/releases/download/v1/a", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withTestPolicy(t)
			t.Setenv("GITHUB_TOKEN", tt.token)
			var mu sync.Mutex
			var got []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				got = append(got, r.Header.Get("Authorization"))
				mu.Unlock()
				_, _ = io.WriteString(w, "ok")
			}))
			defer srv.Close()
			u, _ := url.Parse(tt.url)
			withHosts(t, hostRouter{u.Hostname(): srv})

			resp, err := Get(context.Background(), tt.url)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			resp.Body.Close()
			if len(got) != 1 || got[0] != tt.wantAuth {
				t.Errorf("Authorization headers = %q, want [%q]", got, tt.wantAuth)
			}
		})
	}
}

// TestGitHubAuth_NotForwardedOnRedirect follows a github.com release
// download to its storage host, the way GitHub serves release assets. The
// token must reach github.com and must not reach the storage host.
func TestGitHubAuth_NotForwardedOnRedirect(t *testing.T) {
	withTestPolicy(t)
	t.Setenv("GITHUB_TOKEN", fakeToken)

	var mu sync.Mutex
	var ghAuth, storageAuth string
	var storageHit bool
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		ghAuth = r.Header.Get("Authorization")
		mu.Unlock()
		http.Redirect(w, r, "https://release-assets.githubusercontent.com/asset?sig=x", http.StatusFound)
	}))
	defer gh.Close()
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		storageHit = true
		storageAuth = r.Header.Get("Authorization")
		mu.Unlock()
		_, _ = io.WriteString(w, "binary")
	}))
	defer storage.Close()
	withHosts(t, hostRouter{
		"github.com":                           gh,
		"release-assets.githubusercontent.com": storage,
	})

	resp, err := Download(context.Background(), "https://github.com/o/r/releases/download/v1/a", nil)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "binary" {
		t.Errorf("body = %q, want %q", body, "binary")
	}
	mu.Lock()
	defer mu.Unlock()
	if ghAuth != "Bearer "+fakeToken {
		t.Errorf("github.com got Authorization %q, want the token", ghAuth)
	}
	if !storageHit {
		t.Fatal("the redirect did not reach the storage host")
	}
	if storageAuth != "" {
		t.Errorf("storage host got an Authorization header; the token must not leave GitHub")
	}
}

func TestError_RateLimitHint(t *testing.T) {
	withTestPolicy(t)
	t.Setenv("GITHUB_TOKEN", "")
	srv, _ := stepServer(t, step{status: 429})
	withHosts(t, hostRouter{"github.com": srv})

	_, err := Get(context.Background(), "https://github.com/o/r/releases/download/v1/a")
	if err == nil || !strings.Contains(err.Error(), "GITHUB_TOKEN") {
		t.Errorf("a 429 from GitHub without a token should hint GITHUB_TOKEN, got: %v", err)
	}
}

func TestRetryAfter(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	tests := []struct {
		in   string
		want time.Duration
	}{
		{"", 0},
		{"0", 0},
		{"12", 12 * time.Second},
		{"-5", 0},
		{"soon", 0},
		{now.Add(90 * time.Second).Format(http.TimeFormat), 90 * time.Second},
		{now.Add(-time.Minute).Format(http.TimeFormat), 0},
	}
	for _, tt := range tests {
		if got := retryAfter(tt.in, now); got != tt.want {
			t.Errorf("retryAfter(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestPolicyDelay(t *testing.T) {
	p := Policy{BaseDelay: 4 * time.Second, MaxDelay: 20 * time.Second}
	for attempt, hi := range map[int]time.Duration{1: 4 * time.Second, 2: 8 * time.Second, 3: 16 * time.Second, 4: 20 * time.Second, 60: 20 * time.Second} {
		for range 20 {
			d := p.delay(attempt, 0)
			if d < hi/2 || d > hi {
				t.Fatalf("delay(%d) = %v, want between %v and %v", attempt, d, hi/2, hi)
			}
		}
	}
}
