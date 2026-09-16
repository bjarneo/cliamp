package subsonicapi

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// fakeBandcampServer serves the standard fake Subsonic responses; the
// Bandcamp beta quirks (bare 500 on bad auth, envelope-less "bad version"
// fall-through) are built inline by the tests that need them.
func fakeBandcampServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(subsonicHandler(t))
}

func newBandcampTestClient(srv *httptest.Server) *Client {
	return NewBandcampClient(Config{BaseURL: srv.URL, User: "fan", Password: "subsonic-pass"})
}

func TestBandcampName(t *testing.T) {
	c := NewBandcampClient(Config{BaseURL: "http://localhost", User: "u", Password: "p"})
	if c.Name() != "Bandcamp" {
		t.Errorf("Name() = %q, want Bandcamp", c.Name())
	}
}

func TestBandcampAuthParams(t *testing.T) {
	c := NewBandcampClient(Config{BaseURL: "https://bandcamp.com/api/subsonic", User: "fan", Password: "p"})
	u := c.buildURL("ping", nil)
	// The bandcamp dialect appends .view uniformly: the beta's router only
	// registers some endpoints (createPlaylist) under their .view form.
	if !strings.HasPrefix(u, "https://bandcamp.com/api/subsonic/rest/ping.view?") {
		t.Errorf("URL = %q, want bandcamp.com/api/subsonic/rest/ping.view prefix", u)
	}
	if !strings.Contains(u, "v=1.16.1") {
		t.Errorf("URL = %q, want v=1.16.1 (token auth is a 1.13.0+ feature)", u)
	}
}

func TestBandcampStreamURLNoRawFormat(t *testing.T) {
	srv := fakeBandcampServer(t)
	defer srv.Close()

	c := newBandcampTestClient(srv)
	tracks, err := c.Tracks("pl-1")
	if err != nil {
		t.Fatalf("Tracks() error: %v", err)
	}
	if strings.Contains(tracks[0].Path, "format=raw") {
		t.Errorf("Path %q contains format=raw — transcoding params are unverified on the beta", tracks[0].Path)
	}
	if !playlist.IsSubsonicStreamURL(tracks[0].Path) {
		t.Errorf("IsSubsonicStreamURL(%q) = false, want true", tracks[0].Path)
	}
	if got := tracks[0].Meta(provider.MetaBandcampID); got != "song-1" {
		t.Errorf("Meta(bandcamp.id) = %q, want song-1", got)
	}
	if tracks[0].Meta(provider.MetaNavidromeID) != "" {
		t.Error("bandcamp track carries navidrome.id — scrobble cross-talk hazard")
	}
}

func TestBandcampBadAuthHint(t *testing.T) {
	// The live beta signals bad credentials as a bare HTTP 500, empty body,
	// while ping still answers ok (server up).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/rest/ping.view") {
			w.Write([]byte(`{"subsonic-response":{"status":"ok"}}`))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newBandcampTestClient(srv)
	err := c.ValidateAuth()
	if err == nil {
		t.Fatal("expected error for HTTP 500")
	}
	if !strings.Contains(err.Error(), "Fan Settings") {
		t.Errorf("error = %q, want the Fan Settings credential hint on HTTP 500", err)
	}
	// The hint names the likeliest cause, but a bare status is not proof:
	// classifying it as ErrBadCredentials would let one failing endpoint
	// blank a working pane and accuse credentials that are fine.
	if errors.Is(err, ErrBadCredentials) {
		t.Errorf("error = %q, must not be classified as ErrBadCredentials", err)
	}
}

