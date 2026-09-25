package bandcamp

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bjarneo/cliamp/applog"
	"github.com/bjarneo/cliamp/config"
	"github.com/bjarneo/cliamp/internal/subsonicapi"
	"github.com/bjarneo/cliamp/playlist"
)

// fakeServer serves the endpoints the provider uses. failPlaylists and
// failAlbums simulate the beta's per-endpoint outages (envelope-less
// "bad version" fall-through).
func fakeServer(t *testing.T, failPlaylists, failAlbums bool, hits *atomic.Int64) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits != nil {
			hits.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		path := strings.TrimSuffix(r.URL.Path, ".view")
		switch {
		case strings.HasSuffix(path, "/rest/getPlaylists"):
			if failPlaylists {
				w.Write([]byte(`{"error":true,"error_message":"bad version"}`))
				return
			}
			w.Write([]byte(`{"subsonic-response":{"status":"ok","playlists":{"playlist":[
				{"id":"pl-1","name":"Favorites","songCount":3}]}}}`))
		case strings.HasSuffix(path, "/rest/getAlbumList2"):
			if failAlbums {
				w.Write([]byte(`{"error":true,"error_message":"bad version"}`))
				return
			}
			w.Write([]byte(`{"subsonic-response":{"status":"ok","albumList2":{"album":[
				{"id":"al-1","name":"LP1","artist":"A","songCount":2},
				{"id":"al-2","name":"LP2","artist":"B","songCount":1}]}}}`))
		case strings.HasSuffix(path, "/rest/getAlbum"):
			id := r.URL.Query().Get("id")
			if id == "al-1" {
				w.Write([]byte(`{"subsonic-response":{"status":"ok","album":{"song":[
					{"id":"s-1","title":"One","artist":"A","album":"LP1","duration":100},
					{"id":"s-2","title":"Two","artist":"A","album":"LP1","duration":200}]}}}`))
				return
			}
			w.Write([]byte(`{"subsonic-response":{"status":"ok","album":{"song":[
				{"id":"s-3","title":"Three","artist":"B","album":"LP2","duration":300}]}}}`))
		case strings.HasSuffix(path, "/rest/getPlaylist"):
			w.Write([]byte(`{"subsonic-response":{"status":"ok","playlist":{"entry":[
				{"id":"s-9","title":"Nine","artist":"C","album":"LP9","duration":90}]}}}`))
		case strings.HasSuffix(path, "/rest/search3"):
			// Empty query lists the whole collection; one page, scrambled.
			if r.URL.Query().Get("songOffset") != "0" {
				w.Write([]byte(`{"subsonic-response":{"status":"ok","searchResult3":{"song":[]}}}`))
				return
			}
			w.Write([]byte(`{"subsonic-response":{"status":"ok","searchResult3":{"song":[
				{"id":"s-3","title":"Three","artist":"B","album":"LP2","track":1,"duration":300},
				{"id":"s-2","title":"Two","artist":"A","album":"LP1","track":2,"duration":200},
				{"id":"s-1","title":"One","artist":"A","album":"LP1","track":1,"duration":100}]}}}`))
		default:
			t.Errorf("unexpected path: %s", path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func newTestProvider(url string) *Provider {
	return NewFromConfig(config.BandcampConfig{URL: url, User: "fan", Password: "pass"})
}

func TestNewFromConfig(t *testing.T) {
	tests := []struct {
		name    string
		cfg     config.BandcampConfig
		wantNil bool
	}{
		{"empty", config.BandcampConfig{}, true},
		{"no password", config.BandcampConfig{User: "u"}, true},
		{"no user", config.BandcampConfig{Password: "p"}, true},
		{"valid without url", config.BandcampConfig{User: "u", Password: "p"}, false},
		{"valid with url", config.BandcampConfig{URL: "http://x", User: "u", Password: "p"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewFromConfig(tt.cfg)
			if (p == nil) != tt.wantNil {
				t.Errorf("NewFromConfig(%+v) nil=%v, want nil=%v", tt.cfg, p == nil, tt.wantNil)
			}
		})
	}
}

func TestPlaylists(t *testing.T) {
	tests := []struct {
		name          string
		failPlaylists bool
		wantIDs       []string
	}{
		{"library rows plus playlists", false, []string{recentPurchasesID, allPurchasesID, randomPurchasesID, "pl-1"}},
		// The static Library rows survive a playlist-list outage; the error
		// travels alongside them so the pane warns instead of going blank.
		{"playlists down", true, []string{recentPurchasesID, allPurchasesID, randomPurchasesID}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := fakeServer(t, tt.failPlaylists, false, nil)
			defer srv.Close()

			p := newTestProvider(srv.URL)
			lists, err := p.Playlists()
			if (err != nil) != tt.failPlaylists {
				t.Fatalf("Playlists() error = %v, want error: %v", err, tt.failPlaylists)
			}
			if len(lists) != len(tt.wantIDs) {
				t.Fatalf("got %d rows, want %d: %+v", len(lists), len(tt.wantIDs), lists)
			}
			for i, id := range tt.wantIDs {
				if lists[i].ID != id {
					t.Errorf("lists[%d].ID = %q, want %q", i, lists[i].ID, id)
				}
			}
			for i := range 3 {
				if lists[i].Section != "Library" || !lists[i].ReadOnly {
					t.Errorf("lists[%d] = %+v, want a ReadOnly Library row", i, lists[i])
				}
			}
			if len(lists) > 3 && lists[3].Section != "Your playlists" {
				t.Errorf("lists[3].Section = %q, want Your playlists", lists[3].Section)
			}
		})
	}
}

func TestPlaylists_NoAlbumFetch(t *testing.T) {
	// Listing the pane must not trigger the album fan-out — that work is
	// deferred until the Library row is actually opened.
	srv := fakeServer(t, false, true, nil)
	defer srv.Close()

	p := newTestProvider(srv.URL)
	if _, err := p.Playlists(); err != nil {
		t.Fatalf("Playlists() error: %v", err)
	}
}

func TestTracks_RoutesSyntheticRow(t *testing.T) {
	srv := fakeServer(t, false, false, nil)
	defer srv.Close()

	p := newTestProvider(srv.URL)
	tracks, err := p.Tracks(recentPurchasesID)
	if err != nil {
		t.Fatalf("Tracks(recent) error: %v", err)
	}
	if len(tracks) != 3 {
		t.Fatalf("expected 3 flattened tracks, got %d", len(tracks))
	}
	// Album order preserved: al-1's tracks before al-2's.
	if tracks[0].Title != "One" || tracks[2].Title != "Three" {
		t.Errorf("tracks out of album order: %+v", tracks)
	}

	server, err := p.Tracks("pl-1")
	if err != nil {
		t.Fatalf("Tracks(pl-1) error: %v", err)
	}
	if len(server) != 1 || server[0].Title != "Nine" {
		t.Errorf("Tracks(pl-1) = %+v", server)
	}
}

func TestTracks_OutageIsNotCredentials(t *testing.T) {
	// Server down (everything 500 incl. ping): opening a Library row must
	// surface an outage error, never the wrong-credentials hint.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	p := newTestProvider(srv.URL)
	_, err := p.Tracks(recentPurchasesID)
	if err == nil {
		t.Fatal("expected error")
	}
	if errors.Is(err, subsonicapi.ErrBadCredentials) || strings.Contains(err.Error(), "Fan Settings") {
		t.Errorf("outage misreported as bad credentials: %v", err)
	}
}

func TestRecentTracks_PartialNotCached(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		path := strings.TrimSuffix(r.URL.Path, ".view")
		switch {
		case strings.HasSuffix(path, "/rest/getAlbumList2"):
			w.Write([]byte(`{"subsonic-response":{"status":"ok","albumList2":{"album":[
				{"id":"al-1","name":"LP1","artist":"A","songCount":1},
				{"id":"al-2","name":"LP2","artist":"B","songCount":1}]}}}`))
		case strings.HasSuffix(path, "/rest/getAlbum") && r.URL.Query().Get("id") == "al-2":
			w.WriteHeader(http.StatusNotFound) // transient per-album failure
		case strings.HasSuffix(path, "/rest/getAlbum"):
			w.Write([]byte(`{"subsonic-response":{"status":"ok","album":{"song":[
				{"id":"s-1","title":"One","artist":"A","album":"LP1","duration":100}]}}}`))
		default:
			w.Write([]byte(`{"subsonic-response":{"status":"ok"}}`))
		}
	}))
	defer srv.Close()

	p := newTestProvider(srv.URL)
	tracks, err := p.Tracks(recentPurchasesID)
	if err != nil {
		t.Fatalf("partial result should still load: %v", err)
	}
	if len(tracks) != 1 {
		t.Fatalf("got %d tracks, want 1 (the failed album is skipped)", len(tracks))
	}
	after := hits.Load()
	p.Tracks(recentPurchasesID)
	if hits.Load() == after {
		t.Error("partial result was cached; the missing album is never retried")
	}
}

