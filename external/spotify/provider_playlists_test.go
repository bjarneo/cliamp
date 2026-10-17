package spotify

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/playlist"
	"golang.org/x/oauth2"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// stubSession returns a signed-in Session that sends its Web API and lyrics
// requests to rt. The test changes no global client, so it can run in
// parallel.
func stubSession(rt roundTripFunc) *Session {
	return &Session{
		tokenSource: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "token"}),
		web:         &http.Client{Transport: rt},
	}
}

func TestPlaylistsIncludesFollowedPlaylists(t *testing.T) {
	t.Parallel()
	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
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

	sess := stubSession(rt)
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

func TestPlaylistsCachesOnlySuccessfulResults(t *testing.T) {
	t.Parallel()
	calls := 0
	routes := playlistRoutes(apiResponse(http.StatusOK, `{"items":[],"total":0}`))
	for path, route := range routes {
		original := route
		routes[path] = func(req *http.Request) *http.Response {
			calls++
			return original(req)
		}
	}
	p := stubSpotifyAPI("own", routes)

	first, err := p.Playlists()
	if err != nil {
		t.Fatal(err)
	}
	first[0].Name = "mutated"
	requestCount := calls

	second, err := p.Playlists()
	if err != nil {
		t.Fatal(err)
	}
	if calls != requestCount {
		t.Fatalf("cached Playlists() made %d more requests", calls-requestCount)
	}
	if second[0].Name == "mutated" {
		t.Fatal("caller mutation changed the cached playlist list")
	}
}

func TestPlaylistsKeepsRowsWhenLibraryAuthExpires(t *testing.T) {
	t.Parallel()
	p := stubSpotifyAPI("own", playlistRoutes(apiResponse(http.StatusOK, `{"items":[],"total":0}`)))
	tokens := 0
	p.session.tokenSource = tokenSourceFunc(func() (*oauth2.Token, error) {
		tokens++
		if tokens == 3 {
			return nil, playlist.ErrNeedsAuth
		}
		return &oauth2.Token{AccessToken: "token"}, nil
	})

	lists, err := p.Playlists()
	if err == nil || errors.Is(err, playlist.ErrNeedsAuth) {
		t.Fatalf("Playlists() error = %v, want a partial-result warning", err)
	}
	if len(lists) == 0 || lists[0].ID != savedTracksPlaylistID {
		t.Fatalf("Playlists() rows = %+v, want the usable library rows", lists)
	}
}

func TestPersonalizedPlaylistsNeedsAuthWithoutSession(t *testing.T) {
	p := New(nil, "client", 320)
	if _, err := p.personalizedPlaylists(t.Context()); !errors.Is(err, playlist.ErrNeedsAuth) {
		t.Fatalf("personalizedPlaylists() error = %v, want ErrNeedsAuth", err)
	}
}

func TestPlaylistsNeedsAuthWhenAllSourcesNeedAuth(t *testing.T) {
	t.Parallel()
	p := stubSpotifyAPI("own", playlistRoutes(apiResponse(http.StatusOK, `{"items":[],"total":0}`)))
	p.session.tokenSource = nil

	lists, err := p.Playlists()
	if len(lists) != 0 || !errors.Is(err, playlist.ErrNeedsAuth) {
		t.Fatalf("Playlists() rows=%+v error=%v, want empty rows and ErrNeedsAuth", lists, err)
	}
}
