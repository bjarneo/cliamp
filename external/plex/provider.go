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
	// TrackURIPrefix is the token-free path stored in queues, history,
	// favorites, playlists, resume state, and IPC responses.
	TrackURIPrefix   = "plex:track:"
	prefixPlaylist   = "pl:"
	sectionAlbums    = "Albums"
	sectionPlaylists = "Playlists"
)

// Compile-time interface checks.
var (
	_ provider.Searcher         = (*Provider)(nil)
	_ provider.AlbumTrackLoader = (*Provider)(nil)
	_ provider.TrackFavoriter   = (*Provider)(nil)
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
	partKeys      map[string]string
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
// endpoint; album IDs load via the album children endpoint. Tracks with no
// ratingKey or streamable part are silently skipped. Track paths are token-free
// logical URIs; ResolveSource creates the authenticated URL only when playback
// starts. Album tracks
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
		if t.RatingKey == "" || t.PartKey == "" {
			continue
		}
		p.mu.Lock()
		if p.partKeys == nil {
			p.partKeys = make(map[string]string)
		}
		p.partKeys[t.RatingKey] = t.PartKey
		p.mu.Unlock()
		tracks = append(tracks, playlist.Track{
			Path:         TrackURIPrefix + t.RatingKey,
			Title:        t.Title,
			Artist:       t.ArtistName,
			Album:        t.AlbumName,
			Year:         t.Year,
			TrackNumber:  t.TrackNumber,
			DurationSecs: t.Duration / 1000,
			Stream:       true,
			ProviderMeta: map[string]string{provider.MetaPlexID: t.RatingKey},
		})
		if limit > 0 && len(tracks) >= limit {
			break
		}
	}
	return tracks
}

// ResolveSource turns a token-free Plex track URI into an authenticated
// stream URL at play time. A restored entry fetches its part metadata when
// the in-memory mapping is absent after a restart.
func (p *Provider) ResolveSource(uri string) (string, error) {
	id, ok := trackIDFromPath(uri)
	if !ok {
		return "", fmt.Errorf("plex: invalid track uri %q", uri)
	}
	p.mu.Lock()
	partKey := p.partKeys[id]
	p.mu.Unlock()
	if partKey == "" {
		track, err := p.client.Track(id)
		if err != nil {
			return "", fmt.Errorf("plex: resolve track %s: %w", id, err)
		}
		if track.PartKey == "" {
			return "", fmt.Errorf("plex: track %s has no streamable part", id)
		}
		partKey = track.PartKey
		p.mu.Lock()
		if p.partKeys == nil {
			p.partKeys = make(map[string]string)
		}
		p.partKeys[id] = partKey
		p.mu.Unlock()
	}
	return p.client.StreamURL(partKey), nil
}

func trackIDFromPath(path string) (string, bool) {
	id, ok := strings.CutPrefix(path, TrackURIPrefix)
	if !ok || !validRatingKey(id) {
		return "", false
	}
	return id, true
}

func validRatingKey(id string) bool {
	if id == "" {
		return false
	}
	for _, r := range id {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') &&
			(r < '0' || r > '9') && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

// CanFavoriteTrack reports whether track came from this Plex provider.
func (p *Provider) CanFavoriteTrack(track playlist.Track) bool {
	return plexTrackID(track) != ""
}

// SetTrackFavorite copies cliamp's local favorite state to Plex.
func (p *Provider) SetTrackFavorite(ctx context.Context, track playlist.Track, favorite bool) error {
	id := plexTrackID(track)
	if id == "" {
		return fmt.Errorf("plex: track has no rating key")
	}
	rating := float64(0)
	if favorite {
		rating = 10
	}
	return p.client.SetRating(ctx, id, rating)
}

func plexTrackID(track playlist.Track) string {
	if id := track.Meta(provider.MetaPlexID); id != "" {
		return id
	}
	id, _ := trackIDFromPath(track.Path)
	return id
}

// AlbumTracks returns the tracks for the given album (ratingKey).
// Implements provider.AlbumTrackLoader.
func (p *Provider) AlbumTracks(albumID string) ([]playlist.Track, error) {
	return p.Tracks(albumID)
}
