package spotify

import (
	"context"
	"net/http"
	"slices"
	"testing"

	"golang.org/x/oauth2"

	"github.com/bjarneo/cliamp/internal/httpclient/httpclienttest"
)

// TestWebClientUsesEnvironmentProxy checks that Web API, lyrics and token
// requests follow ALL_PROXY. The test proxy refuses https tunnels, so each
// call fails after it reaches the proxy.
func TestWebClientUsesEnvironmentProxy(t *testing.T) {
	session := func() *Session {
		return &Session{tokenSource: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "token"})}
	}
	tests := []struct {
		name     string
		call     func() error
		wantHost string
	}{
		{
			name: "web api",
			call: func() error {
				_, err := session().webAPIOnce(context.Background(), http.MethodGet, "/v1/me", nil, nil, "")
				return err
			},
			wantHost: "api.spotify.com:443",
		},
		{
			name: "lyrics",
			call: func() error {
				_, err := session().trackLyrics(context.Background(), "track")
				return err
			},
			wantHost: "spclient.wg.spotify.com:443",
		},
		{
			name: "token refresh",
			call: func() error {
				_, err := silentTokenRefresh(context.Background(), "client", "refresh")
				return err
			},
			wantHost: "accounts.spotify.com:443",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proxy := httpclienttest.UseAllProxy(t, nil)
			if err := tt.call(); err == nil {
				t.Fatal("call succeeded through a proxy that opens no tunnels")
			}
			// oauth2 sends the token request twice while it detects the
			// auth style, so compare the distinct hosts.
			if got := slices.Compact(proxy.Hosts()); !slices.Equal(got, []string{tt.wantHost}) {
				t.Errorf("proxy hosts = %q, want [%s]", got, tt.wantHost)
			}
		})
	}
}
