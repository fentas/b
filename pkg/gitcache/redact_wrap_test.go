package gitcache

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

// wrapToken is the token in the auth header of these tests. It is not a
// real value.
const (
	wrapToken  = "FAKE-TOKEN-x"
	wrapHeader = "Authorization: Bearer " + wrapToken
)

// cleanWrapper has a text without the secret, and wraps an error that can
// hold the secret.
type cleanWrapper struct{ err error }

func (w *cleanWrapper) Error() string { return "git failed" }
func (w *cleanWrapper) Unwrap() error { return w.err }

// reachable returns err and each error that err wraps, through
// Unwrap() error and Unwrap() []error.
func reachable(err error) []error {
	if err == nil {
		return nil
	}
	out := []error{err}
	switch u := err.(type) {
	case interface{ Unwrap() error }:
		out = append(out, reachable(u.Unwrap())...)
	case interface{ Unwrap() []error }:
		for _, e := range u.Unwrap() {
			out = append(out, reachable(e)...)
		}
	}
	return out
}

// checkNoSecret fails the test when err, or an error that err wraps, holds
// wrapToken in its text (in each format verb) or in the Stderr of an
// *exec.ExitError, or when errors.As finds a cleanWrapper in err.
func checkNoSecret(t *testing.T, err error) {
	t.Helper()
	for _, e := range reachable(err) {
		for _, verb := range []string{"%s", "%v", "%+v", "%q", "%#v"} {
			if strings.Contains(fmt.Sprintf(verb, e), wrapToken) {
				t.Errorf("the %s text of a reachable %T holds the token", verb, e)
			}
		}
		if exit, ok := e.(*exec.ExitError); ok && strings.Contains(string(exit.Stderr), wrapToken) {
			t.Error("a reachable *exec.ExitError holds the token in its Stderr")
		}
	}
	var w *cleanWrapper
	if errors.As(err, &w) {
		t.Error("errors.As finds the wrapper that wraps the secret")
	}
}

// redactWrap keeps as the cause only an *exec.ExitError, a context
// sentinel or exec.ErrNotFound, and never an error that wraps one of them.
// For every other error, Unwrap gives nil. The text never holds the token.
func TestRedactWrap_KeptCause(t *testing.T) {
	exit := &exec.ExitError{}
	notFound := &exec.Error{Name: "git", Err: exec.ErrNotFound}
	secret := func(cause error) error { return fmt.Errorf("sent %s: %w", wrapHeader, cause) }
	tests := []struct {
		name   string
		err    error
		header string
		cause  error // the error that Unwrap must give
		text   string
	}{
		{"a clean wrapper of the secret", &cleanWrapper{errors.New("sent " + wrapHeader)}, wrapHeader,
			nil, "git failed"},
		{"an errors.Join passed in", errors.Join(errors.New("sent "+wrapHeader), errors.New("second")), wrapHeader,
			nil, "sent Authorization: Bearer ***\nsecond"},
		{"a clean wrapper, a secret error, an *exec.ExitError", &cleanWrapper{secret(exit)}, wrapHeader,
			exit, "git failed"},
		{"an *exec.ExitError under a secret error", secret(exit), wrapHeader,
			exit, "sent Authorization: Bearer ***: <nil>"},
		{"an *exec.ExitError with the secret in its Stderr", fmt.Errorf("git: %w", &exec.ExitError{Stderr: []byte("sent " + wrapHeader)}), wrapHeader,
			nil, "git: <nil>"},
		{"context.DeadlineExceeded under a secret error", secret(context.DeadlineExceeded), wrapHeader,
			context.DeadlineExceeded, "sent Authorization: Bearer ***: context deadline exceeded"},
		{"context.Canceled under a secret error", secret(context.Canceled), wrapHeader,
			context.Canceled, "sent Authorization: Bearer ***: context canceled"},
		{"a clean wrapper, a secret error, exec.ErrNotFound", &cleanWrapper{secret(notFound)}, wrapHeader,
			exec.ErrNotFound, "git failed"},
		{"a sentinel whose own text holds the secret", fmt.Errorf("git: %w", context.DeadlineExceeded), "Authorization: Bearer deadline",
			nil, "git: context *** exceeded"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := redactWrap(tt.err, tt.header)
			if got.Error() != tt.text {
				t.Errorf("Error() = %q, want %q", got.Error(), tt.text)
			}
			if cause := errors.Unwrap(got); cause != tt.cause {
				t.Errorf("Unwrap() = %#v, want %#v", cause, tt.cause)
			}
			if tt.cause != nil && !errors.Is(got, tt.cause) {
				t.Errorf("errors.Is(%v) = false", tt.cause)
			}
			var ee *exec.Error
			if errors.As(got, &ee) {
				t.Error("errors.As finds the *exec.Error, not only the exec.ErrNotFound sentinel")
			}
			checkNoSecret(t, got)
		})
	}
}
