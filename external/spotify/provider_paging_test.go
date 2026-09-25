package spotify

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/playlist"
	"golang.org/x/oauth2"
)

// stubSavedTracks serves /v1/me/tracks pages rendered by body and counts requests.
func stubSavedTracks(t *testing.T, calls *int, body func(offset, limit int) string) *SpotifyProvider {
	t.Helper()
	originalTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/v1/me/tracks" {
			return nil, fmt.Errorf("unexpected Spotify API path %q", req.URL.Path)
		}
		*calls++
		offset, _ := strconv.Atoi(req.URL.Query().Get("offset"))
		limit, _ := strconv.Atoi(req.URL.Query().Get("limit"))
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body(offset, limit))),
			Request:    req,
		}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = originalTransport })
	sess := &Session{tokenSource: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "token"})}
	return New(sess, "client", 320)
}

func savedTracksProvider(t *testing.T, total int, calls *int) *SpotifyProvider {
	t.Helper()
	return stubSavedTracks(t, calls, func(offset, limit int) string {
		return savedTracksBodyShift(offset, limit, total, 0)
	})
}

func drainSavedTracks(t *testing.T, p *SpotifyProvider) int {
	t.Helper()
	return drainFrom(t, p, 0)
}

func TestTracksPagePagesThroughSavedTracks(t *testing.T) {
	for _, tc := range []struct {
		name      string
		total     int
		wantPages int
	}{
		{name: "empty", total: 0, wantPages: 1},
		{name: "single partial page", total: 20, wantPages: 1},
		{name: "exact page boundary", total: 100, wantPages: 2},
		{name: "multiple pages", total: 120, wantPages: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			p := savedTracksProvider(t, tc.total, &calls)
			if got := drainSavedTracks(t, p); got != tc.total {
				t.Errorf("collected %d tracks, want %d", got, tc.total)
			}
			if calls != tc.wantPages {
				t.Errorf("made %d requests, want %d", calls, tc.wantPages)
			}
		})
	}
}

func TestTracksPageRevalidatesCachedSavedTracks(t *testing.T) {
	calls := 0
	p := savedTracksProvider(t, 120, &calls)
	if got := drainSavedTracks(t, p); got != 120 {
		t.Fatalf("first load collected %d tracks, want 120", got)
	}
	afterLoad := calls

	if got := drainSavedTracks(t, p); got != 120 {
		t.Fatalf("cached load collected %d tracks, want 120", got)
	}
	if calls != afterLoad+1 {
		t.Errorf("cached load made %d requests, want 1 revalidation probe", calls-afterLoad)
	}
}

func TestTracksPageRefetchesWhenSavedTracksChanged(t *testing.T) {
	calls := 0
	p := savedTracksProvider(t, 120, &calls)
	if got := drainSavedTracks(t, p); got != 120 {
		t.Fatalf("first load collected %d tracks, want 120", got)
	}

	calls = 0
	p2 := savedTracksProvider(t, 140, &calls)
	p2.trackCache["YOUR MUSIC"] = p.trackCache["YOUR MUSIC"]
	if got := drainSavedTracks(t, p2); got != 140 {
		t.Errorf("collected %d tracks, want 140 after change", got)
	}
	if calls != 4 {
		t.Errorf("made %d requests, want 4 (probe plus 3 pages)", calls)
	}
}

// A same-size library whose newest entry changed is the case the total check
// alone cannot see; only the newest-URI comparison catches it.
func TestTracksPageRefetchesWhenNewestSavedTrackChanged(t *testing.T) {
	calls := 0
	p := savedTracksProvider(t, 60, &calls)
	if got := drainSavedTracks(t, p); got != 60 {
		t.Fatalf("first load collected %d tracks, want 60", got)
	}

	cached := p.trackCache["YOUR MUSIC"]
	cached.tracks[0].Path = "spotify:track:replaced"

	calls = 0
	if got := drainSavedTracks(t, p); got != 60 {
		t.Errorf("collected %d tracks, want 60", got)
	}
	if calls != 3 {
		t.Errorf("made %d requests, want 3 (probe plus 2 pages)", calls)
	}
}

func TestTracksPageAbortedLoadDoesNotCache(t *testing.T) {
	calls := 0
	p := savedTracksProvider(t, 120, &calls)
	if _, next, err := p.TracksPage("YOUR MUSIC", 0); err != nil || next != 50 {
		t.Fatalf("first page: next=%d err=%v", next, err)
	}
	if cached, ok := p.trackCache["YOUR MUSIC"]; ok && cached.tracks != nil {
		t.Error("partial load committed to cache")
	}
	if got := drainSavedTracks(t, p); got != 120 {
		t.Errorf("restart collected %d tracks, want 120", got)
	}
}

// savedTracksBodyShift renders a saved-tracks page for a library that has had
// shift entries prepended: index i holds new{shift-1-i} for the newest ones and
// t{i-shift} below them, which is how a like actually moves every later index.
func savedTracksBodyShift(offset, limit, total, shift int) string {
	var items []string
	for i := offset; i < total && i < offset+limit; i++ {
		id := fmt.Sprintf("t%d", i-shift)
		if i < shift {
			id = fmt.Sprintf("new%d", shift-1-i)
		}
		items = append(items, fmt.Sprintf(
			`{"track":{"id":"%s","name":"%s","type":"track","uri":"spotify:track:%s"}}`, id, id, id))
	}
	return fmt.Sprintf(`{"items":[%s],"total":%d}`, strings.Join(items, ","), total)
}

