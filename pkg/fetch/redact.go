package fetch

import (
	"errors"
	"net/url"
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
