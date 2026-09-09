package podcast

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

const providerTestDirectory = `{"results":[
	{"collectionId":1,"collectionName":"First Show","artistName":"Author","feedUrl":"https://example.com/first",
	 "artworkUrl600":"https://example.com/first.jpg","primaryGenreName":"Science","trackCount":12},
	{"collectionId":2,"collectionName":"Second Show","artistName":"Other","feedUrl":"https://example.com/second",
	 "artworkUrl600":"https://example.com/second.jpg","primaryGenreName":"News","trackCount":8}
]}`

const providerTestRSS = `<rss version="2.0" xmlns:i="http://www.itunes.com/dtds/podcast-1.0.dtd"><channel>
	<title> Feed &amp; Show </title><i:author>Publisher, not the show title</i:author>
	<i:image href="https://example.com/channel.jpg"/>
	<item><title>Episode Nine</title><guid> episode-nine </guid><i:duration>01:02:03</i:duration><i:episode>9</i:episode>
		<i:image href="https://example.com/episode.jpg"/>
		<enclosure url="https://example.com/audio?episode=9&amp;token=yes" type="audio/mpeg"/></item>
	<item><title> </title><enclosure url="https://example.com/fallback.mp3"/></item>
	<item><title>Not audio</title><enclosure url="https://example.com/video.mp4" type="video/mp4"/></item>
</channel></rss>`

func newProviderTest(t *testing.T, handler http.HandlerFunc) *Provider {
	t.Helper()
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
	p := New("us")
	if handler == nil {
		handler = func(w http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected HTTP request: %s %s", r.Method, r.URL)
			http.Error(w, "unexpected request", http.StatusServiceUnavailable)
		}
	}
	p.client = newDirectoryTestClient(t, handler)
	return p
}

func checkProviderPlaylists(t *testing.T, p *Provider, want []playlist.PlaylistInfo) {
	t.Helper()
	got, err := p.Playlists()
	if err != nil || !slices.Equal(got, want) {
		t.Errorf("Playlists = %+v, %v; want %+v, nil", got, err, want)
	}
}

type providerTestTransport func(*http.Request) (*http.Response, error)

func (f providerTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestProviderNewOffline(t *testing.T) {
	oldTransport := http.DefaultTransport
	http.DefaultTransport = providerTestTransport(func(r *http.Request) (*http.Response, error) {
		t.Errorf("constructor or local operation attempted HTTP: %s", r.URL)
		return nil, errors.New("network disabled")
	})
	t.Cleanup(func() { http.DefaultTransport = oldTransport })
	for _, tt := range []struct{ country, want string }{
		{"", "us"}, {" \t", "us"}, {" NO\n", "no"}, {"gB", "gb"},
		{"USA", "us"}, {"u", "us"}, {"1a", "us"}, {"a1", "us"}, {"\u00e9", "us"},
	} {
		t.Run(tt.country, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("CLIAMP_CONFIG_DIR", dir)
			p := New(tt.country)
			if p == nil || p.country != tt.want || p.client == nil || p.shows == nil || p.categoryCache == nil || p.storeErr != nil {
				t.Fatalf("New(%q) = %+v, want initialized provider for %q", tt.country, p, tt.want)
			}
			if want := filepath.Join(dir, "podcast_subscriptions.json"); p.subscriptionPath != want {
				t.Errorf("subscription path = %q, want %q", p.subscriptionPath, want)
			}
			checkProviderPlaylists(t, p, nil)
			if _, err := os.Stat(p.subscriptionPath); !os.IsNotExist(err) {
				t.Fatalf("constructor created a store: %v", err)
			}
			s := show{Title: "Offline Show", FeedURL: "https://example.com/offline", EpisodeCount: 4}
			p.shows[s.FeedURL] = s
			if added, _, err := p.ToggleFavorite("c:" + s.FeedURL); err != nil || !added {
				t.Fatalf("subscribe = %v, %v", added, err)
			}
			p = New(tt.country)
			p.Refresh()
			checkProviderPlaylists(t, p, []playlist.PlaylistInfo{{
				ID: "f:" + s.FeedURL, Name: "[subscribed] Offline Show", TrackCount: 4, Section: "Subscriptions",
			}})
			if added, _, err := p.ToggleFavorite("f:" + s.FeedURL); err != nil || added {
				t.Fatalf("unsubscribe = %v, %v", added, err)
			}
		})
	}
}

