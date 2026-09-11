package jellyfin

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func jsonResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewBufferString(body)),
	}
}

func mockProvider(userID string, fn roundTripFunc) *Provider {
	c := NewClient("https://jf.example.com", "tok", userID, "", "")
	c.SetHTTPClient(&http.Client{Transport: fn})
	return newProvider(c)
}

func TestProviderName(t *testing.T) {
	p := newProvider(NewClient("https://jf.example.com", "tok", "user-1", "", ""))
	if p.Name() != "Jellyfin" {
		t.Fatalf("Name() = %q, want Jellyfin", p.Name())
	}
}

func TestProviderDefaultBrowseMode(t *testing.T) {
	p := newProvider(NewClient("https://jf.example.com", "tok", "user-1", "", ""))
	if got := p.DefaultBrowseMode(); got != provider.BrowseArtistAlbums {
		t.Fatalf("DefaultBrowseMode() = %d, want BrowseArtistAlbums", got)
	}
}

func TestProviderPlaylists(t *testing.T) {
	p := mockProvider("", func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/Users/Me":
			return jsonResponse(`{"Id":"user-1","Name":"Nomad"}`), nil
		case "/Users/user-1/Views":
			return jsonResponse(`{"Items":[{"Id":"lib-1","Name":"Music","CollectionType":"music"}]}`), nil
		case "/Items":
			return jsonResponse(`{"Items":[{"Id":"album-1","Name":"Kind of Blue","AlbumArtist":"Miles Davis","ProductionYear":1959,"ChildCount":5}]}`), nil
		default:
			t.Fatalf("unexpected path %s", req.URL.Path)
			return nil, nil
		}
	})

	lists, err := p.Playlists()
	if err != nil {
		t.Fatalf("Playlists() error: %v", err)
	}
	if len(lists) != 1 {
		t.Fatalf("expected 1 playlist, got %d", len(lists))
	}
	if lists[0].ID != "album-1" || lists[0].TrackCount != 5 {
		t.Fatalf("playlist = %+v", lists[0])
	}
	if lists[0].Name != "Miles Davis — Kind of Blue (1959)" {
		t.Fatalf("playlist name = %q", lists[0].Name)
	}
}

func TestProviderTracks(t *testing.T) {
	p := mockProvider("user-1", func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/Items" {
			t.Fatalf("unexpected path %s", req.URL.Path)
		}
		return jsonResponse(`{
			"Items": [
				{
					"Id":"track-1",
					"Name":"So What",
					"Album":"Kind of Blue",
					"Artists":["Miles Davis"],
					"ProductionYear":1959,
					"IndexNumber":1,
					"RunTimeTicks":5650000000
				}
			]
		}`), nil
	})

	tracks, err := p.Tracks("album-1")
	if err != nil {
		t.Fatalf("Tracks() error: %v", err)
	}
	if len(tracks) != 1 {
		t.Fatalf("expected 1 track, got %d", len(tracks))
	}
	tr := tracks[0]
	if tr.Title != "So What" || tr.Artist != "Miles Davis" || tr.Album != "Kind of Blue" || tr.TrackNumber != 1 || !tr.Stream {
		t.Fatalf("track = %+v", tr)
	}
	if got := tr.Meta(provider.MetaJellyfinID); got != "track-1" {
		t.Fatalf("track meta jellyfin id = %q, want track-1", got)
	}
}

func TestProviderCanReportPlayback(t *testing.T) {
	p := newProvider(NewClient("https://jf.example.com", "tok", "user-1", "", ""))
	if !p.CanReportPlayback(trackWithMeta(provider.MetaJellyfinID, "track-1")) {
		t.Fatal("CanReportPlayback() = false, want true")
	}
	if p.CanReportPlayback(trackWithMeta(provider.MetaNavidromeID, "nav-1")) {
		t.Fatal("CanReportPlayback() = true for non-Jellyfin track")
	}
}

func TestProviderRestoreTrack(t *testing.T) {
	p := newProvider(NewClient("https://jf.example.com/media", "new-token", "user-1", "", ""))
	tests := []struct {
		name  string
		track playlist.Track
		want  bool
	}{
		{
			name:  "current history URL",
			track: playlist.Track{Title: "Song", Path: "https://jf.example.com/media/Items/track-1/Download?api_key=new-token"},
			want:  true,
		},
		{
			name:  "legacy history URL",
			track: playlist.Track{Title: "Song", Path: "https://jf.example.com/media/Items/track-2/Download?api_key=old-token"},
			want:  true,
		},
		{
			name:  "other Jellyfin server",
			track: playlist.Track{Path: "https://other.example.com/Items/track-3/Download"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := p.RestoreTrack(tt.track)
			if ok != tt.want {
				t.Fatalf("RestoreTrack() ok = %v, want %v", ok, tt.want)
			}
			if !ok {
				return
			}
			if got.Title != tt.track.Title || !got.Stream {
				t.Fatalf("restored track = %+v", got)
			}
			if got.Meta(provider.MetaJellyfinID) == "" {
				t.Fatal("restored track is missing Jellyfin item metadata")
			}
			if !bytes.Contains([]byte(got.Path), []byte("api_key=new-token")) {
				t.Fatalf("restored path did not refresh credentials: %q", got.Path)
			}
		})
	}
}

// TestProviderRestoreTrackDefersAuthenticationUntilSourceResolution verifies that
// restoring a track from history defers authentication until source resolution.
func TestProviderRestoreTrackDefersAuthenticationUntilSourceResolution(t *testing.T) {
	p := newProvider(NewClient("https://jf.example.com", "", "", "user", "password"))
	p.client.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected startup request: %s", req.URL)
		return nil, nil
	})})
	oldURL := "https://jf.example.com/Items/track-1/Download?api_key=old-token"
	got, ok := p.RestoreTrack(playlist.Track{Path: oldURL, Title: "Song"})
	if !ok {
		t.Fatal("RestoreTrack() did not recognize history URL")
	}
	if got.Path != oldURL {
		t.Fatalf("RestoreTrack() path = %q, want saved URL before password authentication", got.Path)
	}
	p.client.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/Users/AuthenticateByName" {
			t.Fatalf("unexpected request: %s", req.URL)
		}
		return jsonResponse(`{"User":{"Id":"user-1"},"AccessToken":"new-token"}`), nil
	})})
	source, err := p.ResolveSource(got.Path)
	if err != nil {
		t.Fatalf("ResolveSource() error: %v", err)
	}
	if want := "https://jf.example.com/Items/track-1/Download?ApiKey=new-token&api_key=new-token"; source != want {
		t.Fatalf("ResolveSource() = %q, want %q", source, want)
	}
	if got.Path != oldURL || got.Title != "Song" || got.Meta(provider.MetaJellyfinID) != "track-1" || !got.Stream {
		t.Fatalf("source resolution changed the logical restored track: %+v", got)
	}
}

func TestProviderResolveSourcePropagatesAuthenticationFailure(t *testing.T) {
	p := newProvider(NewClient("https://jf.example.com", "", "", "user", "password"))
	authErr := errors.New("authentication unavailable")
	p.client.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return nil, authErr
	})})
	got, err := p.ResolveSource("https://jf.example.com/Items/track-1/Download?api_key=old-token")
	if !errors.Is(err, authErr) || got != "" {
		t.Fatalf("ResolveSource() = (%q, %v), want no source and authentication error", got, err)
	}
}

func trackWithMeta(key, value string) playlist.Track {
	return playlist.Track{ProviderMeta: map[string]string{key: value}}
}