func TestTracks_SyntheticRowError(t *testing.T) {
	srv := fakeServer(t, false, true, nil)
	defer srv.Close()

	p := newTestProvider(srv.URL)
	if _, err := p.Tracks(recentPurchasesID); err == nil {
		t.Fatal("expected error when the album list is unavailable")
	}
}

func TestRecentTracks_CachedAndRefreshed(t *testing.T) {
	var hits atomic.Int64
	srv := fakeServer(t, false, false, &hits)
	defer srv.Close()

	p := newTestProvider(srv.URL)
	if _, err := p.Tracks(recentPurchasesID); err != nil {
		t.Fatal(err)
	}
	after := hits.Load()
	if _, err := p.Tracks(recentPurchasesID); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != after {
		t.Errorf("second Tracks(recent) hit the server (%d -> %d), want cached", after, hits.Load())
	}

	p.Refresh()
	if _, err := p.Tracks(recentPurchasesID); err != nil {
		t.Fatal(err)
	}
	if hits.Load() == after {
		t.Error("Refresh() did not clear the recent cache")
	}
}

func TestPlaylists_BadAuthFailsPane(t *testing.T) {
	// Rejected credentials must fail the whole pane so the persistent error
	// surface shows — not degrade to the Library row with a transient
	// warning. Mimics the live beta: ping answers ok without validating,
	// data endpoints return a bare 500 on bad credentials.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(strings.TrimSuffix(r.URL.Path, ".view"), "/rest/ping") {
			w.Write([]byte(`{"subsonic-response":{"status":"ok"}}`))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	p := newTestProvider(srv.URL)
	if _, err := p.Playlists(); err == nil {
		t.Fatal("expected error for bad credentials, got partial success")
	}
}