func TestTransportErrorRedactsAuthQuery(t *testing.T) {
	// Point at a closed port: the transport error must not leak the URL
	// query, which carries the auth token and salt.
	c := NewBandcampClient(Config{BaseURL: "http://127.0.0.1:1", User: "fan", Password: "secret"})
	err := c.ValidateAuth()
	if err == nil {
		t.Fatal("expected transport error")
	}
	for _, leak := range []string{"t=", "s=", "u=fan"} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("error %q leaks auth query fragment %q", err, leak)
		}
	}
	// Redaction must not cost callers the *url.Error layer: net.Error
	// classification (Timeout, Temporary) reads through it.
	var ue *url.Error
	if !errors.As(err, &ue) {
		t.Fatalf("redacted error no longer unwraps to *url.Error: %v", err)
	}
	if strings.Contains(ue.URL, "t=") {
		t.Errorf("wrapped URL still carries the auth query: %q", ue.URL)
	}
}

func TestBandcampPingDoesNotValidate(t *testing.T) {
	// The live beta answers ping with status ok regardless of credentials;
	// ValidateAuth must be the credential check, Ping only connectivity.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/rest/ping.view") {
			w.Write([]byte(`{"subsonic-response":{"status":"ok"}}`))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newBandcampTestClient(srv)
	if err := c.Ping(); err != nil {
		t.Fatalf("Ping() error: %v", err)
	}
	if err := c.ValidateAuth(); err == nil {
		t.Fatal("ValidateAuth() should fail when getPlaylists returns 500")
	}
}

func TestBandcampUnsupportedEndpointMemo(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"error":true,"error_message":"bad version"}`))
	}))
	defer srv.Close()

	c := newBandcampTestClient(srv)
	_, err := c.Artists()
	if err == nil {
		t.Fatal("expected error for unimplemented endpoint")
	}
	if !strings.Contains(err.Error(), "doesn't support getArtists") {
		t.Errorf("error = %q, want friendly unsupported-endpoint message", err)
	}

	// Second call must short-circuit on the memo without hitting the server.
	if _, err := c.Artists(); err == nil {
		t.Fatal("expected memoized error on second call")
	}
	if calls != 1 {
		t.Errorf("server hit %d times, want 1 (memo should short-circuit)", calls)
	}

	// Refresh clears the memo so the user can retry after the beta improves.
	c.Refresh()
	c.Artists()
	if calls != 2 {
		t.Errorf("server hit %d times after Refresh, want 2", calls)
	}
}

func TestNoEnvelopeMemoOnlyForBandcamp(t *testing.T) {
	// An envelope-less body fails for every dialect — decoding it would
	// report an empty collection as success. Only Bandcamp recognizes its
	// own fall-through signature as "this endpoint is unimplemented" and
	// memoizes it; for other servers it is a one-off, so the next call
	// reaches the server again.
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"error":true,"error_message":"bad version"}`))
	}))
	defer srv.Close()

	c := NewNavidromeClient(Config{BaseURL: srv.URL, User: "u", Password: "p"})
	if _, err := c.Artists(); err == nil {
		t.Error("envelope-less response accepted as an empty artist list")
	}
	if _, err := c.Artists(); err == nil {
		t.Error("envelope-less response accepted as an empty artist list")
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("getArtists hit %d times, want 2 (only bandcamp memoizes this)", got)
	}
}

