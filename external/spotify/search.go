//go:build !windows

package spotify

import (
	"context"
	"fmt"
	"net/url"
	"strconv"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// Compile-time interface checks.
var (
	_ provider.MultiSearcher         = (*SpotifyProvider)(nil)
	_ provider.ArtistTopTracksLoader = (*SpotifyProvider)(nil)
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
// range of 1..50. Episodes are merged into Tracks after music tracks with
// their spotify:episode: URI preserved (same as SearchTracks). Playlist rows
// leave Section empty (the UI's search tabs provide grouping) and set Owned
// when the owner is the current user (best-effort, like Playlists()).
// Implements provider.MultiSearcher.
func (p *SpotifyProvider) SearchAll(ctx context.Context, query string, limit int) (provider.SearchResults, error) {
	if err := p.ensureSession(); err != nil {
		return provider.SearchResults{}, err
	}

	if limit < 1 {
		limit = 1
	} else if limit > 50 {
		limit = 50
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

// ArtistTopTracks returns an artist's most popular tracks. Like the other
// catalog calls here, no market parameter is sent: with a user OAuth token
// Spotify scopes results to the account's country.
// Implements provider.ArtistTopTracksLoader.
func (p *SpotifyProvider) ArtistTopTracks(artistID string) ([]playlist.Track, error) {
	if err := p.ensureSession(); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), webAPITimeout)
	defer cancel()

	resp, err := p.webAPI(ctx, "GET", fmt.Sprintf("/v1/artists/%s/top-tracks", artistID), nil)
	if err != nil {
		return nil, fmt.Errorf("spotify: artist top tracks: %w", err)
	}
	var result struct {
		Tracks []*spotifyItem `json:"tracks"`
	}
	if err := decodeBody(resp, &result); err != nil {
		return nil, fmt.Errorf("spotify: parse artist top tracks: %w", err)
	}

	var tracks []playlist.Track
	for _, t := range result.Tracks {
		if t == nil || t.ID == "" {
			continue // skip unavailable results
		}
		tracks = append(tracks, trackFromItem(t))
	}
	return tracks, nil
}
