package provider

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

func init() {
	Register(&GitLab{})
}

// GitLab fetches releases from the GitLab Releases API.
type GitLab struct{}

func (g *GitLab) Name() string { return "gitlab" }

func (g *GitLab) Match(ref string) bool {
	ref, _ = ParseRef(ref)
	return strings.HasPrefix(ref, "gitlab.com/")
}

func (g *GitLab) LatestVersion(ref string) (string, error) {
	projectPath := gitlabProjectPath(ref)
	releases, err := gitlabGetReleases(projectPath, 1)
	if err != nil {
		return "", err
	}
	if len(releases) == 0 {
		return "", fmt.Errorf("no releases found for %s", projectPath)
	}
	return releases[0].TagName, nil
}

func (g *GitLab) FetchRelease(ref, version string) (*Release, error) {
	projectPath := gitlabProjectPath(ref)
	if version == "" {
		var err error
		version, err = g.LatestVersion(ref)
		if err != nil {
			return nil, err
		}
	}

	apiURL := fmt.Sprintf("https://gitlab.com/api/v4/projects/%s/releases/%s",
		url.PathEscape(projectPath), url.PathEscape(version))

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	if token := os.Getenv("GITLAB_TOKEN"); token != "" {
		req.Header.Set("PRIVATE-TOKEN", token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("release %s not found for %s", version, projectPath)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GitLab API error %d: %s", resp.StatusCode, string(body))
	}

	var glRelease struct {
		TagName string `json:"tag_name"`
		Assets  struct {
			Links []struct {
				Name      string `json:"name"`
				DirectURL string `json:"direct_asset_url"`
			} `json:"links"`
			Sources []struct {
				Format string `json:"format"`
				URL    string `json:"url"`
			} `json:"sources"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&glRelease); err != nil {
		return nil, fmt.Errorf("decoding GitLab release: %w", err)
	}

	release := &Release{Version: glRelease.TagName}
	for _, link := range glRelease.Assets.Links {
		release.Assets = append(release.Assets, Asset{
			Name: link.Name,
			URL:  link.DirectURL,
		})
	}
	return release, nil
}

type gitlabReleaseSummary struct {
	TagName string `json:"tag_name"`
}

func gitlabGetReleases(projectPath string, perPage int) ([]gitlabReleaseSummary, error) {
	apiURL := fmt.Sprintf("https://gitlab.com/api/v4/projects/%s/releases?per_page=%d",
		url.PathEscape(projectPath), perPage)

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	if token := os.Getenv("GITLAB_TOKEN"); token != "" {
		req.Header.Set("PRIVATE-TOKEN", token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GitLab API error %d: %s", resp.StatusCode, string(body))
	}

	var releases []gitlabReleaseSummary
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, fmt.Errorf("decoding GitLab releases: %w", err)
	}
	return releases, nil
}

func gitlabProjectPath(ref string) string {
	ref, _ = ParseRef(ref)
	return strings.TrimPrefix(ref, "gitlab.com/")
}
