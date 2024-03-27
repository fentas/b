package binary

import (
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	GithubLatestURL = "https://github.com/%s/releases/latest"
	// GithubLatestURL = "https://api.github.com/repos/%s/releases/latest"
)

func GithubLatest(b *Binary) (string, error) {
	if b.GitHubRepo == "" {
		return b.Version, fmt.Errorf("GitHubRepo is not set")
	}
	resp, err := http.Get(fmt.Sprintf(GithubLatestURL, b.GitHubRepo))
	if err != nil {
		return b.Version, err
	}
	resp.Body.Close()
	final := strings.Split(resp.Request.URL.String(), "/")
	return final[len(final)-1], nil
}

func GetBody(url string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}
