package binary

import (
	"context"

	"github.com/fentas/b/pkg/provider"
	pwrap "github.com/fentas/goodies/progress"
	pretty "github.com/jedib0t/go-pretty/v6/progress"
)

type IsBinary interface {
	EnsureBinary(bool) error
	LocalBinary() *LocalBinary
}

type Callback func(*Binary) (string, error)

// SelectAssetFunc is called when multiple assets tie during auto-detection.
// It receives the scored candidates and should return the chosen asset.
type SelectAssetFunc func([]provider.Scored) (*provider.Asset, error)

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
	Alias         string          `json:"-"`
	Name          string          `json:"name" yaml:"name"`
	File          string          `json:"-"`
	IsTarGz       bool            `json:"-"`
	IsTarXz       bool            `json:"-"`
	IsZip         bool            `json:"-"`
	IsDynamic     bool            `json:"-"`
	TarFile       string          `json:"-"`
	TarFileF      Callback        `json:"-"`
	Tracker       *pretty.Tracker `json:"-"`
	Writer        *pwrap.Writer   `json:"-"`
	// for execution
	Envs map[string]string `json:"-"`

	// Provider-based auto-detection (Phase 1)
	AutoDetect   bool   `json:"-"` // use provider system instead of preset
	ProviderRef  string `json:"-"` // e.g. "github.com/derailed/k9s"
	ProviderType string `json:"-"` // e.g. "github", "gitlab", "go", "docker"
	AssetFilter  string          `json:"-"` // glob pattern to filter release assets (e.g. "argsh-so-*")
	SelectAsset  SelectAssetFunc `json:"-"` // interactive asset selector for ambiguous matches
}

type LocalBinary struct {
	Name     string `json:"name"`
	File     string `json:"file,omitempty"`
	Version  string `json:"version,omitempty"`
	Latest   string `json:"latest"`
	Enforced string `json:"enforced,omitempty"`
	// alias is the name of the binary that this binary is a reference to
	// yaml config sets this as reference
	Alias string `json:"alias,omitempty"`
	// Asset is a glob pattern to filter release assets (e.g. "argsh-so-*")
	Asset string `json:"asset,omitempty"`
	// IsProviderRef is true when Name is a provider ref (e.g. github.com/derailed/k9s)
	IsProviderRef bool `json:"-" yaml:"-"`
}
