package embyapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/internal/appmeta"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func mock(c *Client, fn roundTripFunc) *Client {
	c.SetHTTPClient(&http.Client{Transport: fn})
	return c
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

// --- Dialect-specific: Ping endpoint ---

func TestEmbyPingUsesSystemInfo(t *testing.T) {
	c := mock(NewEmbyClient("https://emby.example.com", "tok", "user-1", "", ""), func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/System/Info" {
			t.Fatalf("Ping path = %s, want /System/Info", req.URL.Path)
		}
		return jsonResponse(`{"ServerName":"My Emby","Version":"4.8.0.0"}`), nil
	})
	if err := c.Ping(); err != nil {
		t.Fatalf("Ping() error: %v", err)
	}
}

func TestJellyfinPingUsesUsersMe(t *testing.T) {
	c := mock(NewJellyfinClient("https://jf.example.com", "tok", "user-1", "", ""), func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/Users/Me" {
			t.Fatalf("Ping path = %s, want /Users/Me", req.URL.Path)
		}
		return jsonResponse(`{"Id":"user-1","Name":"Nomad"}`), nil
	})
	if err := c.Ping(); err != nil {
		t.Fatalf("Ping() error: %v", err)
	}
}

// TestJellyfinPingAPIKeyFallsBackToUsers checks that Ping falls back to /Users when
// /Users/Me returns 400 for server-level API keys.
func TestJellyfinPingAPIKeyFallsBackToUsers(t *testing.T) {
	// API keys aren't owned by a user, so /Users/Me returns 400; /Users must
	// succeed to prove the key is valid.
	c := mock(NewJellyfinClient("https://jf.example.com", "tok", "", "", ""), func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/Users/Me":
			return &http.Response{StatusCode: 400, Status: "400 Bad Request", Body: io.NopCloser(bytes.NewBuffer([]byte("Token is not owned by a user.")))}, nil
		case "/Users":
			return jsonResponse(`[{"Id":"user-1","Name":"Alice"}]`), nil
		default:
			t.Fatalf("unexpected path %s", req.URL.Path)
			return nil, nil
		}
	})
	if err := c.Ping(); err != nil {
		t.Fatalf("Ping() with API key error: %v", err)
	}
}

// TestJellyfinPingAPIKeyBadTokenFails verifies that Ping fails when an invalid API key
// is rejected by both /Users/Me and /Users.
func TestJellyfinPingAPIKeyBadTokenFails(t *testing.T) {
	c := mock(NewJellyfinClient("https://jf.example.com", "bad-tok", "", "", ""), func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/Users/Me":
			return &http.Response{StatusCode: 400, Status: "400 Bad Request", Body: io.NopCloser(bytes.NewBuffer(nil))}, nil
		case "/Users":
			return &http.Response{StatusCode: 401, Status: "401 Unauthorized", Body: io.NopCloser(bytes.NewBuffer(nil))}, nil
		default:
			t.Fatalf("unexpected path %s", req.URL.Path)
			return nil, nil
		}
	})
	if err := c.Ping(); err == nil {
		t.Fatal("Ping() with invalid API key succeeded, want error")
	}
}

