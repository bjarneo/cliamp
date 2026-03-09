//go:build windows

// stub_windows.go provides a no-op Spotify implementation on Windows
// where go-librespot (CGO: FLAC, Vorbis, ALSA) cannot compile.

package spotify

import (
	"time"

	"github.com/gopxl/beep/v2"

	"cliamp/playlist"
)

// Session is a no-op on Windows.
type Session struct{}

// SpotifyProvider is a no-op on Windows.
type SpotifyProvider struct{}

// New returns a no-op provider stub.
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

// NewStreamer is a no-op.
func (p *SpotifyProvider) NewStreamer(_ string) (beep.StreamSeekCloser, beep.Format, time.Duration, error) {
	return nil, beep.Format{}, 0, nil
}