func TestProviderCatalogPaginationAndRefresh(t *testing.T) {
	for _, tt := range []struct {
		name, failure string
		emptyChart    bool
		emptyLookup   bool
	}{
		{name: "shows"}, {name: "empty chart", emptyChart: true}, {name: "empty lookup", emptyLookup: true},
		{name: "retry chart", failure: "chart"}, {name: "retry lookup", failure: "lookup"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var charts, lookups atomic.Int32
			var refreshed atomic.Bool
			p := newProviderTest(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v2/us/podcasts/top/100/podcasts.json":
					if charts.Add(1) == 1 && tt.failure == "chart" {
						http.Error(w, "try again", http.StatusServiceUnavailable)
					} else if tt.emptyChart {
						io.WriteString(w, `{"feed":{"results":[]}}`)
					} else if refreshed.Load() {
						io.WriteString(w, `{"feed":{"results":[{"id":"2"},{"id":"1"}]}}`)
					} else {
						io.WriteString(w, `{"feed":{"results":[{"id":"1"},{"id":"2"}]}}`)
					}
				case "/lookup":
					if lookups.Add(1) == 1 && tt.failure == "lookup" {
						http.Error(w, "try again", http.StatusServiceUnavailable)
					} else if tt.emptyLookup {
						io.WriteString(w, `{"results":[]}`)
					} else {
						io.WriteString(w, providerTestDirectory)
					}
				default:
					t.Errorf("unexpected request: %s", r.URL)
					http.NotFound(w, r)
				}
			})
			for _, page := range [][2]int{{-1, 1}, {0, 0}, {0, -1}} {
				if n, err := p.LoadCatalogPage(page[0], page[1]); n != 0 || err != nil {
					t.Errorf("invalid page %v = %d, %v", page, n, err)
				}
			}
			if charts.Load() != 0 || lookups.Load() != 0 {
				t.Fatal("invalid pagination made an HTTP request")
			}
			if tt.failure != "" {
				if n, err := p.LoadCatalogPage(0, 1); n != 0 || err == nil || !strings.Contains(err.Error(), "503") {
					t.Fatalf("failed page = %d, %v; want 0 and HTTP 503", n, err)
				}
				checkProviderPlaylists(t, p, nil)
			}
			want := []playlist.PlaylistInfo{
				{ID: "c:https://example.com/first", Name: "First Show", TrackCount: 12, Section: "Top Shows (US)"},
				{ID: "c:https://example.com/second", Name: "Second Show", TrackCount: 8, Section: "Top Shows (US)"},
			}
			if tt.emptyChart || tt.emptyLookup {
				want = nil
			}
			for _, page := range []struct{ offset, limit, count, visible int }{
				{0, 1, 1, 1}, {1, 10, 1, 2}, {0, 1, 1, 2}, {2, 10, 0, 2}, {100, 1, 0, 2},
			} {
				count, visible := page.count, page.visible
				if len(want) == 0 {
					count, visible = 0, 0
				}
				if n, err := p.LoadCatalogPage(page.offset, page.limit); n != count || err != nil {
					t.Fatalf("page (%d, %d) = %d, %v; want %d, nil", page.offset, page.limit, n, err, count)
				}
				checkProviderPlaylists(t, p, want[:visible])
			}
			wantCharts, wantLookups := int32(1), int32(1)
			if tt.failure != "" {
				wantCharts++
			}
			if tt.failure == "lookup" {
				wantLookups++
			}
			if tt.emptyChart {
				wantLookups = 0
			}
			if charts.Load() != wantCharts || lookups.Load() != wantLookups {
				t.Errorf("cached requests = %d charts/%d lookups, want %d/%d", charts.Load(), lookups.Load(), wantCharts, wantLookups)
			}
			p.Refresh()
			checkProviderPlaylists(t, p, nil)
			refreshed.Store(true)
			if n, err := p.LoadCatalogPage(0, 10); n != len(want) || err != nil {
				t.Fatalf("refreshed page = %d, %v", n, err)
			}
			slices.Reverse(want)
			checkProviderPlaylists(t, p, want)
			if !tt.emptyChart {
				wantLookups++
			}
			if charts.Load() != wantCharts+1 || lookups.Load() != wantLookups {
				t.Errorf("refresh requests = %d charts/%d lookups, want %d/%d", charts.Load(), lookups.Load(), wantCharts+1, wantLookups)
			}
		})
	}
}

