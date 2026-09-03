package radio

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/provider"
)

func TestFetchTagsCleansDuplicatesAndSortsByStationCount(t *testing.T) {
	d := &directory{tags: []Tag{
		{Name: "jazz", StationCount: 100},
		{Name: " Jazz ", StationCount: 25},
		{Name: "rock", StationCount: 200},
		{Name: "empty", StationCount: 0},
		{Name: "  ", StationCount: 10},
	}}
	d.serve(t)

	tags, err := FetchTags()
	if err != nil {
		t.Fatalf("FetchTags: %v", err)
	}
	if len(tags) != 2 {
		t.Fatalf("tags = %+v, want rock and jazz", tags)
	}
	if tags[0].Name != "rock" || tags[0].StationCount != 200 {
		t.Errorf("first tag = %+v, want rock with 200 stations", tags[0])
	}
	if tags[1].Name != "jazz" || tags[1].StationCount != 125 {
		t.Errorf("second tag = %+v, want merged jazz with 125 stations", tags[1])
	}
	if d.lastQuery.Get("order") != "stationcount" || d.lastQuery.Get("reverse") != "true" {
		t.Errorf("tag query = %v, want most stations first", d.lastQuery)
	}
	if d.lastQuery.Get("limit") != "100000" {
		t.Errorf("tag query limit = %q, want the complete index", d.lastQuery.Get("limit"))
	}
}

func TestFetchTagsWrapsTheOperationOnFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	installCatalogClient(t, srv.URL)

	_, err := FetchTags()
	if err == nil || !strings.Contains(err.Error(), "fetch tags: radio-browser: HTTP 500") {
		t.Fatalf("FetchTags error = %v, want wrapped operation and transport context", err)
	}
}

func TestSanitizeTagLabelRemovesTerminalControls(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{name: "plain Unicode", in: "música pop", want: "música pop"},
		{name: "ANSI color", in: "\x1b[31mjazz\x1b[0m", want: "jazz"},
		{name: "terminal title", in: "\x1b]2;owned\ajazz", want: "jazz"},
		{name: "line controls", in: "smooth\njazz\t radio", want: "smooth jazz radio"},
		{name: "other controls", in: "r\x00o\x7fc\u0085k", want: "rock"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sanitizeTagLabel(tc.in); got != tc.want {
				t.Fatalf("sanitizeTagLabel(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestTagBrowserListsTheDirectoryIndex(t *testing.T) {
	d := &directory{tags: []Tag{
		{Name: "pop", StationCount: 5723},
		{Name: "jazz", StationCount: 1134},
	}}
	d.serve(t)
	p := newPlaceProvider(t, "")

	browser := p.GenreBrowserFor(browseTagsID)
	if browser == nil {
		t.Fatal("GenreBrowserFor(tags) returned nil")
	}
	genres, err := browser.Genres()
	if err != nil {
		t.Fatalf("Genres: %v", err)
	}
	if len(genres) != 2 || genres[0].ID != "pop" || genres[0].Name != "pop (5723)" {
		t.Fatalf("genres = %+v, want directory tags with station counts", genres)
	}
	labeler, ok := browser.(provider.GenreLabeler)
	if !ok {
		t.Fatal("tag browser does not implement GenreLabeler")
	}
	if got := labeler.GenreLabel(); got != "Genres & Tags" {
		t.Fatalf("tag browser label = %q, want Genres & Tags", got)
	}
}

func TestTagBrowserKeepsRawTagIDButSanitizesItsLabel(t *testing.T) {
	raw := "\x1b[31mjazz\x1b[0m"
	d := &directory{tags: []Tag{{Name: raw, StationCount: 12}}}
	d.serve(t)
	p := newPlaceProvider(t, "")

	genres, err := p.GenreBrowserFor(browseTagsID).Genres()
	if err != nil {
		t.Fatalf("Genres: %v", err)
	}
	if len(genres) != 1 || genres[0].ID != raw || genres[0].Name != "jazz (12)" {
		t.Fatalf("genres = %+v, want raw query ID and safe display label", genres)
	}
}

func TestTagBrowserLoadsExactTagWithSelectedSort(t *testing.T) {
	d := &directory{stations: []CatalogStation{
		{Name: "Jazz FM", URL: "https://jazz.example/stream", Tags: "jazz,smooth jazz"},
	}}
	d.serve(t)
	p := newPlaceProvider(t, "")
	browser := p.GenreBrowserFor(browseTagsID)

	tracks, err := browser.GenreTracks("jazz", SortTrend)
	if err != nil {
		t.Fatalf("GenreTracks: %v", err)
	}
	if len(tracks) != 1 || tracks[0].Title != "Jazz FM" {
		t.Fatalf("tracks = %+v, want Jazz FM", tracks)
	}
	if d.lastQuery.Get("tag") != "jazz" || d.lastQuery.Get("tagExact") != "true" {
		t.Errorf("query = %v, want exact jazz tag", d.lastQuery)
	}
	if d.lastQuery.Get("order") != SortTrend || d.lastQuery.Get("limit") != "200" {
		t.Errorf("query = %v, want trending order and 200 results", d.lastQuery)
	}
}

func TestTagBrowserRejectsAnEmptyTag(t *testing.T) {
	p := newPlaceProvider(t, "")
	browser := p.GenreBrowserFor(browseTagsID)
	if _, err := browser.GenreTracks("  ", SortVotes); err == nil {
		t.Fatal("GenreTracks should reject an empty tag")
	}
}

func TestRadioBrowseEntriesOfferCountriesAndTags(t *testing.T) {
	p := newPlaceProvider(t, "")
	entries := p.BrowseEntries()
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		ids = append(ids, entry.ID)
	}
	if !slices.Contains(ids, browseCountriesID) || !slices.Contains(ids, browseTagsID) {
		t.Fatalf("browse entries = %v, want countries and tags", ids)
	}
	if p.GenreBrowserFor("browse:unknown") != nil {
		t.Fatal("unknown browse route should not resolve a genre browser")
	}
}

func TestRefreshDoesNotRestoreAnInFlightTagIndex(t *testing.T) {
	var calls atomic.Int32
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseFirst) }) }
	t.Cleanup(release)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if calls.Add(1) == 1 {
			close(firstStarted)
			<-releaseFirst
			_ = json.NewEncoder(w).Encode([]Tag{{Name: "stale", StationCount: 1}})
			return
		}
		_ = json.NewEncoder(w).Encode([]Tag{{Name: "fresh", StationCount: 2}})
	}))
	t.Cleanup(srv.Close)
	installCatalogClient(t, srv.URL)

	p := newPlaceProvider(t, "")
	browser := p.GenreBrowserFor(browseTagsID)
	firstDone := make(chan error, 1)
	go func() {
		_, err := browser.Genres()
		firstDone <- err
	}()

	select {
	case <-firstStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the first tag request")
	}
	p.Refresh()
	release()
	if err := <-firstDone; err != nil {
		t.Fatalf("first Genres: %v", err)
	}

	genres, err := browser.Genres()
	if err != nil {
		t.Fatalf("second Genres: %v", err)
	}
	if len(genres) != 1 || !strings.HasPrefix(genres[0].Name, "fresh ") {
		t.Fatalf("genres after refresh = %+v, want a fresh second fetch", genres)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("tag requests = %d, want 2", got)
	}
}
