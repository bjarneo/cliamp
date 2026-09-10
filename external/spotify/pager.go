//go:build !windows

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

// maxTracksPage caps the limit accepted by TracksPage.
const maxTracksPage = 200

// pagerPos remembers where a TracksPage sequence left off for one playlist:
// the number of playable tracks served so far and the API position that
// follows them. Filtered-out items (local/unavailable tracks) make the two
// diverge, so the UI's "tracks served" offset cannot be used as an API offset.
type pagerPos struct {
	servedOffset int
	nextAPIPos   int
}

// libraryRowCacheTTL bounds the top-tracks and recently-played caches.
const libraryRowCacheTTL = 60 * time.Second

// topTracksCap bounds how many top tracks are fetched (4 pages of 50).
const topTracksCap = 200

// playlistItemsFields is the field projection used for playlist item pages;
// it keeps responses small and matches what trackFromItem consumes.
const playlistItemsFields = "items(item(id,name,type,uri,artists(name),album(name,release_date),show(name),release_date,duration_ms,track_number,is_playable,restrictions(reason))),total"

// TracksPage returns one page of tracks for a playlist ID, "YOUR MUSIC",
// "TOP TRACKS", or "RECENTLY PLAYED", plus the total track count.
// limit is clamped to [1, 200]; an offset beyond the end yields an empty page.
// Implements provider.TrackPager.
func (p *SpotifyProvider) TracksPage(id string, offset, limit int) ([]playlist.Track, int, error) {
	if err := p.ensureSession(); err != nil {
		return nil, 0, err
	}

	if limit < 1 {
		limit = 1
	} else if limit > maxTracksPage {
		limit = maxTracksPage
	}
	if offset < 0 {
		offset = 0
	}

	switch id {
	case topTracksID:
		tracks, err := p.cachedTopTracks()
		if err != nil {
			return nil, 0, err
		}
		return windowTracks(tracks, offset, limit), len(tracks), nil
	case recentlyPlayedID:
		tracks, err := p.cachedRecentlyPlayed()
		if err != nil {
			return nil, 0, err
		}
		return windowTracks(tracks, offset, limit), len(tracks), nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), webAPITimeout)
	defer cancel()

	// Callers pass the number of playable tracks served so far as offset, but
	// the API position may sit further along (filtered items). Continue from
	// the remembered API position when the request continues the served
	// sequence; otherwise treat offset as an API position as before.
	apiOffset := offset
	p.mu.Lock()
	if pos, ok := p.pagerCur[id]; ok && offset == pos.servedOffset {
		apiOffset = pos.nextAPIPos
	}
	p.mu.Unlock()

	tracks, total, next, err := p.fetchTracksPage(ctx, id, apiOffset, limit)
	if err != nil {
		return nil, 0, err
	}
	p.mu.Lock()
	p.pagerCur[id] = pagerPos{servedOffset: offset + len(tracks), nextAPIPos: next}
	p.mu.Unlock()
	return tracks, total, nil
}

// fetchTracksPage collects up to limit playable tracks starting at API
// position offset, requesting at most spotifyTrackPageSize items per call.
// It returns the tracks, the reported total, and the next unfetched API
// position (needed because filtered-out items make the reader fetch past
// offset+size). Handles "YOUR MUSIC" and real playlist IDs.
func (p *SpotifyProvider) fetchTracksPage(ctx context.Context, id string, offset, limit int) ([]playlist.Track, int, int, error) {
	var collected []playlist.Track
	total := 0
	pos := offset

	for len(collected) < limit {
		size := min(spotifyTrackPageSize, limit-len(collected))
		query := url.Values{
			"limit":  {strconv.Itoa(size)},
			"offset": {strconv.Itoa(pos)},
		}
		path := "/v1/me/tracks"
		if id != yourMusicID {
			path = fmt.Sprintf("/v1/playlists/%s/items", id)
			query.Set("fields", playlistItemsFields)
		}

		resp, err := p.webAPI(ctx, "GET", path, query)
		if err != nil {
			return nil, 0, 0, fmt.Errorf("spotify: list tracks: %w", err)
		}
		var result struct {
			Items []struct {
				Item  *spotifyItem `json:"item"`
				Track *spotifyItem `json:"track"`
			} `json:"items"`
			Total int `json:"total"`
		}
		if err := decodeBody(resp, &result); err != nil {
			return nil, 0, 0, fmt.Errorf("spotify: parse tracks: %w", err)
		}

		total = result.Total
		for _, item := range result.Items {
			t := item.Item
			if t == nil {
				t = item.Track
			}
			if t == nil || t.ID == "" {
				continue // skip local/unavailable tracks
			}
			collected = append(collected, trackFromItem(t))
		}

		pos += len(result.Items)
		if len(result.Items) == 0 || pos >= result.Total {
			break
		}
	}
	return collected, total, pos, nil
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