func TestProviderSearchCatalog(t *testing.T) {
	var calls atomic.Int32
	const query = "science & tea + 50%?"
	p := newProviderTest(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/search" || r.URL.Query().Get("genreId") != "" {
			t.Errorf("search URL = %s", r.URL)
		}
		switch r.URL.Query().Get("term") {
		case query:
			io.WriteString(w, providerTestDirectory)
		case "absent":
			io.WriteString(w, `{"results":[]}`)
		case "fail":
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		default:
			t.Errorf("unexpected search query: %s", r.URL)
			http.NotFound(w, r)
		}
	})
	s := show{Title: "First Show", FeedURL: "https://example.com/first", EpisodeCount: 12}
	p.shows[s.FeedURL], p.catalog, p.catalogVisible = s, []show{s}, 1
	if _, _, err := p.ToggleFavorite("c:" + s.FeedURL); err != nil {
		t.Fatal(err)
	}
	base := []playlist.PlaylistInfo{
		{ID: "f:" + s.FeedURL, Name: "[subscribed] First Show", TrackCount: 12, Section: "Subscriptions"},
		{ID: "c:" + s.FeedURL, Name: "[subscribed] First Show", TrackCount: 12, Section: "Top Shows (US)"},
	}
	results := []playlist.PlaylistInfo{
		{ID: "s:" + s.FeedURL, Name: "[subscribed] First Show", TrackCount: 12, Section: "Search Results"},
		{ID: "s:https://example.com/second", Name: "Second Show", TrackCount: 8, Section: "Search Results"},
	}
	checkProviderPlaylists(t, p, base)
	for _, tt := range []struct {
		query   string
		count   int
		clear   bool
		wantErr bool
		want    []playlist.PlaylistInfo
	}{
		{query: "  " + query + "\t", count: 2, want: results},
		{query: "fail", wantErr: true, want: results},
		{query: " \t"}, {clear: true, want: base},
		{query: "absent"}, {clear: true, want: base}, {clear: true, want: base},
	} {
		if tt.clear {
			p.ClearSearch()
		} else if n, err := p.SearchCatalog(tt.query); n != tt.count || (err != nil) != tt.wantErr || (err != nil && !strings.Contains(err.Error(), "503")) {
			t.Fatalf("SearchCatalog(%q) = %d, %v", tt.query, n, err)
		}
		if p.IsSearching() == tt.clear {
			t.Errorf("query %q, clear %v: IsSearching = %v", tt.query, tt.clear, p.IsSearching())
		}
		checkProviderPlaylists(t, p, tt.want)
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("search requests = %d, want 3 (blank and clear are local)", got)
	}
}

func TestProviderSearchCatalogManualFeedTitle(t *testing.T) {
	for _, tt := range []struct {
		name, body, title string
		count             int
	}{
		{"episodes", providerTestRSS, "Feed & Show", 2},
		{"empty show", `<rss><channel><title>Empty Show</title></channel></rss>`, "", 0},
		{"untitled show", strings.Replace(providerTestRSS, "<title> Feed &amp; Show </title>", "", 1), "", 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			p := newProviderTest(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/feed" || r.URL.RawQuery != "format=rss&token=a%2Bb" {
					t.Errorf("manual feed request = %s %s", r.Method, r.URL)
				}
				w.Header().Set("Content-Type", "application/octet-stream")
				io.WriteString(w, tt.body)
			})
			feedURL := p.client.directoryURL + "/feed?format=rss&token=a%2Bb"
			if tt.count == 0 {
				if n, err := p.SearchCatalog(feedURL); n != 0 || err == nil || !strings.Contains(err.Error(), "no playable episodes") {
					t.Fatalf("empty feed search = %d, %v; want no playable episodes error", n, err)
				}
				if p.IsSearching() || len(p.shows) != 0 {
					t.Fatal("empty feed was added to the directory")
				}
				return
			}
			if n, err := p.SearchCatalog("  " + feedURL + "\t"); n != 1 || err != nil {
				t.Fatalf("manual feed search = %d, %v; want one show", n, err)
			}
			title := tt.title
			if title == "" {
				title = feedURL
			}
			checkProviderPlaylists(t, p, []playlist.PlaylistInfo{{
				ID: "s:" + feedURL, Name: title, TrackCount: tt.count, Section: "Search Results",
			}})
			if !p.IsSearching() || calls.Load() != 1 {
				t.Errorf("searching = %v, requests = %d; want true and one feed GET", p.IsSearching(), calls.Load())
			}
		})
	}
}