// mutatingSavedTracks models a library that grows while it is being read. A
// like prepends, so at snapshot k index i holds new{k-1-i} for the k newest
// entries and t{i-k} below them -- meaning pages read either side of a change
// overlap by one. bumps caps how many times the library moves.
func mutatingSavedTracks(t *testing.T, start, bumps int, calls *int) *SpotifyProvider {
	t.Helper()
	total, done := start, 0
	return stubSavedTracks(t, calls, func(offset, limit int) string {
		body := savedTracksBodyShift(offset, limit, total, done)
		if offset == 0 && done < bumps {
			total++
			done++
		}
		return body
	})
}

// A library that changes mid-load shifts every later offset, so pages from two
// snapshots would splice into a list short by one and duplicated by one. The
// revalidation probe cannot see that, so the load must refuse to commit -- and
// must stop, since every later page would mismatch the pinned snapshot too.
func TestTracksPageAbandonsLoadWhenLibraryChangesMidLoad(t *testing.T) {
	calls := 0
	total := 120
	p := stubSavedTracks(t, &calls, func(offset, limit int) string {
		body := savedTracksBodyShift(offset, limit, total, 0)
		if offset == 0 {
			total++ // someone liked a track while page 1 was in flight
		}
		return body
	})

	var err error
	for offset := 0; ; {
		var next int
		if _, next, err = p.TracksPage("YOUR MUSIC", offset); err != nil || next == 0 {
			break
		}
		offset = next
	}

	if err == nil {
		t.Error("a load spanning two snapshots was allowed to run to completion")
	}
	if cached, ok := p.trackCache["YOUR MUSIC"]; ok && cached.tracks != nil {
		t.Errorf("committed a cache spanning two library snapshots (%d tracks)", len(cached.tracks))
	}
	if _, ok := p.pending["YOUR MUSIC"]; ok {
		t.Error("a doomed accumulation was left behind")
	}
}

// Drift is detected on the page that carries the new total, and nothing after
// it can ever be accumulated. Continuing would spend the rest of the library's
// pages on a result already destined to be discarded.
func TestTracksPageStopsSpendingRequestsAfterDrift(t *testing.T) {
	calls := 0
	total := 1000
	p := stubSavedTracks(t, &calls, func(offset, limit int) string {
		body := savedTracksBodyShift(offset, limit, total, 0)
		if offset == 200 {
			total-- // a track is removed while page 200 is in flight
		}
		return body
	})

	for offset := 0; ; {
		_, next, err := p.TracksPage("YOUR MUSIC", offset)
		if err != nil || next == 0 {
			break
		}
		offset = next
	}

	// Pages 0..200 are five requests; the sixth carries the changed total.
	if calls != 6 {
		t.Errorf("made %d requests, want 6: the chain kept fetching past the drift", calls)
	}
}

func TestTracksRestartsWhenLibraryChangesMidLoad(t *testing.T) {
	calls := 0
	p := mutatingSavedTracks(t, 120, 1, &calls)

	tracks, err := p.Tracks("YOUR MUSIC")
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 121 {
		t.Errorf("got %d tracks, want 121 from a single settled snapshot", len(tracks))
	}
	if len(tracks) > 0 && tracks[0].Path != "spotify:track:new0" {
		t.Errorf("newest track is %s, want the settled snapshot's new0", tracks[0].Path)
	}
	seen := make(map[string]bool, len(tracks))
	for _, tr := range tracks {
		if seen[tr.Path] {
			t.Fatalf("duplicate %s: the list was spliced across two snapshots", tr.Path)
		}
		seen[tr.Path] = true
	}
	if cached := p.trackCache["YOUR MUSIC"]; cached == nil || cached.total != 121 {
		t.Errorf("cached total = %v, want 121", cached)
	}
}

func TestTracksGivesUpOnAContinuouslyChangingLibrary(t *testing.T) {
	calls := 0
	p := mutatingSavedTracks(t, 120, 99, &calls)

	if _, err := p.Tracks("YOUR MUSIC"); err == nil {
		t.Fatal("expected an error when the library never settles")
	}
	if _, ok := p.trackCache["YOUR MUSIC"]; ok {
		t.Error("cached a list assembled from a library that never settled")
	}
}

