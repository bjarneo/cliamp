//go:build !windows

package spotify

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/playlist"
)

// decodeCallBody unmarshals a recorded request body into v.
func decodeCallBody(t *testing.T, c apiCall, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(c.Body), v); err != nil {
		t.Fatalf("decode body %q: %v", c.Body, err)
	}
}

// mustCall returns the single recorded call to path.
func mustCall(t *testing.T, m *mockAPI, path string) apiCall {
	t.Helper()
	calls := m.recordedCalls(path)
	if len(calls) != 1 {
		t.Fatalf("%d calls to %s, want 1: %+v", len(calls), path, calls)
	}
	return calls[0]
}

func TestAddTracksToPlaylistChunksAndSkips(t *testing.T) {
	m := newMockAPI(t)
	m.handlers["/v1/playlists/pl1/tracks"] = func(t *testing.T, query url.Values) string {
		return `{"snapshot_id":"snap-new"}`
	}
	p := newTestProvider()
	// Pre-populate caches to observe the snapshot update and invalidation.
	p.mu.Lock()
	p.trackCache["pl1"] = &playlistCache{snapshotID: "snap-old", tracks: []playlist.Track{{Path: "spotify:track:x"}}}
	p.listCache = []playlist.PlaylistInfo{{ID: "pl1", Name: "Playlist"}}
	p.mu.Unlock()

	tracks := make([]playlist.Track, 0, 252)
	for i := range 250 {
		tracks = append(tracks, playlist.Track{Path: fmt.Sprintf("spotify:track:t%d", i)})
	}
	tracks = append(tracks,
		playlist.Track{Path: "/music/local.mp3"}, // no resolvable URI: skipped
		playlist.Track{},                         // no path, no meta: skipped
	)

	added, skipped, err := p.AddTracksToPlaylist(t.Context(), "pl1", tracks)
	if err != nil {
		t.Fatal(err)
	}
	if added != 250 || skipped != 2 {
		t.Fatalf("added, skipped = %d, %d, want 250, 2", added, skipped)
	}

	// 250 uris chunk into 3 POSTs of 100/100/50.
	calls := m.recordedCalls("/v1/playlists/pl1/tracks")
	if len(calls) != 3 {
		t.Fatalf("%d POSTs, want 3", len(calls))
	}
	wantLens := []int{100, 100, 50}
	for i, c := range calls {
		if c.Method != "POST" {
			t.Errorf("call %d method = %s, want POST", i, c.Method)
		}
		var body struct {
			URIs []string `json:"uris"`
		}
		decodeCallBody(t, c, &body)
		if len(body.URIs) != wantLens[i] {
			t.Errorf("chunk %d carried %d uris, want %d", i, len(body.URIs), wantLens[i])
		}
	}
	var lastChunk struct {
		URIs []string `json:"uris"`
	}
	decodeCallBody(t, calls[2], &lastChunk)
	if got := lastChunk.URIs[len(lastChunk.URIs)-1]; got != "spotify:track:t249" {
		t.Errorf("last uri = %q, want spotify:track:t249", got)
	}

	p.mu.Lock()
	cached, ok := p.trackCache["pl1"]
	snapshot, tracksNil := "", cached != nil && cached.tracks == nil
	if ok {
		snapshot = cached.snapshotID
	}
	listCache := p.listCache
	p.mu.Unlock()
	if !ok || snapshot != "snap-new" || !tracksNil {
		t.Errorf("trackCache entry = {snapshot:%q tracks-nil:%v ok:%v}, want {snap-new, true, true}", snapshot, tracksNil, ok)
	}
	if listCache != nil {
		t.Error("listCache not invalidated after add")
	}
}

