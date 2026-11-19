package fetch

import (
	"errors"
	"io"
	"net/url"
	"regexp"
	"strings"
)

// InvalidURL stands for a URL that does not parse, in errors: no part of
// such a string goes into an error.
const InvalidURL = "<invalid URL>"

// RedactURL is u without its user info, query and fragment: the form in
// which b names a URL in errors. User info and a query can hold a
// credential (basic auth, a token, a signed download URL). url.URL.Redacted
// is not enough: it keeps the user name and the query.
func RedactURL(u *url.URL) string {
	if u == nil {
		return ""
	}
	c := *u
	c.User = nil
	c.RawQuery, c.ForceQuery = "", false
	c.Fragment, c.RawFragment = "", ""
	return c.String()
}

// RedactRawURL is RedactURL for a URL string. A string that does not parse
// gives InvalidURL.
func RedactRawURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return InvalidURL
	}
	return RedactURL(u)
}

// redactURLErrors puts the redacted form of the URL into each *url.Error in
// the chain of err, and returns err. net/http returns a *url.Error for a
// failed request, and its Error() prints the URL with its query and user
// name (net/http hides only the password). Call it before the error is
// formatted into another error's text.
func redactURLErrors(err error) error {
	for e := err; e != nil; e = errors.Unwrap(e) {
		if ue, ok := e.(*url.Error); ok {
			ue.URL = RedactRawURL(ue.URL)
		}
	}
	return err
}

// urlInText matches a URL in a text: a scheme, "://", then the characters up
// to a space, a double quote, a backquote or an angle bracket. A backslash
// escape (as %q writes one) stays in the match. A "'" is a URL character, so
// it stays in the match too (a "'" at the end of a match is urlTail).
var urlInText = regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*://(?:\\.|[^\s"<>` + "`" + `\\])*`)

// urlTail is the end of a URL match that belongs to the text after the URL:
// punctuation (": ", ", ", "."), a "'" (the end of a quote) and an escaped
// quote (the end of a string in a text that %q wrote).
var urlTail = regexp.MustCompile(`(?:\\["'` + "`" + `]|[':,.;)\]])+$`)

// ScrubURLs returns s with each URL in it cut to its scheme, host and path:
// the user info, the query and the fragment go. It works on the text, so it
// also cuts a URL that does not parse. The authority of a URL ends at the
// first "/", "?" or "#" after the scheme, and its user info is all up to the
// last "@" in the authority. Punctuation, a "'" and an escaped quote at the
// end of a URL stay.
func ScrubURLs(s string) string {
	return urlInText.ReplaceAllStringFunc(s, scrubURL)
}

func scrubURL(raw string) string {
	tail := urlTail.FindString(raw)
	raw = raw[:len(raw)-len(tail)]
	i := strings.Index(raw, "://") + len("://")
	scheme, rest := raw[:i], raw[i:]
	end := strings.IndexAny(rest, "/?#")
	if end < 0 {
		end = len(rest)
	}
	if at := strings.LastIndexByte(rest[:end], '@'); at >= 0 {
		rest = rest[at+1:]
	}
	if cut := strings.IndexAny(rest, "?#"); cut >= 0 {
		rest = rest[:cut]
	}
	return scheme + rest + tail
}

// scrubbedError is an error whose text holds its URLs only in the form of
// ScrubURLs. Unwrap returns the cause, so errors.Is and errors.As still
// reach it. (The text of a cause that errors.As returns is not scrubbed.)
type scrubbedError struct {
	msg string
	err error
}

func (e *scrubbedError) Error() string { return e.msg }
func (e *scrubbedError) Unwrap() error { return e.err }

// scrubError returns err with the URLs of its text cut (ScrubURLs), or err
// itself when its text has nothing to cut. An error text can hold a URL that
// is not in a URL field: net/http quotes a Location header that does not
// parse ("failed to parse Location header %q").
func scrubError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if s := ScrubURLs(msg); s != msg {
		return &scrubbedError{msg: s, err: err}
	}
	return err
}

// maxErrorBody is the most bytes of a response body that an error quotes.
const maxErrorBody = 512

// maxErrorRead is the most bytes of a response body that ErrorBody reads.
const maxErrorRead = 8 * maxErrorBody

// ErrorBody reads the start of a response body for an error text: at most
// maxErrorBody bytes, with the URLs in it cut (ScrubURLs). An API can echo
// the URL of the request, with its token, in an error body. ErrorBody reads
// maxErrorRead bytes and cuts the URLs before it cuts the text, so that the
// cut cannot keep a part of a URL that ScrubURLs drops. A read of
// maxErrorRead bytes can end inside a URL: cutOpenURL cuts that URL first.
func ErrorBody(body io.Reader) string {
	raw, _ := io.ReadAll(io.LimitReader(body, maxErrorRead))
	s := string(raw)
	if len(raw) == maxErrorRead {
		s = cutOpenURL(s)
	}
	s = ScrubURLs(s)
	if len(s) > maxErrorBody {
		s = strings.ToValidUTF8(s[:maxErrorBody], "") + "..."
	}
	return s
}

// cutOpenURL cuts the last URL in s to its scheme when that URL runs to the
// end of s: s is the start of a longer text, so the "@" that ends the user
// info of that URL can lie after s, where ScrubURLs does not see it. A URL
// match also ends before a backslash at the end of s (the start of an
// escape), so that URL is cut too.
func cutOpenURL(s string) string {
	locs := urlInText.FindAllStringIndex(s, -1)
	if len(locs) == 0 {
		return s
	}
	start, end := locs[len(locs)-1][0], locs[len(locs)-1][1]
	if end != len(s) && s[end:] != `\` {
		return s
	}
	return s[:start+strings.Index(s[start:], "://")+len("://")]
}
