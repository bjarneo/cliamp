package jellyfin

import (
	"github.com/bjarneo/cliamp/config"
	"github.com/bjarneo/cliamp/internal/embyapi"
	"github.com/bjarneo/cliamp/provider"
)

var (
	_ provider.ArtistBrowser             = (*Provider)(nil)
	_ provider.AlbumBrowser              = (*Provider)(nil)
	_ provider.AlbumTrackLoader          = (*Provider)(nil)
	_ provider.DefaultBrowseModeProvider = (*Provider)(nil)
	_ provider.PlaybackReporter          = (*Provider)(nil)
	_ provider.Searcher                  = (*Provider)(nil)
)

// Provider implements playlist.Provider for a Jellyfin server. The shared
// embyapi.Provider holds the provider body.
type Provider struct {
	*embyapi.Provider
}

func newProvider(client *Client) *Provider {
	return &Provider{embyapi.NewProvider(client, "Jellyfin")}
}

// NewFromConfig returns a Provider from a JellyfinConfig, or nil if URL or token is missing.
func NewFromConfig(cfg config.JellyfinConfig) *Provider {
	if !cfg.IsSet() {
		return nil
	}
	return newProvider(NewClient(cfg.URL, cfg.Token, cfg.UserID, cfg.User, cfg.Password))
}

// DefaultBrowseMode always opens Jellyfin as artist → album → songs.
func (p *Provider) DefaultBrowseMode() provider.BrowseMode {
	return provider.BrowseArtistAlbums
}