func TestAddTracksToPlaylistMetaFallback(t *testing.T) {
	m := newMockAPI(t)
	m.handlers["/v1/playlists/pl1/tracks"] = func(t *testing.T, query url.Values) string {
		return `{"snapshot_id":"s"}`
	}

	added, skipped, err := newTestProvider().AddTracksToPlaylist(t.Context(), "pl1", []playlist.Track{
		{ProviderMeta: map[string]string{metaSpotifyID: "tX"}}, // no Path: synthesized from meta
	})
	if err != nil {
		t.Fatal(err)
	}
	if added != 1 || skipped != 0 {
		t.Fatalf("added, skipped = %d, %d, want 1, 0", added, skipped)
	}
	var body struct {
		URIs []string `json:"uris"`
	}
	decodeCallBody(t, mustCall(t, m, "/v1/playlists/pl1/tracks"), &body)
	if len(body.URIs) != 1 || body.URIs[0] != "spotify:track:tX" {
		t.Errorf("uris = %v, want [spotify:track:tX]", body.URIs)
	}
}

func TestAddTracksToPlaylistAllSkipped(t *testing.T) {
	m := newMockAPI(t)
	// No handlers registered: any request would fail the test.

	added, skipped, err := newTestProvider().AddTracksToPlaylist(t.Context(), "pl1", []playlist.Track{{Path: "/a.mp3"}})
	if err != nil {
		t.Fatal(err)
	}
	if added != 0 || skipped != 1 {
		t.Fatalf("added, skipped = %d, %d, want 0, 1", added, skipped)
	}
	if n := m.calls("/v1/playlists/pl1/tracks"); n != 0 {
		t.Errorf("made %d requests, want 0", n)
	}
}