func TestPingNeverMemoized(t *testing.T) {
	// Ping doubles as the outage-vs-credentials disambiguator: one
	// envelope-less answer must not disable it until Refresh.
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"error":true,"error_message":"bad version"}`))
	}))
	defer srv.Close()

	c := newBandcampTestClient(srv)
	if err := c.Ping(); err == nil {
		t.Fatal("expected error for envelope-less ping")
	}
	c.Ping()
	if calls != 2 {
		t.Errorf("ping hit the server %d times, want 2 (must not be memoized)", calls)
	}
}

func TestBareStatusIsNeverBadCredentials(t *testing.T) {
	// Whether the server is up or down, an HTTP status alone cannot tell a
	// rejected credential from a server-side fault, so neither case may
	// reach the user as a credentials verdict. Only a protocol-level
	// rejection (Subsonic error 40) does that.
	for _, tt := range []struct {
		name   string
		pingOK bool
	}{
		{"server up", true},
		{"server down", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.pingOK && strings.HasSuffix(r.URL.Path, "/rest/ping.view") {
					w.Write([]byte(`{"subsonic-response":{"status":"ok"}}`))
					return
				}
				w.WriteHeader(http.StatusInternalServerError)
			}))
			defer srv.Close()
			c := newBandcampTestClient(srv)
			err := c.ValidateAuth()
			if err == nil {
				t.Fatal("expected error")
			}
			if errors.Is(err, ErrBadCredentials) {
				t.Errorf("bare status classified as bad credentials: %v", err)
			}
		})
	}
}

func TestProtocolRejectionIsBadCredentials(t *testing.T) {
	// Subsonic error 40 is unambiguous, so it keeps the sentinel that fails
	// the pane into the credential-error surface.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"subsonic-response":{"status":"failed","error":{"code":40,"message":"Wrong username or password"}}}`))
	}))
	defer srv.Close()

	if err := newBandcampTestClient(srv).ValidateAuth(); !errors.Is(err, ErrBadCredentials) {
		t.Errorf("error = %v, want ErrBadCredentials for Subsonic error 40", err)
	}
}

func TestEndpointSpecific500IsNotCredentials(t *testing.T) {
	// Only the auth probe (getPlaylists) reads a 500 as bad credentials;
	// one failing album must never blame working credentials.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/rest/ping.view") {
			w.Write([]byte(`{"subsonic-response":{"status":"ok"}}`))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newBandcampTestClient(srv)
	_, err := c.AlbumTracks("al-1")
	if err == nil {
		t.Fatal("expected error")
	}
	if errors.Is(err, ErrBadCredentials) || strings.Contains(err.Error(), "Fan Settings") {
		t.Errorf("album 500 misreported as bad credentials: %v", err)
	}
}

func TestUnsupportedEndpointMemoCoversParameterizedCalls(t *testing.T) {
	// The envelope-less fall-through means the route is not implemented,
	// which parameters cannot change: the beta answers bad parameters with
	// a proper ok envelope instead (verified live 2026-08-25). So a
	// parameterized endpoint is memoized like any other and the server is
	// not re-asked until Refresh.
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"error":true,"error_message":"bad version"}`))
	}))
	defer srv.Close()

	c := newBandcampTestClient(srv)
	if _, err := c.Tracks("pl-1"); err == nil {
		t.Fatal("expected the unsupported-endpoint error")
	}
	if _, err := c.Tracks("pl-2"); err == nil {
		t.Fatal("expected the memoized error")
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("getPlaylist hit %d times, want 1 (memoized until Refresh)", got)
	}
	c.Refresh()
	if _, err := c.Tracks("pl-1"); err == nil {
		t.Fatal("expected the error again after Refresh")
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("Refresh did not clear the memo: %d calls, want 2", got)
	}
}

// A single failing endpoint must never blank the pane: with no bare status
// classified as a credentials verdict, callers see an ordinary error and
// keep whatever else works.
func TestEndpointFaultIsNotBlamedOnCredentials(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimSuffix(r.URL.Path, ".view")
		switch {
		case strings.HasSuffix(path, "/rest/getPlaylists"):
			w.WriteHeader(http.StatusInternalServerError)
		default:
			w.Write([]byte(`{"subsonic-response":{"status":"ok","albumList2":{"album":[]}}}`))
		}
	}))
	defer srv.Close()

	c := newBandcampTestClient(srv)
	if _, err := c.Playlists(); err == nil {
		t.Fatal("expected an error")
	} else if errors.Is(err, ErrBadCredentials) {
		t.Errorf("endpoint fault reported as bad credentials: %v", err)
	}
	// The endpoints that do work keep working.
	if _, err := c.AlbumList(SortNewest, 0, 1); err != nil {
		t.Errorf("a healthy endpoint failed alongside it: %v", err)
	}
}

func TestMutationClearsOnlyPlaylistMemo(t *testing.T) {
	// A working createPlaylist proves the playlist routes serve; it says
	// nothing about getArtists, whose verdict must survive.
	var artistCalls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimSuffix(r.URL.Path, ".view")
		switch {
		case strings.HasSuffix(path, "/rest/getArtists"):
			artistCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"error":true,"error_message":"bad version"}`))
		case strings.HasSuffix(path, "/rest/createPlaylist"):
			w.Write([]byte(`{"subsonic-response":{"status":"ok","playlist":{"id":"pl-new"}}}`))
		default:
			w.Write([]byte(`{"subsonic-response":{"status":"ok"}}`))
		}
	}))
	defer srv.Close()

	c := newBandcampTestClient(srv)
	if _, err := c.Artists(); err == nil {
		t.Fatal("expected the unsupported-endpoint error")
	}
	if _, err := c.CreatePlaylist(t.Context(), "new"); err != nil {
		t.Fatalf("CreatePlaylist: %v", err)
	}
	if _, err := c.Artists(); err == nil {
		t.Fatal("expected the memoized error")
	}
	if got := artistCalls.Load(); got != 1 {
		t.Errorf("getArtists hit %d times, want 1 (the mutation must not clear its memo)", got)
	}
}

