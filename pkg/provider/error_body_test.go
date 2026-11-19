package provider

import (
	"net/http"
	"strings"
	"testing"
)

// TestAPIErrors_BodyIsCutAndScrubbed: an API error quotes at most 512 bytes
// of the response body, and a URL that the body echoes loses its user info
// and its query. The test uses withFakeAPI, so it does not run in parallel.
func TestAPIErrors_BodyIsCutAndScrubbed(t *testing.T) {
	body := `{"message":"Validation Failed","url":"https://u-name:p-pass@api.test/repos/x/y?access_token=s3cr3t-token"}` +
		strings.Repeat("x", 2000)
	for _, tc := range []struct {
		name, prefix string
		call         func() error
	}{
		{"GitHub FetchRelease", "GitHub API error 422: ", func() error { _, err := (&GitHub{}).FetchRelease("github.com/x/y", "v1"); return err }},
		{"Gitea LatestVersion", "Gitea API error 422: ", func() error { _, err := (&Gitea{}).LatestVersion("codeberg.org/x/y"); return err }},
		{"Gitea FetchRelease", "Gitea API error 422: ", func() error { _, err := (&Gitea{}).FetchRelease("codeberg.org/x/y", "v1"); return err }},
		{"GitLab FetchRelease", "GitLab API error 422: ", func() error { _, err := (&GitLab{}).FetchRelease("gitlab.com/x/y", "v1"); return err }},
		{"GitLab LatestVersion", "GitLab API error 422: ", func() error { _, err := (&GitLab{}).LatestVersion("gitlab.com/x/y"); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cleanup := withFakeAPI(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusUnprocessableEntity)
				_, _ = w.Write([]byte(body))
			}))
			defer cleanup()
			err := tc.call()
			if err == nil {
				t.Fatal("no error for a 422")
			}
			msg := err.Error()
			for _, secret := range []string{"u-name", "p-pass", "access_token", "s3cr3t"} {
				if strings.Contains(msg, secret) {
					t.Errorf("error holds %q: %.200q", secret, msg)
				}
			}
			if !strings.Contains(msg, tc.prefix+`{"message":"Validation Failed","url":"https://api.test/repos/x/y"}`) {
				t.Errorf("error %.200q does not quote the scrubbed body after %q", msg, tc.prefix)
			}
			if !strings.HasSuffix(msg, "...") || len(msg) > len(tc.prefix)+512+len("...") {
				t.Errorf("error has %d bytes, want at most %d and the end \"...\"", len(msg), len(tc.prefix)+512+len("..."))
			}
		})
	}
}
