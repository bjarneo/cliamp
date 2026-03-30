package spotify

import (
	"context"
	"errors"
	"reflect"
	"testing"

	librespot "github.com/devgianlu/go-librespot"
	"github.com/devgianlu/go-librespot/audio"
	librespotPlayer "github.com/devgianlu/go-librespot/player"
)

type stubAudioSource struct{}

func (stubAudioSource) SetPositionMs(int64) error { return nil }
func (stubAudioSource) PositionMs() int64         { return 0 }
func (stubAudioSource) Read([]float32) (int, error) {
	return 0, nil
}

func successfulTestStream() *librespotPlayer.Stream {
	return &librespotPlayer.Stream{Source: stubAudioSource{}}
}

func assertEvents(t *testing.T, got []string, want ...string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
}

func TestPlaylistAccessible(t *testing.T) {
	const me = "user123"

	tests := []struct {
		name          string
		ownerID       string
		collaborative bool
		userID        string
		want          bool
	}{
		{"own playlist", me, false, me, true},
		{"own collaborative", me, true, me, true},
		{"other user's playlist", "otheruser", false, me, false},
		{"other user's collaborative", "otheruser", true, me, true},
		{"no userID fallback", "otheruser", false, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item := spotifyPlaylistItem{
				ID:            "pl1",
				Name:          "Test",
				Collaborative: tt.collaborative,
			}
			item.Owner.ID = tt.ownerID

			got := playlistAccessible(item, tt.userID)
			if got != tt.want {
				t.Errorf("playlistAccessible(owner=%q, collaborative=%v, userID=%q) = %v, want %v",
					tt.ownerID, tt.collaborative, tt.userID, got, tt.want)
			}
		})
	}
}

func TestNewStreamerFallsBackToInteractiveAfterSilentReconnectFailure(t *testing.T) {
	oldOpenStream := openSpotifyStream
	oldReconnect := reconnectSpotifySession
	oldReconnectInteractive := reconnectSpotifySessionInteractive
	t.Cleanup(func() {
		openSpotifyStream = oldOpenStream
		reconnectSpotifySession = oldReconnect
		reconnectSpotifySessionInteractive = oldReconnectInteractive
	})

	streamCalls := 0
	silentReconnectCalls := 0
	interactiveReconnectCalls := 0
	var events []string
	openSpotifyStream = func(*Session, context.Context, librespot.SpotifyId, int) (*librespotPlayer.Stream, error) {
		events = append(events, "stream")
		streamCalls++
		if streamCalls == 1 {
			return nil, &audio.KeyProviderError{Code: 42}
		}
		return successfulTestStream(), nil
	}
	reconnectSpotifySession = func(*Session, context.Context) error {
		events = append(events, "silent-reconnect")
		silentReconnectCalls++
		return errors.New("stored credentials expired")
	}
	reconnectSpotifySessionInteractive = func(*Session, context.Context) error {
		events = append(events, "interactive-reconnect")
		interactiveReconnectCalls++
		return nil
	}

	p := &SpotifyProvider{session: &Session{}}
	streamer, _, _, err := p.NewStreamer("spotify:track:1234567890123456789012")
	if err != nil {
		t.Fatalf("NewStreamer() error = %v", err)
	}
	if streamer == nil {
		t.Fatal("NewStreamer() streamer = nil, want non-nil")
	}
	if streamCalls != 2 {
		t.Fatalf("stream calls = %d, want 2", streamCalls)
	}
	if silentReconnectCalls != 1 {
		t.Fatalf("silent reconnect calls = %d, want 1", silentReconnectCalls)
	}
	if interactiveReconnectCalls != 1 {
		t.Fatalf("interactive reconnect calls = %d, want 1", interactiveReconnectCalls)
	}
	assertEvents(t, events, "stream", "silent-reconnect", "interactive-reconnect", "stream")
}

