package spotify

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/bjarneo/cliamp/playlist"
)

// libraryRowCacheTTL bounds the top-tracks and recently-played caches.
const libraryRowCacheTTL = 60 * time.Second

// topTracksCap bounds how many top tracks are fetched (4 pages of 50).
const topTracksCap = 200

// isLibraryRowID reports whether id is a synthetic library row backed by a
// playback-history endpoint rather than a playlist or saved-album ID.
func isLibraryRowID(id string) bool {
	return id == topTracksID || id == recentlyPlayedID
}

// libraryRowPage serves one page of a synthetic library row in the
// provider.TrackPager protocol: the [offset, offset+spotifyTrackPageSize)
// window plus the next offset to request, or 0 once past the end. The rows
// are small cached wholes, so paging slices the cache.
func (p *SpotifyProvider) libraryRowPage(id string, offset int) ([]playlist.Track, int, error) {
	tracks, err := p.libraryRowTracks(id)
	if err != nil {
		return nil, 0, err
	}
	next := offset + spotifyTrackPageSize
	if next >= len(tracks) {
		next = 0
	}
	return windowTracks(tracks, offset, spotifyTrackPageSize), next, nil
}

// libraryRowTracks returns the full track list behind a synthetic library row.
func (p *SpotifyProvider) libraryRowTracks(id string) ([]playlist.Track, error) {
	switch id {
	case topTracksID:
		return p.cachedTopTracks()
	case recentlyPlayedID:
		return p.cachedRecentlyPlayed()
	}
	return nil, fmt.Errorf("spotify: unknown library row %q", id)
}

// windowTracks returns the [offset, offset+limit) slice of tracks.
func windowTracks(tracks []playlist.Track, offset, limit int) []playlist.Track {
	if offset >= len(tracks) {
		return nil
	}
	end := min(offset+limit, len(tracks))
	return slices.Clone(tracks[offset:end])
}

// cachedTopTracks returns the user's short-term top tracks (capped at
// topTracksCap), cached for libraryRowCacheTTL.
func (p *SpotifyProvider) cachedTopTracks() ([]playlist.Track, error) {
	if tracks, ok := p.libraryRowCache(&p.topTracks, &p.topTracksAt); ok {
		return tracks, nil
	}

	if err := p.ensureSession(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), webAPITimeout)
	defer cancel()

	var all []playlist.Track
	offset := 0
	for len(all) < topTracksCap {
		size := min(spotifyTrackPageSize, topTracksCap-len(all))
		query := url.Values{
			"time_range": {"short_term"},
			"limit":      {strconv.Itoa(size)},
			"offset":     {strconv.Itoa(offset)},
		}
		resp, err := p.webAPI(ctx, "GET", "/v1/me/top/tracks", query)
		if err != nil {
			return nil, fmt.Errorf("spotify: top tracks: %w", err)
		}
		var result struct {
			Items []*spotifyItem `json:"items"`
			Total int            `json:"total"`
		}
		if err := decodeBody(resp, &result); err != nil {
			return nil, fmt.Errorf("spotify: parse top tracks: %w", err)
		}

		for _, t := range result.Items {
			if t == nil || t.ID == "" {
				continue
			}
			all = append(all, trackFromItem(t))
		}

		offset += len(result.Items)
		if len(result.Items) == 0 || offset >= result.Total {
			break
		}
	}

	p.mu.Lock()
	p.topTracks = all
	p.topTracksAt = time.Now()
	p.mu.Unlock()
	return slices.Clone(all), nil
}

// cachedRecentlyPlayed returns the most recent plays, deduped by URI with the
// newest play kept, from a single 50-item page cached for libraryRowCacheTTL.
func (p *SpotifyProvider) cachedRecentlyPlayed() ([]playlist.Track, error) {
	if tracks, ok := p.libraryRowCache(&p.recentTracks, &p.recentTracksAt); ok {
		return tracks, nil
	}

	if err := p.ensureSession(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), webAPITimeout)
	defer cancel()

	all, err := p.fetchRecentlyPlayedPage(ctx)
	if err != nil {
		return nil, err
	}

	p.mu.Lock()
	p.recentTracks = all
	p.recentTracksAt = time.Now()
	p.mu.Unlock()
	return slices.Clone(all), nil
}

// fetchRecentlyPlayedPage fetches one 50-item recently-played page and returns
// its playable tracks deduped by itemKey (the newest play is kept).
func (p *SpotifyProvider) fetchRecentlyPlayedPage(ctx context.Context) ([]playlist.Track, error) {
	query := url.Values{"limit": {strconv.Itoa(spotifyTrackPageSize)}}
	resp, err := p.webAPI(ctx, "GET", "/v1/me/player/recently-played", query)
	if err != nil {
		return nil, fmt.Errorf("spotify: recently played: %w", err)
	}
	var result struct {
		Items []struct {
			Track *spotifyItem `json:"track"`
		} `json:"items"`
	}
	if err := decodeBody(resp, &result); err != nil {
		return nil, fmt.Errorf("spotify: parse recently played: %w", err)
	}

	seen := make(map[string]bool, len(result.Items))
	var all []playlist.Track
	for _, item := range result.Items {
		if item.Track == nil || item.Track.ID == "" {
			continue
		}
		key := itemKey(item.Track)
		if seen[key] {
			continue // same track played again: keep the newest play only
		}
		seen[key] = true
		all = append(all, trackFromItem(item.Track))
	}
	return all, nil
}

// libraryRowCache returns a cached track slice when still fresh. tracks and
// at point at the provider fields to consult; p.mu guards them.
func (p *SpotifyProvider) libraryRowCache(tracks *[]playlist.Track, at *time.Time) ([]playlist.Track, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !at.IsZero() && time.Since(*at) < libraryRowCacheTTL {
		return slices.Clone(*tracks), true
	}
	return nil, false
}

// probeTopTracksCount reports the user's top-tracks total. ok is false when
// the endpoint is unavailable; Playlists() then omits the Library row.
func (p *SpotifyProvider) probeTopTracksCount(ctx context.Context) (count int, ok bool) {
	if err := p.ensureSession(); err != nil {
		return 0, false
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	query := url.Values{"time_range": {"short_term"}, "limit": {"1"}}
	resp, err := p.webAPI(ctx, "GET", "/v1/me/top/tracks", query)
	if err != nil {
		return 0, false
	}
	var result struct {
		Total int `json:"total"`
	}
	if err := decodeBody(resp, &result); err != nil {
		return 0, false
	}
	return result.Total, true
}

// probeRecentlyPlayedCount reports how many distinct tracks the most recent
// plays contain (the count the Recently Played row shows). ok is false when
// the endpoint is unavailable; Playlists() then omits the Library row.
func (p *SpotifyProvider) probeRecentlyPlayedCount(ctx context.Context) (count int, ok bool) {
	if err := p.ensureSession(); err != nil {
		return 0, false
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	tracks, err := p.fetchRecentlyPlayedPage(ctx)
	if err != nil {
		return 0, false
	}
	return len(tracks), true
}
