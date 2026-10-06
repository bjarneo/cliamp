package ytmusic

import (
	"context"
	"slices"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/bjarneo/cliamp/internal/httpclient/httpclienttest"
)

// TestRequestsUseEnvironmentProxy checks that token requests and YouTube
// Data API requests follow ALL_PROXY. The test proxy refuses https tunnels,
// so each call fails after it reaches the proxy.
func TestRequestsUseEnvironmentProxy(t *testing.T) {
	tests := []struct {
		name     string
		call     func() error
		wantHost string
	}{
		{
			name: "token refresh",
			call: func() error {
				_, err := silentTokenRefresh(context.Background(), "id", "secret", "refresh")
				return err
			},
			wantHost: "oauth2.googleapis.com:443",
		},
		{
			name: "data api",
			call: func() error {
				token := &oauth2.Token{AccessToken: "token", Expiry: time.Now().Add(time.Hour)}
				s, err := newTokenSession(context.Background(), "id", "secret", token, "refresh")
				if err != nil {
					t.Fatalf("newTokenSession: %v", err)
				}
				_, err = s.Service().Playlists.List([]string{"snippet"}).Mine(true).Do()
				return err
			},
			wantHost: "youtube.googleapis.com:443",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proxy := httpclienttest.UseAllProxy(t, nil)
			if err := tt.call(); err == nil {
				t.Fatal("call succeeded through a proxy that opens no tunnels")
			}
			// Clients can retry a failed request, so compare the distinct
			// hosts.
			if got := slices.Compact(proxy.Hosts()); !slices.Equal(got, []string{tt.wantHost}) {
				t.Errorf("proxy hosts = %q, want [%s]", got, tt.wantHost)
			}
		})
	}
}