func TestProviderStaleSearch(t *testing.T) {
	for _, action := range []string{"clear", "newer query"} {
		t.Run(action, func(t *testing.T) {
			started := make(chan struct{})
			gate, release := context.WithCancel(t.Context())
			p := newProviderTest(t, func(w http.ResponseWriter, r *http.Request) {
				term := r.URL.Query().Get("term")
				if r.URL.Path != "/search" || (term != "old" && term != "new") {
					t.Errorf("unexpected search: %s", r.URL)
					http.NotFound(w, r)
					return
				}
				if term == "old" {
					close(started)
					select {
					case <-gate.Done():
					case <-r.Context().Done():
						return
					}
				}
				fmt.Fprintf(w, `{"results":[{"collectionId":1,"collectionName":%q,"feedUrl":"https://example.com/shared"}]}`, term)
			})
			t.Cleanup(release)
			s := show{Title: "Current", FeedURL: "https://example.com/shared"}
			p.shows[s.FeedURL], p.catalog, p.catalogVisible = s, []show{s}, 1
			type result struct {
				count int
				err   error
			}
			done := make(chan result, 1)
			go func() {
				n, err := p.SearchCatalog("old")
				done <- result{n, err}
			}()
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("old search did not start")
			}
			wantTitle, prefix := "Current", "c:"
			section := "Top Shows (US)"
			if action == "clear" {
				p.ClearSearch()
			} else {
				if n, err := p.SearchCatalog("new"); n != 1 || err != nil {
					t.Fatalf("new search = %d, %v", n, err)
				}
				wantTitle, prefix, section = "new", "s:", "Search Results"
			}
			release()
			select {
			case got := <-done:
				if got.count != 0 || got.err != nil {
					t.Errorf("stale search = %d, %v; want 0, nil", got.count, got.err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("old search did not finish")
			}
			if p.IsSearching() != (action == "newer query") {
				t.Errorf("stale search changed IsSearching to %v", p.IsSearching())
			}
			checkProviderPlaylists(t, p, []playlist.PlaylistInfo{{ID: prefix + s.FeedURL, Name: wantTitle, Section: section}})
			// The visible result and the metadata used when subscribing must agree.
			if added, title, err := p.ToggleFavorite(prefix + s.FeedURL); !added || title != wantTitle || err != nil {
				t.Errorf("subscribe after stale search = %v, %q, %v; want true, %q, nil", added, title, err, wantTitle)
			}
		})
	}
}

func TestProviderRejectsURLsWithoutDirectorySearch(t *testing.T) {
	p := newProviderTest(t, nil)
	for _, query := range []string{
		"https://user:secret@example.com/feed", "https://example.com/%zz?token=secret",
		"https:///feed?token=secret", "https:feed", "ftp://user:secret@example.com/feed",
	} {
		if _, err := p.SearchCatalog(query); err == nil {
			t.Errorf("SearchCatalog(%q) did not reject an invalid feed URL", query)
		}
		if _, err := p.SearchTracks(t.Context(), query, 20); err == nil {
			t.Errorf("SearchTracks(%q) did not reject an invalid feed URL", query)
		}
	}
}

func TestProviderStaleCategoryAfterRefresh(t *testing.T) {
	started := make(chan struct{})
	gate, release := context.WithCancel(t.Context())
	var calls atomic.Int32
	p := newProviderTest(t, func(w http.ResponseWriter, r *http.Request) {
		title := "New title"
		if calls.Add(1) == 1 {
			close(started)
			<-gate.Done()
			title = "Old title"
		}
		fmt.Fprintf(w, `{"results":[{"collectionId":1,"collectionName":%q,"feedUrl":"https://example.com/shared"}]}`, title)
	})
	t.Cleanup(release)
	done := make(chan error, 1)
	go func() {
		_, err := p.ArtistAlbums("1318")
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("category request did not start")
	}
	p.Refresh()
	if _, err := p.ArtistAlbums("1318"); err != nil {
		t.Fatal(err)
	}
	release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, title, err := p.ToggleFavorite("https://example.com/shared"); err != nil || title != "New title" {
		t.Fatalf("subscribe after refresh = %q, %v; want current metadata", title, err)
	}
}

func TestProviderCachedPagePreservesNewerMetadata(t *testing.T) {
	p := newProviderTest(t, nil)
	url := "https://example.com/show"
	p.catalog = []show{{FeedURL: url, Title: "Old title"}}
	p.shows[url] = show{FeedURL: url, Title: "New title"}
	if _, err := p.LoadCatalogPage(0, 1); err != nil {
		t.Fatal(err)
	}
	if _, title, err := p.ToggleFavorite("c:" + url); err != nil || title != "New title" {
		t.Fatalf("subscribe after cached page = %q, %v; want current metadata", title, err)
	}
}

func TestProviderSearchTracksIndependent(t *testing.T) {
	for _, searching := range []bool{false, true} {
		t.Run(fmt.Sprintf("catalog search %v", searching), func(t *testing.T) {
			var calls atomic.Int32
			p := newProviderTest(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != "/search" {
					t.Errorf("collection search fetched a feed: %s", r.URL)
				}
				if r.URL.Query().Get("term") == "fail" {
					http.Error(w, "unavailable", http.StatusServiceUnavailable)
					return
				}
				io.WriteString(w, providerTestDirectory)
			})
			var before []playlist.PlaylistInfo
			if searching {
				p.searchResults = []show{{Title: "Catalog search", FeedURL: "https://example.com/catalog"}}
				before = []playlist.PlaylistInfo{{ID: "s:https://example.com/catalog", Name: "Catalog search", Section: "Search Results"}}
			}
			want := []playlist.Track{
				{Path: "https://example.com/first", Title: "First Show", Artist: "Author", Genre: "Science",
					AlbumArtURL: "https://example.com/first.jpg", Stream: true, Feed: true,
					ProviderMeta: map[string]string{playlist.MetaKind: playlist.MetaKindAlbum, playlist.MetaAlbumID: "https://example.com/first"}},
				{Path: "https://example.com/second", Title: "Second Show", Artist: "Other", Genre: "News",
					AlbumArtURL: "https://example.com/second.jpg", Stream: true, Feed: true,
					ProviderMeta: map[string]string{playlist.MetaKind: playlist.MetaKindAlbum, playlist.MetaAlbumID: "https://example.com/second"}},
			}
			for _, tt := range []struct{ limit, count int }{{1, 1}, {0, 2}, {-1, 2}, {10, 2}} {
				got, err := p.SearchTracks(t.Context(), "independent", tt.limit)
				if err != nil || !reflect.DeepEqual(got, want[:tt.count]) {
					t.Errorf("SearchTracks limit %d = %+v, %v; want %+v", tt.limit, got, err, want[:tt.count])
				}
				checkProviderPlaylists(t, p, before)
			}
			if got, err := p.SearchTracks(t.Context(), " \t", 10); len(got) != 0 || err != nil {
				t.Errorf("blank SearchTracks = %+v, %v", got, err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if _, err := p.SearchTracks(ctx, "canceled", 10); !errors.Is(err, context.Canceled) {
				t.Errorf("canceled SearchTracks = %v", err)
			}
			if got, err := p.SearchTracks(t.Context(), "fail", 10); got != nil || err == nil || !strings.Contains(err.Error(), "503") {
				t.Errorf("failed SearchTracks = %+v, %v", got, err)
			}
			checkProviderPlaylists(t, p, before)
			if p.IsSearching() != searching || calls.Load() != 5 {
				t.Errorf("IsSearching = %v, requests = %d; want %v, 5", p.IsSearching(), calls.Load(), searching)
			}
		})
	}
}

func TestProviderCategoriesCache(t *testing.T) {
	for _, mode := range []string{"shows", "empty", "retry"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			p := newProviderTest(t, func(w http.ResponseWriter, r *http.Request) {
				call := calls.Add(1)
				if r.URL.Path != "/search" || r.URL.Query().Get("genreId") != "1318" || r.URL.Query().Get("term") != "Technology" {
					t.Errorf("category request = %s", r.URL)
				}
				if mode == "retry" && call == 1 {
					http.Error(w, "try again", http.StatusServiceUnavailable)
				} else if mode == "empty" {
					io.WriteString(w, `{"results":[]}`)
				} else {
					io.WriteString(w, providerTestDirectory)
				}
			})
			artists, err := p.Artists()
			if err != nil || len(artists) != len(categories) {
				t.Fatalf("Artists = %+v, %v", artists, err)
			}
			for i, c := range categories {
				if artists[i] != (provider.ArtistInfo{ID: c.ID, Name: c.Name}) {
					t.Errorf("category %d = %+v, want %+v", i, artists[i], c)
				}
			}
			if _, err := p.ArtistAlbums("not-a-category"); err == nil {
				t.Error("unknown category returned no error")
			}
			if calls.Load() != 0 {
				t.Fatal("listing or rejecting categories used the network")
			}
			wantCalls := int32(1)
			if mode == "retry" {
				if _, err := p.ArtistAlbums("1318"); err == nil || !strings.Contains(err.Error(), "503") {
					t.Fatalf("failed category = %v", err)
				}
				wantCalls++
			}
			want := []provider.AlbumInfo{
				{ID: "https://example.com/first", Name: "First Show", Artist: "Author", Genre: "Science", TrackCount: 12},
				{ID: "https://example.com/second", Name: "Second Show", Artist: "Other", Genre: "News", TrackCount: 8},
			}
			if mode == "empty" {
				want = nil
			}
			for i := range 3 {
				if i == 2 {
					p.Refresh()
					wantCalls++
				}
				got, err := p.ArtistAlbums("1318")
				if err != nil || !slices.Equal(got, want) || calls.Load() != wantCalls {
					t.Errorf("category call %d = %+v, %v, %d requests; want %+v, nil, %d", i, got, err, calls.Load(), want, wantCalls)
				}
			}
			if len(want) > 0 {
				if added, title, err := p.ToggleFavorite(want[0].ID); !added || title != want[0].Name || err != nil {
					t.Errorf("subscribe to category result = %v, %q, %v", added, title, err)
				}
			}
		})
	}
}

