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
	m.handlers["/v1/playlists/pl1/items"] = func(t *testing.T, query url.Values) string {
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
	calls := m.recordedCalls("/v1/playlists/pl1/items")
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
	m.handlers["/v1/playlists/pl1/items"] = func(t *testing.T, query url.Values) string {
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
	decodeCallBody(t, mustCall(t, m, "/v1/playlists/pl1/items"), &body)
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
	if n := m.calls("/v1/playlists/pl1/items"); n != 0 {
		t.Errorf("made %d requests, want 0", n)
	}
}

func TestToggleTrackLike(t *testing.T) {
	tests := []struct {
		name       string
		contains   string // /v1/me/library/contains reply
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
			m.handlers["/v1/me/library/contains"] = func(t *testing.T, query url.Values) string {
				if got := query.Get("uris"); got != "spotify:track:t1" {
					t.Errorf("contains uris = %q, want spotify:track:t1", got)
				}
				return tt.contains
			}
			m.handlers["/v1/me/library"] = func(t *testing.T, query url.Values) string {
				if got := query.Get("uris"); got != "spotify:track:t1" {
					t.Errorf("flip uris = %q, want spotify:track:t1", got)
				}
				return ``
			}
			p := newTestProvider()
			p.mu.Lock()
			p.trackCache[savedTracksPlaylistID] = &playlistCache{tracks: []playlist.Track{{Path: "spotify:track:t1"}}}
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

			// The contains round-trip, then the body-less flip request with
			// the uri in the query string.
			c := mustCall(t, m, "/v1/me/library")
			if c.Method != tt.wantMethod {
				t.Errorf("flip method = %s, want %s", c.Method, tt.wantMethod)
			}
			if c.Body != "" {
				t.Errorf("flip body = %q, want none (uris go in the query string)", c.Body)
			}

			// YOUR MUSIC and recently-played caches are invalidated.
			p.mu.Lock()
			_, yourMusicCached := p.trackCache[savedTracksPlaylistID]
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
	if n := m.calls("/v1/me/library/contains"); n != 0 {
		t.Errorf("made %d contains requests, want 0", n)
	}
}

func TestPlaylistFollowRequestShapes(t *testing.T) {
	m := newMockAPI(t)
	m.handlers["/v1/me/library"] = func(t *testing.T, query url.Values) string {
		if got := query.Get("uris"); got != "spotify:playlist:pl1" {
			t.Errorf("uris = %q, want spotify:playlist:pl1", got)
		}
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

	calls := m.recordedCalls("/v1/me/library")
	if len(calls) != 2 {
		t.Fatalf("%d calls, want 2 (follow + unfollow)", len(calls))
	}
	for i, want := range []string{"PUT", "DELETE"} {
		if calls[i].Method != want {
			t.Errorf("call %d method = %s, want %s", i, calls[i].Method, want)
		}
		if calls[i].Body != "" {
			t.Errorf("call %d body = %q, want none (uris go in the query string)", i, calls[i].Body)
		}
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
	m.handlers["/v1/me/library"] = func(t *testing.T, query url.Values) string {
		if got := query.Get("uris"); got != "spotify:artist:art1" {
			t.Errorf("uris = %q, want spotify:artist:art1", got)
		}
		return ``
	}

	if err := newTestProvider().FollowArtist(t.Context(), "art1"); err != nil {
		t.Fatal(err)
	}
	if err := newTestProvider().UnfollowArtist(t.Context(), "art1"); err != nil {
		t.Fatal(err)
	}

	calls := m.recordedCalls("/v1/me/library")
	if len(calls) != 2 {
		t.Fatalf("%d calls, want 2 (follow + unfollow)", len(calls))
	}
	for i, want := range []string{"PUT", "DELETE"} {
		if calls[i].Method != want {
			t.Errorf("call %d method = %s, want %s", i, calls[i].Method, want)
		}
		if calls[i].Body != "" {
			t.Errorf("call %d body = %q, want none (uris go in the query string)", i, calls[i].Body)
		}
	}
}

func TestRemoveTrackFromPlaylist(t *testing.T) {
	tests := []struct {
		name     string
		position int
		track    playlist.Track
		wantURI  string
		wantErr  string
	}{
		{
			name:     "uri from track path",
			position: 1,
			track:    playlist.Track{Path: "spotify:track:t1"},
			wantURI:  "spotify:track:t1",
		},
		{
			name:     "episode uri passes through",
			position: 3,
			track:    playlist.Track{Path: "spotify:episode:e9"},
			wantURI:  "spotify:episode:e9",
		},
		{
			name:     "uri synthesized from provider meta",
			position: 0,
			track:    playlist.Track{ProviderMeta: map[string]string{metaSpotifyID: "tX"}},
			wantURI:  "spotify:track:tX",
		},
		{
			name:     "negative position rejected",
			position: -1,
			track:    playlist.Track{Path: "spotify:track:t1"},
			wantErr:  "invalid position",
		},
		{
			name:     "unresolvable uri rejected",
			position: 0,
			track:    playlist.Track{Path: "/music/local.mp3"},
			wantErr:  "no spotify URI",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newMockAPI(t)
			m.handlers["/v1/playlists/pl1/items"] = func(t *testing.T, query url.Values) string {
				return `{"snapshot_id":"snap2"}`
			}
			p := newTestProvider()
			p.mu.Lock()
			p.trackCache["pl1"] = &playlistCache{snapshotID: "snap1", tracks: []playlist.Track{{Path: "spotify:track:t0"}}}
			p.mu.Unlock()

			err := p.RemoveTrackFromPlaylist(t.Context(), "pl1", tt.position, tt.track)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}

			// The removal request carries the resolved URI in the items array
			// and no positions: the caller's position indexes a filtered view
			// and must not be trusted as a raw API position.
			c := mustCall(t, m, "/v1/playlists/pl1/items")
			if c.Method != "DELETE" {
				t.Errorf("method = %s, want DELETE", c.Method)
			}
			var body struct {
				Items []struct {
					URI string `json:"uri"`
				} `json:"items"`
			}
			decodeCallBody(t, c, &body)
			if len(body.Items) != 1 || body.Items[0].URI != tt.wantURI {
				t.Errorf("items body = %+v, want uri %q", body.Items, tt.wantURI)
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
