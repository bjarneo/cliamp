package spotify

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	"github.com/bjarneo/cliamp/playlist"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestPlaylistsIncludesFollowedPlaylists(t *testing.T) {
	originalTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		var body string
		switch req.URL.Path {
		case "/v1/me":
			body = `{"id":"me"}`
		case "/v1/me/tracks":
			body = `{"total":1}`
		case "/v1/me/playlists":
			body = `{"items":[` +
				`{"id":"owned","name":"Owned","snapshot_id":"one","owner":{"id":"me"},"items":{"total":2}},` +
				`{"id":"followed","name":"Followed","snapshot_id":"two","owner":{"id":"other"},"items":{"total":3}}` +
				`],"total":2}`
		case "/v1/me/albums":
			// Returned most-recently-added first; the provider re-sorts by artist.
			body = `{"items":[` +
				`{"album":{"id":"al0","name":"Punk In Drublic","total_tracks":17,"artists":[{"name":"NOFX"}]}},` +
				`{"album":{"id":"al1","name":"Suffer","total_tracks":15,"artists":[{"name":"Bad Religion"}]}}` +
				`],"total":2}`
		default:
			return nil, fmt.Errorf("unexpected Spotify API path %q", req.URL.Path)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    req,
		}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	sess := &Session{tokenSource: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "token"})}
	got, err := New(sess, "client", 320).Playlists()
	if err != nil {
		t.Fatal(err)
	}

	want := []struct {
		id      string
		section string
	}{
		{id: "YOUR MUSIC", section: "Library"},
		{id: "owned", section: "Your playlists"},
		{id: "followed", section: "Followed playlists"},
		// Saved albums sort alphabetically by artist: Bad Religion before NOFX.
		{id: "spotify:album:al1", section: "Saved albums"},
		{id: "spotify:album:al0", section: "Saved albums"},
	}
	if len(got) != len(want) {
		t.Fatalf("Playlists() returned %d playlists, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].ID != want[i].id || got[i].Section != want[i].section {
			t.Errorf("playlist %d = (%q, %q), want (%q, %q)", i, got[i].ID, got[i].Section, want[i].id, want[i].section)
		}
	}

	// The saved album row carries its artist and track count for display.
	album := got[len(got)-1]
	if album.Name != "NOFX - Punk In Drublic" {
		t.Errorf("saved album name = %q, want %q", album.Name, "NOFX - Punk In Drublic")
	}
	if album.TrackCount != 17 {
		t.Errorf("saved album track count = %d, want 17", album.TrackCount)
	}
}

// Auto mode leads the listing with the rootlist. When that endpoint is
// unavailable the whole library must still arrive through the Web API, since a
// lesser listing beats none at all.
func TestPlaylistsFallsBackToWebWhenRootlistUnavailable(t *testing.T) {
	originalTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		var body string
		switch req.URL.Path {
		case "/v1/me":
			body = `{"id":"me"}`
		case "/v1/me/tracks":
			body = `{"total":1}`
		case "/v1/me/playlists":
			body = `{"items":[` +
				`{"id":"owned","name":"Owned","snapshot_id":"one","owner":{"id":"me"},"items":{"total":2}}` +
				`],"total":1}`
		case "/v1/me/albums":
			body = `{"items":[],"total":0}`
		default:
			return nil, fmt.Errorf("unexpected Spotify API path %q", req.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK",
			Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	// The session has no librespot connection, so the rootlist read fails and
	// auto mode falls back to the Web API listing.
	sess := &Session{tokenSource: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "token"})}
	got, err := New(sess, "client", 320).Playlists()
	if err != nil {
		t.Fatalf("listing fell back to an error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d playlists, want 2: %#v", len(got), got)
	}
	if got[0].ID != "YOUR MUSIC" || got[0].Section != "Library" || got[0].TrackCount != 1 {
		t.Errorf("liked songs row = %+v", got[0])
	}
	if got[1].ID != "owned" || got[1].Section != "Your playlists" {
		t.Errorf("playlist row = %+v", got[1])
	}
}

// The listing's cache decision must spare a playlist the cache has never
// seen: its resolved URIs belong to a first read that may be paging the list
// right now, and deleting them would make that read re-resolve and splice two
// snapshots into one committed list. A playlist whose snapshot moved is the
// opposite: the edit is proven, so the whole read is discarded, not just the
// resolve -- the same protection a revision change gets.
func TestWebListingSparesAFirstReadAndDiscardsAMovedSnapshot(t *testing.T) {
	originalTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		var body string
		switch req.URL.Path {
		case "/v1/me":
			body = `{"id":"me"}`
		case "/v1/me/tracks":
			body = `{"total":0}`
		case "/v1/me/playlists":
			body = `{"items":[` +
				`{"id":"fresh","name":"Fresh","snapshot_id":"three","owner":{"id":"me"},"items":{"total":1}},` +
				`{"id":"moved","name":"Moved","snapshot_id":"two","owner":{"id":"me"},"items":{"total":1}}` +
				`],"total":2}`
		case "/v1/me/albums":
			body = `{"items":[],"total":0}`
		default:
			return nil, fmt.Errorf("unexpected Spotify API path %q", req.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK",
			Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	sess := &Session{tokenSource: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "token"})}
	p := New(sess, "client", 320)

	// "fresh" is midway through its very first read: nothing is cached yet,
	// so the read exists only as the resolve and the accumulation.
	p.pending["fresh"] = &pendingTracks{
		total: 1, want: 1,
		tracks: []playlist.Track{{Path: "spotify:track:f0"}},
		uris:   []string{"spotify:track:f0"},
	}

	// "moved" is cached at "one"; the listing is about to report "two".
	p.trackCache["moved"] = &playlistCache{snapshotID: "one", tracks: []playlist.Track{{Path: "spotify:track:m0"}}, total: 1}
	p.pending["moved"] = &pendingTracks{
		total: 1, want: 1,
		tracks: []playlist.Track{{Path: "spotify:track:m0"}},
		uris:   []string{"spotify:track:m0"},
	}

	if _, err := p.Playlists(); err != nil {
		t.Fatal(err)
	}

	if pend := p.pending["fresh"]; pend == nil || pend.uris == nil {
		t.Error("the listing discarded a first read, whose snapshot nothing said was stale")
	}

	if p.pending["moved"] != nil {
		t.Error("the read, and the resolve it holds, survived a moved snapshot")
	}
	if c := p.trackCache["moved"]; c == nil || c.snapshotID != "two" || len(c.tracks) != 0 {
		t.Errorf("a moved snapshot left the entry %v, want a fresh marker at the new snapshot", c)
	}
}