func TestPlaylists_OutageDegrades(t *testing.T) {
	// A server that 500s everything INCLUDING ping is an outage, not a
	// credentials diagnosis — the Library rows still come back, with a
	// plain error alongside for the status line.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	p := newTestProvider(srv.URL)
	lists, err := p.Playlists()
	if err == nil || errors.Is(err, subsonicapi.ErrBadCredentials) {
		t.Fatalf("outage must surface as a plain error alongside the rows, got %v", err)
	}
	if len(lists) != 3 || lists[0].ID != recentPurchasesID {
		t.Errorf("expected only the Library rows during an outage, got %+v", lists)
	}
}

func TestSyntheticRowIsReadOnly(t *testing.T) {
	srv := fakeServer(t, false, false, nil)
	defer srv.Close()

	p := newTestProvider(srv.URL)
	lists, err := p.Playlists()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{recentPurchasesID, allPurchasesID, randomPurchasesID} {
		err := p.AddTrackToPlaylist(t.Context(), id, playlist.Track{})
		if err == nil {
			t.Errorf("AddTrackToPlaylist(%s) must be rejected client-side", id)
		} else if row, _ := libraryRow(id); !strings.Contains(err.Error(), row.Name) {
			t.Errorf("rejection for %s names the wrong row: %v", id, err)
		}
		if _, _, err := p.AddTracksToPlaylist(t.Context(), id, nil); err == nil {
			t.Errorf("AddTracksToPlaylist(%s) must be rejected client-side", id)
		}
	}
	_ = lists
}

func TestTracks_AllPurchasesSorted(t *testing.T) {
	var hits atomic.Int64
	srv := fakeServer(t, false, false, &hits)
	defer srv.Close()

	p := newTestProvider(srv.URL)
	tracks, err := p.Tracks(allPurchasesID)
	if err != nil {
		t.Fatalf("Tracks(all) error: %v", err)
	}
	got := make([]string, len(tracks))
	for i, tr := range tracks {
		got[i] = tr.Title
	}
	// Server returned Three, Two, One; sorted by artist/album/track → One, Two, Three.
	if want := []string{"One", "Two", "Three"}; !slices.Equal(got, want) {
		t.Errorf("all purchases order = %v, want %v", got, want)
	}

	after := hits.Load()
	if _, err := p.Tracks(allPurchasesID); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != after {
		t.Errorf("second Tracks(all) hit the server (%d -> %d), want cached", after, hits.Load())
	}
}

