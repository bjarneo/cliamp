// Package soundcloud implements a playlist.Provider backed by yt-dlp.
//
// Search uses yt-dlp's "scsearch:" protocol and works without any
// configuration. When [soundcloud] user is set, browse exposes that
// profile's Tracks, Likes, and Reposts — public for most accounts. With
// cookies_from set, yt-dlp picks up the user's browser session for
// subscriber-gated content.
package soundcloud

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"cliamp/playlist"
	"cliamp/provider"
	"cliamp/resolve"
)

// Compile-time interface checks.
var (
	_ playlist.Provider = (*Provider)(nil)
	_ provider.Searcher = (*Provider)(nil)
)

// Config holds settings for the SoundCloud provider.
type Config struct {
	Disabled    bool   // true only when user explicitly sets enabled = false
	User        string // SoundCloud username (the path segment, e.g. "yourname"). Optional.
	CookiesFrom string // browser name for yt-dlp --cookies-from-browser (e.g. "firefox"). Optional.
}

// IsSet reports whether the SoundCloud provider should be exposed.
// SoundCloud is enabled by default (search works without config); only an
// explicit enabled = false in config disables it.
func (c Config) IsSet() bool { return !c.Disabled }

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

// NewFromConfig returns a provider, or nil when SoundCloud is explicitly
// disabled. Sets resolve's yt-dlp cookies as a side effect when CookiesFrom
// is non-empty so any yt-dlp invocation (search, browse, playback) uses the
// user's signed-in session.
func NewFromConfig(cfg Config) *Provider {
	if cfg.Disabled {
		return nil
	}
	if cfg.CookiesFrom != "" {
		resolve.SetYTDLCookiesFrom(cfg.CookiesFrom)
	}
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

// Tracks resolves a SoundCloud page URL (or scsearch query) via yt-dlp. The
// playlistID is the value produced by Playlists.
func (p *Provider) Tracks(playlistID string) ([]playlist.Track, error) {
	if playlistID == "" {
		return nil, fmt.Errorf("soundcloud: empty playlist id")
	}
	return resolve.ResolveYTDLBatch(playlistID, 0, 0)
}

// SearchTracks runs `yt-dlp scsearch{limit}:{query}` and returns matched
// tracks. Implements provider.Searcher.
func (p *Provider) SearchTracks(_ context.Context, query string, limit int) ([]playlist.Track, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 10
	}
	return resolve.ResolveYTDLBatch(fmt.Sprintf("scsearch%d:%s", limit, q), 0, 0)
}