func TestNewStreamerSilentReconnectSuccessReturnsStreamerWithoutInteractiveAuth(t *testing.T) {
	oldOpenStream := openSpotifyStream
	oldReconnect := reconnectSpotifySession
	oldReconnectInteractive := reconnectSpotifySessionInteractive
	t.Cleanup(func() {
		openSpotifyStream = oldOpenStream
		reconnectSpotifySession = oldReconnect
		reconnectSpotifySessionInteractive = oldReconnectInteractive
	})

	streamCalls := 0
	silentReconnectCalls := 0
	interactiveReconnectCalls := 0
	var events []string
	openSpotifyStream = func(*Session, context.Context, librespot.SpotifyId, int) (*librespotPlayer.Stream, error) {
		events = append(events, "stream")
		streamCalls++
		if streamCalls == 1 {
			return nil, &audio.KeyProviderError{Code: 7}
		}
		return successfulTestStream(), nil
	}
	reconnectSpotifySession = func(*Session, context.Context) error {
		events = append(events, "silent-reconnect")
		silentReconnectCalls++
		return nil
	}
	reconnectSpotifySessionInteractive = func(*Session, context.Context) error {
		events = append(events, "interactive-reconnect")
		interactiveReconnectCalls++
		return nil
	}

	p := &SpotifyProvider{session: &Session{}}
	streamer, format, duration, err := p.NewStreamer("spotify:track:1234567890123456789012")
	if err != nil {
		t.Fatalf("NewStreamer() error = %v", err)
	}
	if streamer == nil {
		t.Fatal("NewStreamer() streamer = nil, want non-nil")
	}
	if streamCalls != 2 {
		t.Fatalf("stream calls = %d, want 2", streamCalls)
	}
	if silentReconnectCalls != 1 {
		t.Fatalf("silent reconnect calls = %d, want 1", silentReconnectCalls)
	}
	if interactiveReconnectCalls != 0 {
		t.Fatalf("interactive reconnect calls = %d, want 0", interactiveReconnectCalls)
	}
	if format.SampleRate != 44100 {
		t.Fatalf("format.SampleRate = %d, want %d", format.SampleRate, 44100)
	}
	if duration != 0 {
		t.Fatalf("duration = %v, want 0", duration)
	}
	assertEvents(t, events, "stream", "silent-reconnect", "stream")
}

func TestNewStreamerFallsBackToInteractiveAfterSilentReconnectAuthError(t *testing.T) {
	oldOpenStream := openSpotifyStream
	oldReconnect := reconnectSpotifySession
	oldReconnectInteractive := reconnectSpotifySessionInteractive
	t.Cleanup(func() {
		openSpotifyStream = oldOpenStream
		reconnectSpotifySession = oldReconnect
		reconnectSpotifySessionInteractive = oldReconnectInteractive
	})

	streamCalls := 0
	silentReconnectCalls := 0
	interactiveReconnectCalls := 0
	var events []string
	openSpotifyStream = func(*Session, context.Context, librespot.SpotifyId, int) (*librespotPlayer.Stream, error) {
		events = append(events, "stream")
		streamCalls++
		if streamCalls < 3 {
			return nil, &audio.KeyProviderError{Code: 9}
		}
		return successfulTestStream(), nil
	}
	reconnectSpotifySession = func(*Session, context.Context) error {
		events = append(events, "silent-reconnect")
		silentReconnectCalls++
		return nil
	}
	reconnectSpotifySessionInteractive = func(*Session, context.Context) error {
		events = append(events, "interactive-reconnect")
		interactiveReconnectCalls++
		return nil
	}

	p := &SpotifyProvider{session: &Session{}}
	streamer, _, _, err := p.NewStreamer("spotify:track:1234567890123456789012")
	if err != nil {
		t.Fatalf("NewStreamer() error = %v", err)
	}
	if streamer == nil {
		t.Fatal("NewStreamer() streamer = nil, want non-nil")
	}
	if streamCalls != 3 {
		t.Fatalf("stream calls = %d, want 3", streamCalls)
	}
	if silentReconnectCalls != 1 {
		t.Fatalf("silent reconnect calls = %d, want 1", silentReconnectCalls)
	}
	if interactiveReconnectCalls != 1 {
		t.Fatalf("interactive reconnect calls = %d, want 1", interactiveReconnectCalls)
	}
	assertEvents(t, events, "stream", "silent-reconnect", "stream", "interactive-reconnect", "stream")
}
