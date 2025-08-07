package binary

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const (
	GithubLatestURL = "https://github.com/%s/releases/latest"
	// GithubLatestURL = "https://api.github.com/repos/%s/releases/latest"
)

var (
	// List of special extensions to try
	Extensions = []string{"tar.gz", "tar.xz"}
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

// GetFileExtensionFromURL returns the file extension from a URL
func GetFileExtensionFromURL(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	for _, ext := range Extensions {
		if strings.HasSuffix(u.Path, "."+ext) {
			return ext, nil
		}
	}
	// Default to the last period
	pos := strings.LastIndex(u.Path, ".")
	if pos == -1 {
		return "", errors.New("couldn't find a period to indicate a file extension")
	}

	return u.Path[pos+1 : len(u.Path)], nil
}
