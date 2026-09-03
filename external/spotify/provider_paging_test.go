package spotify

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

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

func TestTracksPageIgnoresNonContiguousPages(t *testing.T) {
	calls := 0
	p := savedTracksProvider(t, 200, &calls)
	if _, next, err := p.TracksPage("YOUR MUSIC", 0); err != nil || next != 50 {
		t.Fatalf("first page: next=%d err=%v", next, err)
	}
	// A page from a superseded chain lands at an offset the current
	// accumulation is not waiting for; it must not be spliced in.
	page, next, err := p.TracksPage("YOUR MUSIC", 100)
	if err != nil || len(page) != 50 || next != 150 {
		t.Fatalf("stale page not served to its caller: len=%d next=%d err=%v", len(page), next, err)
	}
	pend := p.pending["YOUR MUSIC"]
	if pend == nil || len(pend.tracks) != 50 || pend.want != 50 {
		t.Fatalf("non-contiguous page polluted accumulation: len=%d want=%d", len(pend.tracks), pend.want)
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

// Accumulations are keyed by playlist, so several lists can each be abandoned
// part-way and later resume from their own stopping point.
func TestTracksPageResumesEachPlaylistIndependently(t *testing.T) {
	totals := map[string]int{"alpha": 200, "beta": 150}
	calls := 0
	originalTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		id := strings.TrimSuffix(strings.TrimPrefix(req.URL.Path, "/v1/playlists/"), "/items")
		total, ok := totals[id]
		if !ok {
			return nil, fmt.Errorf("unexpected Spotify API path %q", req.URL.Path)
		}
		calls++
		offset, _ := strconv.Atoi(req.URL.Query().Get("offset"))
		limit, _ := strconv.Atoi(req.URL.Query().Get("limit"))
		var items []string
		for i := offset; i < total && i < offset+limit; i++ {
			items = append(items, fmt.Sprintf(
				`{"item":{"id":"%s%d","name":"n","type":"track","uri":"spotify:track:%s%d"}}`, id, i, id, i))
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(
				fmt.Sprintf(`{"items":[%s],"total":%d}`, strings.Join(items, ","), total))),
			Request: req,
		}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	sess := &Session{tokenSource: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "token"})}
	p := New(sess, "client", 320)

	// Get two lists part-way, abandoning each.
	for _, step := range []struct {
		id     string
		stopAt int
	}{{"alpha", 100}, {"beta", 50}} {
		offset := 0
		for offset < step.stopAt {
			_, next, err := p.TracksPage(step.id, offset)
			if err != nil {
				t.Fatal(err)
			}
			offset = next
		}
	}

	for _, want := range []struct {
		id     string
		tracks int
		next   int
	}{{"alpha", 100, 100}, {"beta", 50, 50}} {
		calls = 0
		page, next, err := p.TracksPage(want.id, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) != want.tracks || next != want.next {
			t.Errorf("%s resumed with %d tracks at %d, want %d at %d", want.id, len(page), next, want.tracks, want.next)
		}
		if calls != 1 {
			t.Errorf("%s took %d requests to resume, want 1", want.id, calls)
		}
		if page[0].Path != fmt.Sprintf("spotify:track:%s0", want.id) {
			t.Errorf("%s resumed with another playlist's tracks: %s", want.id, page[0].Path)
		}
	}
}
