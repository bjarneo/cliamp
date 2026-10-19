package spotify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/playlist"
	librespot "github.com/devgianlu/go-librespot"
	"github.com/devgianlu/go-librespot/spclient"
)

func playbackAPIClient(t *testing.T, rt http.RoundTripper) *spclient.Spclient {
	t.Helper()
	sp, err := spclient.NewSpclient(t.Context(), &librespot.NullLogger{}, &http.Client{Transport: rt}, func(context.Context) string { return "playback.test" }, func(context.Context, bool) (string, error) { return "playback-token", nil }, "device", "client-token")
	if err != nil {
		t.Fatal(err)
	}
	return sp
}

func homeCard(section, category, title, uri string) map[string]any {
	return map[string]any{"component": map[string]any{"category": category}, "text": map[string]any{"title": title}, "metadata": map[string]any{"sectionId": section, "uri": uri}}
}

func homeBody(t *testing.T, cards ...map[string]any) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{"body": cards})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestPersonalizedHomeDiscovery(t *testing.T) {
	const a = "spotify:playlist:AAAAAAAAAAAAAAAAAAAAAA"
	const b = "spotify:playlist:BBBBBBBBBBBBBBBBBBBBBB"
	const c = "spotify:playlist:CCCCCCCCCCCCCCCCCCCCCC"
	body := homeBody(t,
		homeCard(homeShortcutsShelf, "card", "Shortcut duplicate", a),
		homeCard(madeForYouShelf, "header", "Made for another account", ""),
		homeCard(madeForYouShelf, "card", "Découvertes de la semaine", a),
		homeCard(madeForYouShelf, "card", "Mix", b),
		homeCard(madeForYouShelf, "card", "Duplicate", a),
		homeCard(madeForYouShelf, "card", "Album", "spotify:album:AAAAAAAAAAAAAAAAAAAAAA"),
		homeCard(madeForYouShelf, "card", "Invalid", "spotify:playlist:../bad"),
		homeCard(madeForYouShelf, "card", "", c),
		homeCard("unrelated", "card", "Editorial", c),
		homeCard(homeShortcutsShelf, "card", "Mix", c),
	)
	requests := 0
	sp := playbackAPIClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		if r.Method != "GET" || r.URL.Path != "/homeview/v1/home" || r.URL.Query().Get("platform") != "desktop" {
			t.Errorf("unexpected discovery request %s %s", r.Method, r.URL)
		}
		if r.Header.Get("Authorization") != "Bearer playback-token" || r.Header.Get("Client-Token") != "client-token" {
			t.Error("missing playback authentication")
		}
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) > 20*time.Second {
			t.Error("missing bounded discovery context")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	}))
	lists, err := fetchHomePlaylists(t.Context(), sp)
	if err != nil {
		t.Fatal(err)
	}
	want := []playlist.PlaylistInfo{{ID: a, Name: "Découvertes de la semaine", Section: madeForYouSection}, {ID: b, Name: "Mix", Section: madeForYouSection}, {ID: c, Name: "Mix", Section: homeShortcutsSection}}
	if len(lists) != len(want) {
		t.Fatalf("lists=%+v", lists)
	}
	for i := range want {
		if lists[i] != want[i] {
			t.Errorf("list %d=%+v, want %+v", i, lists[i], want[i])
		}
	}
	if requests != 1 {
		t.Fatalf("discovery paged or retried: %d requests", requests)
	}
}

func TestPersonalizedHomeFailuresAndEmptyShelf(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		wantErr    bool
	}{
		{"forbidden", "", 403, true},
		{"rate limited", "", 429, true},
		{"malformed", "{", 200, true},
		{"missing Home", "{}", 200, true},
		{"generic fallback", homeBody(t, homeCard("popular", "card", "Popular", "spotify:playlist:AAAAAAAAAAAAAAAAAAAAAA")), 200, true},
		{"explicit empty shelf", homeBody(t, homeCard(madeForYouShelf, "header", "Made for you", "")), 200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			sp := playbackAPIClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
				requests++
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			}))
			lists, err := fetchHomePlaylists(t.Context(), sp)
			if (err != nil) != tc.wantErr || len(lists) != 0 {
				t.Fatalf("lists=%v error=%v", lists, err)
			}
			if requests != 1 {
				t.Fatalf("requests=%d", requests)
			}
		})
	}
}