// Backing out of a still-loading list and re-entering must not refetch the
// pages already paid for; the accumulation resumes where it stopped.
func TestTracksPageResumesAnAbandonedLoad(t *testing.T) {
	calls := 0
	p := savedTracksProvider(t, 200, &calls)

	if _, next, err := p.TracksPage("YOUR MUSIC", 0); err != nil || next != 50 {
		t.Fatalf("page 0: next=%d err=%v", next, err)
	}
	if _, next, err := p.TracksPage("YOUR MUSIC", 50); err != nil || next != 100 {
		t.Fatalf("page 50: next=%d err=%v", next, err)
	}
	calls = 0

	page, next, err := p.TracksPage("YOUR MUSIC", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 100 {
		t.Errorf("resumed with %d tracks, want the 100 already loaded", len(page))
	}
	if next != 100 {
		t.Errorf("resumed at offset %d, want 100", next)
	}
	if calls != 1 {
		t.Errorf("made %d requests to resume, want 1 (page 0 only)", calls)
	}

	if got := drainFrom(t, p, next) + len(page); got != 200 {
		t.Errorf("collected %d tracks in total, want 200", got)
	}
	if cached := p.trackCache["YOUR MUSIC"]; cached == nil || len(cached.tracks) != 200 {
		t.Error("resumed load did not commit a complete cache")
	}
}

// A library that changed while the user was away must discard the abandoned
// accumulation rather than resuming onto a different snapshot.
func TestTracksPageDiscardsAbandonedLoadWhenLibraryChanged(t *testing.T) {
	calls := 0
	p := savedTracksProvider(t, 200, &calls)

	if _, next, err := p.TracksPage("YOUR MUSIC", 0); err != nil || next != 50 {
		t.Fatalf("page 0: next=%d err=%v", next, err)
	}
	if _, _, err := p.TracksPage("YOUR MUSIC", 50); err != nil {
		t.Fatal(err)
	}
	// Someone likes a track while the list is closed.
	p.pending["YOUR MUSIC"].total = 199

	page, next, err := p.TracksPage("YOUR MUSIC", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 50 || next != 50 {
		t.Errorf("got %d tracks and next=%d, want a fresh page 0 of 50 and next=50", len(page), next)
	}
	if pend := p.pending["YOUR MUSIC"]; pend == nil || len(pend.tracks) != 50 || pend.total != 200 {
		t.Error("stale accumulation survived a snapshot change")
	}
}

func drainFrom(t *testing.T, p *SpotifyProvider, offset int) int {
	t.Helper()
	got := 0
	for {
		page, next, err := p.TracksPage("YOUR MUSIC", offset)
		if err != nil {
			t.Fatal(err)
		}
		got += len(page)
		if next == 0 {
			return got
		}
		offset = next
	}
}

// A same-total swap while the list is closed -- one track unliked, another
// liked -- leaves the total intact but moves the head. Resuming onto that
// accumulation would splice the old ordering onto a new suffix, so the head
// comparison must discard it even though the total still matches.
func TestTracksPageDiscardsAbandonedLoadWhenHeadChangedAtSameTotal(t *testing.T) {
	calls := 0
	shift := 0
	p := stubSavedTracks(t, &calls, func(offset, limit int) string {
		return savedTracksBodyShift(offset, limit, 200, shift)
	})

	if _, next, err := p.TracksPage("YOUR MUSIC", 0); err != nil || next != 50 {
		t.Fatalf("page 0: next=%d err=%v", next, err)
	}
	if _, _, err := p.TracksPage("YOUR MUSIC", 50); err != nil {
		t.Fatal(err)
	}
	shift = 1 // one unliked, one liked: same total, different head

	page, next, err := p.TracksPage("YOUR MUSIC", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 50 || next != 50 {
		t.Fatalf("got %d tracks and next=%d, want a fresh page 0 of 50 and next=50", len(page), next)
	}
	pend := p.pending["YOUR MUSIC"]
	if pend == nil || len(pend.tracks) != 50 {
		t.Fatalf("stale accumulation survived a same-total head change: %v", pend)
	}
	if pend.tracks[0].Path != "spotify:track:new0" {
		t.Errorf("restarted accumulation begins with %s, want the new head", pend.tracks[0].Path)
	}
}

// playlistStub serves one playlist's items plus its snapshot_id, so a test can
// move the playlist underneath a load without changing its length.
func playlistStub(t *testing.T, id string, total int, snapshot, prefix *string, calls *int) *SpotifyProvider {
	t.Helper()
	originalTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		*calls++
		switch req.URL.Path {
		case "/v1/playlists/" + id:
			return jsonResponse(req, fmt.Sprintf(`{"snapshot_id":%q}`, *snapshot))
		case "/v1/playlists/" + id + "/items":
			offset, _ := strconv.Atoi(req.URL.Query().Get("offset"))
			limit, _ := strconv.Atoi(req.URL.Query().Get("limit"))
			var items []string
			for i := offset; i < total && i < offset+limit; i++ {
				items = append(items, fmt.Sprintf(
					`{"item":{"id":"%s%d","name":"n","type":"track","uri":"spotify:track:%s%d"}}`,
					*prefix, i, *prefix, i))
			}
			return jsonResponse(req, fmt.Sprintf(`{"items":[%s],"total":%d}`, strings.Join(items, ","), total))
		}
		return nil, fmt.Errorf("unexpected Spotify API path %q", req.URL.Path)
	})
	t.Cleanup(func() { http.DefaultTransport = originalTransport })
	sess := &Session{tokenSource: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "token"})}
	p := New(sess, "client", 320)
	p.trackCache[id] = &playlistCache{snapshotID: *snapshot}
	return p
}

func jsonResponse(req *http.Request, body string) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: make(http.Header),
		Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
}

