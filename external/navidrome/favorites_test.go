package navidrome

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

func TestCanFavoriteTrack(t *testing.T) {
	c := New("http://nav.example", "user", "pass")
	for _, tc := range []struct {
		name  string
		track playlist.Track
		want  bool
	}{
		{name: "server song", track: playlist.Track{Path: "http://nav.example/rest/stream", ProviderMeta: map[string]string{provider.MetaNavidromeID: "song-1"}}, want: true},
		{name: "local file", track: playlist.Track{Path: "/music/song.mp3"}},
		{name: "stream without an ID", track: playlist.Track{Path: "http://nav.example/rest/stream"}},
	} {
		if got := c.CanFavoriteTrack(tc.track); got != tc.want {
			t.Errorf("%s: CanFavoriteTrack = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestSetTrackFavorite(t *testing.T) {
	for _, tc := range []struct {
		name         string
		favorite     bool
		body         string
		wantEndpoint string
		wantErr      string
	}{
		{name: "star", favorite: true, body: `{"subsonic-response":{"status":"ok"}}`, wantEndpoint: "/rest/star"},
		{name: "unstar", body: `{"subsonic-response":{"status":"ok"}}`, wantEndpoint: "/rest/unstar"},
		{
			name: "server error", favorite: true, wantEndpoint: "/rest/star",
			body:    `{"subsonic-response":{"status":"failed","error":{"code":50,"message":"User is not authorized"}}}`,
			wantErr: "User is not authorized",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var gotPath, gotID string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath, gotID = r.URL.Path, r.URL.Query().Get("id")
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			c := New(srv.URL, "user", "pass")
			track := playlist.Track{Path: c.streamURL("song-1"), ProviderMeta: map[string]string{provider.MetaNavidromeID: "song-1"}}

			err := c.SetTrackFavorite(context.Background(), track, tc.favorite)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
			} else if err != nil {
				t.Fatalf("SetTrackFavorite: %v", err)
			}
			if gotPath != tc.wantEndpoint || gotID != "song-1" {
				t.Fatalf("request = %s?id=%s, want %s?id=song-1", gotPath, gotID, tc.wantEndpoint)
			}
		})
	}
}

func TestSetTrackFavoriteNeedsSongID(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer srv.Close()
	c := New(srv.URL, "user", "pass")
	if err := c.SetTrackFavorite(context.Background(), playlist.Track{Path: "/music/song.mp3"}, true); err == nil {
		t.Fatal("a track without a song ID should fail")
	}
	if calls != 0 {
		t.Fatalf("requests = %d, want 0", calls)
	}
}

func TestSetTrackFavoriteHonorsContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"subsonic-response":{"status":"ok"}}`))
	}))
	defer srv.Close()
	c := New(srv.URL, "user", "pass")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	track := playlist.Track{ProviderMeta: map[string]string{provider.MetaNavidromeID: "song-1"}}
	if err := c.SetTrackFavorite(ctx, track, true); err == nil {
		t.Fatal("a canceled context should stop the request")
	}
}