// TestJellyfinUserIDAPIKeyFallback verifies that UserID falls back to /Users and picks the
// first user when authenticated with an API key.
func TestJellyfinUserIDAPIKeyFallback(t *testing.T) {
	// /Users/Me returns 400 for API keys; fall back to /Users and pick the
	// first user.
	c := mock(NewJellyfinClient("https://jf.example.com", "tok", "", "", ""), func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/Users/Me":
			return &http.Response{StatusCode: 400, Status: "400 Bad Request", Body: io.NopCloser(bytes.NewBuffer(nil))}, nil
		case "/Users":
			return jsonResponse(`[{"Id":"user-1","Name":"Alice"}]`), nil
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

// --- Dialect-specific: Emby API-key user-id fallback ---

func TestEmbyUserIDAPIKeyFallback(t *testing.T) {
	// /Users/Me returns 500 for server-level API keys; fall back to /Users.
	c := mock(NewEmbyClient("https://emby.example.com", "tok", "", "", ""), func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/Users/Me":
			return &http.Response{StatusCode: 500, Status: "500 Internal Server Error", Body: io.NopCloser(bytes.NewBuffer(nil))}, nil
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

// --- Dialect-specific: auth header scheme ---

func TestEmbyAuthHeaderScheme(t *testing.T) {
	c := mock(NewEmbyClient("https://emby.example.com", "tok", "", "", ""), func(req *http.Request) (*http.Response, error) {
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
			return jsonResponse(`{"Items":[{"Id":"music-1","Name":"Music","CollectionType":"music"}]}`), nil
		default:
			t.Fatalf("unexpected path %s", req.URL.Path)
			return nil, nil
		}
	})
	if _, err := c.MusicLibraries(); err != nil {
		t.Fatalf("MusicLibraries() error: %v", err)
	}
}

func TestJellyfinAuthHeaderScheme(t *testing.T) {
	c := mock(NewJellyfinClient("https://jf.example.com", "tok", "", "", ""), func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/Users/Me":
			return jsonResponse(`{"Id":"user-1","Name":"Nomad"}`), nil
		case "/Users/user-1/Views":
			if got := req.Header.Get("X-Emby-Token"); got != "tok" {
				t.Fatalf("X-Emby-Token = %q, want tok", got)
			}
			if got := req.Header.Get("X-Emby-Authorization"); !strings.HasPrefix(got, "MediaBrowser ") {
				t.Fatalf("X-Emby-Authorization = %q, want MediaBrowser scheme", got)
			}
			return jsonResponse(`{"Items":[{"Id":"music-1","Name":"Music","CollectionType":"music"}]}`), nil
		default:
			t.Fatalf("unexpected path %s", req.URL.Path)
			return nil, nil
		}
	})
	if _, err := c.MusicLibraries(); err != nil {
		t.Fatalf("MusicLibraries() error: %v", err)
	}
}

// --- Dialect-specific: password auth ---

func TestEmbyAuthenticatesWithPassword(t *testing.T) {
	c := mock(NewEmbyClient("https://emby.example.com", "", "", "alice", "s3cret"), func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/Users/AuthenticateByName":
			if req.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", req.Method)
			}
			if got := req.Header.Get("Authorization"); !strings.HasPrefix(got, "Emby ") {
				t.Fatalf("auth request Authorization = %q, want Emby scheme", got)
			}
			return jsonResponse(`{"User":{"Id":"user-1"},"AccessToken":"tok-1"}`), nil
		case "/Users/user-1/Views":
			if got := req.Header.Get("Authorization"); !strings.HasPrefix(got, "Emby ") || !strings.Contains(got, `Token="tok-1"`) {
				t.Fatalf("Authorization = %q, want Emby scheme with token", got)
			}
			return jsonResponse(`{"Items":[{"Id":"music-1","Name":"Music","CollectionType":"music"}]}`), nil
		default:
			t.Fatalf("unexpected path %s", req.URL.Path)
			return nil, nil
		}
	})
	if _, err := c.MusicLibraries(); err != nil {
		t.Fatalf("MusicLibraries() error: %v", err)
	}
	if c.token != "tok-1" || c.userID != "user-1" {
		t.Fatalf("client auth state = token:%q userID:%q", c.token, c.userID)
	}
}

func TestJellyfinAuthenticatesWithPassword(t *testing.T) {
	c := mock(NewJellyfinClient("https://jf.example.com", "", "", "finamp", "1qazxsw2"), func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/Users/AuthenticateByName":
			if got := req.Header.Get("X-Emby-Authorization"); !strings.HasPrefix(got, "MediaBrowser ") {
				t.Fatalf("auth request X-Emby-Authorization = %q, want MediaBrowser scheme", got)
			}
			return jsonResponse(`{"User":{"Id":"user-1"},"AccessToken":"tok-1"}`), nil
		case "/Users/user-1/Views":
			if got := req.Header.Get("X-Emby-Token"); got != "tok-1" {
				t.Fatalf("X-Emby-Token = %q, want tok-1", got)
			}
			return jsonResponse(`{"Items":[{"Id":"music-1","Name":"Music","CollectionType":"music"}]}`), nil
		default:
			t.Fatalf("unexpected path %s", req.URL.Path)
			return nil, nil
		}
	})
	if _, err := c.MusicLibraries(); err != nil {
		t.Fatalf("MusicLibraries() error: %v", err)
	}
	if c.token != "tok-1" || c.userID != "user-1" {
		t.Fatalf("client auth state = token:%q userID:%q", c.token, c.userID)
	}
}

