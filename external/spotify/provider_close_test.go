package spotify

import (
	"errors"
	"net/http"
	"sync"
	"testing"

	"github.com/bjarneo/cliamp/playlist"
)

// TestCloseDuringPlaylists runs Close next to a Web API load. Run it with
// -race: webAPIWithRetry must read p.session under p.mu, and a load that
// loses the session must report ErrNeedsAuth instead of a nil dereference.
func TestCloseDuringPlaylists(t *testing.T) {
	// Without stored credentials, ensureSession cannot rebuild the session.
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
	rt := routeTransport(map[string]func(*http.Request) *http.Response{
		"/v1/me":           apiResponse(http.StatusOK, `{"id":"me"}`),
		"/v1/me/tracks":    apiResponse(http.StatusOK, `{"total":0}`),
		"/v1/me/playlists": apiResponse(http.StatusOK, `{"items":[],"total":0}`),
		"/v1/me/albums":    apiResponse(http.StatusOK, `{"items":[],"total":0}`),
	})

	for range 50 {
		p := New(stubSession(rt), "client", 320)
		var wg sync.WaitGroup
		wg.Go(func() {
			if _, err := p.Playlists(); err != nil && !errors.Is(err, playlist.ErrNeedsAuth) {
				t.Errorf("Playlists() error = %v, want nil or ErrNeedsAuth", err)
			}
		})
		wg.Go(p.Close)
		wg.Wait()
	}
}
