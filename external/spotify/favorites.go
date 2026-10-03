package spotify

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

var _ provider.TrackFavoriter = (*SpotifyProvider)(nil)

// libraryPath saves and removes items in the user's library. It replaces the
// deprecated PUT and DELETE /v1/me/tracks endpoints. It needs the
// user-library-modify scope.
// See: https://developer.spotify.com/documentation/web-api/reference/save-library-items
const libraryPath = "/v1/me/library"

// trackURIPrefix marks the Spotify tracks that Liked Songs can hold.
const trackURIPrefix = "spotify:track:"

// CanFavoriteTrack reports whether track is a Spotify track.
// Implements provider.TrackFavoriter.
func (p *SpotifyProvider) CanFavoriteTrack(track playlist.Track) bool {
	return strings.HasPrefix(track.Path, trackURIPrefix)
}

// SetTrackFavorite saves track to Liked Songs or removes it.
// Implements provider.TrackFavoriter.
func (p *SpotifyProvider) SetTrackFavorite(ctx context.Context, track playlist.Track, favorite bool) error {
	if !p.CanFavoriteTrack(track) {
		return fmt.Errorf("spotify: %q is not a Spotify track", track.Path)
	}
	if err := p.ensureSession(); err != nil {
		return err
	}
	method := http.MethodPut
	if !favorite {
		method = http.MethodDelete
	}
	resp, err := p.webAPIWithRetry(ctx, method, libraryPath, url.Values{"uris": {track.Path}}, nil, "", http.StatusOK, http.StatusNoContent)
	if err != nil {
		return fmt.Errorf("spotify: update liked songs: %w", err)
	}
	resp.Body.Close()

	// Liked Songs changed, so its cached tracks and count are stale.
	p.mu.Lock()
	delete(p.trackCache, savedTracksPlaylistID)
	p.listCache = nil
	p.mu.Unlock()
	return nil
}