// An unchanged snapshot_id is Spotify's own proof that a playlist has not been
// touched, so the pages already read can be trusted and the load resumes.
func TestTracksPageResumesPlaylistOnUnchangedSnapshot(t *testing.T) {
	calls := 0
	snapshot, prefix := "snap-1", "p"
	p := playlistStub(t, "alpha", 200, &snapshot, &prefix, &calls)

	for offset := 0; offset < 100; {
		_, next, err := p.TracksPage("alpha", offset)
		if err != nil {
			t.Fatal(err)
		}
		offset = next
	}
	calls = 0

	page, next, err := p.TracksPage("alpha", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 100 || next != 100 {
		t.Errorf("resumed with %d tracks at %d, want 100 at 100", len(page), next)
	}
	if calls != 2 {
		t.Errorf("took %d requests to resume, want 2 (page 0 plus the snapshot probe)", calls)
	}
}

// A changed snapshot_id means the playlist was edited, and an ordinary playlist
// can be edited anywhere -- a same-total edit below the head would shift every
// later offset and stitch the halves together a track short. Restart instead.
func TestTracksPageRestartsPlaylistOnChangedSnapshot(t *testing.T) {
	calls := 0
	snapshot, prefix := "snap-1", "p"
	p := playlistStub(t, "alpha", 200, &snapshot, &prefix, &calls)

	for offset := 0; offset < 100; {
		_, next, err := p.TracksPage("alpha", offset)
		if err != nil {
			t.Fatal(err)
		}
		offset = next
	}

	snapshot, prefix = "snap-2", "q" // edited: same length, different contents

	page, next, err := p.TracksPage("alpha", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 50 || next != 50 {
		t.Fatalf("got %d tracks and next=%d, want a fresh page 0 of 50 and next=50", len(page), next)
	}
	if page[0].Path != "spotify:track:q0" {
		t.Errorf("restarted page 0 begins with %s, want the edited playlist's head", page[0].Path)
	}
}

// Saved albums come from /v1/albums/{id}/tracks, not the playlist-items
// endpoint. Every Spotify list opens through TracksPage now, so without a guard
// here an album ID would be spliced into a playlist URL.
func TestTracksPageServesSavedAlbumsWholly(t *testing.T) {
	var paths []string
	originalTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		paths = append(paths, req.URL.Path)
		var body string
		switch {
		case strings.HasPrefix(req.URL.Path, "/v1/albums/") && strings.HasSuffix(req.URL.Path, "/tracks"):
			body = `{"items":[{"id":"a1","name":"One","type":"track","uri":"spotify:track:a1"},` +
				`{"id":"a2","name":"Two","type":"track","uri":"spotify:track:a2"}],"total":2}`
		case strings.HasPrefix(req.URL.Path, "/v1/albums/"):
			body = `{"id":"alb","name":"Album","total_tracks":2,"artists":[{"name":"Artist"}]}`
		default:
			return nil, fmt.Errorf("unexpected Spotify API path %q", req.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	sess := &Session{tokenSource: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "token"})}
	p := New(sess, "client", 320)

	tracks, next, err := p.TracksPage(savedAlbumIDPrefix+"alb", 0)
	if err != nil {
		t.Fatalf("saved album failed to load: %v", err)
	}
	if next != 0 {
		t.Errorf("next = %d, want 0: an album arrives whole", next)
	}
	if len(tracks) != 2 {
		t.Errorf("got %d tracks, want 2", len(tracks))
	}
	for _, path := range paths {
		if strings.HasPrefix(path, "/v1/playlists/") {
			t.Errorf("an album ID was routed to the playlist-items endpoint: %s", path)
		}
	}
}

// On the client path a page is sliced from a resolved URI list, so validating a
// resume against a cached resolve would be the snapshot the abandoned chain
// started from agreeing with itself. The cached resolve must be dropped before
// page 0 is fetched, so the proof describes the library now.
func TestTracksPageReresolvesBeforeProvingAResume(t *testing.T) {
	calls := 0
	p := savedTracksProvider(t, 200, &calls)

	if _, next, err := p.TracksPage("YOUR MUSIC", 0); err != nil || next != 50 {
		t.Fatalf("page 0: next=%d err=%v", next, err)
	}
	if _, _, err := p.TracksPage("YOUR MUSIC", 50); err != nil {
		t.Fatal(err)
	}

	// Stand in for the resolve the abandoned chain was reading from, and watch
	// what page zero is handed.
	p.mu.Lock()
	pend := p.pending["YOUR MUSIC"]
	if pend == nil {
		p.mu.Unlock()
		t.Fatal("no accumulation to resume; the test no longer covers its case")
	}
	pend.uris = []string{"spotify:track:stale"}
	p.mu.Unlock()

	var carried [][]string
	real := p.clientPage
	p.clientPage = func(ctx context.Context, id string, off int, uris []string) (tracksPage, error) {
		carried = append(carried, uris)
		return real(ctx, id, off, uris)
	}

	if _, _, err := p.TracksPage("YOUR MUSIC", 0); err != nil {
		t.Fatal(err)
	}

	for _, u := range carried {
		if u != nil {
			t.Error("page zero was handed the abandoned read's resolve, so the proof compares that snapshot with itself")
		}
	}
}

// Liked Songs leads with the client protocol, so a client path that cannot
// serve must be asked once per read and skipped for the rest of it -- the
// mirror of the stickiness the Web API path already has.
func TestTracksPageStopsRetryingADecliningClientPath(t *testing.T) {
	calls := 0
	p := savedTracksProvider(t, 200, &calls)

	attempts := 0
	real := p.clientPage
	p.clientPage = func(ctx context.Context, id string, off int, uris []string) (tracksPage, error) {
		attempts++
		return real(ctx, id, off, uris)
	}

	// No librespot session, so every client attempt fails and the Web API serves.
	if got := drainSavedTracks(t, p); got != 200 {
		t.Fatalf("collected %d tracks, want 200", got)
	}
	if attempts != 1 {
		t.Errorf("client path attempted %d times in one read, want 1 -- the decline is not preventing anything", attempts)
	}
}

// A read that ends without completing -- an error, or the user backing out --
// must not leave the client path refused for every later read.
func TestTracksPageForgetsADeclineFromAnAbandonedRead(t *testing.T) {
	calls := 0
	p := savedTracksProvider(t, 200, &calls)

	attempts := 0
	real := p.clientPage
	p.clientPage = func(ctx context.Context, id string, off int, uris []string) (tracksPage, error) {
		attempts++
		return real(ctx, id, off, uris)
	}

	// Read page zero, then walk away without finishing the list.
	if _, _, err := p.TracksPage("YOUR MUSIC", 0); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 {
		t.Fatalf("client attempted %d times on page zero, want 1", attempts)
	}

	// Both flags belong to the read that recorded them. The Web API's is set
	// here by hand because reaching it needs a refusal the stub does not give,
	// and an unforgotten one would send every later read of this list to the
	// undocumented endpoint for the life of the process.
	p.noteWebDeclined("YOUR MUSIC")

	// Re-entering is a new read and must ask both paths again.
	if _, _, err := p.TracksPage("YOUR MUSIC", 0); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 {
		t.Errorf("client attempted %d times across two reads, want 2 -- a decline outlived the read that recorded it", attempts)
	}
	if p.webDeclined("YOUR MUSIC") {
		t.Error("a web decline outlived the read that recorded it")
	}
}

// Tracks serves the committed list to IPC and CLI callers. Liked Songs has no
// snapshot_id to invalidate it, so without a check of its own those callers
// would get a list of any age.
func TestTracksRevalidatesCachedSavedTracks(t *testing.T) {
	calls := 0
	p := savedTracksProvider(t, 100, &calls)

	first, err := p.Tracks("YOUR MUSIC")
	if err != nil || len(first) != 100 {
		t.Fatalf("first read: %d tracks, err=%v", len(first), err)
	}
	before := calls

	if _, err := p.Tracks("YOUR MUSIC"); err != nil {
		t.Fatal(err)
	}
	if calls != before+1 {
		t.Errorf("a cached read made %d requests, want exactly 1 (the revalidation probe); "+
			"0 means nothing was checked, more means the cache was skipped entirely", calls-before)
	}
}

// Tracks() shares the refusal flags with the paged read, so a decline recorded
// by an abandoned TracksPage read must not route a later Tracks() read down
// the losing path either.
func TestTracksForgetsADeclineFromAnAbandonedRead(t *testing.T) {
	calls := 0
	p := savedTracksProvider(t, 200, &calls)

	attempts := 0
	real := p.clientPage
	p.clientPage = func(ctx context.Context, id string, off int, uris []string) (tracksPage, error) {
		attempts++
		return real(ctx, id, off, uris)
	}

	// Abandon a paged read after page zero declines the client path.
	if _, _, err := p.TracksPage("YOUR MUSIC", 0); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 {
		t.Fatalf("client attempted %d times on page zero, want 1", attempts)
	}

	// Tracks() is a new read and must ask the client path again.
	if _, err := p.Tracks("YOUR MUSIC"); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 {
		t.Errorf("client attempted %d times across two reads, want 2 -- a decline outlived the read that recorded it", attempts)
	}
}

// A read already paging Liked Songs is slicing the stored resolve. The cached
// list cannot be proved current without taking that resolve away, so the check
// must leave it alone and report unproven -- the re-read it causes is the
// designed failure direction.
func TestSavedTracksCheckLeavesALiveChainAlone(t *testing.T) {
	p := &SpotifyProvider{
		trackCache: map[string]*playlistCache{},
		pending: map[string]*pendingTracks{"YOUR MUSIC": {
			total: 1, want: 1, uris: []string{"spotify:track:a"},
		}},
	}

	unchanged, err := p.savedTracksUnchangedClient(context.Background(), []playlist.Track{{Path: "spotify:track:a"}}, 1)
	if err != nil {
		t.Fatalf("a live chain made the check fail rather than report unproven: %v", err)
	}
	if unchanged {
		t.Error("a live chain cannot prove the cache current, so the check must ask for a re-read")
	}
	if pend := p.pending["YOUR MUSIC"]; pend == nil || len(pend.uris) != 1 {
		t.Error("the check disturbed a live read's resolve")
	}
}

// seedResolve stands in for a client-served read having resolved the list.
func seedResolve(p *SpotifyProvider, playlistID string) {
	p.mu.Lock()
	if pend := p.pending[playlistID]; pend != nil {
		pend.uris = []string{"spotify:track:a", "spotify:track:b"}
	}
	p.mu.Unlock()
}

// resolveHeld reports whether any read still holds a resolve for this list.
func resolveHeld(p *SpotifyProvider, playlistID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	pend := p.pending[playlistID]
	return pend != nil && pend.uris != nil
}

// However a read ends, the snapshot it resolved must end with it. A resolve
// that outlives its read would be sliced by the next one, which is the whole
// list served from contents nothing revalidated.
func TestReadEndsDropTheResolve(t *testing.T) {
	t.Run("committed", func(t *testing.T) {
		calls := 0
		p := savedTracksProvider(t, 100, &calls)
		seedResolve(p, "YOUR MUSIC")
		if got := drainSavedTracks(t, p); got != 100 {
			t.Fatalf("collected %d tracks, want 100", got)
		}
		if resolveHeld(p, "YOUR MUSIC") {
			t.Error("the resolve outlived a completed read")
		}
	})

	t.Run("abandoned on drift", func(t *testing.T) {
		calls := 0
		total := 120
		p := stubSavedTracks(t, &calls, func(offset, limit int) string {
			body := savedTracksBodyShift(offset, limit, total, 0)
			if offset == 0 {
				total++ // liked while page one was in flight
			}
			return body
		})
		seedResolve(p, "YOUR MUSIC")

		var err error
		for offset := 0; ; {
			var next int
			if _, next, err = p.TracksPage("YOUR MUSIC", offset); err != nil || next == 0 {
				break
			}
			offset = next
		}
		if err == nil {
			t.Fatal("a read spanning two snapshots ran to completion")
		}
		if resolveHeld(p, "YOUR MUSIC") {
			t.Error("the resolve outlived a read the library changed under")
		}
	})
}

// Only Liked Songs leads with the client protocol. An ordinary playlist must
// still try the Web API first, which is what keeps default behaviour unchanged.
func TestOrdinaryPlaylistsStillLeadWithTheWebAPI(t *testing.T) {
	calls := 0
	snapshot, prefix := "snap-1", "t"
	p := playlistStub(t, "somelist", 60, &snapshot, &prefix, &calls)

	attempts := 0
	real := p.clientPage
	p.clientPage = func(ctx context.Context, id string, off int, uris []string) (tracksPage, error) {
		attempts++
		return real(ctx, id, off, uris)
	}

	if _, _, err := p.TracksPage("somelist", 0); err != nil {
		t.Fatal(err)
	}
	if attempts != 0 {
		t.Errorf("an ordinary playlist reached the client protocol %d times before the Web API", attempts)
	}
	if calls == 0 {
		t.Error("the Web API was never asked")
	}
}

// The reason the client path exists at all: a playlist the Web API refuses --
// another user's list, a Spotify-owned mix -- must be served whole by the
// client protocol, and the refusal must not be re-earned once per page.
func TestTracksPageServesAWebRefusedPlaylistThroughTheClient(t *testing.T) {
	webCalls := 0
	originalTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/v1/playlists/mix/items" {
			return nil, fmt.Errorf("unexpected Spotify API path %q", req.URL.Path)
		}
		webCalls++
		return &http.Response{StatusCode: http.StatusForbidden, Status: "403 Forbidden",
			Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: req}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = originalTransport })
	sess := &Session{tokenSource: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "token"})}
	p := New(sess, "client", 320)

	uris := make([]string, 150)
	for i := range uris {
		uris[i] = fmt.Sprintf("spotify:track:m%d", i)
	}
	p.clientPage = func(_ context.Context, _ string, off int, carried []string) (tracksPage, error) {
		if carried == nil {
			carried = uris
		}
		page := tracksPage{total: len(carried), pageSize: spotifyMetadataBatch, uris: carried}
		for _, u := range carried[off:min(off+spotifyMetadataBatch, len(carried))] {
			page.tracks = append(page.tracks, playlist.Track{Path: u})
		}
		return page, nil
	}

	var got []playlist.Track
	for offset := 0; ; {
		page, next, err := p.TracksPage("mix", offset)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, page...)
		if next == 0 {
			break
		}
		offset = next
	}
	if len(got) != 150 {
		t.Fatalf("collected %d tracks, want 150", len(got))
	}
	if webCalls != 1 {
		t.Errorf("asked the refusing Web API %d times, want 1", webCalls)
	}
}