func TestProviderTracksMetadata(t *testing.T) {
	for _, tt := range []struct {
		name, prefix, title, channelArt, wantArt string
		context                                  bool
	}{
		{"catalog", "c:", "Directory Show", "https://example.com/channel.jpg", "https://example.com/channel.jpg", false},
		{"subscription", "f:", "Directory Show", "", "https://example.com/directory.jpg", false},
		{"search after refresh", "s:", "Directory Show", "", "https://example.com/directory.jpg", false},
		{"album context", "", "Directory Show", "", "https://example.com/directory.jpg", true},
		{"feed metadata fallback", "", "", "https://example.com/channel.jpg", "https://example.com/channel.jpg", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			p := newProviderTest(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/feed" || r.URL.RawQuery != "token=original" {
					t.Errorf("feed request = %s %s", r.Method, r.URL)
				}
				w.Header().Set("Content-Type", "application/octet-stream")
				io.WriteString(w, strings.Replace(providerTestRSS, "https://example.com/channel.jpg", tt.channelArt, 1))
			})
			feedURL := p.client.directoryURL + "/feed?token=original"
			p.shows[feedURL] = show{Title: tt.title, FeedURL: feedURL, Author: "Directory Author", Genre: "Science", Artwork: "https://example.com/directory.jpg"}
			p.Refresh()
			var tracks []playlist.Track
			var err error
			if tt.context {
				tracks, err = p.AlbumTracksContext(t.Context(), feedURL)
			} else {
				tracks, err = p.Tracks(tt.prefix + feedURL)
			}
			title := tt.title
			if title == "" {
				title = "Feed & Show"
			}
			want := []playlist.Track{
				{Path: "https://example.com/audio?episode=9&token=yes", Title: "Episode Nine", Artist: title, Album: title,
					Genre: "Science", TrackNumber: 9, DurationSecs: 3723, Stream: true, AlbumArtURL: "https://example.com/episode.jpg",
					ProviderMeta: map[string]string{"podcast.feed": feedURL, "podcast.guid": "episode-nine"}},
				{Path: "https://example.com/fallback.mp3", Title: "Untitled episode", Artist: title, Album: title,
					Genre: "Science", Stream: true, AlbumArtURL: tt.wantArt,
					ProviderMeta: map[string]string{"podcast.feed": feedURL, "podcast.guid": "https://example.com/fallback.mp3"}},
			}
			if err != nil || !reflect.DeepEqual(tracks, want) {
				t.Errorf("episodes = %+v, %v; want %+v (Feed and Realtime false)", tracks, err, want)
			}
			if calls.Load() != 1 {
				t.Errorf("requests = %d, want one feed GET and no media requests", calls.Load())
			}
		})
	}
}

