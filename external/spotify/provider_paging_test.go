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

func savedTracksBody(offset, limit, total int) string {
	var items []string
	for i := offset; i < total && i < offset+limit; i++ {
		items = append(items, fmt.Sprintf(
			`{"track":{"id":"t%d","name":"Track %d","type":"track","uri":"spotify:track:t%d"}}`, i, i, i))
	}
	return fmt.Sprintf(`{"items":[%s],"total":%d}`, strings.Join(items, ","), total)
}

func savedTracksProvider(t *testing.T, total int, calls *int) *SpotifyProvider {
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
			Body:       io.NopCloser(strings.NewReader(savedTracksBody(offset, limit, total))),
			Request:    req,
		}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = originalTransport })
	sess := &Session{tokenSource: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "token"})}
	return New(sess, "client", 320)
}

func drainSavedTracks(t *testing.T, p *SpotifyProvider) int {
	t.Helper()
	got, offset := 0, 0
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

// A library that changes mid-load shifts every later offset, so pages from two
// snapshots would splice into a list short by one and duplicated by one. The
// revalidation probe cannot see that, so the accumulation must refuse to commit.
func TestTracksPageAbandonsLoadWhenLibraryChangesMidLoad(t *testing.T) {
	calls := 0
	total := 120
	originalTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/v1/me/tracks" {
			return nil, fmt.Errorf("unexpected Spotify API path %q", req.URL.Path)
		}
		calls++
		offset, _ := strconv.Atoi(req.URL.Query().Get("offset"))
		limit, _ := strconv.Atoi(req.URL.Query().Get("limit"))
		body := savedTracksBody(offset, limit, total)
		if offset == 0 {
			total++ // someone liked a track while page 1 was in flight
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
	p := New(sess, "client", 320)
	drainSavedTracks(t, p)

	if cached, ok := p.trackCache["YOUR MUSIC"]; ok && cached.tracks != nil {
		t.Errorf("committed a cache spanning two library snapshots (%d tracks)", len(cached.tracks))
	}
}