// A continuation slices the resolve its own accumulation holds, but the fetch
// happens outside the lock, and a page-zero re-entry -- a second daemon
// connection re-opening the list -- can replace the accumulation while that
// page is in flight. When the replacement happened because the library moved,
// the in-flight page still belongs to the dead read's snapshot, and a
// same-total edit is exactly the replacement reason that no later total check
// can catch. The page must be served to its caller, never accumulated into the
// read that replaced it.
func TestTracksPageDoesNotAccumulateIntoAReplacedRead(t *testing.T) {
	snapshot := "snap-1"
	oldList := make([]string, 200)
	newList := make([]string, 200)
	for i := range oldList {
		oldList[i] = fmt.Sprintf("spotify:track:p%d", i)
		newList[i] = fmt.Sprintf("spotify:track:p%d", i)
		if i >= 100 {
			newList[i] = fmt.Sprintf("spotify:track:q%d", i) // edited below the head, same total
		}
	}
	list := oldList

	originalTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/v1/playlists/alpha" {
			return jsonResponse(req, fmt.Sprintf(`{"snapshot_id":%q}`, snapshot))
		}
		return nil, fmt.Errorf("unexpected Spotify API path %q", req.URL.Path)
	})
	t.Cleanup(func() { http.DefaultTransport = originalTransport })
	sess := &Session{tokenSource: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "token"})}
	p := New(sess, "client", 320)
	p.apiMode = apiModeClient
	p.trackCache["alpha"] = &playlistCache{snapshotID: snapshot}

	serve := func(ctx context.Context, id string, off int, uris []string) (tracksPage, error) {
		if uris == nil {
			uris = list // a fresh resolve sees the library as it is now
		}
		page := tracksPage{total: len(uris), pageSize: 100, uris: uris}
		for _, u := range uris[off:min(off+100, len(uris))] {
			page.tracks = append(page.tracks, playlist.Track{Path: u})
		}
		return page, nil
	}
	p.clientPage = serve

	// Read one takes page zero and is left mid-flight on the old snapshot.
	if _, next, err := p.TracksPage("alpha", 0); err != nil || next != 100 {
		t.Fatalf("page zero: next=%d err=%v", next, err)
	}

	// While read one's continuation is in flight, the playlist is edited to the
	// same-length newList and a second reader re-enters at page zero, whose
	// proof sees the new snapshot and restarts the accumulation.
	inContinuation := false
	p.clientPage = func(ctx context.Context, id string, off int, uris []string) (tracksPage, error) {
		if off == 100 && !inContinuation {
			inContinuation = true
			list, snapshot = newList, "snap-2"
			if _, _, err := p.TracksPage("alpha", 0); err != nil {
				t.Fatalf("second reader's page zero: %v", err)
			}
			inContinuation = false
		}
		return serve(ctx, id, off, uris)
	}

	page, next, err := p.TracksPage("alpha", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 100 || next != 0 {
		t.Fatalf("continuation served %d tracks with next=%d, want 100 and 0", len(page), next)
	}

	pend := p.pending["alpha"]
	if pend == nil {
		t.Fatal("the replaced read's accumulation vanished: the superseded page completed it and it committed")
	}
	if len(pend.tracks) != 100 || pend.want != 100 {
		t.Fatalf("replaced read's accumulation holds %d tracks at want %d, want 100 at 100", len(pend.tracks), pend.want)
	}
	for i, tr := range pend.tracks {
		want := fmt.Sprintf("spotify:track:p%d", i)
		if i >= 100 {
			want = fmt.Sprintf("spotify:track:q%d", i)
		}
		if tr.Path != want {
			t.Fatalf("accumulation[%d] = %s, want %s: a superseded page was spliced into the read that replaced it", i, tr.Path, want)
		}
	}
	if cached := p.trackCache["alpha"]; cached != nil && cached.tracks != nil {
		t.Error("a superseded page completed a read it does not belong to and committed the cache")
	}
}

