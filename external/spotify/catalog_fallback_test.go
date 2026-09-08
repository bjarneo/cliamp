package spotify

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"golang.org/x/oauth2"
)

// devModeForbidden serves 403 to the user's own Development Mode client and 200
// to keymaster, the way Spotify treats a playlist the user does not own. It
// records the bearer token of every request so the retry can be observed.
func devModeForbidden(t *testing.T, catalog oauth2.TokenSource) (*SpotifyProvider, *[]string) {
	t.Helper()
	var seen []string

	originalTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		token := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
		seen = append(seen, token)

		if token != "keymaster" {
			return &http.Response{
				StatusCode: http.StatusForbidden,
				Status:     "403 Forbidden",
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"error":{"status":403,"message":"Forbidden"}}`)),
				Request:    req,
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"items":[],"total":0}`)),
			Request:    req,
		}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	sess := &Session{
		tokenSource:   oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "devmode"}),
		catalogSource: catalog,
	}
	return New(sess, "custom-client", 320), &seen
}

func TestWebAPIRetriesForbiddenWithCatalogClient(t *testing.T) {
	p, seen := devModeForbidden(t, oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "keymaster"}))

	resp, err := p.webAPI(context.Background(), "GET", "/v1/playlists/someone-elses/items", nil)
	if err != nil {
		t.Fatalf("webAPI() error = %v, want success via catalog client", err)
	}
	defer resp.Body.Close()

	want := []string{"devmode", "keymaster"}
	if len(*seen) != len(want) {
		t.Fatalf("tokens used = %v, want %v", *seen, want)
	}
	for i := range want {
		if (*seen)[i] != want[i] {
			t.Errorf("request %d used token %q, want %q", i, (*seen)[i], want[i])
		}
	}
}

func TestWebAPIForbiddenWithoutCatalogClientDoesNotRetry(t *testing.T) {
	p, seen := devModeForbidden(t, nil)

	if _, err := p.webAPI(context.Background(), "GET", "/v1/playlists/someone-elses/items", nil); err == nil {
		t.Fatal("webAPI() error = nil, want the 403 surfaced")
	}
	// Without a keymaster token there is nothing to retry with, so the 403 must
	// surface on the first attempt rather than burning the retry budget.
	if len(*seen) != 1 {
		t.Errorf("made %d requests, want 1", len(*seen))
	}
}

func TestCatalogRefreshTokenOnlyStoredForCustomClient(t *testing.T) {
	tests := []struct {
		name     string
		clientID string
		want     bool
	}{
		{"custom client keeps a keymaster token", "custom-client", true},
		{"built-in client needs no second token", DefaultClientID, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flows := interactiveOAuthFlows(tt.clientID)
			got := len(flows) > 1
			if got != tt.want {
				t.Errorf("separate keymaster flow = %v, want %v", got, tt.want)
			}
			// Whichever path runs, the keymaster flow must carry the read
			// scopes; "streaming" alone cannot fetch playlist items.
			last := flows[len(flows)-1]
			if last.clientID != DefaultClientID {
				t.Fatalf("last flow client = %q, want keymaster", last.clientID)
			}
			if len(last.scopes) != len(oauthScopes) {
				t.Errorf("keymaster scopes = %v, want the full read set", last.scopes)
			}
		})
	}
}
