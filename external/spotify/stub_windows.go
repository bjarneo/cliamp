//go:build windows

// stub_windows.go provides a no-op Spotify implementation on Windows
// where go-librespot (CGO: FLAC, Vorbis, ALSA) cannot compile.

package spotify

import (
	"context"
	"errors"
	"time"

	"github.com/gopxl/beep/v2"

	"cliamp/playlist"
)

var errSpotifyUnavailable = errors.New("spotify: unavailable on Windows (go-librespot requires CGO)")

// Session is a no-op on Windows.
type Session struct{}

// SpotifyProvider is a no-op on Windows.
type SpotifyProvider struct{}

// New returns nil — Spotify is disabled on Windows because
// go-librespot requires CGO (FLAC, Vorbis, ALSA) which cannot
// cross-compile. Callers must nil-check the return value.
func New(_ *Session, _ string) *SpotifyProvider { return nil }

// Close is a no-op.
func (p *SpotifyProvider) Close() {}

// Name returns the provider name.
func (p *SpotifyProvider) Name() string { return "Spotify" }

// Playlists returns nil — Spotify is unavailable on Windows.
func (p *SpotifyProvider) Playlists() ([]playlist.PlaylistInfo, error) { return nil, nil }

// Tracks returns nil — Spotify is unavailable on Windows.
func (p *SpotifyProvider) Tracks(_ string) ([]playlist.Track, error) { return nil, nil }

// Authenticate is a no-op.
func (p *SpotifyProvider) Authenticate() error { return nil }

// URISchemes returns the URI prefixes handled by this provider.
func (p *SpotifyProvider) URISchemes() []string { return []string{"spotify:"} }

// NewStreamer returns an error — Spotify streaming is unavailable on Windows.
func (p *SpotifyProvider) NewStreamer(_ string) (beep.StreamSeekCloser, beep.Format, time.Duration, error) {
	return nil, beep.Format{}, 0, errSpotifyUnavailable
}

// SearchTracks is a no-op on Windows.
func (p *SpotifyProvider) SearchTracks(_ context.Context, _ string, _ int) ([]playlist.Track, error) {
	return nil, nil
}

// AddTrackToPlaylist is a no-op on Windows.
func (p *SpotifyProvider) AddTrackToPlaylist(_ context.Context, _ string, _ playlist.Track) error {
	return nil
}

// CreatePlaylist is a no-op on Windows.
func (p *SpotifyProvider) CreatePlaylist(_ context.Context, _ string) (string, error) {
	return "", nil
}