// The web API counts every playlist item; the client protocol's resolve keeps
// tracks only, so the two can disagree about the total even with nothing
// edited. When that mismatch restarts Tracks(), the restarted read must drop
// the resolve the failed attempt was holding and take a fresh one, or it will
// finish from a snapshot taken before whatever moved the total.
func TestTracksRestartDropsTheResolveItWasHolding(t *testing.T) {
	originalTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/v1/playlists/beta/items" && req.URL.Query().Get("offset") == "0" {
			return jsonResponse(req, `{"items":[],"total":1005}`)
		}
		return nil, fmt.Errorf("http status 500 Internal Server Error")
	})
	t.Cleanup(func() { http.DefaultTransport = originalTransport })
	sess := &Session{tokenSource: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "token"})}
	p := New(sess, "client", 320)

	uris := make([]string, 1000)
	for i := range uris {
		uris[i] = fmt.Sprintf("spotify:track:t%d", i)
	}
	var zeroOffsetCarries [][]string
	p.clientPage = func(ctx context.Context, id string, off int, carried []string) (tracksPage, error) {
		if off == 0 {
			zeroOffsetCarries = append(zeroOffsetCarries, carried)
		}
		if carried == nil {
			carried = uris
		}
		page := tracksPage{total: len(carried), pageSize: 100, uris: carried}
		for _, u := range carried[off:min(off+100, len(carried))] {
			page.tracks = append(page.tracks, playlist.Track{Path: u})
		}
		return page, nil
	}

	tracks, err := p.Tracks("beta")
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 1000 {
		t.Fatalf("collected %d tracks, want the resolve's 1000", len(tracks))
	}
	// The first page went to the Web API (total 1005), the first client page
	// disagreed (1000) and restarted the read. That restart's page zero must
	// have been handed nothing, so it took a fresh resolve.
	if len(zeroOffsetCarries) != 1 || zeroOffsetCarries[0] != nil {
		t.Errorf("the restarted read's page zero carried %v, want nil: it is slicing the failed attempt's snapshot", zeroOffsetCarries)
	}
}

