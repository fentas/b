package fetch

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// within runs f and returns its error. It fails the test if f does not
// return within d. f must not call t: it can outlive the test.
func within(t *testing.T, d time.Duration, f func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- f() }()
	select {
	case err := <-done:
		return err
	case <-time.After(d):
		t.Fatalf("still running after %v: a stalled try is not bounded", d)
		return nil
	}
}

// getAndClose is Get that closes the body.
func getAndClose(url string) error {
	resp, err := Get(context.Background(), url)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// stallServer runs handler for each request, numbered from 1. quit closes
// when the client gives up the request or the test ends; a handler that
// stalls waits for it.
func stallServer(t *testing.T, handler func(n int32, w http.ResponseWriter, r *http.Request, quit <-chan struct{})) (*httptest.Server, *int32) {
	t.Helper()
	var n int32
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		quit := make(chan struct{})
		go func() {
			select {
			case <-r.Context().Done():
			case <-release:
			}
			close(quit)
		}()
		handler(atomic.AddInt32(&n, 1), w, r, quit)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) }) // runs before srv.Close
	return srv, &n
}

func flush(w http.ResponseWriter) { w.(http.Flusher).Flush() }

func TestDo_RetriesOnlyGetAndHead(t *testing.T) {
	tests := []struct {
		method    string
		body      string
		wantTries int32
	}{
		{http.MethodGet, "", 2},
		{http.MethodHead, "", 2},
		{http.MethodPost, "", 1},
		{http.MethodPut, "payload", 1},
		{http.MethodDelete, "", 1},
		// A GET with a body cannot be sent again either.
		{http.MethodGet, "payload", 1},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s body=%q", tt.method, tt.body), func(t *testing.T) {
			withTestPolicy(t)
			var mu sync.Mutex
			var bodies []string
			srv, tries := stallServer(t, func(n int32, w http.ResponseWriter, r *http.Request, _ <-chan struct{}) {
				b, _ := io.ReadAll(r.Body)
				if n == 1 {
					mu.Lock()
					bodies = append(bodies, string(b))
					mu.Unlock()
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				w.WriteHeader(http.StatusOK)
			})
			var body io.Reader
			if tt.body != "" {
				body = strings.NewReader(tt.body)
			}
			req, err := http.NewRequest(tt.method, srv.URL, body)
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			resp, err := Do(req)
			if err != nil {
				t.Fatalf("Do: %v", err)
			}
			resp.Body.Close()
			if got := atomic.LoadInt32(tries); got != tt.wantTries {
				t.Errorf("server got %d requests, want %d", got, tt.wantTries)
			}
			if tt.wantTries == 1 && resp.StatusCode != http.StatusServiceUnavailable {
				t.Errorf("status = %d, want the 503 of the one try", resp.StatusCode)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(bodies) != 1 || bodies[0] != tt.body {
				t.Errorf("first request bodies = %q, want [%q]", bodies, tt.body)
			}
		})
	}
}

// The first try gets no response headers; the second try answers.
func TestGet_HeaderTimeoutIsRetried(t *testing.T) {
	withTestPolicy(t)
	DefaultPolicy.HeaderTimeout = 50 * time.Millisecond
	srv, tries := stallServer(t, func(n int32, w http.ResponseWriter, r *http.Request, quit <-chan struct{}) {
		if n == 1 {
			<-quit
			return
		}
		_, _ = io.WriteString(w, "ok")
	})
	if err := within(t, 5*time.Second, func() error { return getAndClose(srv.URL) }); err != nil {
		t.Errorf("Get: %v", err)
	}
	if got := atomic.LoadInt32(tries); got != 2 {
		t.Errorf("server got %d requests, want 2", got)
	}
}

// Without headers on any try, the error names the timeout.
func TestGet_HeaderTimeoutExhausted(t *testing.T) {
	withTestPolicy(t)
	DefaultPolicy.Attempts = 2
	DefaultPolicy.HeaderTimeout = 50 * time.Millisecond
	srv, _ := stallServer(t, func(n int32, w http.ResponseWriter, r *http.Request, quit <-chan struct{}) { <-quit })
	err := within(t, 5*time.Second, func() error { return getAndClose(srv.URL) })
	if err == nil || !strings.Contains(err.Error(), "no response headers") {
		t.Errorf("err = %v, want a header timeout", err)
	}
}

