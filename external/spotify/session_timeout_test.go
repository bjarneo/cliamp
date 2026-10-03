package spotify

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

// TestWebRequestsTimeOut checks that a stalled connection ends in a timeout
// error for each request that webHTTPClient sends, even when the caller
// context has no deadline.
func TestWebRequestsTimeOut(t *testing.T) {
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
	originalClient := webHTTPClient
	webHTTPClient = &http.Client{
		Timeout: 50 * time.Millisecond,
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			<-req.Context().Done()
			return nil, req.Context().Err()
		}),
	}
	t.Cleanup(func() { webHTTPClient = originalClient })

	staticSession := func() *Session {
		return &Session{tokenSource: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "token"})}
	}
	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "web api",
			call: func() error {
				_, err := staticSession().webAPIOnce(context.Background(), http.MethodGet, "/v1/me", nil, nil, "")
				return err
			},
		},
		{
			name: "lyrics",
			call: func() error {
				_, err := staticSession().trackLyrics(context.Background(), "track")
				return err
			},
		},
		{
			name: "token refresh",
			call: func() error {
				expired := &oauth2.Token{AccessToken: "old", RefreshToken: "refresh", Expiry: time.Now().Add(-time.Hour)}
				_, err := webAPITokenSource("client", expired, storedCreds{}).Token()
				return err
			},
		},
		{
			name: "silent token refresh",
			call: func() error {
				_, err := silentTokenRefresh(context.Background(), "client", "refresh")
				return err
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			done := make(chan error, 1)
			go func() { done <- tt.call() }()
			select {
			case err := <-done:
				var urlErr *url.Error
				if !errors.As(err, &urlErr) || !urlErr.Timeout() {
					t.Fatalf("error = %v, want a client timeout", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("request did not time out")
			}
		})
	}
}