// The resume proof runs against one accumulation, but the probe is a request,
// and a page-zero re-entry can replace that accumulation while the probe is in
// flight. Adopting into the replacement hands it the prover's resolve -- taken
// before whatever moved the library -- so the replacement's own pages and the
// prover's continuation splice two snapshots into one committed list.
func TestTracksPageDoesNotAdoptIntoAReplacedRead(t *testing.T) {
	snapshot := "snap-1"
	oldList := make([]string, 200)
	newList := make([]string, 200)
	for i := range oldList {
		oldList[i] = fmt.Sprintf("spotify:track:p%d", i)
		newList[i] = fmt.Sprintf("spotify:track:q%d", i)
	}
	list := oldList

	probes := 0
	var p *SpotifyProvider
	originalTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/v1/playlists/alpha" {
			return nil, fmt.Errorf("unexpected Spotify API path %q", req.URL.Path)
		}
		probes++
		if probes == 1 {
			// The library moves while the prover's probe is in flight, and a
			// third reader re-enters at page zero: its proof sees the moved
			// snapshot and replaces the abandoned accumulation.
			list, snapshot = newList, "snap-2"
			if _, _, err := p.TracksPage("alpha", 0); err != nil {
				t.Fatalf("replacing reader's page zero: %v", err)
			}
			// The probe itself left before the edit, so it answers with the
			// snapshot it was asked about.
			return jsonResponse(req, `{"snapshot_id":"snap-1"}`)
		}
		return jsonResponse(req, fmt.Sprintf(`{"snapshot_id":%q}`, snapshot))
	})
	t.Cleanup(func() { http.DefaultTransport = originalTransport })
	sess := &Session{tokenSource: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "token"})}
	p = New(sess, "client", 320)
	p.apiMode = apiModeClient
	p.trackCache["alpha"] = &playlistCache{snapshotID: "snap-1"}

	serve := func(ctx context.Context, id string, off int, uris []string) (tracksPage, error) {
		if uris == nil {
			uris = list
		}
		page := tracksPage{total: len(uris), pageSize: 100, uris: uris}
		for _, u := range uris[off:min(off+100, len(uris))] {
			page.tracks = append(page.tracks, playlist.Track{Path: u})
		}
		return page, nil
	}
	p.clientPage = serve

	// Read one takes page zero on the old snapshot and is abandoned mid-load.
	if _, next, err := p.TracksPage("alpha", 0); err != nil || next != 100 {
		t.Fatalf("page zero: next=%d err=%v", next, err)
	}

	// Read two re-enters; its probe passes against the old snapshot, but the
	// accumulation it proved against has been replaced by the time it lands.
	page, next, err := p.TracksPage("alpha", 0)
	if err != nil {
		t.Fatal(err)
	}
	if next != 100 {
		t.Fatalf("read two resumed at %d, want 100", next)
	}
	// Whatever chain is standing must be uniform: read two either adopted the
	// accumulation it proved against or restarted its own. Serving the
	// replacement's tracks with the prover's resolve is the splice.
	if page[0].Path != "spotify:track:p0" {
		t.Fatalf("read two was served the replacement's head %s with a resolve from before the edit", page[0].Path)
	}

	// The standing chain runs to completion and commits.
	for next != 0 {
		if _, next, err = p.TracksPage("alpha", next); err != nil {
			t.Fatal(err)
		}
	}
	cached := p.trackCache["alpha"]
	if cached == nil || len(cached.tracks) != 200 {
		t.Fatalf("committed %v tracks, want 200", cached)
	}
	for i, tr := range cached.tracks {
		if want := fmt.Sprintf("spotify:track:p%d", i); tr.Path != want {
			t.Fatalf("committed list spliced at [%d]: got %s among p*, want a list from one snapshot", i, tr.Path)
		}
	}
}

