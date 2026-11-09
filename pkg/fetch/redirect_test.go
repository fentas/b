package fetch

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// TestGitHubTokenHostMatch checks which URLs get GITHUB_TOKEN. Only an
// https URL with an ASCII GitHub host on the default port does.
func TestGitHubTokenHostMatch(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want bool
	}{
		{"github.com", "https://github.com/o/r", true},
		{"api.github.com", "https://api.github.com/repos/o/r", true},
		{"ASCII upper case", "https://GitHub.COM/o/r", true},
		{"one trailing dot", "https://github.com./o/r", true},
		{"explicit port 443", "https://github.com:443/o/r", true},
		// U+0130 lowercases to "i" in Unicode, but IDNA turns the host into
		// a different (punycode) domain.
		{"U+0130 in the host", "https://GİTHUB.COM/o/r", false},
		{"fullwidth letters in the host", "https://ｇｉｔｈｕｂ.com/o/r", false},
		{"Cyrillic look-alike letter", "https://gitһub.com/o/r", false},
		{"two trailing dots", "https://github.com../o/r", false},
		{"port 8443", "https://github.com:8443/o/r", false},
		{"user info", "https://user:pass@github.com/o/r", false},
		{"user name only", "https://user@github.com/o/r", false},
		{"plain http", "http://github.com/o/r", false},
		{"subdomain", "https://sub.github.com/o/r", false},
		{"suffix look-alike", "https://notgithub.com/o/r", false},
		{"prefix look-alike", "https://github.com.example.test/o/r", false},
		{"storage host", "https://release-assets.githubusercontent.com/a", false},
	}
	t.Setenv("GITHUB_TOKEN", fakeToken)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, tt.url, nil)
			if err != nil {
				t.Fatalf("NewRequest(%q): %v", tt.url, err)
			}
			setGitHubAuth(req)
			got := req.Header.Get("Authorization") != ""
			if got != tt.want {
				t.Errorf("token sent = %v, want %v", got, tt.want)
			}
		})
	}
}

// hop is one request as the client sent it.
type hop struct {
	url, auth, privateToken string
	header                  http.Header
}

// recordingRouter is a hostRouter that records each request it sends, with
// the credential headers it carries at that point.
type recordingRouter struct {
	hosts hostRouter
	mu    sync.Mutex
	hops  []hop
}

func (r *recordingRouter) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	r.hops = append(r.hops, hop{
		url:          req.URL.String(),
		auth:         foldGet(req.Header, "Authorization"),
		privateToken: foldGet(req.Header, "Private-Token"),
		header:       req.Header.Clone(),
	})
	r.mu.Unlock()
	return r.hosts.RoundTrip(req)
}

// foldGet returns the values of every key that matches name without regard
// to case. Header.Get finds only the canonical key, but the transport sends
// every key that is in the map.
func foldGet(h http.Header, name string) string {
	var vals []string
	for key, v := range h {
		if strings.EqualFold(key, name) {
			vals = append(vals, v...)
		}
	}
	return strings.Join(vals, ",")
}