func TestPersonalizedHomeCancellation(t *testing.T) {
	sp := playbackAPIClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) { return nil, r.Context().Err() }))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := fetchHomePlaylists(ctx, sp); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
}

func TestCanonicalPlaylistRoutesAvoidPublicTrackAPI(t *testing.T) {
	const uri = "spotify:playlist:AAAAAAAAAAAAAAAAAAAAAA"
	calls := 0
	p := New(stubSession(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"items":[],"total":0}`))}, nil
	})), "client", 320)
	if _, err := p.Tracks(uri); err == nil || !strings.Contains(err.Error(), "playback session is closed") {
		t.Fatalf("Tracks error=%v", err)
	}
	if _, next, err := p.TracksPage(uri, 0); next != 0 || err == nil || !strings.Contains(err.Error(), "playback session is closed") {
		t.Fatalf("TracksPage next=%d error=%v", next, err)
	}
	if _, _, err := p.TracksPage(uri, 1); err == nil {
		t.Fatal("accepted a second context page")
	}
	if calls != 0 {
		t.Fatalf("context selection used Web API %d times", calls)
	}
	if _, err := p.Tracks("AAAAAAAAAAAAAAAAAAAAAA"); err != nil || calls != 1 {
		t.Fatalf("bare library ID lost its existing route: calls=%d error=%v", calls, err)
	}
	p.writable["AAAAAAAAAAAAAAAAAAAAAA"] = true
	if p.CanAddToPlaylist(playlist.PlaylistInfo{ID: uri}) {
		t.Fatal("Home card offered as a writable library playlist")
	}
}

func TestContextPlaylistPageLimit(t *testing.T) {
	requests := 0
	sp := playbackAPIClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		page := `{"tracks":[{"uri":"spotify:track:2FY7b99s15jUprqC0M5NCT","metadata":{"title":"Song"}}],"next_page_url":"hm://context/repeated"}`
		if strings.HasPrefix(r.URL.Path, "/context-resolve/") {
			page = `{"pages":[` + page + `]}`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(page))}, nil
	}))
	tracks, err := resolveContextTracks(t.Context(), sp, "spotify:playlist:AAAAAAAAAAAAAAAAAAAAAA")
	if err == nil || !strings.Contains(err.Error(), "100 pages") || len(tracks) != 0 || requests != 101 {
		t.Fatalf("tracks=%d requests=%d error=%v", len(tracks), requests, err)
	}
}

func TestContextPlaylistExactlyPageLimit(t *testing.T) {
	requests := 0
	sp := playbackAPIClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		page := `{"tracks":[{"uri":"spotify:track:2FY7b99s15jUprqC0M5NCT","metadata":{"title":"Song"}}]}`
		if requests < 100 {
			page = strings.TrimSuffix(page, "}") + `,"next_page_url":"hm://context/next"}`
		}
		if strings.HasPrefix(r.URL.Path, "/context-resolve/") {
			page = `{"pages":[` + page + `]}`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(page))}, nil
	}))
	tracks, err := resolveContextTracks(t.Context(), sp, "spotify:playlist:AAAAAAAAAAAAAAAAAAAAAA")
	if err != nil || len(tracks) != 100 || requests != 100 {
		t.Fatalf("tracks=%d requests=%d error=%v", len(tracks), requests, err)
	}
}

func hubBody(t *testing.T, id string, cards ...map[string]any) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{"id": id, "body": []any{map[string]any{"children": cards}}})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestMadeForYouHubMerge(t *testing.T) {
	const a = "spotify:playlist:AAAAAAAAAAAAAAAAAAAAAA"
	const b = "spotify:playlist:BBBBBBBBBBBBBBBBBBBBBB"
	const c = "spotify:playlist:CCCCCCCCCCCCCCCCCCCCCC"
	for _, tc := range []struct {
		name                  string
		hubStatus, homeStatus int
		hubID                 string
		want                  []playlist.PlaylistInfo
		wantErr               bool
	}{
		{"merge", 200, 200, "mfy-hub", []playlist.PlaylistInfo{{ID: a, Name: "Mix 1", Section: madeForYouSection}, {ID: b, Name: "Localized mix", Section: madeForYouSection}, {ID: c, Name: "Shortcut", Section: homeShortcutsSection}}, false},
		{"hub survives Home refusal", 200, 403, "mfy-hub", []playlist.PlaylistInfo{{ID: a, Name: "Mix 1", Section: madeForYouSection}, {ID: b, Name: "Localized mix", Section: madeForYouSection}}, true},
		{"Home survives hub refusal", 403, 200, "mfy-hub", []playlist.PlaylistInfo{{ID: a, Name: "Shortcut Mix 1", Section: homeShortcutsSection}, {ID: c, Name: "Shortcut", Section: homeShortcutsSection}}, true},
		{"reject generic Browse fallback", 200, 200, "browse-error", []playlist.PlaylistInfo{{ID: a, Name: "Shortcut Mix 1", Section: homeShortcutsSection}, {ID: c, Name: "Shortcut", Section: homeShortcutsSection}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			sp := playbackAPIClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				deadline, ok := r.Context().Deadline()
				if !ok || time.Until(deadline) > 10*time.Second {
					t.Error("unbounded discovery request")
				}
				status := tc.homeStatus
				body := homeBody(t, homeCard(madeForYouShelf, "header", "Made for You", ""), homeCard(homeShortcutsShelf, "card", "Shortcut Mix 1", a), homeCard(homeShortcutsShelf, "card", "Shortcut", c))
				if calls == 1 {
					if r.Method != http.MethodGet || r.URL.Path != "/hubview-mobile-v1/browse/0JQ5DAt0tbjZptfcdMSKl3" || r.URL.Query().Get("platform") != "android" || r.Header.Get("App-Platform") != "Android" || r.Header.Get("User-Agent") == "" {
						t.Errorf("unexpected hub request: %s", r.URL)
					}
					status = tc.hubStatus
					body = hubBody(t, tc.hubID, homeCard("hub", "card", "Mix 1", a), homeCard("hub", "card", "Localized mix", b), homeCard("hub", "card", "Duplicate", a), homeCard("hub", "card", "Album", "spotify:album:AAAAAAAAAAAAAAAAAAAAAA"), homeCard("hub", "card", "Invalid", "spotify:playlist:bad"), homeCard("hub", "card", "", c))
				} else if r.URL.Path != "/homeview/v1/home" {
					t.Errorf("unexpected Home request: %s", r.URL)
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}, nil
			}))
			got, err := fetchPersonalizedPlaylists(t.Context(), sp)
			if (err != nil) != tc.wantErr || len(got) != len(tc.want) {
				t.Fatalf("lists=%+v error=%v", got, err)
			}
			for i, want := range tc.want {
				if got[i] != want {
					t.Errorf("list %d=%+v want %+v", i, got[i], want)
				}
			}
			if calls != 2 {
				t.Fatalf("requests=%d", calls)
			}
		})
	}
}

func TestMadeForYouHubBounds(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		wantErr    bool
	}{
		{"empty hub", hubBody(t, "mfy-hub"), false},
		{"malformed", "{", true},
		{"missing hub", "{}", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sp := playbackAPIClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			}))
			lists, err := fetchMadeForYouHub(t.Context(), sp)
			if (err != nil) != tc.wantErr || len(lists) != 0 {
				t.Fatalf("lists=%v error=%v", lists, err)
			}
		})
	}
	sp := playbackAPIClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) { return nil, r.Context().Err() }))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := fetchPersonalizedPlaylists(ctx, sp); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
}

func TestMadeForYouHubCardLimit(t *testing.T) {
	cards := make([]map[string]any, 1001)
	for i := range cards {
		cards[i] = homeCard("hub", "card", "Mix", "spotify:playlist:AAAAAAAAAAAAAAAAAAAAAA")
	}
	body := hubBody(t, "mfy-hub", cards...)
	sp := playbackAPIClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	}))
	lists, err := fetchMadeForYouHub(t.Context(), sp)
	if len(lists) != 0 || err == nil || !strings.Contains(err.Error(), "1000 cards") {
		t.Fatalf("lists=%d error=%v", len(lists), err)
	}
}