// Two readers can hold the same continuation page in flight at once -- two
// daemon connections paging one list in lockstep. Both capture the same
// accumulation and slice the same page from it; the first to land advances the
// offset the accumulation wants, so the second must be served to its caller
// without being accumulated, or its page is appended a second time and the
// committed list grows duplicates.
func TestTracksPageIgnoresADuplicateContinuation(t *testing.T) {
	uris := make([]string, 300)
	for i := range uris {
		uris[i] = fmt.Sprintf("spotify:track:t%d", i)
	}

	sess := &Session{tokenSource: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "token"})}
	p := New(sess, "client", 320)
	p.apiMode = apiModeClient
	raced := false
	p.clientPage = func(ctx context.Context, id string, off int, carried []string) (tracksPage, error) {
		if carried == nil {
			carried = uris
		}
		if off == 100 && !raced {
			// A second reader asks for the same page while the first is in
			// flight, and lands first.
			raced = true
			if _, _, err := p.TracksPage(id, 100); err != nil {
				t.Fatalf("racing continuation: %v", err)
			}
		}
		page := tracksPage{total: len(carried), pageSize: 100, uris: carried}
		for _, u := range carried[off:min(off+100, len(carried))] {
			page.tracks = append(page.tracks, playlist.Track{Path: u})
		}
		return page, nil
	}

	if _, next, err := p.TracksPage("alpha", 0); err != nil || next != 100 {
		t.Fatalf("page zero: next=%d err=%v", next, err)
	}
	// This continuation's twin has already landed inside the fetch above.
	if _, next, err := p.TracksPage("alpha", 100); err != nil || next != 200 {
		t.Fatalf("continuation: next=%d err=%v", next, err)
	}

	pend := p.pending["alpha"]
	if pend == nil {
		t.Fatal("the accumulation vanished mid-load")
	}
	if len(pend.tracks) != 200 || pend.want != 200 {
		t.Fatalf("accumulation holds %d tracks at want %d, want 200 at 200: a page already landed was accumulated twice",
			len(pend.tracks), pend.want)
	}
	seen := map[string]int{}
	for _, tr := range pend.tracks {
		seen[tr.Path]++
	}
	for uri, n := range seen {
		if n != 1 {
			t.Fatalf("%s appears %d times in the accumulation", uri, n)
		}
	}
}

// A resume keeps paying for the remaining pages with the resolve the proof just
// validated, not the one the abandoned read was holding: the proof describes
// the library now, and only that resolve is known to agree with it.
func TestAResumedReadCarriesTheFreshResolveForward(t *testing.T) {
	sess := &Session{tokenSource: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "token"})}
	p := New(sess, "client", 320)
	p.apiMode = apiModeClient
	fresh := make([]string, 200)
	for i := range fresh {
		fresh[i] = fmt.Sprintf("spotify:track:f%d", i)
	}
	p.clientPage = func(ctx context.Context, id string, off int, uris []string) (tracksPage, error) {
		if uris == nil {
			uris = fresh
		}
		page := tracksPage{total: len(uris), pageSize: 100, uris: uris}
		for _, u := range uris[off:min(off+100, len(uris))] {
			page.tracks = append(page.tracks, playlist.Track{Path: u})
		}
		return page, nil
	}

	if _, next, err := p.TracksPage("YOUR MUSIC", 0); err != nil || next != 100 {
		t.Fatalf("page zero: next=%d err=%v", next, err)
	}
	// Stand in for the resolve the abandoned chain was reading from.
	p.mu.Lock()
	p.pending["YOUR MUSIC"].uris = []string{"spotify:track:stale"}
	p.mu.Unlock()

	if _, next, err := p.TracksPage("YOUR MUSIC", 0); err != nil || next != 100 {
		t.Fatalf("resume: next=%d err=%v", next, err)
	}
	p.mu.Lock()
	pend := p.pending["YOUR MUSIC"]
	held := pend.uris
	p.mu.Unlock()
	if len(held) != len(fresh) || held[0] != fresh[0] {
		t.Fatalf("the resumed read is slicing %v, want the resolve its proof validated", held)
	}
}

// A read slices every page from the snapshot it started with. Before the
// resolve belonged to the read, it sat in a map keyed by playlist, so a second
// reader of the same list -- reachable in daemon mode, where each IPC
// connection gets its own goroutine -- would overwrite or delete it, and the
// first read would finish by slicing somebody else's snapshot. A same-total
// edit in that window commits a list short by one and duplicated by one, which
// nothing afterwards can detect.
func TestConcurrentReadsDoNotShareAResolve(t *testing.T) {
	calls := 0
	p := savedTracksProvider(t, 200, &calls)

	// Two reads, each with its own resolve, distinguishable by content.
	first := []string{"spotify:track:first"}
	second := []string{"spotify:track:second"}

	var served [][]string
	p.clientPage = func(_ context.Context, _ string, offset int, uris []string) (tracksPage, error) {
		served = append(served, uris)
		if uris == nil {
			uris = first
		}
		return tracksPage{
			tracks:   []playlist.Track{{Path: uris[0]}},
			total:    2,
			pageSize: 1,
			uris:     uris,
		}, nil
	}
	p.apiMode = apiModeClient

	// Read one takes page zero and is left mid-flight.
	if _, next, err := p.TracksPage("YOUR MUSIC", 0); err != nil || next != 1 {
		t.Fatalf("read one page zero: next=%d err=%v", next, err)
	}
	p.mu.Lock()
	pend := p.pending["YOUR MUSIC"]
	if pend == nil {
		p.mu.Unlock()
		t.Fatal("read one left no accumulation")
	}
	pend.uris = second // a second reader's snapshot, were it able to reach in
	p.mu.Unlock()

	// Read one's continuation must slice what IT is holding, whatever that is,
	// rather than a snapshot handed to it by a map shared with other readers.
	served = nil
	if _, _, err := p.TracksPage("YOUR MUSIC", 1); err != nil {
		t.Fatal(err)
	}
	if len(served) != 1 {
		t.Fatalf("continuation made %d client calls, want 1", len(served))
	}
	if len(served[0]) == 0 || served[0][0] != "spotify:track:second" {
		t.Errorf("continuation sliced %v, want the resolve its own accumulation holds", served[0])
	}
}
