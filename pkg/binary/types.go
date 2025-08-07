package binary

import (
	"context"

	pwrap "github.com/fentas/goodies/progress"
	pretty "github.com/jedib0t/go-pretty/v6/progress"
)

type IsBinary interface {
	EnsureBinary(bool) error
	LocalBinary() *LocalBinary
}

type Callback func(*Binary) (string, error)

type Binary struct {
	Context context.Context `json:"-"`
	// for installation
	URL           string          `json:"-"`
	URLF          Callback        `json:"-"`
	GitHubRepo    string          `json:"repo"`
	GitHubFile    string          `json:"-"`
	GitHubFileF   Callback        `json:"-"`
	Version       string          `json:"-"`
	VersionF      Callback        `json:"-"`
	VersionLocalF Callback        `json:"-"`
	Name          string          `json:"name" yaml:"name"`
	File          string          `json:"-"`
	IsTarGz       bool            `json:"-"`
	IsTarXz       bool            `json:"-"`
	IsZip         bool            `json:"-"`
	TarFile       string          `json:"-"`
	TarFileF      Callback        `json:"-"`
	Tracker       *pretty.Tracker `json:"-"`
	Writer        *pwrap.Writer   `json:"-"`
	// for execution
	Envs map[string]string `json:"-"`
}

type LocalBinary struct {
	Name     string `json:"name"`
	File     string `json:"file,omitempty"`
	Version  string `json:"version,omitempty"`
	Latest   string `json:"latest"`
	Enforced string `json:"enforced,omitempty"`
}
