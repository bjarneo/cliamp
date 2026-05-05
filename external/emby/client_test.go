package emby

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"cliamp/internal/appmeta"
	"cliamp/playlist"
	"cliamp/provider"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func useTestClient(t *testing.T, fn roundTripFunc) {
	t.Helper()
	old := apiClient
	apiClient = &http.Client{Transport: fn}
	t.Cleanup(func() {
		apiClient = old
	})
}

func jsonResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewBufferString(body)),
	}
}

func noContentResponse() *http.Response {
	return &http.Response{
		StatusCode: http.StatusNoContent,
		Status:     "204 No Content",
		Body:       io.NopCloser(bytes.NewBuffer(nil)),
	}
}

func TestClientPing(t *testing.T) {
	c := NewClient("https://emby.example.com", "tok", "user-1", "", "")
	useTestClient(t, func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/System/Info" {
			t.Fatalf("Ping() called unexpected path %s, want /System/Info", req.URL.Path)
		}
		return jsonResponse(`{"ServerName":"My Emby","Version":"4.8.0.0"}`), nil
	})
	if err := c.Ping(); err != nil {
		t.Fatalf("Ping() error: %v", err)
	}
}

func TestClientUserIDAPIKeyFallback(t *testing.T) {
	// API key auth: /Users/Me returns 500, should fall back to /Users list.
	c := NewClient("https://emby.example.com", "tok", "", "", "")
	useTestClient(t, func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/Users/Me":
			return &http.Response{
				StatusCode: 500,
				Status:     "500 Internal Server Error",
				Body:       io.NopCloser(bytes.NewBuffer(nil)),
			}, nil
		case "/Users":
			return jsonResponse(`[{"Id":"user-1","Name":"Alice"},{"Id":"user-2","Name":"Bob"}]`), nil
		case "/Users/user-1/Views":
			return jsonResponse(`{"Items":[{"Id":"lib-1","Name":"Music","CollectionType":"music"}]}`), nil
		default:
			t.Fatalf("unexpected path %s", req.URL.Path)
			return nil, nil
		}
	})

	libs, err := c.MusicLibraries()
	if err != nil {
		t.Fatalf("MusicLibraries() error: %v", err)
	}
	if c.userID != "user-1" {
		t.Fatalf("userID = %q after API key fallback, want user-1", c.userID)
	}
	if len(libs) != 1 || libs[0].ID != "lib-1" {
		t.Fatalf("libraries = %+v", libs)
	}
}

func TestClientMusicLibraries(t *testing.T) {
	c := NewClient("https://emby.example.com", "tok", "", "", "")
	useTestClient(t, func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/Users/Me":
			return jsonResponse(`{"Id":"user-1","Name":"Nomad"}`), nil
		case "/Users/user-1/Views":
			if got := req.Header.Get("X-Emby-Token"); got != "tok" {
				t.Fatalf("X-Emby-Token = %q, want tok", got)
			}
			if got := req.Header.Get("Authorization"); !strings.HasPrefix(got, "Emby ") {
				t.Fatalf("Authorization = %q, want Emby scheme", got)
			}
			return jsonResponse(`{
				"Items": [
					{"Id":"music-1","Name":"Music","CollectionType":"music"},
					{"Id":"movies-1","Name":"Movies","CollectionType":"movies"}
				]
			}`), nil
		default:
			t.Fatalf("unexpected path %s", req.URL.Path)
			return nil, nil
		}
	})

	libs, err := c.MusicLibraries()
	if err != nil {
		t.Fatalf("MusicLibraries() error: %v", err)
	}
	if len(libs) != 1 {
		t.Fatalf("expected 1 music library, got %d", len(libs))
	}
	if libs[0].ID != "music-1" || libs[0].Name != "Music" {
		t.Fatalf("library = %+v, want music-1/Music", libs[0])
	}
}