func TestToggleTrackLike(t *testing.T) {
	tests := []struct {
		name       string
		contains   string // /v1/me/tracks/contains reply
		wantMethod string
		wantLiked  bool
		track      playlist.Track
	}{
		{
			name:       "unsaved becomes saved via uri path",
			contains:   `[false]`,
			wantMethod: "PUT",
			wantLiked:  true,
			track:      playlist.Track{Path: "spotify:track:t1"},
		},
		{
			name:       "saved becomes unsaved via uri path",
			contains:   `[true]`,
			wantMethod: "DELETE",
			wantLiked:  false,
			track:      playlist.Track{Path: "spotify:track:t1"},
		},
		{
			name:       "unsaved becomes saved via provider meta",
			contains:   `[false]`,
			wantMethod: "PUT",
			wantLiked:  true,
			track:      playlist.Track{ProviderMeta: map[string]string{metaSpotifyID: "t1"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newMockAPI(t)
			m.handlers["/v1/me/tracks/contains"] = func(t *testing.T, query url.Values) string {
				if got := query.Get("ids"); got != "t1" {
					t.Errorf("contains ids = %q, want t1", got)
				}
				return tt.contains
			}
			m.handlers["/v1/me/tracks"] = func(t *testing.T, query url.Values) string {
				return ``
			}
			p := newTestProvider()
			p.mu.Lock()
			p.trackCache[yourMusicID] = &playlistCache{tracks: []playlist.Track{{Path: "spotify:track:t1"}}}
			p.recentTracks = []playlist.Track{{Path: "spotify:track:t1"}}
			p.recentTracksAt = time.Now()
			p.mu.Unlock()

			liked, err := p.ToggleTrackLike(t.Context(), tt.track)
			if err != nil {
				t.Fatal(err)
			}
			if liked != tt.wantLiked {
				t.Errorf("liked = %v, want %v", liked, tt.wantLiked)
			}

			// The contains round-trip, then the flip request with the id body.
			c := mustCall(t, m, "/v1/me/tracks")
			if c.Method != tt.wantMethod {
				t.Errorf("flip method = %s, want %s", c.Method, tt.wantMethod)
			}
			var body struct {
				IDs []string `json:"ids"`
			}
			decodeCallBody(t, c, &body)
			if len(body.IDs) != 1 || body.IDs[0] != "t1" {
				t.Errorf("ids body = %v, want [t1]", body.IDs)
			}

			// YOUR MUSIC and recently-played caches are invalidated.
			p.mu.Lock()
			_, yourMusicCached := p.trackCache[yourMusicID]
			recentTracks, recentAt := p.recentTracks, p.recentTracksAt
			p.mu.Unlock()
			if yourMusicCached {
				t.Error("trackCache[YOUR MUSIC] not invalidated")
			}
			if recentTracks != nil || !recentAt.IsZero() {
				t.Error("recently-played cache not cleared")
			}
		})
	}
}

func TestToggleTrackLikeUnresolvable(t *testing.T) {
	m := newMockAPI(t)
	// No handlers registered: no request may be made.

	_, err := newTestProvider().ToggleTrackLike(t.Context(), playlist.Track{Path: "/music/local.mp3"})
	if err == nil || !strings.Contains(err.Error(), "no spotify track ID") {
		t.Fatalf("err = %v, want unresolvable-track error", err)
	}
	if n := m.calls("/v1/me/tracks/contains"); n != 0 {
		t.Errorf("made %d contains requests, want 0", n)
	}
}

func TestPlaylistFollowRequestShapes(t *testing.T) {
	m := newMockAPI(t)
	m.handlers["/v1/playlists/pl1/followers"] = func(t *testing.T, query url.Values) string {
		return ``
	}
	p := newTestProvider()
	p.mu.Lock()
	p.listCache = []playlist.PlaylistInfo{{ID: "pl1", Name: "Playlist"}}
	p.mu.Unlock()

	if err := p.FollowPlaylistByID(t.Context(), "pl1"); err != nil {
		t.Fatal(err)
	}
	if err := p.UnfollowPlaylistByID(t.Context(), "pl1"); err != nil {
		t.Fatal(err)
	}

	calls := m.recordedCalls("/v1/playlists/pl1/followers")
	if len(calls) != 2 {
		t.Fatalf("%d calls, want 2 (follow + unfollow)", len(calls))
	}
	if calls[0].Method != "PUT" || calls[0].Body != "{}" {
		t.Errorf("follow = (%s, %q), want (PUT, {})", calls[0].Method, calls[0].Body)
	}
	if calls[1].Method != "DELETE" || calls[1].Body != "" {
		t.Errorf("unfollow = (%s, %q), want (DELETE, no body)", calls[1].Method, calls[1].Body)
	}

	p.mu.Lock()
	listCache := p.listCache
	p.mu.Unlock()
	if listCache != nil {
		t.Error("listCache not invalidated after follow")
	}
}

func TestArtistFollowRequestShapes(t *testing.T) {
	m := newMockAPI(t)
	m.handlers["/v1/me/following"] = func(t *testing.T, query url.Values) string {
		if got := query.Get("type"); got != "artist" {
			t.Errorf("type = %q, want artist", got)
		}
		return ``
	}

	if err := newTestProvider().FollowArtist(t.Context(), "art1"); err != nil {
		t.Fatal(err)
	}
	if err := newTestProvider().UnfollowArtist(t.Context(), "art1"); err != nil {
		t.Fatal(err)
	}

	calls := m.recordedCalls("/v1/me/following")
	if len(calls) != 2 {
		t.Fatalf("%d calls, want 2 (follow + unfollow)", len(calls))
	}
	for i, want := range []string{"PUT", "DELETE"} {
		if calls[i].Method != want {
			t.Errorf("call %d method = %s, want %s", i, calls[i].Method, want)
		}
		var body struct {
			IDs []string `json:"ids"`
		}
		decodeCallBody(t, calls[i], &body)
		if len(body.IDs) != 1 || body.IDs[0] != "art1" {
			t.Errorf("call %d ids body = %v, want [art1]", i, body.IDs)
		}
	}
}

func TestRemoveTrackFromPlaylist(t *testing.T) {
	tests := []struct {
		name        string
		position    int
		cacheTracks []playlist.Track // nil: no cache entry
		wantURI     string
		wantFetch   bool // expect the one-item /items fetch
		wantErr     string
	}{
		{
			name:        "resolved from cache",
			position:    1,
			cacheTracks: []playlist.Track{{Path: "spotify:track:t0"}, {Path: "spotify:track:t1"}, {Path: "spotify:track:t2"}},
			wantURI:     "spotify:track:t1",
		},
		{
			name:        "cache too short falls back to fetch",
			position:    5,
			cacheTracks: []playlist.Track{{Path: "spotify:track:t0"}},
			wantURI:     "spotify:track:t5",
			wantFetch:   true,
		},
		{
			name:      "no cache resolves via fetch",
			position:  2,
			wantURI:   "spotify:track:t2",
			wantFetch: true,
		},
		{
			name:     "negative position rejected",
			position: -1,
			wantErr:  "invalid position",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newMockAPI(t)
			m.handlers["/v1/playlists/pl1/tracks"] = func(t *testing.T, query url.Values) string {
				return `{"snapshot_id":"snap2"}`
			}
			if tt.wantFetch {
				m.handlers["/v1/playlists/pl1/items"] = func(t *testing.T, query url.Values) string {
					if got := query.Get("offset"); got != fmt.Sprintf("%d", tt.position) {
						t.Errorf("offset = %q, want %d", got, tt.position)
					}
					if got := query.Get("limit"); got != "1" {
						t.Errorf("limit = %q, want 1", got)
					}
					if got := query.Get("fields"); got != playlistItemsFields {
						t.Errorf("fields = %q, want the shared projection", got)
					}
					return fmt.Sprintf(`{"items":[{"item":{"id":"t%d","name":"T","type":"track","uri":"spotify:track:t%d"}}],"total":10}`,
						tt.position, tt.position)
				}
			}
			p := newTestProvider()
			if tt.cacheTracks != nil {
				p.mu.Lock()
				p.trackCache["pl1"] = &playlistCache{snapshotID: "snap1", tracks: tt.cacheTracks}
				p.mu.Unlock()
			}

			err := p.RemoveTrackFromPlaylist(t.Context(), "pl1", tt.position)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}

			// The removal request carries the resolved URI and the position.
			c := mustCall(t, m, "/v1/playlists/pl1/tracks")
			if c.Method != "DELETE" {
				t.Errorf("method = %s, want DELETE", c.Method)
			}
			var body struct {
				Tracks []struct {
					URI       string `json:"uri"`
					Positions []int  `json:"positions"`
				} `json:"tracks"`
			}
			decodeCallBody(t, c, &body)
			if len(body.Tracks) != 1 || body.Tracks[0].URI != tt.wantURI {
				t.Errorf("tracks body = %+v, want uri %q", body.Tracks, tt.wantURI)
			}
			if got := body.Tracks[0].Positions; len(got) != 1 || got[0] != tt.position {
				t.Errorf("positions = %v, want [%d]", got, tt.position)
			}

			if n := m.calls("/v1/playlists/pl1/items"); (n > 0) != tt.wantFetch {
				t.Errorf("items fetched %d times, wantFetch = %v", n, tt.wantFetch)
			}

			// The snapshot changed: the playlist's track cache is gone.
			p.mu.Lock()
			_, cached := p.trackCache["pl1"]
			listCache := p.listCache
			p.mu.Unlock()
			if cached {
				t.Error("trackCache entry not invalidated after remove")
			}
			if listCache != nil {
				t.Error("listCache not invalidated after remove")
			}
		})
	}
}

func TestRenamePlaylistByID(t *testing.T) {
	m := newMockAPI(t)
	m.handlers["/v1/playlists/pl1"] = func(t *testing.T, query url.Values) string {
		return `{"id":"pl1","name":"New Name"}`
	}
	p := newTestProvider()
	p.mu.Lock()
	p.listCache = []playlist.PlaylistInfo{{ID: "pl1", Name: "Old Name"}}
	p.listCacheAt = time.Now()
	p.mu.Unlock()

	if err := p.RenamePlaylistByID(t.Context(), "pl1", "New Name"); err != nil {
		t.Fatal(err)
	}

	c := mustCall(t, m, "/v1/playlists/pl1")
	if c.Method != "PUT" {
		t.Errorf("method = %s, want PUT", c.Method)
	}
	var body struct {
		Name string `json:"name"`
	}
	decodeCallBody(t, c, &body)
	if body.Name != "New Name" {
		t.Errorf("name body = %q, want New Name", body.Name)
	}

	p.mu.Lock()
	listCache := p.listCache
	p.mu.Unlock()
	if listCache != nil {
		t.Error("listCache not invalidated after rename")
	}
}