func TestProviderTrackErrors(t *testing.T) {
	for _, tt := range []struct {
		name, body, want string
		status           int
		short            bool
	}{
		{name: "HTTP", status: 503, want: "podcast episodes: http status 503"},
		{name: "XML", body: `<rss><channel>`, want: "podcast episodes: parsing feed"},
		{name: "short body", body: `<rss><channel/></rss>`, short: true, want: "podcast episodes:"},
		{name: "empty feed", body: `<rss><channel/></rss>`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := newProviderTest(t, func(w http.ResponseWriter, r *http.Request) {
				if tt.short {
					w.Header().Set("Content-Length", "1000")
				}
				if tt.status != 0 {
					w.WriteHeader(tt.status)
				}
				io.WriteString(w, tt.body)
			})
			for _, load := range []func(string) ([]playlist.Track, error){p.Tracks, func(id string) ([]playlist.Track, error) {
				return p.AlbumTracksContext(t.Context(), id)
			}} {
				tracks, err := load(p.client.directoryURL + "/feed")
				if len(tracks) != 0 || (err != nil) != (tt.want != "") || (err != nil && !strings.Contains(err.Error(), tt.want)) {
					t.Errorf("episodes = %+v, %v; want empty and error containing %q", tracks, err, tt.want)
				}
				if tt.short && !errors.Is(err, io.ErrUnexpectedEOF) {
					t.Errorf("short body error = %v, want wrapped unexpected EOF", err)
				}
			}
		})
	}
	p := newProviderTest(t, nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if tracks, err := p.AlbumTracksContext(ctx, p.client.directoryURL+"/feed"); tracks != nil || !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "podcast episodes:") {
		t.Errorf("canceled episodes = %+v, %v; want wrapped context.Canceled", tracks, err)
	}
}

