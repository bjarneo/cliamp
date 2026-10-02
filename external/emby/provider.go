package emby

import (
	"github.com/bjarneo/cliamp/config"
	"github.com/bjarneo/cliamp/internal/embyapi"
	"github.com/bjarneo/cliamp/provider"
)

var (
	_ provider.ArtistBrowser    = (*Provider)(nil)
	_ provider.AlbumBrowser     = (*Provider)(nil)
	_ provider.AlbumTrackLoader = (*Provider)(nil)
	_ provider.PlaybackReporter = (*Provider)(nil)
	_ provider.Searcher         = (*Provider)(nil)
)

// Provider implements playlist.Provider for an Emby server. The shared
// embyapi.Provider holds the provider body.
type Provider struct {
	*embyapi.Provider
}

func newProvider(client *Client) *Provider {
	return &Provider{embyapi.NewProvider(client, "Emby")}
}

// NewFromConfig returns a Provider from an EmbyConfig, or nil if URL or token is missing.
func NewFromConfig(cfg config.EmbyConfig) *Provider {
	if !cfg.IsSet() {
		return nil
	}
	return newProvider(NewClient(cfg.URL, cfg.Token, cfg.UserID, cfg.User, cfg.Password))
}
