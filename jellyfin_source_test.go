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
	"github.com/bjarneo/cliamp/external/emby"
	"github.com/bjarneo/cliamp/external/jellyfin"
	"github.com/bjarneo/cliamp/player"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
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
	set := &providerSet{entries: []provider.Entry{{Key: "jellyfin", Name: "Jellyfin", Provider: prov}}}
	set.registerPlayerHooks(engine)
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

// With Jellyfin and Emby both configured, the player refreshes a saved
// download URL with the token of the server that owns it. A URL of another
// host plays as saved.
func TestServerSourceResolutionPicksOwner(t *testing.T) {
	newServer := func(t *testing.T) (*httptest.Server, *atomic.Value) {
		t.Helper()
		var token atomic.Value
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token.Store(r.URL.Query().Get("api_key"))
			http.Error(w, "stop before decoding", http.StatusServiceUnavailable)
		}))
		t.Cleanup(srv.Close)
		return srv, &token
	}
	jf, jfToken := newServer(t)
	em, emToken := newServer(t)
	other, otherToken := newServer(t)

	set := &providerSet{entries: []provider.Entry{
		{Key: "jellyfin", Name: "Jellyfin", Provider: jellyfin.NewFromConfig(config.JellyfinConfig{URL: jf.URL, Token: "jf-token", UserID: "user-1"})},
		{Key: "emby", Name: "Emby", Provider: emby.NewFromConfig(config.EmbyConfig{URL: em.URL, Token: "emby-token", UserID: "user-2"})},
	}}
	engine := &player.Player{}
	set.registerPlayerHooks(engine)

	for _, tt := range []struct {
		name  string
		path  string
		token *atomic.Value
		want  string
	}{
		{name: "jellyfin", path: jf.URL + "/Items/one/Download?api_key=old", token: jfToken, want: "jf-token"},
		{name: "emby", path: em.URL + "/Items/two/Download?api_key=old", token: emToken, want: "emby-token"},
		{name: "other host", path: other.URL + "/Items/three/Download?api_key=old", token: otherToken, want: "old"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := engine.PlayAtForGeneration(tt.path, time.Minute, 0, 1); err == nil {
				t.Fatal("engine opened the stream, want the deliberate rejection")
			}
			if got, _ := tt.token.Load().(string); got != tt.want {
				t.Fatalf("server got token %q, want %q", got, tt.want)
			}
		})
	}
}