func TestProviderSubscriptionPersistence(t *testing.T) {
	p := newProviderTest(t, nil)
	first := show{ID: "1", Title: "First", FeedURL: "https://example.com/first", Author: "Author", Genre: "Science", Artwork: "https://example.com/art.jpg", EpisodeCount: 12}
	second := show{ID: "1", Title: "Second", FeedURL: "https://example.com/second"}
	renamed := first
	renamed.ID, renamed.Title = "99", "Renamed"
	p.shows[first.FeedURL], p.shows[second.FeedURL] = first, second
	for _, tt := range []struct {
		id    string
		show  show
		added bool
		want  []show
	}{
		{"c:" + first.FeedURL, first, true, []show{first}},
		{"s:" + second.FeedURL, second, true, []show{second, first}},
		{first.FeedURL, renamed, false, []show{second}},
		{"s:" + first.FeedURL, renamed, true, []show{renamed, second}},
		{"f:" + second.FeedURL, second, false, []show{renamed}},
		{"f:" + first.FeedURL, renamed, false, nil},
	} {
		p.shows[tt.show.FeedURL] = tt.show
		if added, title, err := p.ToggleFavorite(tt.id); added != tt.added || title != tt.show.Title || err != nil {
			t.Fatalf("ToggleFavorite(%q) = %v, %q, %v", tt.id, added, title, err)
		}
		data, err := os.ReadFile(p.subscriptionPath)
		if err != nil {
			t.Fatal(err)
		}
		var stored []show
		if err := json.Unmarshal(data, &stored); err != nil || !slices.Equal(stored, tt.want) {
			t.Errorf("stored subscriptions = %+v, %v; want %+v", stored, err, tt.want)
		}
		info, err := os.Stat(p.subscriptionPath)
		if err != nil {
			t.Fatal(err)
		}
		// Windows does not expose Unix owner/group permission bits.
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
			t.Fatalf("subscription file mode = %o; want 0600", info.Mode().Perm())
		}
		restored := New("us")
		restored.client = p.client
		p.Refresh()
		var wantLists []playlist.PlaylistInfo
		for _, s := range tt.want {
			wantLists = append(wantLists, playlist.PlaylistInfo{ID: "f:" + s.FeedURL, Name: "[subscribed] " + s.Title, TrackCount: s.EpisodeCount, Section: "Subscriptions"})
			if restored.shows[s.FeedURL] != s {
				t.Errorf("restored metadata = %+v, want %+v", restored.shows[s.FeedURL], s)
			}
		}
		checkProviderPlaylists(t, p, wantLists)
		checkProviderPlaylists(t, restored, wantLists)
	}
}