func TestConcurrentJellyfinAuthenticationUsesSingleRequest(t *testing.T) {
	var authCalls atomic.Int32
	c := mock(NewJellyfinClient("https://jf.example.com", "", "", "finamp", "1qazxsw2"), func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/Users/AuthenticateByName" {
			t.Fatalf("unexpected path %s", req.URL.Path)
		}
		authCalls.Add(1)
		time.Sleep(25 * time.Millisecond)
		return jsonResponse(`{"User":{"Id":"user-1"},"AccessToken":"tok-1"}`), nil
	})

	const callers = 16
	start := make(chan struct{})
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	wg.Add(callers)
	for range callers {
		go func() {
			defer wg.Done()
			<-start
			errs <- c.ensureAuth()
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Errorf("ensureAuth() error: %v", err)
		}
	}
	if got := authCalls.Load(); got != 1 {
		t.Fatalf("authentication requests = %d, want 1", got)
	}
}

// --- Dialect-specific: scrobble metadata key + auth header ---

func TestEmbyReportNowPlaying(t *testing.T) {
	appmeta.SetVersion("v1.31.2")
	t.Cleanup(func() { appmeta.SetVersion("dev") })
	c := mock(NewEmbyClient("https://emby.example.com", "tok", "user-1", "", ""), func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/Sessions/Playing" {
			t.Fatalf("path = %s, want /Sessions/Playing", req.URL.Path)
		}
		if got := req.Header.Get("Authorization"); !strings.HasPrefix(got, "Emby ") || !strings.Contains(got, `Version="v1.31.2"`) {
			t.Fatalf("Authorization = %q, want Emby scheme with release version", got)
		}
		var payload playbackInfo
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if payload.ItemID != "track-1" || !payload.CanSeek || payload.PositionTicks != 15*time.Second.Nanoseconds()/100 {
			t.Fatalf("payload = %+v", payload)
		}
		return noContentResponse(), nil
	})
	track := playlist.Track{ProviderMeta: map[string]string{provider.MetaEmbyID: "track-1"}}
	if err := c.ReportNowPlaying(track, 15*time.Second, true); err != nil {
		t.Fatalf("ReportNowPlaying() error: %v", err)
	}
}

func TestJellyfinReportNowPlaying(t *testing.T) {
	c := mock(NewJellyfinClient("https://jf.example.com", "tok", "user-1", "", ""), func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/Sessions/Playing" {
			t.Fatalf("path = %s, want /Sessions/Playing", req.URL.Path)
		}
		if got := req.Header.Get("X-Emby-Token"); got != "tok" {
			t.Fatalf("X-Emby-Token = %q, want tok", got)
		}
		var payload playbackInfo
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if payload.ItemID != "track-1" {
			t.Fatalf("payload ItemID = %q, want track-1 (from jellyfin meta key)", payload.ItemID)
		}
		return noContentResponse(), nil
	})
	track := playlist.Track{ProviderMeta: map[string]string{provider.MetaJellyfinID: "track-1"}}
	if err := c.ReportNowPlaying(track, 15*time.Second, true); err != nil {
		t.Fatalf("ReportNowPlaying() error: %v", err)
	}
}

// --- Shared behavior (parsing/caching/URLs): tested once, dialect-agnostic ---

