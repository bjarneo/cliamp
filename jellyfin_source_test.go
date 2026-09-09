package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/config"
	"github.com/bjarneo/cliamp/external/jellyfin"
	"github.com/bjarneo/cliamp/player"
	"github.com/bjarneo/cliamp/playlist"
)

func TestJellyfinSourceResolutionForPlayAndPreload(t *testing.T) {
	var authCalls, streamCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/media/Users/AuthenticateByName":
			authCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"User":{"Id":"user-1"},"AccessToken":"new-token"}`)
		case "/media/Items/one/Download", "/media/Items/two/Download":
			streamCalls.Add(1)
			if got := r.URL.Query().Get("api_key"); got != "new-token" {
				t.Errorf("engine requested stream with token %q, want new-token", got)
			}
			// Stop after observing the engine's HTTP request, before decoding or audio output.
			http.Error(w, "stop before decoding", http.StatusServiceUnavailable)
		default:
			t.Errorf("unexpected request: %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	prov := jellyfin.NewFromConfig(config.JellyfinConfig{
		URL: srv.URL + "/media", User: "user", Password: "password",
	})
	savedPaths := []string{
		srv.URL + "/media/Items/one/Download?api_key=old-token",
		srv.URL + "/media/Items/two/Download?api_key=old-token",
	}
	tracks := make([]playlist.Track, len(savedPaths))
	for i, path := range savedPaths {
		track, ok := prov.RestoreTrack(playlist.Track{Path: path})
		if !ok || track.Path != path {
			t.Fatalf("RestoreTrack() = (%+v, %v), want saved path %q", track, ok, path)
		}
		tracks[i] = track
	}
	if authCalls.Load() != 0 || streamCalls.Load() != 0 {
		t.Fatal("restoring context made a network request")
	}

	engine := &player.Player{}
	for _, scheme := range []string{"http://", "https://"} {
		engine.RegisterSourceResolver(scheme, func(rawURL string) (player.ResolvedSource, error) {
			u, err := prov.ResolveSource(rawURL)
			return player.ResolvedSource{URL: u}, err
		})
	}
	engine.RegisterBufferedURLMatcher(jellyfin.IsStreamURL)
	for _, tt := range []struct {
		name string
		open func() error
	}{
		{name: "play", open: func() error {
			return engine.PlayAtForGeneration(tracks[0].Path, 3*time.Minute, 95*time.Second, 1)
		}},
		{name: "preload", open: func() error {
			return engine.PreloadForGeneration(tracks[1].Path, 3*time.Minute, 1)
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.open(); err == nil || !strings.Contains(err.Error(), "nav buffer: http status 503 Service Unavailable") {
				t.Fatalf("engine source open error = %v, want deliberate stream rejection", err)
			}
		})
	}
	if authCalls.Load() != 1 || streamCalls.Load() != 2 {
		t.Fatalf("authentication/stream requests = %d/%d, want 1/2", authCalls.Load(), streamCalls.Load())
	}
	for i, track := range tracks {
		if track.Path != savedPaths[i] {
			t.Fatalf("logical path changed to %q, want %q", track.Path, savedPaths[i])
		}
	}
}