func TestMissingEnvelopeIsNotSilentSuccess(t *testing.T) {
	// A proxy or captive portal answering 200 with unrelated JSON must not
	// decode into an empty, error-free collection.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"message":"maintenance"}`))
	}))
	defer srv.Close()

	c := NewNavidromeClient(Config{BaseURL: srv.URL, User: "u", Password: "p"})
	lists, err := c.Playlists()
	if err == nil {
		t.Fatalf("non-Subsonic JSON accepted as success (%d playlists)", len(lists))
	}
	if !strings.Contains(err.Error(), "subsonic-response") {
		t.Errorf("error does not say what was wrong: %v", err)
	}
}

func TestPartialBatchAddReportsProgress(t *testing.T) {
	// Bandcamp adds one song per call, so a mid-batch failure leaves the
	// playlist partly written; the error has to say how far it got or a
	// retry silently duplicates those tracks.
	var adds atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(strings.TrimSuffix(r.URL.Path, ".view"), "/rest/updatePlaylist") {
			if adds.Add(1) > 2 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
		}
		w.Write([]byte(`{"subsonic-response":{"status":"ok"}}`))
	}))
	defer srv.Close()

	c := newBandcampTestClient(srv)
	tracks := make([]playlist.Track, 4)
	for i := range tracks {
		tracks[i] = playlist.Track{ProviderMeta: map[string]string{provider.MetaBandcampID: fmt.Sprintf("s-%d", i)}}
	}
	added, _, err := c.AddTracksToPlaylist(t.Context(), "pl-1", tracks)
	if err == nil {
		t.Fatal("expected the mid-batch failure")
	}
	if added != 2 {
		t.Errorf("added = %d, want 2", added)
	}
	if !strings.Contains(err.Error(), "added 2 of 4") {
		t.Errorf("error does not report partial progress: %v", err)
	}
}

func TestPlaylists_EmptyCached(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte(`{"subsonic-response":{"status":"ok","playlists":{"playlist":[]}}}`))
	}))
	defer srv.Close()

	c := newBandcampTestClient(srv)
	for range 3 {
		if _, err := c.Playlists(); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Errorf("getPlaylists hit %d times for an empty list, want 1 (empty result must be cached)", calls)
	}
}

func TestRetryDelay(t *testing.T) {
	for _, tt := range []struct {
		header string
		want   time.Duration
	}{
		{"", time.Second},
		{"garbage", time.Second},
		{"0", time.Second},
		{"3", 3 * time.Second},
		{"60", retryAfterCap},
		{"10000000000", retryAfterCap}, // would overflow int64 nanoseconds
	} {
		if got := retryDelay(tt.header); got != tt.want {
			t.Errorf("retryDelay(%q) = %v, want %v", tt.header, got, tt.want)
		}
	}
}

