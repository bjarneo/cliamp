package spotify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// Compile-time interface checks.
var (
	_ provider.PlaylistBatchWriter   = (*SpotifyProvider)(nil)
	_ provider.TrackLiker            = (*SpotifyProvider)(nil)
	_ provider.PlaylistFollower      = (*SpotifyProvider)(nil)
	_ provider.ArtistFollower        = (*SpotifyProvider)(nil)
	_ provider.PlaylistTrackRemover  = (*SpotifyProvider)(nil)
	_ provider.RemotePlaylistRenamer = (*SpotifyProvider)(nil)
)

// maxAddTracksPerRequest is Spotify's cap on uris per POST to
// /v1/playlists/{id}/items.
const maxAddTracksPerRequest = 100

// spotifyTrackURI resolves the canonical spotify: URI for a track: the Path
// when it already is a track/episode URI, else one synthesized from the
// ProviderMeta spotify.id. Empty when neither resolves.
func spotifyTrackURI(t playlist.Track) string {
	if strings.HasPrefix(t.Path, "spotify:track:") || strings.HasPrefix(t.Path, "spotify:episode:") {
		return t.Path
	}
	if id := t.ProviderMeta[metaSpotifyID]; id != "" {
		return "spotify:track:" + id
	}
	return ""
}

// spotifyTrackID resolves a track's Spotify ID from its ProviderMeta or its
// spotify:track: URI. Empty when unresolvable (episodes use a different
// library endpoint and are not supported by TrackLiker).
func spotifyTrackID(t playlist.Track) string {
	if id := t.ProviderMeta[metaSpotifyID]; id != "" {
		return id
	}
	if id, ok := strings.CutPrefix(t.Path, "spotify:track:"); ok {
		return id
	}
	return ""
}

// AddTracksToPlaylist adds tracks to a playlist in one operation, POSTing at
// most maxAddTracksPerRequest uris per request. Tracks without a resolvable
// spotify:track:/spotify:episode: URI (or ProviderMeta spotify.id) are counted
// as skipped, not sent. Implements provider.PlaylistBatchWriter.
func (p *SpotifyProvider) AddTracksToPlaylist(ctx context.Context, playlistID string, tracks []playlist.Track) (added, skipped int, err error) {
	if err := p.ensureSession(); err != nil {
		return 0, 0, err
	}

	var uris []string
	for _, t := range tracks {
		if uri := spotifyTrackURI(t); uri != "" {
			uris = append(uris, uri)
		} else {
			skipped++
		}
	}

	path := fmt.Sprintf("/v1/playlists/%s/items", playlistID)
	for len(uris) > 0 {
		size := min(maxAddTracksPerRequest, len(uris))
		chunk := uris[:size]

		body, _ := json.Marshal(map[string]any{"uris": chunk})
		resp, err := p.webAPIWithBody(ctx, http.MethodPost, path, nil, bytes.NewReader(body), "application/json", http.StatusOK, http.StatusCreated)
		if err != nil {
			return added, skipped, fmt.Errorf("spotify: add tracks: %w", err)
		}
		var result struct {
			SnapshotID string `json:"snapshot_id"`
		}
		if err := decodeBody(resp, &result); err != nil {
			return added, skipped, fmt.Errorf("spotify: parse add tracks: %w", err)
		}

		added += len(chunk)
		uris = uris[size:]

		p.mu.Lock()
		// Track the new snapshot where a cache entry exists; the cached track
		// list is stale now and refetches on the next Tracks().
		if cached, ok := p.trackCache[playlistID]; ok {
			if result.SnapshotID != "" {
				cached.snapshotID = result.SnapshotID
				cached.tracks = nil
			} else {
				delete(p.trackCache, playlistID)
			}
		}
		p.listCache = nil
		p.mu.Unlock()
	}
	return added, skipped, nil
}

// ToggleTrackLike flips the track's saved state in the user's library and
// returns the new state. The current state is learned from
// /v1/me/library/contains, then PUT (save) or DELETE (remove) /v1/me/library
// applies the flip. Both calls address the item by its spotify:track: URI in
// the uris query parameter — the endpoints take no request body. Implements
// provider.TrackLiker.
func (p *SpotifyProvider) ToggleTrackLike(ctx context.Context, track playlist.Track) (bool, error) {
	if err := p.ensureSession(); err != nil {
		return false, err
	}

	id := spotifyTrackID(track)
	if id == "" {
		return false, fmt.Errorf("spotify: toggle like: track has no spotify track ID")
	}

	uri := "spotify:track:" + id
	resp, err := p.webAPI(ctx, http.MethodGet, "/v1/me/library/contains", url.Values{"uris": {uri}})
	if err != nil {
		return false, fmt.Errorf("spotify: check liked: %w", err)
	}
	var states []bool
	if err := decodeBody(resp, &states); err != nil {
		return false, fmt.Errorf("spotify: parse liked state: %w", err)
	}
	liked := len(states) > 0 && states[0]

	method := http.MethodPut // not saved yet: save it
	if liked {
		method = http.MethodDelete // saved: remove it
	}
	resp, err = p.webAPIWithBody(ctx, method, "/v1/me/library", url.Values{"uris": {uri}}, nil, "", http.StatusOK, http.StatusNoContent)
	if err != nil {
		return false, fmt.Errorf("spotify: toggle like: %w", err)
	}
	resp.Body.Close()

	// YOUR MUSIC rows are affected: the liked-songs track cache, the
	// recently-played library row cache, and the playlist list counts.
	p.mu.Lock()
	delete(p.trackCache, savedTracksPlaylistID)
	p.recentTracks = nil
	p.recentTracksAt = time.Time{}
	p.listCache = nil
	p.mu.Unlock()

	return !liked, nil
}

