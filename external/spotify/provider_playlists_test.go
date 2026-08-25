package spotify

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"golang.org/x/oauth2"
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
	}
	if len(got) != len(want) {
		t.Fatalf("Playlists() returned %d playlists, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].ID != want[i].id || got[i].Section != want[i].section {
			t.Errorf("playlist %d = (%q, %q), want (%q, %q)", i, got[i].ID, got[i].Section, want[i].id, want[i].section)
		}
	}
}