func TestMutationClearsUnsupportedMemo(t *testing.T) {
	flaky := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/rest/getPlaylists.view") && flaky {
			w.Write([]byte(`{"error":true,"error_message":"bad version"}`))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/rest/createPlaylist.view") {
			w.Write([]byte(`{"subsonic-response":{"status":"ok","playlist":{"id":"pl-new"}}}`))
			return
		}
		w.Write([]byte(`{"subsonic-response":{"status":"ok","playlists":{"playlist":[{"id":"pl-new","name":"New","songCount":0}]}}}`))
	}))
	defer srv.Close()

	c := newBandcampTestClient(srv)
	if _, err := c.Playlists(); err == nil {
		t.Fatal("expected memoized unsupported error first")
	}
	flaky = false
	if _, err := c.CreatePlaylist(t.Context(), "New"); err != nil {
		t.Fatal(err)
	}
	lists, err := c.Playlists()
	if err != nil {
		t.Fatalf("Playlists() after a successful mutation still memoized: %v", err)
	}
	if len(lists) != 1 {
		t.Errorf("got %d playlists, want 1", len(lists))
	}
}

func TestCredentialErrorMessageClean(t *testing.T) {
	err := checkSubsonicError("navidrome", "failed", &subsonicError{Code: 40, Message: "Wrong username or password"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrBadCredentials) {
		t.Error("code 40 must match ErrBadCredentials")
	}
	if strings.Contains(err.Error(), "bad credentials") {
		t.Errorf("sentinel text leaked into the user-facing message: %q", err)
	}
}

func TestBandcampScrobbleGatedOff(t *testing.T) {
	c := NewBandcampClient(Config{BaseURL: "http://localhost", User: "u", Password: "p"})
	track := playlist.Track{ProviderMeta: map[string]string{provider.MetaBandcampID: "song-1"}}
	if c.CanReportPlayback(track) {
		t.Error("CanReportPlayback() = true — scrobble must be gated off until the beta's semantics are verified")
	}
}

func TestBandcampSortTypes(t *testing.T) {
	c := NewBandcampClient(Config{BaseURL: "http://localhost", User: "u", Password: "p"})
	sorts := c.AlbumSortTypes()
	if len(sorts) != 3 {
		t.Fatalf("expected 3 conservative sort types, got %d", len(sorts))
	}
	if c.DefaultAlbumSort() != SortNewest {
		t.Errorf("DefaultAlbumSort() = %q, want newest (recent purchases first)", c.DefaultAlbumSort())
	}
}

func TestCreatePlaylist(t *testing.T) {
	var gotName string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/rest/createPlaylist.view") {
			gotName = r.URL.Query().Get("name")
			w.Write([]byte(`{"subsonic-response":{"status":"ok","playlist":{"id":"pl-new"}}}`))
			return
		}
		w.Write([]byte(`{"subsonic-response":{"status":"ok"}}`))
	}))
	defer srv.Close()

	c := newBandcampTestClient(srv)
	id, err := c.CreatePlaylist(t.Context(), "Road Trip")
	if err != nil {
		t.Fatalf("CreatePlaylist() error: %v", err)
	}
	if id != "pl-new" {
		t.Errorf("id = %q, want pl-new", id)
	}
	if gotName != "Road Trip" {
		t.Errorf("name = %q, want Road Trip", gotName)
	}
}

