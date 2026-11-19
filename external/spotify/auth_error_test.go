package spotify

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	librespot "github.com/devgianlu/go-librespot"
	"github.com/devgianlu/go-librespot/ap"
	"github.com/devgianlu/go-librespot/audio"

	"github.com/bjarneo/cliamp/playlist"
)

// TestStreamErrorClassification pins down which stream errors rebuild the
// session and which ask for sign-in. Spotify refuses an audio key on a
// session that is signed in, so a refusal must do neither. Rapid track
// skipping cancels a stream setup, and that must not rebuild the session.
func TestStreamErrorClassification(t *testing.T) {
	// go-librespot's player.NewStream wraps the key error this way.
	keyErr := fmt.Errorf("failed retrieving audio key: %w", &audio.KeyProviderError{Code: 1})

	tests := []struct {
		name        string
		err         error
		wantLost    bool
		wantRefused bool
	}{
		{"audio key refused", keyErr, false, true},
		{"closed access point", fmt.Errorf("failed retrieving audio key: %w", ap.ErrAccesspointClosed), true, false},
		{"setup timeout", fmt.Errorf("librespot: fetch chunk: %w", context.DeadlineExceeded), false, false},
		{"skip cancellation", context.Canceled, false, false},
		{"plain network error", errors.New("connection reset by peer"), false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isSessionLost(tt.err); got != tt.wantLost {
				t.Fatalf("isSessionLost(%v) = %v, want %v", tt.err, got, tt.wantLost)
			}
			got := streamError(tt.err)
			if !errors.Is(got, tt.err) {
				t.Fatalf("streamError(%v) = %v, want it to wrap the cause", tt.err, got)
			}
			if errors.Is(got, playlist.ErrNeedsAuth) {
				t.Fatalf("streamError(%v) = %v, want no sign-in request", tt.err, got)
			}
			if refused := strings.Contains(got.Error(), "Spotify refused the audio key for this track"); refused != tt.wantRefused {
				t.Fatalf("streamError(%v) = %q, want refusal message %v", tt.err, got, tt.wantRefused)
			}
		})
	}
}

// TestClosedAccesspointIsSessionLost drives go-librespot's real key provider
// over a closed access point, the state it leaves after giving up on a dropped
// connection, so a library change to that error breaks this test rather than
// leaving playback dead until restart.
func TestClosedAccesspointIsSessionLost(t *testing.T) {
	accesspoint := ap.NewAccesspoint(&librespot.NullLogger{}, func(context.Context) string { return "127.0.0.1:1" }, "test-device")
	accesspoint.Close()
	keys := audio.NewAudioKeyProvider(&librespot.NullLogger{}, accesspoint)

	_, err := keys.Request(context.Background(), make([]byte, 16), make([]byte, 20))
	if err == nil {
		t.Fatal("key request on a closed access point succeeded")
	}
	// Wrapped the way go-librespot's player.NewStream wraps it.
	err = fmt.Errorf("failed retrieving audio key: %w", err)
	if !isSessionLost(err) {
		t.Fatalf("isSessionLost(%v) = false, want true", err)
	}
	if isSessionLost(fmt.Errorf("failed retrieving audio key: %w", context.DeadlineExceeded)) {
		t.Fatal("a key request timeout must not rebuild the session")
	}
}