// FollowPlaylistByID follows a playlist. Implements provider.PlaylistFollower.
func (p *SpotifyProvider) FollowPlaylistByID(ctx context.Context, playlistID string) error {
	return p.setPlaylistFollowed(ctx, playlistID, true)
}

// UnfollowPlaylistByID unfollows a playlist. For playlists the user owns,
// Spotify deletes the playlist server-side.
func (p *SpotifyProvider) UnfollowPlaylistByID(ctx context.Context, playlistID string) error {
	return p.setPlaylistFollowed(ctx, playlistID, false)
}

// setPlaylistFollowed saves or removes the playlist in the user's library via
// PUT/DELETE /v1/me/library, addressed by its spotify:playlist: URI in the
// uris query parameter (no request body).
func (p *SpotifyProvider) setPlaylistFollowed(ctx context.Context, playlistID string, follow bool) error {
	if err := p.ensureSession(); err != nil {
		return err
	}

	method := http.MethodPut
	if !follow {
		method = http.MethodDelete
	}
	uri := "spotify:playlist:" + playlistID
	resp, err := p.webAPIWithBody(ctx, method, "/v1/me/library", url.Values{"uris": {uri}}, nil, "", http.StatusOK, http.StatusNoContent)
	if err != nil {
		return fmt.Errorf("spotify: %s playlist: %w", followWord(follow), err)
	}
	resp.Body.Close()

	p.mu.Lock()
	p.listCache = nil
	p.mu.Unlock()
	return nil
}

// FollowArtist follows an artist. Implements provider.ArtistFollower.
func (p *SpotifyProvider) FollowArtist(ctx context.Context, artistID string) error {
	return p.setArtistFollowed(ctx, artistID, true)
}

// UnfollowArtist unfollows an artist.
func (p *SpotifyProvider) UnfollowArtist(ctx context.Context, artistID string) error {
	return p.setArtistFollowed(ctx, artistID, false)
}

// setArtistFollowed follows or unfollows the artist via PUT/DELETE
// /v1/me/library, addressed by its spotify:artist: URI in the uris query
// parameter (no request body). NOTE: the save-library-items docs omit artist
// URIs from their supported list while library/contains includes them; if
// Spotify rejects artist URIs here this is the place to revisit.
func (p *SpotifyProvider) setArtistFollowed(ctx context.Context, artistID string, follow bool) error {
	if err := p.ensureSession(); err != nil {
		return err
	}

	method := http.MethodPut
	if !follow {
		method = http.MethodDelete
	}
	uri := "spotify:artist:" + artistID
	resp, err := p.webAPIWithBody(ctx, method, "/v1/me/library", url.Values{"uris": {uri}}, nil, "", http.StatusOK, http.StatusNoContent)
	if err != nil {
		return fmt.Errorf("spotify: %s artist: %w", followWord(follow), err)
	}
	resp.Body.Close()
	return nil
}

func followWord(follow bool) string {
	if follow {
		return "follow"
	}
	return "unfollow"
}

// RemoveTrackFromPlaylist removes every occurrence of the track's URI from a
// playlist. The URI is resolved from the track itself — not from position —
// because the caller's position indexes a filtered (playable-only) view while
// the API's positions count raw items; sending a mismatched position could
// target the wrong item. Implements provider.PlaylistTrackRemover.
func (p *SpotifyProvider) RemoveTrackFromPlaylist(ctx context.Context, playlistID string, position int, track playlist.Track) error {
	if err := p.ensureSession(); err != nil {
		return err
	}
	if position < 0 {
		return fmt.Errorf("spotify: remove track: invalid position %d", position)
	}

	uri := spotifyTrackURI(track)
	if uri == "" {
		return fmt.Errorf("spotify: remove track: track has no spotify URI")
	}

	body, _ := json.Marshal(map[string]any{
		"items": []map[string]any{{"uri": uri}},
	})
	path := fmt.Sprintf("/v1/playlists/%s/items", playlistID)
	resp, err := p.webAPIWithBody(ctx, http.MethodDelete, path, nil, bytes.NewReader(body), "application/json", http.StatusOK)
	if err != nil {
		return fmt.Errorf("spotify: remove track: %w", err)
	}
	resp.Body.Close()

	// The snapshot changed: the cached track list (and the list's track
	// counts) are stale.
	p.mu.Lock()
	delete(p.trackCache, playlistID)
	p.listCache = nil
	p.mu.Unlock()
	return nil
}

// RenamePlaylistByID renames a playlist by ID. Implements
// provider.RemotePlaylistRenamer.
func (p *SpotifyProvider) RenamePlaylistByID(ctx context.Context, playlistID, newName string) error {
	if err := p.ensureSession(); err != nil {
		return err
	}

	body, _ := json.Marshal(map[string]any{"name": newName})
	path := fmt.Sprintf("/v1/playlists/%s", playlistID)
	resp, err := p.webAPIWithBody(ctx, http.MethodPut, path, nil, bytes.NewReader(body), "application/json", http.StatusOK)
	if err != nil {
		return fmt.Errorf("spotify: rename playlist: %w", err)
	}
	resp.Body.Close()

	p.mu.Lock()
	p.listCache = nil
	p.mu.Unlock()
	return nil
}
