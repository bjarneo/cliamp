package subsonicapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

func TestNavidromeCanRateTrack(t *testing.T) {
	c := NewNavidromeClient(Config{BaseURL: "http://localhost", User: "u", Password: "p"})
	for _, tc := range []struct {
		name  string
		track playlist.Track
		want  bool
	}{
		{name: "Navidrome song", track: playlist.Track{ProviderMeta: map[string]string{provider.MetaNavidromeID: "song-1"}}, want: true},
		{name: "missing song ID", track: playlist.Track{}},
		{name: "different provider", track: playlist.Track{ProviderMeta: map[string]string{provider.MetaBandcampID: "song-1"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := c.CanRateTrack(tc.track); got != tc.want {
				t.Fatalf("CanRateTrack() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSetTrackRating(t *testing.T) {
	var gotPath, gotID, gotRating string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotID = r.URL.Query().Get("id")
		gotRating = r.URL.Query().Get("rating")
		w.Write([]byte(`{"subsonic-response":{"status":"ok"}}`))
	}))
	defer srv.Close()

	c := NewNavidromeClient(Config{BaseURL: srv.URL, User: "u", Password: "p"})
	track := playlist.Track{Path: "navidrome song", ProviderMeta: map[string]string{provider.MetaNavidromeID: "song-1"}}
	if err := c.SetTrackRating(context.Background(), track, 4); err != nil {
		t.Fatalf("SetTrackRating: %v", err)
	}
	if gotPath != "/rest/setRating" || gotID != "song-1" || gotRating != "4" {
		t.Fatalf("request = %s?id=%s&rating=%s, want /rest/setRating?id=song-1&rating=4", gotPath, gotID, gotRating)
	}
}

func TestSetTrackRatingInvalidatesCachedPlaylistsContainingSong(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  string
		wantErr bool
	}{
		{name: "success", status: "ok"},
		{name: "uncertain failure", status: "failed", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				fmt.Fprintf(w, `{"subsonic-response":{"status":%q}}`, tc.status)
			}))
			defer srv.Close()

			c := NewNavidromeClient(Config{BaseURL: srv.URL, User: "u", Password: "p"})
			target := playlist.Track{Path: "target", ProviderMeta: map[string]string{provider.MetaNavidromeID: "song-1"}}
			other := playlist.Track{Path: "other", ProviderMeta: map[string]string{provider.MetaNavidromeID: "song-2"}}
			c.trackCache = map[string][]playlist.Track{
				"contains-target": {target, other},
				"unrelated":       {other},
			}

			err := c.SetTrackRating(context.Background(), target, 4)
			if (err != nil) != tc.wantErr {
				t.Fatalf("SetTrackRating error = %v, wantErr %v", err, tc.wantErr)
			}
			if _, ok := c.trackCache["contains-target"]; ok {
				t.Fatal("playlist containing rated song remained cached")
			}
			if _, ok := c.trackCache["unrelated"]; !ok {
				t.Fatal("unrelated playlist was evicted")
			}
		})
	}
}

func TestSetTrackRatingPreventsInflightFetchFromRestoringStaleCache(t *testing.T) {
	fetchStarted := make(chan struct{})
	releaseFetch := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseFetch) }) }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/getPlaylist":
			close(fetchStarted)
			<-releaseFetch
			w.Write([]byte(`{"subsonic-response":{"status":"ok","playlist":{"entry":[]}}}`))
		case "/rest/setRating":
			w.Write([]byte(`{"subsonic-response":{"status":"ok"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	defer release()

	c := NewNavidromeClient(Config{BaseURL: srv.URL, User: "u", Password: "p"})
	fetched := make(chan error, 1)
	go func() {
		_, err := c.Tracks("playlist-1")
		fetched <- err
	}()
	select {
	case <-fetchStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("getPlaylist was not requested")
	}
	track := playlist.Track{ProviderMeta: map[string]string{provider.MetaNavidromeID: "song-1"}}
	if err := c.SetTrackRating(context.Background(), track, 4); err != nil {
		t.Fatalf("SetTrackRating: %v", err)
	}
	release()
	if err := <-fetched; err != nil {
		t.Fatalf("Tracks: %v", err)
	}
	if _, ok := c.trackCache["playlist-1"]; ok {
		t.Fatal("in-flight fetch restored a cache entry from before the rating update")
	}
}

func TestSetTrackRatingRejectsInvalidInputWithoutRequest(t *testing.T) {
	for _, tc := range []struct {
		name   string
		track  playlist.Track
		rating int
		want   string
	}{
		{name: "below range", track: playlist.Track{ProviderMeta: map[string]string{provider.MetaNavidromeID: "song-1"}}, rating: -1, want: "between 0 and 5"},
		{name: "above range", track: playlist.Track{ProviderMeta: map[string]string{provider.MetaNavidromeID: "song-1"}}, rating: 6, want: "between 0 and 5"},
		{name: "missing song ID", track: playlist.Track{Path: "local.mp3"}, rating: 3, want: "no song ID"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
			defer srv.Close()
			c := NewNavidromeClient(Config{BaseURL: srv.URL, User: "u", Password: "p"})
			err := c.SetTrackRating(context.Background(), tc.track, tc.rating)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want text %q", err, tc.want)
			}
			if calls != 0 {
				t.Fatalf("requests = %d, want 0", calls)
			}
		})
	}
}

func TestSetTrackRatingHonorsContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"subsonic-response":{"status":"ok"}}`))
	}))
	defer srv.Close()
	c := NewNavidromeClient(Config{BaseURL: srv.URL, User: "u", Password: "p"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	track := playlist.Track{ProviderMeta: map[string]string{provider.MetaNavidromeID: "song-1"}}
	if err := c.SetTrackRating(ctx, track, 5); err == nil {
		t.Fatal("SetTrackRating with canceled context returned nil")
	}
}

func TestBandcampRatingGatedOff(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer srv.Close()
	c := NewBandcampClient(Config{BaseURL: srv.URL, User: "u", Password: "p"})
	track := playlist.Track{ProviderMeta: map[string]string{provider.MetaBandcampID: "song-1"}}
	if c.CanRateTrack(track) {
		t.Error("CanRateTrack() = true for unverified Bandcamp ratings")
	}
	if err := c.SetTrackRating(context.Background(), track, 4); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("SetTrackRating error = %v, want unsupported", err)
	}
	if calls != 0 {
		t.Fatalf("requests = %d, want 0", calls)
	}
}
