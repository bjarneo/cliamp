package plex

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/bjarneo/cliamp/config"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// ID prefixes and section labels used to group Plex playlists and albums in
// the browse view. Playlist IDs are prefixed so they can be told apart from
// album ratingKeys (both live in the same server-side metadata key space).
const (
	prefixPlaylist   = "pl:"
	sectionAlbums    = "Albums"
	sectionPlaylists = "Playlists"
)

// Compile-time interface checks.
var (
	_ provider.Searcher         = (*Provider)(nil)
	_ provider.AlbumTrackLoader = (*Provider)(nil)
)

// Provider implements playlist.Provider for a Plex Media Server.
// Playlists() returns server audio playlists (Section "Playlists") followed
// by albums from all music library sections (Section "Albums").
// Tracks() returns the tracks for a playlist or album ratingKey; playlist IDs
// are distinguished by the "pl:" prefix.
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
	return newProvider(NewClient(cfg.URL, cfg.Token, cfg.Libraries...))
}

// Name returns the display name used in the provider selector.
func (p *Provider) Name() string { return "Plex" }

// Playlists returns the server's audio playlists first, then all albums
// across all Plex music library sections, each tagged with its section so the
// browse view renders them under headers. Results are cached after the first
// successful call.
func (p *Provider) Playlists() ([]playlist.PlaylistInfo, error) {
	p.mu.Lock()
	if p.playlistCache != nil {
		cached := slices.Clone(p.playlistCache)
		p.mu.Unlock()
		return cached, nil
	}
	p.mu.Unlock()

	var lists []playlist.PlaylistInfo

	serverPlaylists, err := p.client.Playlists()
	if err != nil {
		return nil, fmt.Errorf("plex: list playlists: %w", err)
	}
	for _, pl := range serverPlaylists {
		lists = append(lists, playlist.PlaylistInfo{
			ID:           prefixPlaylist + pl.RatingKey,
			Name:         pl.Title,
			TrackCount:   pl.TrackCount,
			DurationSecs: pl.DurationSecs,
			Section:      sectionPlaylists,
		})
	}

	sections, err := p.client.MusicSections()
	if err != nil {
		return nil, err
	}
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
				Section:    sectionAlbums,
			})
		}
	}

	if len(lists) == 0 {
		return nil, fmt.Errorf("plex: no matching music libraries or audio playlists found on this server")
	}

	p.mu.Lock()
	p.playlistCache = lists
	p.mu.Unlock()

	return slices.Clone(lists), nil
}

// Refresh clears cached playlist and track data so the next call re-fetches
// from the server. Implements playlist.Refresher.
func (p *Provider) Refresh() {
	p.mu.Lock()
	p.playlistCache = nil
	p.trackCache = nil
	p.mu.Unlock()
}

// Tracks returns the tracks for the playlist or album identified by
// playlistID. Playlist IDs (prefixed "pl:") load via the server playlists
// endpoint; album IDs load via the album children endpoint. Each track's Path
// is a complete authenticated HTTP URL ready for the player. Tracks with no
// streamable part (missing Media/Part data) are silently skipped. Album tracks
// are cached per album ID; playlist tracks are intentionally not cached so
// smart playlists always reflect their current server contents.
func (p *Provider) Tracks(playlistID string) ([]playlist.Track, error) {
	isPlaylist := strings.HasPrefix(playlistID, prefixPlaylist)

	p.mu.Lock()
	if !isPlaylist && p.trackCache != nil {
		if cached, ok := p.trackCache[playlistID]; ok {
			p.mu.Unlock()
			return slices.Clone(cached), nil
		}
	}
	p.mu.Unlock()

	var (
		plexTracks []Track
		err        error
	)
	if isPlaylist {
		plexTracks, err = p.client.PlaylistTracks(strings.TrimPrefix(playlistID, prefixPlaylist))
	} else {
		plexTracks, err = p.client.Tracks(playlistID)
	}
	if err != nil {
		return nil, err
	}

	tracks := p.convertTracks(plexTracks, 0)

	if !isPlaylist {
		p.mu.Lock()
		if p.trackCache == nil {
			p.trackCache = make(map[string][]playlist.Track)
		}
		p.trackCache[playlistID] = tracks
		p.mu.Unlock()
	}

	return slices.Clone(tracks), nil
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
