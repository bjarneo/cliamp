package tidal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/playlist"
)

// testClient returns a client with a valid-looking token pointed at srv.
func testClient(srv *httptest.Server) *client {
	c := newClient("id", "secret")
	c.baseURL = srv.URL + "/"
	c.http = srv.Client()
	c.accessToken = "token"
	c.tokenType = "Bearer"
	c.refreshToken = "refresh"
	c.expiresAt = time.Now().Add(time.Hour)
	c.countryCode = "US"
	c.userID = "42"
	return c
}

func TestFetchListPagination(t *testing.T) {
	// 150 favorite artists: page 1 full (100), page 2 partial (50).
	total := 150
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("countryCode"); got != "US" {
			t.Errorf("countryCode = %q, want US", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			t.Errorf("Authorization = %q", got)
		}
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		var items []map[string]any
		for i := offset; i < offset+limit && i < total; i++ {
			items = append(items, map[string]any{
				"item": map[string]any{"id": i, "name": fmt.Sprintf("artist-%d", i)},
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items":              items,
			"totalNumberOfItems": total,
		})
	}))
	defer srv.Close()

	c := testClient(srv)
	artists, err := c.favoriteArtists(context.Background())
	if err != nil {
		t.Fatalf("favoriteArtists: %v", err)
	}
	if len(artists) != total {
		t.Fatalf("got %d artists, want %d", len(artists), total)
	}
	if artists[0].Name != "artist-0" || artists[total-1].Name != "artist-149" {
		t.Errorf("unexpected boundary items: %q, %q", artists[0].Name, artists[total-1].Name)
	}
}

func TestFetchListMaxItems(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		items := make([]map[string]any, limit)
		for i := range items {
			items[i] = map[string]any{"item": map[string]any{"id": i, "title": "t"}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items":              items,
			"totalNumberOfItems": 10000,
		})
	}))
	defer srv.Close()

	c := testClient(srv)
	tracks, err := c.favoriteTracks(context.Background(), 250)
	if err != nil {
		t.Fatalf("favoriteTracks: %v", err)
	}
	if len(tracks) != 250 {
		t.Errorf("got %d tracks, want cap of 250", len(tracks))
	}
}

func TestUserPlaylistsRespectsPageCap(t *testing.T) {
	// The playlistsAndFavoritePlaylists endpoint rejects limit > 50, unlike
	// most v1 list endpoints which allow 100.
	total := 120
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit > 50 {
			w.WriteHeader(http.StatusBadRequest)
			if _, err := fmt.Fprint(w, `{"status":400,"subStatus":1001,"userMessage":"Too big page, max page size is [50]"}`); err != nil {
				t.Errorf("write error: %v", err)
			}
			return
		}
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		var items []map[string]any
		for i := offset; i < offset+limit && i < total; i++ {
			items = append(items, map[string]any{
				"playlist": map[string]any{"uuid": fmt.Sprintf("uuid-%d", i), "title": fmt.Sprintf("playlist-%d", i)},
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items":              items,
			"totalNumberOfItems": total,
		})
	}))
	defer srv.Close()

	c := testClient(srv)
	playlists, err := c.userPlaylists(context.Background())
	if err != nil {
		t.Fatalf("userPlaylists: %v", err)
	}
	if len(playlists) != total {
		t.Fatalf("got %d playlists, want %d", len(playlists), total)
	}
	if playlists[0].Title != "playlist-0" || playlists[total-1].Title != "playlist-119" {
		t.Errorf("unexpected boundary items: %q, %q", playlists[0].Title, playlists[total-1].Title)
	}
}

func TestSearchAlbums(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search/albums" {
			t.Errorf("path = %q, want /search/albums", r.URL.Path)
		}
		if got := r.URL.Query().Get("query"); got != "query" {
			t.Errorf("query param = %q, want query", got)
		}
		if got := r.URL.Query().Get("limit"); got != "10" {
			t.Errorf("limit param = %q, want 10", got)
		}
		fmt.Fprint(w, `{"items": [{"id": 1, "title": "A", "artist": {"id": 2, "name": "Artist"}}]}`)
	}))
	defer srv.Close()

	c := testClient(srv)
	albums, err := c.searchAlbums(context.Background(), "query", 10)
	if err != nil {
		t.Fatalf("searchAlbums: %v", err)
	}
	if len(albums) != 1 {
		t.Fatalf("got %d albums, want 1", len(albums))
	}
	a := albums[0]
	if a.ID.String() != "1" || a.Title != "A" || a.Artist.Name != "Artist" {
		t.Errorf("album = %+v", a)
	}
}

func TestDoRequestRefreshesOn401(t *testing.T) {
	// The refresh path persists rotated tokens; keep the write away from the
	// user's real credentials file.
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())

	var apiCalls, tokenCalls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/sessions", func(w http.ResponseWriter, r *http.Request) {
		apiCalls.Add(1)
		if r.Header.Get("Authorization") != "Bearer fresh" {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"status":401}`)
			return
		}
		fmt.Fprint(w, `{"sessionId":"s","countryCode":"NO","userId":7}`)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		tokenCalls.Add(1)
		fmt.Fprint(w, `{"access_token":"fresh","token_type":"Bearer","expires_in":3600}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := testClient(srv)
	c.tokenURL = srv.URL + "/token"
	if err := c.loadSession(context.Background()); err != nil {
		t.Fatalf("loadSession: %v", err)
	}
	if got := tokenCalls.Load(); got != 1 {
		t.Errorf("token refreshes = %d, want 1", got)
	}
	if got := apiCalls.Load(); got != 2 {
		t.Errorf("api calls = %d, want 2 (401 then retry)", got)
	}
	if c.countryCode != "NO" {
		t.Errorf("countryCode = %q, want NO", c.countryCode)
	}
}

func TestRevokedRefreshTokenDropsClientAndAsksForAuth(t *testing.T) {
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())

	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"invalid_grant","error_description":"revoked"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := testClient(srv)
	c.tokenURL = srv.URL + "/token"
	c.expiresAt = time.Now().Add(-time.Hour) // force a refresh attempt

	p := New("lossless", "", "")
	p.client = c

	_, err := p.Tracks(favoriteTracksID)
	if !errors.Is(err, playlist.ErrNeedsAuth) {
		t.Fatalf("err = %v, want playlist.ErrNeedsAuth", err)
	}
	p.mu.Lock()
	dropped := p.client == nil
	p.mu.Unlock()
	if !dropped {
		t.Error("client not dropped after revoked refresh token")
	}
}

func TestDoRequestRetriesOn429(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		fmt.Fprint(w, `{"sessionId":"s","countryCode":"NO","userId":7}`)
	}))
	defer srv.Close()

	c := testClient(srv)
	if err := c.loadSession(context.Background()); err != nil {
		t.Fatalf("loadSession after 429: %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("calls = %d, want 2 (429 then success)", got)
	}
}
