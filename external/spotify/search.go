package spotify

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// Compile-time interface checks.
var (
	_ provider.MultiSearcher = (*SpotifyProvider)(nil)
)

// searchPlaylist is the simplified playlist object returned by /v1/search.
// The track total lives under "tracks" (classic API) or "items" (post-episode
// rename); whichever is present wins.
type searchPlaylist struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Owner struct {
		ID string `json:"id"`
	} `json:"owner"`
	Tracks *struct {
		Total int `json:"total"`
	} `json:"tracks"`
	Items *struct {
		Total int `json:"total"`
	} `json:"items"`
}

func (sp searchPlaylist) trackCount() int {
	if sp.Tracks != nil {
		return sp.Tracks.Total
	}
	if sp.Items != nil {
		return sp.Items.Total
	}
	return 0
}

// SearchAll searches tracks, episodes, albums, artists, and playlists in one
// query. limit is the per-type result count, clamped to Spotify's accepted
// range of 1..10 (the server defaults to 5 when omitted). Episodes are merged
// into Tracks after music tracks with their spotify:episode: URI preserved
// (same as SearchTracks). Playlist rows leave Section empty (the UI's search
// tabs provide grouping) and set Owned when the owner is the current user
// (best-effort, like Playlists()).
// Implements provider.MultiSearcher.
func (p *SpotifyProvider) SearchAll(ctx context.Context, query string, limit int) (provider.SearchResults, error) {
	if err := p.ensureSession(); err != nil {
		return provider.SearchResults{}, err
	}

	if limit < 1 {
		limit = 1
	} else if limit > 10 {
		limit = 10
	}

	// No market parameter: with a user OAuth token Spotify implicitly scopes
	// results to the account's country (same as SearchTracks).
	q := url.Values{
		"q":     {query},
		"type":  {"track,album,artist,playlist,episode"},
		"limit": {strconv.Itoa(limit)},
	}

	resp, err := p.webAPI(ctx, "GET", "/v1/search", q)
	if err != nil {
		return provider.SearchResults{}, friendlySearchError(err)
	}

	var result struct {
		Tracks struct {
			Items []*spotifyItem `json:"items"`
		} `json:"tracks"`
		Episodes struct {
			Items []*spotifyItem `json:"items"`
		} `json:"episodes"`
		Albums struct {
			Items []spotifyAlbum `json:"items"`
		} `json:"albums"`
		Artists struct {
			Items []spotifyArtist `json:"items"`
		} `json:"artists"`
		Playlists struct {
			Items []searchPlaylist `json:"items"`
		} `json:"playlists"`
	}
	if err := decodeBody(resp, &result); err != nil {
		return provider.SearchResults{}, fmt.Errorf("spotify: parse search: %w", err)
	}

	var out provider.SearchResults
	for _, items := range [][]*spotifyItem{result.Tracks.Items, result.Episodes.Items} {
		for _, t := range items {
			if t == nil || t.ID == "" {
				continue // skip null/unavailable results
			}
			out.Tracks = append(out.Tracks, trackFromItem(t))
		}
	}
	for _, a := range result.Albums.Items {
		out.Albums = append(out.Albums, albumFromSpotify(a))
	}
	for _, a := range result.Artists.Items {
		out.Artists = append(out.Artists, provider.ArtistInfo{ID: a.ID, Name: a.Name})
	}
	userID := p.currentUserID(ctx)
	for _, pl := range result.Playlists.Items {
		out.Playlists = append(out.Playlists, playlist.PlaylistInfo{
			ID:         pl.ID,
			Name:       pl.Name,
			TrackCount: pl.trackCount(),
			Owned:      userID != "" && pl.Owner.ID == userID,
		})
	}
	return out, nil
}

// friendlySearchError rewrites Spotify's misleading 400 "Invalid limit" reply
// from /v1/search into something a user can act on. Since Nov 27, 2024 Spotify
// returns this error for developer apps registered in Development Mode — the
// rest of the API (playback, playlists, library) keeps working, but the
// catalog endpoints (/v1/search etc.) are blocked. The limit value is fine.
func friendlySearchError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if strings.Contains(msg, "400") && strings.Contains(msg, "Invalid limit") {
		return fmt.Errorf("spotify: search blocked — your client_id is too new. Spotify's Nov 27 2024 change blocks /v1/search for apps in Development Mode (the rest of cliamp still works on your app). Remove client_id from [spotify] in config.toml to use the built-in fallback for search, or apply for Extended Quota Mode")
	}
	return fmt.Errorf("spotify: search: %w", err)
}