func TestAddTracksToPlaylist(t *testing.T) {
	// The beta honors only the LAST songIdToAdd of a multi-value call, so
	// the bandcamp dialect adds one song per updatePlaylist request.
	var calls [][]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/rest/updatePlaylist.view") {
			if got := r.URL.Query().Get("playlistId"); got != "pl-1" {
				t.Errorf("playlistId = %q, want pl-1", got)
			}
			calls = append(calls, r.URL.Query()["songIdToAdd"])
		}
		w.Write([]byte(`{"subsonic-response":{"status":"ok"}}`))
	}))
	defer srv.Close()

	c := newBandcampTestClient(srv)
	tracks := []playlist.Track{
		{ProviderMeta: map[string]string{provider.MetaBandcampID: "song-1"}},
		{Path: "/home/user/local.mp3"}, // foreign track: skipped
		{ProviderMeta: map[string]string{provider.MetaBandcampID: "song-2"}},
	}
	added, skipped, err := c.AddTracksToPlaylist(t.Context(), "pl-1", tracks)
	if err != nil {
		t.Fatalf("AddTracksToPlaylist() error: %v", err)
	}
	if added != 2 || skipped != 1 {
		t.Errorf("added=%d skipped=%d, want 2/1", added, skipped)
	}
	if len(calls) != 2 || len(calls[0]) != 1 || calls[0][0] != "song-1" || calls[1][0] != "song-2" {
		t.Errorf("updatePlaylist calls = %v, want two single-song calls [song-1] [song-2]", calls)
	}
}

func TestAddTracksToPlaylist_NavidromeBatchesOneCall(t *testing.T) {
	var calls [][]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/rest/updatePlaylist") {
			calls = append(calls, r.URL.Query()["songIdToAdd"])
		}
		w.Write([]byte(`{"subsonic-response":{"status":"ok"}}`))
	}))
	defer srv.Close()

	c := NewNavidromeClient(Config{BaseURL: srv.URL, User: "u", Password: "p"})
	tracks := []playlist.Track{
		{ProviderMeta: map[string]string{provider.MetaNavidromeID: "song-1"}},
		{ProviderMeta: map[string]string{provider.MetaNavidromeID: "song-2"}},
	}
	added, _, err := c.AddTracksToPlaylist(t.Context(), "pl-1", tracks)
	if err != nil {
		t.Fatalf("AddTracksToPlaylist() error: %v", err)
	}
	if added != 2 {
		t.Errorf("added = %d, want 2", added)
	}
	if len(calls) != 1 || len(calls[0]) != 2 {
		t.Errorf("updatePlaylist calls = %v, want one call with two songIdToAdd values", calls)
	}
}

func TestAddTrackToPlaylist_ForeignTrack(t *testing.T) {
	c := NewBandcampClient(Config{BaseURL: "http://localhost", User: "u", Password: "p"})
	err := c.AddTrackToPlaylist(t.Context(), "pl-1", playlist.Track{Path: "/tmp/x.mp3"})
	if err == nil {
		t.Fatal("expected error adding a track with no bandcamp id")
	}
}

func TestBrowseSortValidation(t *testing.T) {
	// Bandcamp's beta answers an unsupported getAlbumList2 type with an
	// empty, error-free album list, so its dialect declares its sort list
	// exhaustive and unknown values fall back. Navidrome takes the full
	// Subsonic set (random, highest, ...) even though the picker lists a
	// subset, so a hand-edited value must survive.
	tests := []struct {
		name string
		make func(Config) *Client
		sort string
		want string
	}{
		{"bandcamp keeps a supported sort", NewBandcampClient, SortAlphabeticalByArtist, SortAlphabeticalByArtist},
		{"bandcamp falls back on an unsupported sort", NewBandcampClient, SortRecent, SortNewest},
		{"bandcamp falls back when unset", NewBandcampClient, "", SortNewest},
		{"navidrome keeps a sort the picker omits", NewNavidromeClient, "random", "random"},
		{"navidrome keeps a listed sort", NewNavidromeClient, SortByYear, SortByYear},
		{"navidrome falls back when unset", NewNavidromeClient, "", SortAlphabeticalByName},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := tt.make(Config{BaseURL: "http://localhost", User: "u", Password: "p", BrowseSort: tt.sort})
			if got := c.DefaultAlbumSort(); got != tt.want {
				t.Errorf("DefaultAlbumSort() = %q, want %q", got, tt.want)
			}
		})
	}
}
