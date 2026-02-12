package provider

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// Known Gitea/Forgejo instances.
var knownGiteaHosts = []string{
	"codeberg.org",
	"gitea.com",
}

func init() {
	Register(&Gitea{})
}

// Gitea fetches releases from Gitea/Forgejo instances (including Codeberg).
type Gitea struct{}

func (g *Gitea) Name() string { return "gitea" }

func (g *Gitea) Match(ref string) bool {
	ref, _ = ParseRef(ref)
	for _, host := range knownGiteaHosts {
		if strings.HasPrefix(ref, host+"/") {
			return true
		}
	}
	return false
}

func (g *Gitea) LatestVersion(ref string) (string, error) {
	host, owner, repo := giteaParts(ref)
	apiURL := fmt.Sprintf("https://%s/api/v1/repos/%s/%s/releases?limit=1", host, owner, repo)

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return "", err
	}
	giteaSetAuth(req)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("Gitea API error %d: %s", resp.StatusCode, string(body))
	}

	var releases []struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return "", fmt.Errorf("decoding Gitea releases: %w", err)
	}
	if len(releases) == 0 {
		return "", fmt.Errorf("no releases found for %s/%s on %s", owner, repo, host)
	}
	return releases[0].TagName, nil
}

func (g *Gitea) FetchRelease(ref, version string) (*Release, error) {
	host, owner, repo := giteaParts(ref)
	if version == "" {
		var err error
		version, err = g.LatestVersion(ref)
		if err != nil {
			return nil, err
		}
	}

	apiURL := fmt.Sprintf("https://%s/api/v1/repos/%s/%s/releases/tags/%s", host, owner, repo, version)
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	giteaSetAuth(req)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("release %s not found for %s/%s on %s", version, owner, repo, host)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Gitea API error %d: %s", resp.StatusCode, string(body))
	}

	var gRelease struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
			Size               int64  `json:"size"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&gRelease); err != nil {
		return nil, fmt.Errorf("decoding Gitea release: %w", err)
	}

	release := &Release{Version: gRelease.TagName}
	for _, a := range gRelease.Assets {
		release.Assets = append(release.Assets, Asset{
			Name: a.Name,
			URL:  a.BrowserDownloadURL,
			Size: a.Size,
		})
	}
	return release, nil
}

func giteaParts(ref string) (host, owner, repo string) {
	ref, _ = ParseRef(ref)
	for _, h := range knownGiteaHosts {
		if strings.HasPrefix(ref, h+"/") {
			rest := strings.TrimPrefix(ref, h+"/")
			parts := strings.SplitN(rest, "/", 3)
			if len(parts) >= 2 {
				return h, parts[0], parts[1]
			}
		}
	}
	return "", "", ""
}

func giteaSetAuth(req *http.Request) {
	if token := os.Getenv("GITEA_TOKEN"); token != "" {
		req.Header.Set("Authorization", "token "+token)
	}
}