func TestProviderSubscriptionStoreValidation(t *testing.T) {
	for _, tt := range []struct {
		name, data string
		corrupt    bool
	}{
		{"filter and deduplicate", `[
			{"id":"1","title":"First","feed_url":"https://example.com/first"},
			{"id":"2","title":"Duplicate","feed_url":"https://example.com/first"},
			{"id":"1","title":"Second","feed_url":"https://example.com/second"},
			{"feed_url":"file:///tmp/feed"},{"feed_url":"https://user:pass@example.com/feed"},{}
		]`, false},
		{"truncated", `[{"title":"Partial","feed_url":"https://example.com/partial"},`, true},
		{"wrong root", `{"subscriptions":[]}`, true},
		{"wrong field type", `[{"feed_url":42}]`, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := newProviderTest(t, nil)
			if err := os.WriteFile(p.subscriptionPath, []byte(tt.data), 0o600); err != nil {
				t.Fatal(err)
			}
			restored := New("us")
			restored.client = p.client
			if !tt.corrupt {
				if restored.storeErr != nil || len(restored.shows) != 2 {
					t.Errorf("loaded store = %+v, %v", restored.shows, restored.storeErr)
				}
				checkProviderPlaylists(t, restored, []playlist.PlaylistInfo{
					{ID: "f:https://example.com/first", Name: "[subscribed] First", Section: "Subscriptions"},
					{ID: "f:https://example.com/second", Name: "[subscribed] Second", Section: "Subscriptions"},
				})
			} else {
				checkProviderPlaylists(t, restored, nil)
				s := show{Title: "New", FeedURL: "https://example.com/new"}
				restored.shows[s.FeedURL] = s
				restored.Refresh()
				if added, _, err := restored.ToggleFavorite(s.FeedURL); added || err == nil || !strings.Contains(err.Error(), "load podcast subscriptions:") {
					t.Errorf("toggle with corrupt store = %v, %v; want load error", added, err)
				}
				checkProviderPlaylists(t, restored, nil)
			}
			if data, err := os.ReadFile(p.subscriptionPath); err != nil || string(data) != tt.data {
				t.Errorf("original store changed: %s, %v", data, err)
			}
		})
	}
}

func TestProviderSubscriptionWriteRollback(t *testing.T) {
	for _, remove := range []bool{false, true} {
		t.Run(fmt.Sprintf("remove %v", remove), func(t *testing.T) {
			p := newProviderTest(t, nil)
			first := show{Title: "First", FeedURL: "https://example.com/first"}
			second := show{Title: "Second", FeedURL: "https://example.com/second"}
			p.shows[first.FeedURL], p.shows[second.FeedURL] = first, second
			if _, _, err := p.ToggleFavorite(first.FeedURL); err != nil {
				t.Fatal(err)
			}
			path := p.subscriptionPath
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			// Replacing a directory fails even when tests run as root.
			p.subscriptionPath = t.TempDir()
			target := second
			if remove {
				target = first
			}
			if added, title, err := p.ToggleFavorite(target.FeedURL); added || title != target.Title || err == nil || !strings.Contains(err.Error(), "save podcast subscriptions:") {
				t.Errorf("failed toggle = %v, %q, %v", added, title, err)
			}
			checkProviderPlaylists(t, p, []playlist.PlaylistInfo{{ID: "f:" + first.FeedURL, Name: "[subscribed] First", Section: "Subscriptions"}})
			if after, err := os.ReadFile(path); err != nil || string(after) != string(before) {
				t.Errorf("failed toggle changed persisted store: %s, %v", after, err)
			}
			p.subscriptionPath = path
			if added, _, err := p.ToggleFavorite(target.FeedURL); added == remove || err != nil {
				t.Errorf("retry toggle = %v, %v; want added %v", added, err, !remove)
			}
		})
	}
}

func TestProviderRejectsUnsafeShowIDs(t *testing.T) {
	p := newProviderTest(t, nil)
	for _, id := range []string{
		"", "c:0", "s:0", "f:/tmp/feed", "file:///etc/passwd", "c:file:///tmp/feed",
		"s:ftp://example.com/feed", "f://example.com/feed", "c:https:///feed", "https://:443/feed",
		"https://example.com/%zz", "https://bad host/feed", "browse:categories", "x:" + p.client.directoryURL,
		"s:" + strings.Replace(p.client.directoryURL, "http://", "http://user:secret@", 1),
	} {
		t.Run(id, func(t *testing.T) {
			if p.IsFavoritableID(id) || p.CanRefreshPlaylist(id) {
				t.Errorf("unsafe ID %q advertised as favoritable or refreshable", id)
			}
			if tracks, err := p.Tracks(id); tracks != nil || err == nil || !strings.Contains(err.Error(), "invalid podcast show ID") {
				t.Errorf("Tracks(%q) = %+v, %v; want invalid ID", id, tracks, err)
			}
			if tracks, err := p.AlbumTracksContext(t.Context(), id); tracks != nil || err == nil || !strings.Contains(err.Error(), "invalid podcast show ID") {
				t.Errorf("AlbumTracksContext(%q) = %+v, %v; want invalid ID", id, tracks, err)
			}
			if added, _, err := p.ToggleFavorite(id); added || err == nil {
				t.Errorf("ToggleFavorite(%q) = %v, %v; want error", id, added, err)
			}
		})
	}
}
