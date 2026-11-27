package yandex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

func TestCanFavoriteTrack(t *testing.T) {
	p := New("test-token")
	tests := []struct {
		name  string
		track playlist.Track
		want  bool
	}{
		{
			name:  "yandex track with id",
			track: playlist.Track{Path: "yandex:123", ProviderMeta: map[string]string{provider.MetaYandexID: "123"}},
			want:  true,
		},
		{
			name:  "yandex track with empty id",
			track: playlist.Track{Path: "yandex:123", ProviderMeta: map[string]string{provider.MetaYandexID: ""}},
			want:  false,
		},
		{
			name:  "local track",
			track: playlist.Track{Path: "/music/song.mp3"},
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := p.CanFavoriteTrack(tt.track); got != tt.want {
				t.Errorf("CanFavoriteTrack() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSetTrackFavorite(t *testing.T) {
	tests := []struct {
		name         string
		track        playlist.Track
		favorite     bool
		accountCode  int
		likeCode     int
		wantPath     string
		wantTrackIDs string
		wantErr      bool
	}{
		{
			name:         "like sends add-multiple",
			track:        playlist.Track{Path: "yandex:99", ProviderMeta: map[string]string{provider.MetaYandexID: "99"}},
			favorite:     true,
			accountCode:  http.StatusOK,
			likeCode:     http.StatusOK,
			wantPath:     "/users/42/likes/tracks/add-multiple",
			wantTrackIDs: "99",
		},
		{
			name:         "unlike sends remove",
			track:        playlist.Track{Path: "yandex:99", ProviderMeta: map[string]string{provider.MetaYandexID: "99"}},
			favorite:     false,
			accountCode:  http.StatusOK,
			likeCode:     http.StatusOK,
			wantPath:     "/users/42/likes/tracks/remove",
			wantTrackIDs: "99",
		},
		{
			name:     "missing track id fails without HTTP call",
			track:    playlist.Track{Path: "yandex:missing"},
			favorite: true,
			wantErr:  true,
		},
		{
			name:        "account status error propagates",
			track:       playlist.Track{Path: "yandex:99", ProviderMeta: map[string]string{provider.MetaYandexID: "99"}},
			favorite:    true,
			accountCode: http.StatusUnauthorized,
			likeCode:    http.StatusOK,
			wantErr:     true,
		},
		{
			name:         "api error propagates",
			track:        playlist.Track{Path: "yandex:99", ProviderMeta: map[string]string{provider.MetaYandexID: "99"}},
			favorite:     false,
			accountCode:  http.StatusOK,
			likeCode:     http.StatusInternalServerError,
			wantPath:     "/users/42/likes/tracks/remove",
			wantTrackIDs: "99",
			wantErr:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotMethod, gotPath, gotTrackIDs string
			var calls atomic.Int32

			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path == "/account/status" {
					if tt.accountCode != http.StatusOK {
						w.WriteHeader(tt.accountCode)
						_, _ = w.Write([]byte("unauthorized"))
						return
					}
					writeResult(w, map[string]any{"account": map[string]any{"uid": 42}})
					return
				}
				gotMethod = r.Method
				gotPath = r.URL.Path
				if err := r.ParseForm(); err == nil {
					gotTrackIDs = r.Form.Get("track-ids")
				}
				if tt.likeCode != http.StatusOK {
					w.WriteHeader(tt.likeCode)
					_, _ = w.Write([]byte("server error"))
					return
				}
				writeResult(w, map[string]any{"revision": 1})
			}))
			t.Cleanup(ts.Close)

			p := New("test-token")
			p.api.apiBase = ts.URL

			err := p.SetTrackFavorite(context.Background(), tt.track, tt.favorite)
			if (err != nil) != tt.wantErr {
				t.Fatalf("SetTrackFavorite() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantPath == "" {
				if tt.accountCode == 0 && calls.Load() != 0 {
					t.Fatalf("HTTP calls = %d, want 0", calls.Load())
				}
				return
			}
			if gotMethod != http.MethodPost {
				t.Errorf("method = %q, want POST", gotMethod)
			}
			if gotPath != tt.wantPath {
				t.Errorf("path = %q, want %q", gotPath, tt.wantPath)
			}
			if gotTrackIDs != tt.wantTrackIDs {
				t.Errorf("track-ids = %q, want %q", gotTrackIDs, tt.wantTrackIDs)
			}
		})
	}
}

func TestClientLikeUnlikeTracksEmptyNoOp(t *testing.T) {
	var calls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(ts.Close)

	c := newClient("test-token")
	c.apiBase = ts.URL

	if err := c.likeTracks(context.Background(), 42, nil); err != nil {
		t.Fatalf("likeTracks(nil) error = %v", err)
	}
	if err := c.unlikeTracks(context.Background(), 42, []string{}); err != nil {
		t.Fatalf("unlikeTracks(empty) error = %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("HTTP calls = %d, want 0", calls.Load())
	}
}