func TestTracks_RandomPurchasesSubset(t *testing.T) {
	srv := fakeServer(t, false, false, nil)
	defer srv.Close()

	p := newTestProvider(srv.URL)
	tracks, err := p.Tracks(randomPurchasesID)
	if err != nil {
		t.Fatalf("Tracks(random) error: %v", err)
	}
	if len(tracks) != 3 {
		t.Fatalf("random sample size = %d, want 3 (whole collection when smaller than the cap)", len(tracks))
	}
	seen := map[string]bool{}
	for _, tr := range tracks {
		seen[tr.Title] = true
	}
	if len(seen) != 3 {
		t.Errorf("random sample has duplicates: %v", tracks)
	}
}

func TestRecentTracks_EmptyCollectionCached(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"subsonic-response":{"status":"ok","albumList2":{"album":[]}}}`))
	}))
	defer srv.Close()

	p := newTestProvider(srv.URL)
	if _, err := p.Tracks(recentPurchasesID); err != nil {
		t.Fatal(err)
	}
	after := hits.Load()
	if _, err := p.Tracks(recentPurchasesID); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != after {
		t.Errorf("empty collection re-fetched (%d -> %d hits); empty result must be cached", after, hits.Load())
	}
}

func TestValidate(t *testing.T) {
	srv := fakeServer(t, false, false, nil)
	defer srv.Close()
	if err := Validate(config.BandcampConfig{URL: srv.URL, User: "fan", Password: "pass"}); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

// collectionServer serves an owned collection of total songs through
// search3, honoring songOffset and capping each page at pageCap (which
// models a server that quietly returns fewer songs than songCount asked
// for). onCall runs before each response, with the 1-based call number.
func collectionServer(t *testing.T, total, pageCap int, onCall func(n int64)) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(strings.TrimSuffix(r.URL.Path, ".view"), "/rest/search3") {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if n := calls.Add(1); onCall != nil {
			onCall(n)
		}
		offset, _ := strconv.Atoi(r.URL.Query().Get("songOffset"))
		want, _ := strconv.Atoi(r.URL.Query().Get("songCount"))
		n := min(min(want, pageCap), max(total-offset, 0))
		var sb strings.Builder
		sb.WriteString(`{"subsonic-response":{"status":"ok","searchResult3":{"song":[`)
		for i := range n {
			if i > 0 {
				sb.WriteByte(',')
			}
			id := offset + i
			fmt.Fprintf(&sb, `{"id":"s-%d","title":"T","artist":"A","album":"LP","track":%d,"duration":1}`, id, id)
		}
		sb.WriteString(`]}}}`)
		w.Write([]byte(sb.String()))
	}))
	return srv, &calls
}

func TestTracks_AllPurchasesPagesShortPages(t *testing.T) {
	// A server that caps songCount below the requested page size must still
	// yield the whole collection: the crawl advances by what each page
	// actually returned and ends on the first page that adds nothing new.
	const total = 250
	srv, calls := collectionServer(t, total, 100, nil)
	defer srv.Close()

	p := newTestProvider(srv.URL)
	tracks, err := p.Tracks(allPurchasesID)
	if err != nil {
		t.Fatalf("Tracks(all) error: %v", err)
	}
	if len(tracks) != total {
		t.Fatalf("got %d tracks, want the whole collection (%d)", len(tracks), total)
	}
	// 100, 100, 50, then an empty page to prove the end.
	if got := calls.Load(); got != 4 {
		t.Errorf("search3 calls = %d, want 4", got)
	}
	if _, err := p.Tracks(allPurchasesID); err != nil || calls.Load() != 4 {
		t.Errorf("complete crawl was not cached (calls=%d, err=%v)", calls.Load(), err)
	}
}

func TestTracks_AllPurchasesSmallCollectionOnRepeatingServer(t *testing.T) {
	// A server that ignores songOffset and answers with a page smaller than
	// the one requested has simply handed back the whole collection twice:
	// the crawl ends, and nothing is missing, so there is nothing to warn
	// about.
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Write([]byte(`{"subsonic-response":{"status":"ok","searchResult3":{"song":[
			{"id":"s-1","title":"One","artist":"A","album":"LP","track":1,"duration":1},
			{"id":"s-2","title":"Two","artist":"A","album":"LP","track":2,"duration":1}]}}}`))
	}))
	defer srv.Close()

	p := newTestProvider(srv.URL)
	tracks, err := p.Tracks(allPurchasesID)
	if err != nil {
		t.Fatalf("Tracks(all) error: %v", err)
	}
	if len(tracks) != 2 {
		t.Errorf("got %d tracks, want 2 deduplicated", len(tracks))
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("search3 calls = %d, want 2 (first page, then the duplicate that ends it)", got)
	}
	if _, err := p.Tracks(allPurchasesID); err != nil || calls.Load() != 2 {
		t.Errorf("short list was not cached (calls=%d, err=%v)", calls.Load(), err)
	}
	if n := p.truncationNotice(); n != "" {
		t.Errorf("a complete small collection must not warn, got %q", n)
	}
}

func TestTracks_AllPurchasesAnnouncesTruncation(t *testing.T) {
	// A *full* page repeated means songOffset was ignored with more songs
	// behind it: that list really is short of the collection, so it is
	// kept (re-crawling on every open is worse) but never passed off as
	// everything the fan owns.
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var sb strings.Builder
		sb.WriteString(`{"subsonic-response":{"status":"ok","searchResult3":{"song":[`)
		for i := range allPageSize {
			if i > 0 {
				sb.WriteByte(',')
			}
			fmt.Fprintf(&sb, `{"id":"s-%d","title":"T","artist":"A","album":"LP","track":%d,"duration":1}`, i, i)
		}
		sb.WriteString(`]}}}`)
		w.Write([]byte(sb.String()))
	}))
	defer srv.Close()

	p := newTestProvider(srv.URL)
	tracks, err := p.Tracks(allPurchasesID)
	if err != nil {
		t.Fatalf("Tracks(all) error: %v", err)
	}
	if len(tracks) != allPageSize {
		t.Fatalf("got %d tracks, want %d deduplicated", len(tracks), allPageSize)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("search3 calls = %d, want 2", got)
	}
	if !strings.Contains(p.truncationNotice(), "not the whole collection") {
		t.Errorf("truncation was not announced, notice = %q", p.truncationNotice())
	}
	if _, err := p.Tracks(allPurchasesID); err != nil || calls.Load() != 2 {
		t.Errorf("short list was not cached (calls=%d, err=%v)", calls.Load(), err)
	}
	// Served from cache now, so the notice has to persist: otherwise every
	// later open passes the short list off as the whole collection.
	if !strings.Contains(p.truncationNotice(), "not the whole collection") {
		t.Errorf("truncation notice lost once the list was cached: %q", p.truncationNotice())
	}
}

func TestTracks_AllPurchasesKeepsPartialOnPageFailure(t *testing.T) {
	// One failing page must not cost the thousands of tracks already
	// fetched; the list is served and flagged, like fetchRecent does.
	var calls atomic.Int64
	srv, _ := collectionServer(t, 3*allPageSize, allPageSize, nil)
	defer srv.Close()
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, srv.URL+r.URL.Path+"?"+r.URL.RawQuery, http.StatusTemporaryRedirect)
	}))
	defer failing.Close()

	p := newTestProvider(failing.URL)
	tracks, err := p.Tracks(allPurchasesID)
	if err != nil {
		t.Fatalf("Tracks(all) error: %v", err)
	}
	if want := 2 * allPageSize; len(tracks) != want {
		t.Fatalf("got %d tracks, want the %d fetched before the failure", len(tracks), want)
	}
	if !strings.Contains(p.truncationNotice(), "failed partway") {
		t.Errorf("partial listing was not announced, notice = %q", p.truncationNotice())
	}
}

func TestTracks_AllPurchasesDedupesWithoutIDs(t *testing.T) {
	// A response that omits song ids must still let the crawl recognize a
	// repeated page; collapsing every such track onto one empty key would
	// both lose tracks and defeat the termination check.
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Write([]byte(`{"subsonic-response":{"status":"ok","searchResult3":{"song":[
			{"title":"One","artist":"A","album":"LP","track":1,"duration":100},
			{"title":"Two","artist":"A","album":"LP","track":2,"duration":200}]}}}`))
	}))
	defer srv.Close()

	p := newTestProvider(srv.URL)
	tracks, err := p.Tracks(allPurchasesID)
	if err != nil {
		t.Fatalf("Tracks(all) error: %v", err)
	}
	if len(tracks) != 2 {
		t.Errorf("got %d tracks, want both id-less songs kept", len(tracks))
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("search3 calls = %d, want 2 (the repeated page ends the crawl)", got)
	}
}

func TestTracks_AllPurchasesRestartsAfterRefresh(t *testing.T) {
	// A Refresh landing mid-crawl (Ctrl+R) abandons the stale crawl at the
	// next page boundary and starts over, so the caller gets post-refresh
	// data from one fresh crawl instead of a discarded one plus a repeat.
	total := 2*allPageSize + 1
	var prov atomic.Pointer[Provider]
	srv, calls := collectionServer(t, total, allPageSize, func(n int64) {
		if n == 2 {
			prov.Load().Refresh()
		}
	})
	defer srv.Close()

	p := newTestProvider(srv.URL)
	prov.Store(p)
	tracks, err := p.Tracks(allPurchasesID)
	if err != nil {
		t.Fatalf("Tracks(all) error: %v", err)
	}
	if len(tracks) != total {
		t.Fatalf("got %d tracks, want %d", len(tracks), total)
	}
	// Two pages of the abandoned crawl, then the full four-page crawl.
	if got := calls.Load(); got != 6 {
		t.Errorf("search3 calls = %d, want 6", got)
	}
	if _, err := p.Tracks(allPurchasesID); err != nil || calls.Load() != 6 {
		t.Errorf("post-refresh crawl not cached (calls=%d, err=%v)", calls.Load(), err)
	}
}

func TestEndpointRequiresTLS(t *testing.T) {
	// Every request carries the Subsonic token and salt in its query string,
	// so the url override may not route them over a cleartext link — except
	// to loopback, where the traffic never leaves the machine.
	tests := []struct {
		url    string
		want   string
		wantOK bool
	}{
		{"", DefaultURL, true},
		{"https://bandcamp.com/api/subsonic2", "https://bandcamp.com/api/subsonic2", true},
		{"http://localhost:8080/api/subsonic", "http://localhost:8080/api/subsonic", true},
		{"http://127.0.0.1:8080/api/subsonic", "http://127.0.0.1:8080/api/subsonic", true},
		{"http://[::1]:8080/api/subsonic", "http://[::1]:8080/api/subsonic", true},
		{"http://bandcamp.com/api/subsonic", "", false},
		{"http://192.168.1.10/api/subsonic", "", false},
		{"ftp://bandcamp.com/api/subsonic", "", false},
		{"bandcamp.com/api/subsonic", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			got, err := endpoint(config.BandcampConfig{URL: tt.url})
			if (err == nil) != tt.wantOK {
				t.Fatalf("endpoint(%q) error = %v, want ok=%v", tt.url, err, tt.wantOK)
			}
			if got != tt.want {
				t.Errorf("endpoint(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

func TestInsecureURLNeverReceivesCredentials(t *testing.T) {
	// Bandcamp credentials only work at Bandcamp, so an insecure override
	// falls back to the official endpoint rather than being used, and the
	// setup wizard refuses it outright instead of validating elsewhere.
	cfg := config.BandcampConfig{URL: "http://fan:hunter2@example.com/api/subsonic", User: "u", Password: "p"}
	if got := newClient(cfg).BaseURL(); got != DefaultURL {
		t.Errorf("client base = %q, want the %q fallback", got, DefaultURL)
	}
	err := Validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("Validate() = %v, want an https requirement error", err)
	}
	if strings.Contains(err.Error(), "hunter2") {
		t.Errorf("error leaks the url's userinfo: %v", err)
	}
}

func TestTruncationWarnsOncePerOpen(t *testing.T) {
	// The crawl records the notice and the opened row announces it, so a
	// fresh crawl and a later cache hit each produce exactly one warning.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var sb strings.Builder
		sb.WriteString(`{"subsonic-response":{"status":"ok","searchResult3":{"song":[`)
		for i := range allPageSize {
			if i > 0 {
				sb.WriteByte(',')
			}
			fmt.Fprintf(&sb, `{"id":"s-%d","title":"T","artist":"A","album":"LP","track":%d,"duration":1}`, i, i)
		}
		sb.WriteString(`]}}}`)
		w.Write([]byte(sb.String()))
	}))
	defer srv.Close()

	count := func() int {
		n := 0
		for _, e := range applog.Drain() {
			if strings.Contains(e.Text, "not the whole collection") {
				n++
			}
		}
		return n
	}
	p := newTestProvider(srv.URL)
	applog.Drain()
	if _, err := p.Tracks(allPurchasesID); err != nil {
		t.Fatalf("Tracks(all) error: %v", err)
	}
	if n := count(); n != 1 {
		t.Errorf("first open warned %d times, want 1", n)
	}
	if _, err := p.Tracks(allPurchasesID); err != nil {
		t.Fatalf("Tracks(all) error: %v", err)
	}
	if n := count(); n != 1 {
		t.Errorf("cached open warned %d times, want 1", n)
	}
}