func TestClientAlbumsByLibrary(t *testing.T) {
	c := NewClient("https://emby.example.com", "tok", "user-1", "", "")
	useTestClient(t, func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/Items" {
			t.Fatalf("unexpected path %s", req.URL.Path)
		}
		q := req.URL.Query()
		if got := q.Get("parentId"); got != "lib-1" {
			t.Fatalf("parentId = %q, want lib-1", got)
		}
		if got := q.Get("includeItemTypes"); got != "MusicAlbum" {
			t.Fatalf("includeItemTypes = %q, want MusicAlbum", got)
		}
		return jsonResponse(`{
			"Items": [
				{
					"Id":"album-1",
					"Name":"Kind of Blue",
					"AlbumArtist":"Miles Davis",
					"AlbumArtists":[{"Id":"artist-1","Name":"Miles Davis"}],
					"ProductionYear":1959,
					"ChildCount":5
				}
			]
		}`), nil
	})

	albums, err := c.AlbumsByLibrary("lib-1")
	if err != nil {
		t.Fatalf("AlbumsByLibrary() error: %v", err)
	}
	if len(albums) != 1 {
		t.Fatalf("expected 1 album, got %d", len(albums))
	}
	a := albums[0]
	if a.ID != "album-1" || a.Name != "Kind of Blue" || a.Artist != "Miles Davis" || a.ArtistID != "artist-1" || a.Year != 1959 || a.TrackCount != 5 {
		t.Fatalf("album = %+v", a)
	}
}

func TestClientTracks(t *testing.T) {
	c := NewClient("https://emby.example.com", "tok", "user-1", "", "")
	useTestClient(t, func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/Items" {
			t.Fatalf("unexpected path %s", req.URL.Path)
		}
		q := req.URL.Query()
		if got := q.Get("parentId"); got != "album-1" {
			t.Fatalf("parentId = %q, want album-1", got)
		}
		if got := q.Get("includeItemTypes"); got != "Audio" {
			t.Fatalf("includeItemTypes = %q, want Audio", got)
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

	tracks, err := c.Tracks("album-1")
	if err != nil {
		t.Fatalf("Tracks() error: %v", err)
	}
	if len(tracks) != 1 {
		t.Fatalf("expected 1 track, got %d", len(tracks))
	}
	tr := tracks[0]
	if tr.ID != "track-1" || tr.Name != "So What" || tr.Artist != "Miles Davis" || tr.Album != "Kind of Blue" || tr.Year != 1959 || tr.TrackNumber != 1 || tr.DurationSecs != 565 {
		t.Fatalf("track = %+v", tr)
	}
}

func TestClientStreamURL(t *testing.T) {
	c := NewClient("https://emby.example.com", "tok", "user-1", "", "")
	tests := []struct {
		itemID     string
		wantPrefix string
	}{
		{"track-1", "https://emby.example.com/Items/track-1/Download?"},
		{"album-99", "https://emby.example.com/Items/album-99/Download?"},
	}
	for _, tc := range tests {
		t.Run(tc.itemID, func(t *testing.T) {
			u := c.StreamURL(tc.itemID)
			if !strings.HasPrefix(u, tc.wantPrefix) {
				t.Fatalf("URL = %q, want prefix %q", u, tc.wantPrefix)
			}
			if !strings.Contains(u, "api_key=tok") {
				t.Fatalf("URL missing api_key: %q", u)
			}
		})
	}
}

func TestClientAuthenticatesWithPassword(t *testing.T) {
	c := NewClient("https://emby.example.com", "", "", "alice", "s3cret")
	authCalls := 0
	useTestClient(t, func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/Users/AuthenticateByName":
			authCalls++
			if req.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", req.Method)
			}
			if got := req.Header.Get("Authorization"); !strings.HasPrefix(got, "Emby ") {
				t.Fatalf("auth request Authorization = %q, want Emby scheme (not MediaBrowser)", got)
			}
			return jsonResponse(`{"User":{"Id":"user-1"},"AccessToken":"tok-1"}`), nil
		case "/Users/user-1/Views":
			if got := req.Header.Get("X-Emby-Token"); got != "tok-1" {
				t.Fatalf("X-Emby-Token = %q, want tok-1", got)
			}
			if got := req.Header.Get("Authorization"); !strings.HasPrefix(got, "Emby ") || !strings.Contains(got, `Token="tok-1"`) {
				t.Fatalf("Authorization = %q, want Emby scheme with token", got)
			}
			return jsonResponse(`{"Items":[{"Id":"music-1","Name":"Music","CollectionType":"music"}]}`), nil
		default:
			t.Fatalf("unexpected path %s", req.URL.Path)
			return nil, nil
		}
	})

	libs, err := c.MusicLibraries()
	if err != nil {
		t.Fatalf("MusicLibraries() error: %v", err)
	}
	if authCalls != 1 {
		t.Fatalf("authCalls = %d, want 1", authCalls)
	}
	if c.token != "tok-1" || c.userID != "user-1" {
		t.Fatalf("client auth state = token:%q userID:%q", c.token, c.userID)
	}
	if len(libs) != 1 || libs[0].ID != "music-1" {
		t.Fatalf("libraries = %+v", libs)
	}
}

