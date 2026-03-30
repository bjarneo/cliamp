package plex

import (
	"context"
	"fmt"
	"sync"

	"cliamp/config"
	"cliamp/playlist"
	"cliamp/provider"
)

// Compile-time interface checks.
var (
	_ provider.Searcher         = (*Provider)(nil)
	_ provider.AlbumTrackLoader = (*Provider)(nil)
)

// Provider implements playlist.Provider for a Plex Media Server.
// Playlists() returns all albums across all music library sections.
// Tracks() returns the tracks for a given album ratingKey.
type Provider struct {
	client        *Client
	mu            sync.Mutex
	playlistCache []playlist.PlaylistInfo
	trackCache    map[string][]playlist.Track
}

// newProvider returns a Provider backed by the given Client.
func newProvider(client *Client) *Provider {
	return &Provider{client: client}
}

// NewFromConfig returns a Provider from a PlexConfig, or nil if URL or Token is missing.
func NewFromConfig(cfg config.PlexConfig) *Provider {
	if !cfg.IsSet() {
		return nil
	}
	return newProvider(NewClient(cfg.URL, cfg.Token))
}

// Name returns the display name used in the provider selector.
func (p *Provider) Name() string { return "Plex" }

// Playlists returns all albums across all Plex music library sections.
// Each album is a PlaylistInfo whose ID is the album's Plex ratingKey.
// Results are cached after the first successful call.
func (p *Provider) Playlists() ([]playlist.PlaylistInfo, error) {
	p.mu.Lock()
	if p.playlistCache != nil {
		cached := p.playlistCache
		p.mu.Unlock()
		return cached, nil
	}
	p.mu.Unlock()

	sections, err := p.client.MusicSections()
	if err != nil {
		return nil, err
	}
	if len(sections) == 0 {
		return nil, fmt.Errorf("plex: no music libraries found on this server")
	}

	var lists []playlist.PlaylistInfo
	for _, sec := range sections {
		albums, err := p.client.Albums(sec.Key)
		if err != nil {
			return nil, err
		}
		for _, a := range albums {
			name := a.ArtistName + " — " + a.Title
			if a.Year > 0 {
				name = fmt.Sprintf("%s — %s (%d)", a.ArtistName, a.Title, a.Year)
			}
			lists = append(lists, playlist.PlaylistInfo{
				ID:         a.RatingKey,
				Name:       name,
				TrackCount: a.TrackCount,
			})
		}
	}

	p.mu.Lock()
	p.playlistCache = lists
	p.mu.Unlock()

	return lists, nil
}

// Tracks returns the tracks for the album identified by albumRatingKey.
// Each track's Path is a complete authenticated HTTP URL ready for the player.
// Tracks with no streamable part (missing Media/Part data) are silently skipped.
// Results are cached per albumRatingKey.
func (p *Provider) Tracks(albumRatingKey string) ([]playlist.Track, error) {
	p.mu.Lock()
	if p.trackCache != nil {
		if cached, ok := p.trackCache[albumRatingKey]; ok {
			p.mu.Unlock()
			return cached, nil
		}
	}
	p.mu.Unlock()

	plexTracks, err := p.client.Tracks(albumRatingKey)
	if err != nil {
		return nil, err
	}

	tracks := p.convertTracks(plexTracks, 0)

	p.mu.Lock()
	if p.trackCache == nil {
		p.trackCache = make(map[string][]playlist.Track)
	}
	p.trackCache[albumRatingKey] = tracks
	p.mu.Unlock()

	return tracks, nil
}

// SearchTracks searches the Plex music library for tracks matching query.
// Implements provider.Searcher.
func (p *Provider) SearchTracks(_ context.Context, query string, limit int) ([]playlist.Track, error) {
	plexTracks, err := p.client.Search(query)
	if err != nil {
		return nil, err
	}
	return p.convertTracks(plexTracks, limit), nil
}

// convertTracks converts Plex tracks to playlist tracks, skipping entries
// without a streamable part. If limit > 0, at most limit tracks are returned.
func (p *Provider) convertTracks(plexTracks []Track, limit int) []playlist.Track {
	tracks := make([]playlist.Track, 0, len(plexTracks))
	for _, t := range plexTracks {
		if t.PartKey == "" {
			continue
		}
		tracks = append(tracks, playlist.Track{
			Path:         p.client.StreamURL(t.PartKey),
			Title:        t.Title,
			Artist:       t.ArtistName,
			Album:        t.AlbumName,
			Year:         t.Year,
			TrackNumber:  t.TrackNumber,
			DurationSecs: t.Duration / 1000,
			Stream:       true,
		})
		if limit > 0 && len(tracks) >= limit {
			break
		}
	}
	return tracks
}

// AlbumTracks returns the tracks for the given album (ratingKey).
// Implements provider.AlbumTrackLoader.
func (p *Provider) AlbumTracks(albumID string) ([]playlist.Track, error) {
	return p.Tracks(albumID)
}
