package spotify

import (
	"context"
	"errors"
	"fmt"
	"testing"

	librespot "github.com/devgianlu/go-librespot"
	"github.com/devgianlu/go-librespot/ap"
	"github.com/devgianlu/go-librespot/audio"
)

// TestIsAuthError pins down which errors trigger Spotify re-authentication.
// Rapid track skipping cancels in-flight stream creation, surfacing
// context.DeadlineExceeded / context.Canceled — these MUST NOT be treated as
// auth errors, otherwise the streamer escalates to a browser re-auth flow
// even though the session is healthy. See the regression discussion in
// fix/spotify-rapid-skip-reauth.
func TestIsAuthError(t *testing.T) {
	wrappedDeadline := fmt.Errorf("librespot: fetch chunk: %w", context.DeadlineExceeded)
	wrappedCanceled := fmt.Errorf("librespot: fetch chunk: %w", context.Canceled)
	keyErr := &audio.KeyProviderError{Code: 1}
	wrappedKeyErr := fmt.Errorf("spotify: %w", keyErr)

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"context.DeadlineExceeded (skip cancellation)", context.DeadlineExceeded, false},
		{"wrapped DeadlineExceeded", wrappedDeadline, false},
		{"context.Canceled (skip cancellation)", context.Canceled, false},
		{"wrapped Canceled", wrappedCanceled, false},
		{"plain network error", errors.New("connection reset by peer"), false},
		{"KeyProviderError (real auth signal)", keyErr, true},
		{"wrapped KeyProviderError", wrappedKeyErr, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isAuthError(tt.err)
			if got != tt.want {
				t.Fatalf("isAuthError(%v) = %v, want %v", tt.err, got, tt.want)
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