func TestClientReportNowPlaying(t *testing.T) {
	appmeta.SetVersion("v1.31.2")
	t.Cleanup(func() { appmeta.SetVersion("dev") })
	c := NewClient("https://emby.example.com", "tok", "user-1", "", "")
	track := playlist.Track{
		ProviderMeta: map[string]string{provider.MetaEmbyID: "track-1"},
	}

	useTestClient(t, func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", req.Method)
		}
		if req.URL.Path != "/Sessions/Playing" {
			t.Fatalf("path = %s, want /Sessions/Playing", req.URL.Path)
		}
		if got := req.Header.Get("X-Emby-Token"); got != "tok" {
			t.Fatalf("X-Emby-Token = %q, want tok", got)
		}
		if got := req.Header.Get("Authorization"); !strings.HasPrefix(got, "Emby ") {
			t.Fatalf("Authorization scheme = %q, want Emby prefix", got)
		}
		if got := req.Header.Get("Authorization"); !strings.Contains(got, `Version="v1.31.2"`) {
			t.Fatalf("Authorization = %q, want release version", got)
		}

		var payload playbackInfo
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if payload.ItemID != "track-1" || !payload.CanSeek || payload.PositionTicks != 15*time.Second.Nanoseconds()/100 || payload.PlayMethod != "DirectPlay" {
			t.Fatalf("payload = %+v", payload)
		}
		return noContentResponse(), nil
	})

	if err := c.ReportNowPlaying(track, 15*time.Second, true); err != nil {
		t.Fatalf("ReportNowPlaying() error: %v", err)
	}
}

func TestClientReportScrobble(t *testing.T) {
	c := NewClient("https://emby.example.com", "tok", "user-1", "", "")
	track := playlist.Track{
		ProviderMeta: map[string]string{provider.MetaEmbyID: "track-1"},
	}

	call := 0
	useTestClient(t, func(req *http.Request) (*http.Response, error) {
		call++
		switch call {
		case 1:
			if req.URL.Path != "/Sessions/Playing/Progress" {
				t.Fatalf("progress path = %s", req.URL.Path)
			}
			var payload playbackInfo
			if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
				t.Fatalf("decode progress payload: %v", err)
			}
			if payload.ItemID != "track-1" || !payload.CanSeek || payload.PositionTicks != 42*time.Second.Nanoseconds()/100 {
				t.Fatalf("progress payload = %+v", payload)
			}
		case 2:
			if req.URL.Path != "/Sessions/Playing/Stopped" {
				t.Fatalf("stopped path = %s", req.URL.Path)
			}
			var payload playbackStopInfo
			if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
				t.Fatalf("decode stop payload: %v", err)
			}
			if payload.ItemID != "track-1" || payload.PositionTicks != 42*time.Second.Nanoseconds()/100 || payload.Failed {
				t.Fatalf("stop payload = %+v", payload)
			}
		default:
			t.Fatalf("unexpected extra call %d", call)
		}
		return noContentResponse(), nil
	})

	if err := c.ReportScrobble(track, 42*time.Second, true); err != nil {
		t.Fatalf("ReportScrobble() error: %v", err)
	}
	if call != 2 {
		t.Fatalf("call count = %d, want 2", call)
	}
}