// redirectServer redirects /start?to=<url> to <url> and answers "ok" to
// anything else.
func redirectServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if to := r.URL.Query().Get("to"); to != "" {
			http.Redirect(w, r, to, http.StatusFound)
			return
		}
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestRedirectCredentials follows one redirect and checks the credential
// headers on the second hop. Go keeps Authorization on a redirect to the
// same host (also from https to http, or to another port) and to a
// subdomain, and it keeps PRIVATE-TOKEN on any redirect; checkRedirect
// must remove them.
func TestRedirectCredentials(t *testing.T) {
	tests := []struct {
		name string
		from string // the first URL
		to   string // the redirect target
		// header and value are set by the caller; empty means the request
		// carries GITHUB_TOKEN from the environment.
		header, value string
		// restore installs a CheckRedirect on http.DefaultClient that copies
		// the credentials of the first request to every hop.
		restore  bool
		wantKept bool
	}{
		{name: "GitHub, same origin", from: "https://github.com/start", to: "https://github.com/end", wantKept: true},
		{name: "GitHub to api.github.com", from: "https://github.com/start", to: "https://api.github.com/end", wantKept: true},
		{name: "GitHub, https to http on the same host", from: "https://github.com/start", to: "http://github.com/end"},
		{name: "GitHub to another port", from: "https://github.com/start", to: "https://github.com:8443/end"},
		{name: "GitHub to a subdomain", from: "https://github.com/start", to: "https://sub.github.com/end"},
		{name: "GitHub to a non-GitHub host", from: "https://github.com/start", to: "https://example.test/end"},
		{name: "GitHub to a non-GitHub host, CheckRedirect restores", from: "https://github.com/start", to: "https://example.test/end", restore: true},
		{name: "GitHub to a subdomain, CheckRedirect restores", from: "https://github.com/start", to: "https://sub.github.com/end", restore: true},
		{name: "GitLab token, same origin", from: "https://gitlab.com/start", to: "https://gitlab.com/end", header: "PRIVATE-TOKEN", value: "gitlab-test-token", wantKept: true},
		{name: "GitLab token to another host", from: "https://gitlab.com/start", to: "https://example.test/end", header: "PRIVATE-TOKEN", value: "gitlab-test-token"},
		{name: "GitLab token, https to http", from: "https://gitlab.com/start", to: "http://gitlab.com/end", header: "PRIVATE-TOKEN", value: "gitlab-test-token"},
		{name: "Gitea token, same origin", from: "https://codeberg.org/start", to: "https://codeberg.org/end", header: "Authorization", value: "token gitea-test-token", wantKept: true},
		{name: "Gitea token to another host", from: "https://codeberg.org/start", to: "https://example.test/end", header: "Authorization", value: "token gitea-test-token"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withTestPolicy(t)
			token := ""
			if tt.header == "" {
				token = fakeToken
			}
			t.Setenv("GITHUB_TOKEN", token)

			srv := redirectServer(t)
			rec := &recordingRouter{hosts: hostRouter{}}
			for _, raw := range []string{tt.from, tt.to} {
				u, _ := url.Parse(raw)
				rec.hosts[u.Hostname()] = srv
			}
			client := &http.Client{Transport: rec}
			if tt.restore {
				client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
					for _, h := range credentialHeaders {
						if v := via[0].Header.Get(h); v != "" {
							req.Header.Set(h, v)
						}
					}
					return nil
				}
			}
			old := http.DefaultClient
			http.DefaultClient = client
			t.Cleanup(func() { http.DefaultClient = old })

			req, err := http.NewRequest(http.MethodGet, tt.from+"?to="+url.QueryEscape(tt.to), nil)
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			if tt.header != "" {
				req.Header.Set(tt.header, tt.value)
			}
			resp, err := Do(req)
			if err != nil {
				t.Fatalf("Do: %v", err)
			}
			resp.Body.Close()

			rec.mu.Lock()
			defer rec.mu.Unlock()
			if len(rec.hops) != 2 {
				t.Fatalf("hops = %+v, want 2", rec.hops)
			}
			first, second := rec.hops[0], rec.hops[1]
			if first.auth == "" && first.privateToken == "" {
				t.Fatalf("the first hop has no credential: %+v", first)
			}
			kept := second.auth != "" || second.privateToken != ""
			if kept != tt.wantKept {
				t.Errorf("second hop %s: credential kept = %v, want %v", second.url, kept, tt.wantKept)
			}
		})
	}
}

// A CheckRedirect error of the caller still stops the redirect.
func TestRedirect_CallerCheckRedirectStillApplies(t *testing.T) {
	withTestPolicy(t)
	srv := redirectServer(t)
	old := http.DefaultClient
	http.DefaultClient = &http.Client{
		Transport: hostRouter{"example.test": srv},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	t.Cleanup(func() { http.DefaultClient = old })

	resp, err := Get(context.Background(), "https://example.test/start?to="+url.QueryEscape("https://example.test/end"))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Errorf("status = %d, want 302 (the caller stopped the redirect)", resp.StatusCode)
	}
}

