//go:build !windows

package spotify

import (
	"context"
	"errors"
	"fmt"
	"testing"

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
