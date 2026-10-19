package model

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

type urlTestProvider struct {
	playlist.Provider
	called bool
	err    error
}

func (p *urlTestProvider) CanResolveURL(raw string) bool { return raw == "spotify:playlist:test" }
func (p *urlTestProvider) ResolveURL(ctx context.Context, _ string) ([]playlist.Track, error) {
	p.called = true
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return []playlist.Track{{Path: "spotify:track:test"}}, p.err
}

func TestResolveProviderURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Header().Set("Content-Type", "audio/mpeg") }))
	defer server.Close()
	failure := errors.New("provider refused")
	for _, tt := range []struct {
		name     string
		raw      string
		err      error
		canceled bool
	}{
		{name: "provider link", raw: "spotify:playlist:test"},
		{name: "provider error", raw: "spotify:playlist:test", err: failure},
		{name: "cancellation", raw: "spotify:playlist:test", canceled: true},
		{name: "ordinary stream", raw: server.URL + "/audio.mp3"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tt.canceled {
				cancel()
			}
			urlProvider := &urlTestProvider{err: tt.err}
			tracks, err := resolveProviderURL(ctx, []provider.Entry{{Provider: urlProvider}}, tt.raw)
			wantErr := tt.err
			if tt.canceled {
				wantErr = context.Canceled
			}
			if !errors.Is(err, wantErr) {
				t.Fatalf("error = %v", err)
			}
			if tt.raw == "spotify:playlist:test" && !urlProvider.called || tt.raw != "spotify:playlist:test" && urlProvider.called {
				t.Fatalf("provider resolver called = %v", urlProvider.called)
			}
			if wantErr != nil {
				return
			}
			wantPath := "spotify:track:test"
			if tt.name == "ordinary stream" {
				wantPath = tt.raw
			}
			if len(tracks) != 1 || tracks[0].Path != wantPath {
				t.Fatalf("tracks = %v", tracks)
			}
		})
	}
}
