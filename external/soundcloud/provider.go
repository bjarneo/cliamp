// Package soundcloud implements a playlist.Provider backed by yt-dlp.
//
// SoundCloud is opt-in: it only registers when [soundcloud] enabled = true
// is set in config. Once enabled, search uses yt-dlp's "scsearch:" protocol
// and works without further configuration. When [soundcloud] user is set,
// browse exposes that profile's Tracks, Likes, and Reposts — public for most
// accounts. With cookies_from set, yt-dlp picks up the user's browser session
// for subscriber-gated content.
package soundcloud

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
	"github.com/bjarneo/cliamp/resolve"
)

// Compile-time interface checks.
var (
	_ playlist.Provider = (*Provider)(nil)
	_ provider.Searcher = (*Provider)(nil)
)

// Config holds settings for the SoundCloud provider.
type Config struct {
	Enabled     bool   // true only when user explicitly sets enabled = true
	User        string // SoundCloud username (the path segment, e.g. "yourname"). Optional.
	CookiesFrom string // browser name for yt-dlp --cookies-from-browser (e.g. "firefox"). Optional.
}

// IsSet reports whether the SoundCloud provider should be exposed.
// SoundCloud is opt-in: requires enabled = true in [soundcloud].
func (c Config) IsSet() bool { return c.Enabled }

// Provider implements playlist.Provider and provider.Searcher for SoundCloud
// via yt-dlp.
type Provider struct {
	user string // optional configured username for browse
}

// defaultBrowse seeds the empty-user discovery view. SoundCloud's official
// charts/discover endpoints all 404 through yt-dlp, so these are search-backed
// virtual playlists — the ID is the yt-dlp page URL passed straight to
// ResolveYTDLBatch.
var defaultBrowse = []playlist.PlaylistInfo{
	{ID: "scsearch50:trending", Name: "Trending", Section: "Browse"},
	{ID: "scsearch50:hip hop", Name: "Hip-Hop", Section: "Browse"},
	{ID: "scsearch50:electronic", Name: "Electronic", Section: "Browse"},
	{ID: "scsearch50:house", Name: "House", Section: "Browse"},
	{ID: "scsearch50:lo-fi", Name: "Lo-Fi", Section: "Browse"},
	{ID: "scsearch50:indie", Name: "Indie", Section: "Browse"},
	{ID: "scsearch50:pop", Name: "Pop", Section: "Browse"},
}

// NewFromConfig returns a provider, or nil when SoundCloud is not enabled.
// Registers SoundCloud's yt-dlp cookie source so search, browse, and playback
// use the user's signed-in session without affecting other providers.
func NewFromConfig(cfg Config) *Provider {
	if !cfg.Enabled {
		return nil
	}
	resolve.SetYTDLCookiesForHost("soundcloud.com", cfg.CookiesFrom)
	return &Provider{user: strings.TrimSpace(cfg.User)}
}

func (p *Provider) Name() string { return "SoundCloud" }

// Playlists exposes the configured user's Tracks, Likes, and Reposts when a
// username is set; otherwise a curated set of genre searches so the empty
// state has something playable. Ctrl+F always opens search regardless.
func (p *Provider) Playlists() ([]playlist.PlaylistInfo, error) {
	if p.user == "" {
		return slices.Clone(defaultBrowse), nil
	}
	base := "https://soundcloud.com/" + url.PathEscape(p.user)
	return []playlist.PlaylistInfo{
		{ID: base + "/tracks", Name: "Tracks", Section: p.user},
		{ID: base + "/likes", Name: "Likes", Section: p.user},
		{ID: base + "/reposts", Name: "Reposts", Section: p.user},
	}, nil
}

// Tracks resolves a SoundCloud page URL or scsearch query through yt-dlp.
// The playlistID value comes from Playlists.
func (p *Provider) Tracks(playlistID string) ([]playlist.Track, error) {
	if err := validatePlaylistID(playlistID); err != nil {
		return nil, err
	}
	return resolve.ResolveYTDLBatch(playlistID, 0, 0, "")
}

// validatePlaylistID accepts only HTTPS SoundCloud URLs without userinfo
// and scsearch queries.
func validatePlaylistID(playlistID string) error {
	if playlistID == "" {
		return errors.New("soundcloud: empty playlist id")
	}
	if isSoundCloudSearch(playlistID) {
		return nil
	}
	u, err := url.Parse(playlistID)
	if err != nil || u.Scheme != "https" || u.Opaque != "" || u.User != nil {
		return errors.New("soundcloud: invalid playlist id")
	}
	rawHost := u.Hostname()
	if rawHost == "" || strings.HasPrefix(rawHost, ".") || strings.Contains(rawHost, "..") {
		return errors.New("soundcloud: invalid playlist id")
	}
	host := playlist.NormalizeHost(rawHost)
	if host != "soundcloud.com" && (!strings.HasSuffix(host, ".soundcloud.com") || len(host) <= len(".soundcloud.com")) {
		return errors.New("soundcloud: invalid playlist id")
	}
	return nil
}

// isSoundCloudSearch reports whether id is a valid scsearch query.
func isSoundCloudSearch(id string) bool {
	rest, ok := strings.CutPrefix(id, "scsearch")
	if !ok {
		return false
	}
	count, query, ok := strings.Cut(rest, ":")
	if !ok || strings.TrimSpace(query) == "" {
		return false
	}
	for i := range len(count) {
		if count[i] < '0' || count[i] > '9' {
			return false
		}
	}
	return true
}

// SearchTracks runs `yt-dlp scsearch{limit}:{query}` and returns matched
// tracks. Implements provider.Searcher.
func (p *Provider) SearchTracks(ctx context.Context, query string, limit int) ([]playlist.Track, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 10
	}
	return resolve.ResolveYTDLBatchContext(ctx, fmt.Sprintf("scsearch%d:%s", limit, q), 0, 0, "")
}
