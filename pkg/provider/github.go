package provider

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

func init() {
	Register(&GitHub{})
}

// GitHub fetches releases from the GitHub Releases API.
type GitHub struct{}

func (g *GitHub) Name() string { return "github" }

func (g *GitHub) Match(ref string) bool {
	// Matches "github.com/owner/repo" or bare "owner/repo" (no dots in owner).
	ref, _ = ParseRef(ref)
	if strings.HasPrefix(ref, "github.com/") {
		parts := strings.Split(strings.TrimPrefix(ref, "github.com/"), "/")
		return len(parts) >= 2 && parts[0] != "" && parts[1] != ""
	}
	// Bare owner/repo: no dots in owner (to avoid matching gitlab.com/... etc.)
	parts := strings.SplitN(ref, "/", 3)
	if len(parts) == 2 && !strings.Contains(parts[0], ".") && parts[0] != "" && parts[1] != "" {
		return true
	}
	return false
}

func (g *GitHub) LatestVersion(ref string) (string, error) {
	owner, repo := githubOwnerRepo(ref)
	// Use redirect-based approach (faster, no API rate limit).
	url := fmt.Sprintf("https://github.com/%s/%s/releases/latest", owner, repo)
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("no releases found for %s/%s", owner, repo)
	}
	final := resp.Request.URL.Path
	parts := strings.Split(final, "/")
	return parts[len(parts)-1], nil
}

func (g *GitHub) FetchRelease(ref, version string) (*Release, error) {
	owner, repo := githubOwnerRepo(ref)
	if version == "" {
		var err error
		version, err = g.LatestVersion(ref)
		if err != nil {
			return nil, err
		}
	}

	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/tags/%s", owner, repo, version)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("release %s not found for %s/%s", version, owner, repo)
	}
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("GitHub API rate limited (set GITHUB_TOKEN for higher limits)")
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GitHub API error %d: %s", resp.StatusCode, string(body))
	}

	var ghRelease struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
			Size               int64  `json:"size"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ghRelease); err != nil {
		return nil, fmt.Errorf("decoding GitHub release: %w", err)
	}

	release := &Release{Version: ghRelease.TagName}
	for _, a := range ghRelease.Assets {
		release.Assets = append(release.Assets, Asset{
			Name: a.Name,
			URL:  a.BrowserDownloadURL,
			Size: a.Size,
		})
	}
	return release, nil
}

// githubOwnerRepo extracts owner and repo from a ref.
func githubOwnerRepo(ref string) (owner, repo string) {
	ref, _ = ParseRef(ref)
	ref = strings.TrimPrefix(ref, "github.com/")
	parts := strings.SplitN(ref, "/", 3)
	if len(parts) >= 2 {
		return parts[0], parts[1]
	}
	return ref, ""
}