// The first 200 sends part of its body and stops; the try ends after
// IdleTimeout, and the second try starts from byte zero.
func TestDownload_StalledBodyIsRetried(t *testing.T) {
	withTestPolicy(t)
	DefaultPolicy.IdleTimeout = 50 * time.Millisecond
	full := strings.Repeat("abcdefghij", 50)
	srv, tries := stallServer(t, func(n int32, w http.ResponseWriter, r *http.Request, quit <-chan struct{}) {
		w.Header().Set("Content-Length", fmt.Sprint(len(full)))
		if n == 1 {
			_, _ = io.WriteString(w, full[:100])
			flush(w)
			<-quit
			return
		}
		_, _ = io.WriteString(w, full)
	})
	var body []byte
	err := within(t, 5*time.Second, func() error {
		resp, err := Download(context.Background(), srv.URL, nil)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		body, err = io.ReadAll(resp.Body)
		return err
	})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if string(body) != full {
		t.Errorf("body has %d bytes, want the %d bytes of the full body", len(body), len(full))
	}
	if got := atomic.LoadInt32(tries); got != 2 {
		t.Errorf("server got %d requests, want 2", got)
	}
}

// A body that is slow but keeps sending is not cut, even when the whole
// download takes longer than IdleTimeout.
func TestDownload_SlowProgressIsNotCut(t *testing.T) {
	withTestPolicy(t)
	DefaultPolicy.IdleTimeout = 150 * time.Millisecond
	chunk := strings.Repeat("x", 100)
	srv, tries := stallServer(t, func(n int32, w http.ResponseWriter, r *http.Request, _ <-chan struct{}) {
		for range 10 {
			_, _ = io.WriteString(w, chunk)
			flush(w)
			time.Sleep(40 * time.Millisecond) // 400ms in total
		}
	})
	resp, err := Download(context.Background(), srv.URL, nil)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if len(body) != 10*len(chunk) {
		t.Errorf("body has %d bytes, want %d", len(body), 10*len(chunk))
	}
	if got := atomic.LoadInt32(tries); got != 1 {
		t.Errorf("server got %d requests, want 1", got)
	}
}

// The body of a response that Do returns also ends when it stalls: the
// read fails instead of blocking for ever.
func TestDo_StalledBodyReadEnds(t *testing.T) {
	withTestPolicy(t)
	DefaultPolicy.IdleTimeout = 50 * time.Millisecond
	srv, _ := stallServer(t, func(n int32, w http.ResponseWriter, r *http.Request, quit <-chan struct{}) {
		_, _ = io.WriteString(w, `{"tag_name":`)
		flush(w)
		<-quit
	})
	resp, err := Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	readErr := within(t, 5*time.Second, func() error {
		_, err := io.ReadAll(resp.Body)
		return err
	})
	if readErr == nil || !strings.Contains(readErr.Error(), "no body data") {
		t.Errorf("read error = %v, want an idle timeout", readErr)
	}
}

// A 503 whose body stops after the headers: the drain before the retry
// must not block.
func TestGet_StalledErrorBodyIsBounded(t *testing.T) {
	withTestPolicy(t)
	DefaultPolicy.IdleTimeout = 50 * time.Millisecond
	srv, tries := stallServer(t, func(n int32, w http.ResponseWriter, r *http.Request, quit <-chan struct{}) {
		if n == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, "busy")
			flush(w)
			<-quit
			return
		}
		_, _ = io.WriteString(w, "ok")
	})
	if err := within(t, 5*time.Second, func() error { return getAndClose(srv.URL) }); err != nil {
		t.Errorf("Get: %v", err)
	}
	if got := atomic.LoadInt32(tries); got != 2 {
		t.Errorf("server got %d requests, want 2", got)
	}
}

// A 503 whose body sends one byte at a time without end: the idle limit
// does not stop it, the drain time limit does.
func TestGet_DribblingErrorBodyIsBounded(t *testing.T) {
	withTestPolicy(t)
	old := drainTimeout
	drainTimeout = 50 * time.Millisecond
	t.Cleanup(func() { drainTimeout = old })
	srv, tries := stallServer(t, func(n int32, w http.ResponseWriter, r *http.Request, quit <-chan struct{}) {
		if n > 1 {
			_, _ = io.WriteString(w, "ok")
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		for {
			if _, err := io.WriteString(w, "."); err != nil {
				return
			}
			flush(w)
			select {
			case <-quit:
				return
			case <-time.After(5 * time.Millisecond):
			}
		}
	})
	if err := within(t, 5*time.Second, func() error { return getAndClose(srv.URL) }); err != nil {
		t.Errorf("Get: %v", err)
	}
	if got := atomic.LoadInt32(tries); got != 2 {
		t.Errorf("server got %d requests, want 2", got)
	}
}