func TestAlbumsByLibrary(t *testing.T) {
	c := mock(NewJellyfinClient("https://jf.example.com", "tok", "user-1", "", ""), func(req *http.Request) (*http.Response, error) {
		q := req.URL.Query()
		if req.URL.Path != "/Items" || q.Get("parentId") != "lib-1" || q.Get("includeItemTypes") != "MusicAlbum" {
			t.Fatalf("unexpected request %s?%s", req.URL.Path, req.URL.RawQuery)
		}
		return jsonResponse(`{"Items":[{"Id":"album-1","Name":"Kind of Blue","AlbumArtist":"Miles Davis","AlbumArtists":[{"Id":"artist-1","Name":"Miles Davis"}],"ProductionYear":1959,"ChildCount":5}]}`), nil
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

func TestTracksParsing(t *testing.T) {
	c := mock(NewJellyfinClient("https://jf.example.com", "tok", "user-1", "", ""), func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/Items" || req.URL.Query().Get("includeItemTypes") != "Audio" {
			t.Fatalf("unexpected request %s?%s", req.URL.Path, req.URL.RawQuery)
		}
		return jsonResponse(`{"Items":[{"Id":"track-1","Name":"So What","Album":"Kind of Blue","Artists":["Miles Davis"],"ProductionYear":1959,"IndexNumber":1,"RunTimeTicks":5650000000}]}`), nil
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

// TestStreamURL verifies that StreamURL builds the download URL with both
// legacy api_key and modern ApiKey query parameters.
func TestStreamURL(t *testing.T) {
	c := NewEmbyClient("https://emby.example.com", "tok", "user-1", "", "")
	u := c.StreamURL("track-1")
	if !strings.HasPrefix(u, "https://emby.example.com/Items/track-1/Download?") {
		t.Fatalf("URL = %q, want Download route prefix", u)
	}
	if !strings.Contains(u, "api_key=tok") {
		t.Fatalf("URL missing api_key: %q", u)
	}
	if !strings.Contains(u, "ApiKey=tok") {
		t.Fatalf("URL missing ApiKey: %q", u)
	}
}

func TestStreamURLFromCurrentAuth(t *testing.T) {
	withToken := NewJellyfinClient("https://jf.example.com", "token", "user-1", "", "")
	if got, ok := withToken.StreamURLFromCurrentAuth("track-1"); !ok || !strings.Contains(got, "api_key=token") {
		t.Fatalf("StreamURLFromCurrentAuth() = (%q, %v), want current token", got, ok)
	}

	passwordOnly := NewJellyfinClient("https://jf.example.com", "", "", "user", "password")
	if got, ok := passwordOnly.StreamURLFromCurrentAuth("track-1"); ok || got != "" {
		t.Fatalf("StreamURLFromCurrentAuth() = (%q, %v), want no URL before authentication", got, ok)
	}
}

func TestStreamItemID(t *testing.T) {
	c := NewJellyfinClient("https://jf.example.com/media", "new-token", "user-1", "", "")
	tests := []struct {
		name string
		url  string
		want string
	}{
		{name: "matching server", url: "https://jf.example.com/media/Items/track-1/Download?api_key=old-token", want: "track-1"},
		{name: "case insensitive route", url: "https://JF.EXAMPLE.COM/media/items/track-2/download", want: "track-2"},
		{name: "different server", url: "https://other.example.com/media/Items/track-1/Download"},
		{name: "outside base path", url: "https://jf.example.com/Items/track-1/Download"},
		{name: "extra path", url: "https://jf.example.com/media/Items/track-1/Download/more"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := c.StreamItemID(tt.url)
			if got != tt.want || ok != (tt.want != "") {
				t.Fatalf("StreamItemID(%q) = (%q, %v), want (%q, %v)", tt.url, got, ok, tt.want, tt.want != "")
			}
		})
	}
}

// TestResolveSourceAuthenticatesWithPassword verifies that source resolution
// authenticates via password when no token is present and appends auth params.
func TestResolveSourceAuthenticatesWithPassword(t *testing.T) {
	for _, baseURL := range []string{"https://jf.example.com/media", "http://jf.lan:8096/media"} {
		t.Run(baseURL, func(t *testing.T) {
			authCalls := 0
			c := mock(NewJellyfinClient(baseURL+"/", "", "", "user", "password"), func(req *http.Request) (*http.Response, error) {
				authCalls++
				if req.Method != http.MethodPost || req.URL.String() != baseURL+"/Users/AuthenticateByName" {
					t.Fatalf("unexpected authentication request: %s %s", req.Method, req.URL)
				}
				var credentials map[string]string
				if err := json.NewDecoder(req.Body).Decode(&credentials); err != nil {
					t.Fatal(err)
				}
				if credentials["Username"] != "user" || credentials["Pw"] != "password" {
					t.Fatalf("unexpected credentials: %v", credentials)
				}
				return jsonResponse(`{"User":{"Id":"user-1"},"AccessToken":"new-token"}`), nil
			})
			for _, itemID := range []string{"track-1", "track-2"} {
				savedURL := baseURL + "/Items/" + itemID + "/Download?api_key=old-token"
				got, err := c.ResolveSource(savedURL)
				if err != nil {
					t.Fatalf("ResolveSource() error: %v", err)
				}
				if want := baseURL + "/Items/" + itemID + "/Download?ApiKey=new-token&api_key=new-token"; got != want {
					t.Fatalf("ResolveSource() = %q, want %q", got, want)
				}
			}
			if authCalls != 1 {
				t.Fatalf("authentication requests = %d, want 1", authCalls)
			}
		})
	}
}

func TestResolveSourceAuthenticationFailure(t *testing.T) {
	c := mock(NewJellyfinClient("https://jf.example.com", "", "", "user", "password"), func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusUnauthorized,
			Status:     "401 Unauthorized",
			Body:       io.NopCloser(strings.NewReader("")),
		}, nil
	})
	got, err := c.ResolveSource("https://jf.example.com/Items/track-1/Download?api_key=old-token")
	if err == nil || !strings.Contains(err.Error(), "jellyfin: auth: http status 401 Unauthorized") {
		t.Fatalf("ResolveSource() error = %v, want authentication failure", err)
	}
	if got != "" {
		t.Fatalf("ResolveSource() = %q, want no source after authentication failure", got)
	}
}

// TestResolveSourceWithTokenDoesNotRequest ensures that resolving a source URL
// when an auth token already exists does not send an extra authentication request.
func TestResolveSourceWithTokenDoesNotRequest(t *testing.T) {
	c := mock(NewJellyfinClient("https://jf.example.com/media", "new-token", "", "", ""), func(req *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected request with configured token: %s", req.URL)
		return nil, nil
	})
	got, err := c.ResolveSource("https://jf.example.com/media/Items/track-1/Download?api_key=old-token")
	if err != nil {
		t.Fatalf("ResolveSource() error: %v", err)
	}
	if want := "https://jf.example.com/media/Items/track-1/Download?ApiKey=new-token&api_key=new-token"; got != want {
		t.Fatalf("ResolveSource() = %q, want %q", got, want)
	}
}

func TestResolveSourcePassesThroughUnrelatedSources(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{name: "foreign host", url: "https://other.example.com:8920/media/Items/track-1/Download?api_key=old-token"},
		{name: "lookalike host", url: "https://jf.example.com.evil:8920/media/Items/track-1/Download?api_key=old-token"},
		{name: "different scheme", url: "http://jf.example.com:8920/media/Items/track-1/Download?api_key=old-token"},
		{name: "different port", url: "https://jf.example.com:8921/media/Items/track-1/Download?api_key=old-token"},
		{name: "missing port", url: "https://jf.example.com/media/Items/track-1/Download?api_key=old-token"},
		{name: "outside base path", url: "https://jf.example.com:8920/Items/track-1/Download?api_key=old-token"},
		{name: "lookalike base path", url: "https://jf.example.com:8920/media-other/Items/track-1/Download?api_key=old-token"},
		{name: "unrelated route", url: "https://jf.example.com:8920/media/radio.mp3?token=original&x=%2f"},
		{name: "invalid URL", url: "%"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := mock(NewJellyfinClient("https://jf.example.com:8920/media", "", "", "user", "password"), func(req *http.Request) (*http.Response, error) {
				t.Fatalf("unexpected authentication for unrelated source: %s", req.URL)
				return nil, nil
			})
			got, err := c.ResolveSource(tt.url)
			if err != nil || got != tt.url {
				t.Fatalf("ResolveSource() = (%q, %v), want unchanged %q", got, err, tt.url)
			}
		})
	}
}

func TestReportScrobble(t *testing.T) {
	c := NewJellyfinClient("https://jf.example.com", "tok", "user-1", "", "")
	call := 0
	mock(c, func(req *http.Request) (*http.Response, error) {
		call++
		switch call {
		case 1:
			if req.URL.Path != "/Sessions/Playing/Progress" {
				t.Fatalf("progress path = %s", req.URL.Path)
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
	track := playlist.Track{ProviderMeta: map[string]string{provider.MetaJellyfinID: "track-1"}}
	if err := c.ReportScrobble(track, 42*time.Second, true); err != nil {
		t.Fatalf("ReportScrobble() error: %v", err)
	}
	if call != 2 {
		t.Fatalf("call count = %d, want 2", call)
	}
}

func TestIsStreamURL(t *testing.T) {
	if !IsStreamURL("https://x/Items/abc/Download?api_key=z") {
		t.Fatal("download URL should be a stream URL")
	}
	if IsStreamURL("https://x/Items/abc") {
		t.Fatal("non-download URL should not be a stream URL")
	}
}
