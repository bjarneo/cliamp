package jellyfin

import (
	"fmt"
	"sync"
	"time"

	"cliamp/config"
	"cliamp/playlist"
	"cliamp/provider"
)

var (
	_ provider.ArtistBrowser    = (*Provider)(nil)
	_ provider.AlbumBrowser     = (*Provider)(nil)
	_ provider.AlbumTrackLoader = (*Provider)(nil)
	_ provider.PlaybackReporter = (*Provider)(nil)
)

// Provider implements playlist.Provider for a Jellyfin server.
// Playlists() returns albums across all music views.
// Tracks() returns the tracks for a given album item.
type Provider struct {
	client        *Client
	mu            sync.Mutex
	playlistCache []playlist.PlaylistInfo
	trackCache    map[string][]playlist.Track
}

func newProvider(client *Client) *Provider {
	return &Provider{client: client}
}

// NewFromConfig returns a Provider from a JellyfinConfig, or nil if URL or token is missing.
func NewFromConfig(cfg config.JellyfinConfig) *Provider {
	if !cfg.IsSet() {
		return nil
	}
	return newProvider(NewClient(cfg.URL, cfg.Token, cfg.UserID, cfg.User, cfg.Password))
}

// Name returns the display name used in the provider selector.
func (p *Provider) Name() string { return "Jellyfin" }

func (p *Provider) Artists() ([]provider.ArtistInfo, error) {
	return p.client.Artists()
}

func (p *Provider) ArtistAlbums(artistID string) ([]provider.AlbumInfo, error) {
	return p.client.ArtistAlbums(artistID)
}

func (p *Provider) AlbumList(sortType string, offset, size int) ([]provider.AlbumInfo, error) {
	return p.client.AlbumList(sortType, offset, size)
}

func (p *Provider) AlbumSortTypes() []provider.SortType {
	return p.client.AlbumSortTypes()
}

func (p *Provider) DefaultAlbumSort() string {
	return p.client.DefaultAlbumSort()
}

func (p *Provider) AlbumTracks(albumID string) ([]playlist.Track, error) {
	return p.Tracks(albumID)
}

func (p *Provider) CanReportPlayback(track playlist.Track) bool {
	return track.Meta(provider.MetaJellyfinID) != ""
}

func (p *Provider) ReportNowPlaying(track playlist.Track, position time.Duration, canSeek bool) {
	_ = p.client.ReportNowPlaying(track, position, canSeek)
}

func (p *Provider) ReportScrobble(track playlist.Track, elapsed, _ time.Duration, canSeek bool) {
	_ = p.client.ReportScrobble(track, elapsed, canSeek)
}

// Playlists returns all albums across all Jellyfin music views.
// Results are cached after the first successful call.
func (p *Provider) Playlists() ([]playlist.PlaylistInfo, error) {
	p.mu.Lock()
	if p.playlistCache != nil {
		cached := p.playlistCache
		p.mu.Unlock()
		return cached, nil
	}
	p.mu.Unlock()

	albums, err := p.client.Albums()
	if err != nil {
		return nil, err
	}

	out := make([]playlist.PlaylistInfo, 0, len(albums))
	for _, a := range albums {
		name := a.Name
		if a.Artist != "" {
			name = a.Artist + " — " + a.Name
		}
		if a.Year > 0 {
			name = fmt.Sprintf("%s (%d)", name, a.Year)
		}
		out = append(out, playlist.PlaylistInfo{
			ID:         a.ID,
			Name:       name,
			TrackCount: a.TrackCount,
		})
	}

	p.mu.Lock()
	p.playlistCache = out
	p.mu.Unlock()

	return out, nil
}

// Tracks returns the tracks for one album item.
// Results are cached per album id.
func (p *Provider) Tracks(albumID string) ([]playlist.Track, error) {
	p.mu.Lock()
	if p.trackCache != nil {
		if cached, ok := p.trackCache[albumID]; ok {
			p.mu.Unlock()
			return cached, nil
		}
	}
	p.mu.Unlock()

	jfTracks, err := p.client.Tracks(albumID)
	if err != nil {
		return nil, err
	}

	out := make([]playlist.Track, 0, len(jfTracks))
	for _, t := range jfTracks {
		out = append(out, playlist.Track{
			Path:         p.client.StreamURL(t.ID),
			Title:        t.Name,
			Artist:       t.Artist,
			Album:        t.Album,
			Year:         t.Year,
			TrackNumber:  t.TrackNumber,
			DurationSecs: t.DurationSecs,
			Stream:       true,
			ProviderMeta: map[string]string{provider.MetaJellyfinID: t.ID},
		})
	}

	p.mu.Lock()
	if p.trackCache == nil {
		p.trackCache = make(map[string][]playlist.Track)
	}
	p.trackCache[albumID] = out
	p.mu.Unlock()

	return out, nil
}