// The default limit of 10 redirects still applies when the caller has no
// CheckRedirect.
func TestRedirect_DefaultLimit(t *testing.T) {
	withTestPolicy(t)
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&n, 1)
		http.Redirect(w, r, "https://example.test/loop", http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	withHosts(t, hostRouter{"example.test": srv})

	_, err := Get(context.Background(), "https://example.test/loop")
	if err == nil || !strings.Contains(err.Error(), "stopped after 10 redirects") {
		t.Errorf("err = %v, want the 10-redirect limit", err)
	}
	if got := atomic.LoadInt32(&n); got != 10 {
		t.Errorf("server got %d requests, want 10", got)
	}
}

// A CheckRedirect of the caller can put a credential back under a key that
// is not canonical, which Header.Del does not remove. Every spelling must
// be removed on a redirect off the origin.
func TestRedirect_RawCredentialKeysAreRemoved(t *testing.T) {
	for _, tt := range []struct {
		name, from string
		keys       []string
	}{
		{"GitHub, lower-case authorization", "https://github.com/start", []string{"authorization"}},
		{"GitHub, upper-case AUTHORIZATION", "https://github.com/start", []string{"AUTHORIZATION"}},
		{"GitLab, upper-case PRIVATE-TOKEN", "https://gitlab.com/start", []string{"PRIVATE-TOKEN"}},
		{"GitLab, lower-case private-token", "https://gitlab.com/start", []string{"private-token"}},
		{"all spellings at once", "https://gitlab.com/start", []string{"authorization", "AUTHORIZATION", "PRIVATE-TOKEN", "private-token"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			withTestPolicy(t)
			t.Setenv("GITHUB_TOKEN", fakeToken)
			srv := redirectServer(t)
			from, _ := url.Parse(tt.from)
			rec := &recordingRouter{hosts: hostRouter{from.Hostname(): srv, "example.test": srv}}
			client := &http.Client{
				Transport: rec,
				CheckRedirect: func(req *http.Request, via []*http.Request) error {
					for _, k := range tt.keys {
						req.Header[k] = []string{"restored-test-credential"}
					}
					return nil
				},
			}
			old := http.DefaultClient
			http.DefaultClient = client
			t.Cleanup(func() { http.DefaultClient = old })

			req, err := http.NewRequest(http.MethodGet, tt.from+"?to="+url.QueryEscape("https://example.test/end"), nil)
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			if from.Hostname() == "gitlab.com" {
				req.Header.Set("PRIVATE-TOKEN", "gitlab-test-token")
			}
			resp, err := Do(req)
			if err != nil {
				t.Fatalf("Do: %v", err)
			}
			resp.Body.Close()

			rec.mu.Lock()
			defer rec.mu.Unlock()
			if len(rec.hops) != 2 {
				t.Fatalf("hops = %+v, want 2", rec.hops)
			}
			if h := rec.hops[1]; h.auth != "" || h.privateToken != "" {
				t.Errorf("second hop %s carries a credential: %+v", h.url, h)
			}
		})
	}
}

// follow sends a GET for first through a recordingRouter that sends every
// host to one redirectServer, with an optional CheckRedirect of the caller.
// setup changes the first request. It returns the hops after the first one.
func follow(t *testing.T, first string, setup func(*http.Request), restore func(*http.Request)) []hop {
	t.Helper()
	withTestPolicy(t)
	t.Setenv("GITHUB_TOKEN", "")
	srv := redirectServer(t)
	rec := &recordingRouter{hosts: hostRouter{}}
	for _, h := range []string{"example.test", "sub.example.test", "other.test", "github.com", "api.github.com"} {
		rec.hosts[h] = srv
	}
	client := &http.Client{Transport: rec}
	if restore != nil {
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			restore(req)
			return nil
		}
	}
	old := http.DefaultClient
	http.DefaultClient = client
	t.Cleanup(func() { http.DefaultClient = old })

	req, err := http.NewRequest(http.MethodGet, first, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if setup != nil {
		setup(req)
	}
	resp, err := Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.hops) < 2 {
		t.Fatalf("hops = %+v, want a redirect", rec.hops)
	}
	return rec.hops[1:]
}

// redirectTo returns from with a query that makes redirectServer redirect
// to to.
func redirectTo(from, to string) string {
	return from + "?to=" + url.QueryEscape(to)
}

// Go sets Referer on a redirect to the previous URL, query included. The
// query of a signed download URL holds a token, so Referer must not go to
// another origin.
func TestRedirect_RefererStaysOnTheOrigin(t *testing.T) {
	const secret = "signed-test-value"
	signed := func(from, to string) string {
		return from + "?token=" + secret + "&to=" + url.QueryEscape(to)
	}
	for _, tt := range []struct {
		name  string
		first string
		want  []bool // per hop after the first: Referer is sent
	}{
		{"same origin", signed("https://example.test/start", "https://example.test/end"), []bool{true}},
		{"to another host", signed("https://example.test/start", "https://other.test/end"), []bool{false}},
		{"to another port", signed("https://example.test/start", "https://example.test:8443/end"), []bool{false}},
		{"to a subdomain", signed("https://example.test/start", "https://sub.example.test/end"), []bool{false}},
		{"GitHub to api.github.com", signed("https://github.com/start", "https://api.github.com/end"), []bool{false}},
		{"back to the first origin", signed("https://example.test/start", signed("https://other.test/start", "https://example.test/end")), []bool{false, false}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			hops := follow(t, tt.first, nil, nil)
			if len(hops) != len(tt.want) {
				t.Fatalf("hops = %+v, want %d after the first", hops, len(tt.want))
			}
			for i, h := range hops {
				ref := foldGet(h.header, "Referer")
				if tt.want[i] && !strings.Contains(ref, secret) {
					t.Errorf("hop %s: Referer = %q, want the previous URL", h.url, ref)
				}
				if !tt.want[i] && ref != "" {
					t.Errorf("hop %s: Referer = %q, want none", h.url, ref)
				}
			}
		})
	}
}

// Go keeps Cookie and Proxy-Authorization on a redirect to a subdomain or
// to another port on the same host, and a CheckRedirect of the caller can
// put them back under any spelling. They must stay on the origin.
func TestRedirect_CookieAndProxyAuthStayOnTheOrigin(t *testing.T) {
	headers := []string{"Cookie", "Proxy-Authorization"}
	for _, tt := range []struct {
		name  string
		first string
		keys  []string // raw keys a CheckRedirect of the caller sets on each hop
		want  []bool   // per hop after the first: the headers are sent
	}{
		{"same origin", redirectTo("https://example.test/start", "https://example.test/end"), nil, []bool{true}},
		{"to a subdomain", redirectTo("https://example.test/start", "https://sub.example.test/end"), nil, []bool{false}},
		{"to another port", redirectTo("https://example.test/start", "https://example.test:8443/end"), nil, []bool{false}},
		{"to a subdomain, then on the subdomain", redirectTo("https://example.test/start", redirectTo("https://sub.example.test/start", "https://sub.example.test/end")), nil, []bool{false, false}},
		{"raw cookie keys put back", redirectTo("https://example.test/start", "https://other.test/end"), []string{"cookie", "COOKIE"}, []bool{false}},
		{"raw proxy-authorization keys put back", redirectTo("https://example.test/start", "https://other.test/end"), []string{"proxy-authorization", "PROXY-AUTHORIZATION"}, []bool{false}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			setup := func(req *http.Request) {
				req.Header.Set("Cookie", "session=test-cookie")
				req.Header.Set("Proxy-Authorization", "Basic test-proxy-credential")
			}
			var restore func(*http.Request)
			if tt.keys != nil {
				restore = func(req *http.Request) {
					for _, k := range tt.keys {
						req.Header[k] = []string{"restored-test-value"}
					}
				}
			}
			hops := follow(t, tt.first, setup, restore)
			if len(hops) != len(tt.want) {
				t.Fatalf("hops = %+v, want %d after the first", hops, len(tt.want))
			}
			for i, h := range hops {
				for _, name := range headers {
					got := foldGet(h.header, name)
					if tt.want[i] && got == "" {
						t.Errorf("hop %s: no %s, want it kept on the origin", h.url, name)
					}
					if !tt.want[i] && got != "" {
						t.Errorf("hop %s: %s = %q, want none", h.url, name, got)
					}
				}
			}
		})
	}
}
