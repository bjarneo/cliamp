package audiobookshelf

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

func mockProvider(fn roundTripFunc) *Provider {
	return newProvider(mockClient("tok", "", "", nil, fn))
}

const bookItemJSON = `{
	"id":"book-1",
	"mediaType":"book",
	"media":{
		"duration":7200,
		"numTracks":2,
		"metadata":{"title":"Mistborn","authorName":"Brandon Sanderson","publishedYear":"2006"},
		"chapters":[
			{"id":0,"start":0,"end":3600,"title":"Chapter One"},
			{"id":1,"start":3600,"end":5400,"title":"Chapter Two"},
			{"id":2,"start":5400,"end":7200,"title":"Chapter Three"}
		],
		"tracks":[
			{"index":1,"startOffset":0,"duration":3600,"ino":"111","metadata":{"filename":"part1.m4b"}},
			{"index":2,"startOffset":3600,"duration":3600,"ino":"222","metadata":{"filename":"part2.m4b"}}
		]
	}
}`

const podcastItemJSON = `{
	"id":"pod-1",
	"mediaType":"podcast",
	"media":{
		"numEpisodes":2,
		"metadata":{"title":"Darknet Diaries","author":"Jack Rhysider"},
		"episodes":[
			{"id":"ep-1","title":"Old One","index":1,"publishedAt":1000,"audioFile":{"ino":"901","duration":1800}},
			{"id":"ep-2","title":"New One","index":2,"publishedAt":2000,"audioFile":{"ino":"902","duration":2400}}
		]
	}
}`

func TestProviderName(t *testing.T) {
	p := mockProvider(func(req *http.Request) (*http.Response, error) {
		t.Fatal("no request expected")
		return nil, nil
	})
	if p.Name() != "Audiobookshelf" {
		t.Fatalf("Name() = %q, want Audiobookshelf", p.Name())
	}
}

func TestPlaylistsSectionsBooksAndPodcasts(t *testing.T) {
	p := mockProvider(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/api/libraries":
			return jsonResponse(`{"libraries":[
				{"id":"lib-b","name":"Audiobooks","mediaType":"book"},
				{"id":"lib-p","name":"Podcasts","mediaType":"podcast"}
			]}`), nil
		case "/api/libraries/lib-b/items":
			return jsonResponse(`{"total":1,"results":[` + bookItemJSON + `]}`), nil
		case "/api/libraries/lib-p/items":
			return jsonResponse(`{"total":1,"results":[` + podcastItemJSON + `]}`), nil
		default:
			t.Fatalf("unexpected path %s", req.URL.Path)
			return nil, nil
		}
	})

	lists, err := p.Playlists()
	if err != nil {
		t.Fatalf("Playlists() error: %v", err)
	}
	if len(lists) != 2 {
		t.Fatalf("got %d playlists, want 2", len(lists))
	}
	book := lists[0]
	if book.ID != "b:book-1" || book.Section != sectionAudiobooks {
		t.Fatalf("book playlist = %+v", book)
	}
	if book.Name != "Brandon Sanderson — Mistborn" {
		t.Fatalf("book name = %q", book.Name)
	}
	if book.TrackCount != 2 || book.DurationSecs != 7200 {
		t.Fatalf("book counts = %+v", book)
	}
	show := lists[1]
	if show.ID != "p:pod-1" || show.Section != sectionPodcasts {
		t.Fatalf("show playlist = %+v", show)
	}
	if show.Name != "Darknet Diaries" || show.TrackCount != 2 {
		t.Fatalf("show playlist = %+v", show)
	}
}

func TestPlaylistsEmptyCatalogCached(t *testing.T) {
	calls := 0
	p := mockProvider(func(req *http.Request) (*http.Response, error) {
		calls++
		switch req.URL.Path {
		case "/api/libraries":
			return jsonResponse(`{"libraries":[{"id":"lib-b","name":"Audiobooks","mediaType":"book"}]}`), nil
		case "/api/libraries/lib-b/items":
			return jsonResponse(`{"total":0,"results":[]}`), nil
		default:
			t.Fatalf("unexpected path %s", req.URL.Path)
			return nil, nil
		}
	})

	lists, err := p.Playlists()
	if err != nil {
		t.Fatalf("Playlists() error: %v", err)
	}
	if len(lists) != 0 {
		t.Fatalf("got %d playlists, want 0", len(lists))
	}

	if _, err := p.Playlists(); err != nil {
		t.Fatalf("cached Playlists() error: %v", err)
	}
	if calls != 2 {
		t.Fatalf("http calls = %d, want 2 (libraries + items on the first call; second call must hit the cache)", calls)
	}
}

func TestTracksBookUsesChapterTitles(t *testing.T) {
	calls := 0
	p := mockProvider(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/api/items/book-1" {
			t.Fatalf("unexpected path %s", req.URL.Path)
		}
		calls++
		return jsonResponse(bookItemJSON), nil
	})

	tracks, err := p.Tracks("b:book-1")
	if err != nil {
		t.Fatalf("Tracks() error: %v", err)
	}
	if len(tracks) != 2 {
		t.Fatalf("got %d tracks, want 2", len(tracks))
	}

	first := tracks[0]
	if first.Title != "Chapter One" {
		t.Fatalf("first title = %q, want Chapter One (file covers one chapter)", first.Title)
	}
	if first.Artist != "Brandon Sanderson" || first.Album != "Mistborn" || first.Year != 2006 {
		t.Fatalf("first track = %+v", first)
	}
	if first.TrackNumber != 1 || first.DurationSecs != 3600 || !first.Stream {
		t.Fatalf("first track = %+v", first)
	}
	if first.Meta(provider.MetaAudiobookshelfID) != "book-1" {
		t.Fatalf("first meta id = %q", first.Meta(provider.MetaAudiobookshelfID))
	}
	if first.Meta(provider.MetaAudiobookshelfOffset) != "0" {
		t.Fatalf("first offset = %q, want 0", first.Meta(provider.MetaAudiobookshelfOffset))
	}
	if first.Meta(provider.MetaAudiobookshelfTotal) != "7200" {
		t.Fatalf("first total = %q, want 7200", first.Meta(provider.MetaAudiobookshelfTotal))
	}

	second := tracks[1]
	if second.Title != "part2.m4b" {
		t.Fatalf("second title = %q, want the filename (file spans two chapters)", second.Title)
	}
	if second.Meta(provider.MetaAudiobookshelfOffset) != "3600" {
		t.Fatalf("second offset = %q, want 3600", second.Meta(provider.MetaAudiobookshelfOffset))
	}

	if _, err := p.Tracks("b:book-1"); err != nil {
		t.Fatalf("cached Tracks() error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("http calls = %d, want 1 (second call must hit the cache)", calls)
	}
}

func TestTracksPodcastNewestFirst(t *testing.T) {
	p := mockProvider(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/api/items/pod-1" {
			t.Fatalf("unexpected path %s", req.URL.Path)
		}
		return jsonResponse(podcastItemJSON), nil
	})

	tracks, err := p.Tracks("p:pod-1")
	if err != nil {
		t.Fatalf("Tracks() error: %v", err)
	}
	if len(tracks) != 2 {
		t.Fatalf("got %d tracks, want 2", len(tracks))
	}
	if tracks[0].Title != "New One" || tracks[1].Title != "Old One" {
		t.Fatalf("episode order = %q, %q", tracks[0].Title, tracks[1].Title)
	}
	if tracks[0].Album != "Darknet Diaries" || tracks[0].Artist != "Jack Rhysider" {
		t.Fatalf("episode track = %+v", tracks[0])
	}
	if tracks[0].DurationSecs != 2400 {
		t.Fatalf("episode duration = %d, want 2400", tracks[0].DurationSecs)
	}
	if tracks[0].Meta(provider.MetaAudiobookshelfEpisode) != "ep-2" {
		t.Fatalf("episode meta = %q", tracks[0].Meta(provider.MetaAudiobookshelfEpisode))
	}
}

func TestTracksUnknownPlaylistID(t *testing.T) {
	p := mockProvider(func(req *http.Request) (*http.Response, error) {
		t.Fatal("no request expected")
		return nil, nil
	})
	if _, err := p.Tracks("x:nope"); err == nil {
		t.Fatal("Tracks() error = nil, want an error for an unprefixed id")
	}
}

func TestRefreshClearsCaches(t *testing.T) {
	calls := 0
	p := mockProvider(func(req *http.Request) (*http.Response, error) {
		calls++
		return jsonResponse(bookItemJSON), nil
	})

	if _, err := p.Tracks("b:book-1"); err != nil {
		t.Fatalf("Tracks() error: %v", err)
	}
	p.Refresh()
	if _, err := p.Tracks("b:book-1"); err != nil {
		t.Fatalf("Tracks() after Refresh error: %v", err)
	}
	if calls != 2 {
		t.Fatalf("http calls = %d, want 2", calls)
	}
}

func TestArtistsListsAuthorsFromBookLibraries(t *testing.T) {
	p := mockProvider(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/api/libraries":
			return jsonResponse(`{"libraries":[
				{"id":"lib-b","name":"Audiobooks","mediaType":"book"},
				{"id":"lib-p","name":"Podcasts","mediaType":"podcast"}
			]}`), nil
		case "/api/libraries/lib-b/authors":
			return jsonResponse(`{"authors":[
				{"id":"au-2","name":"andy weir","numBooks":2},
				{"id":"au-1","name":"Brandon Sanderson","numBooks":9}
			]}`), nil
		default:
			t.Fatalf("unexpected path %s", req.URL.Path)
			return nil, nil
		}
	})

	artists, err := p.Artists()
	if err != nil {
		t.Fatalf("Artists() error: %v", err)
	}
	if len(artists) != 2 {
		t.Fatalf("got %d artists, want 2", len(artists))
	}
	if artists[0].Name != "andy weir" || artists[1].Name != "Brandon Sanderson" {
		t.Fatalf("artists not sorted case-insensitively: %+v", artists)
	}
	if artists[1].ID != "au-1" || artists[1].AlbumCount != 9 {
		t.Fatalf("artist = %+v", artists[1])
	}
}

func TestArtistAlbumsPrefixesBookIDs(t *testing.T) {
	p := mockProvider(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/api/authors/au-1" {
			t.Fatalf("unexpected path %s", req.URL.Path)
		}
		return jsonResponse(`{"id":"au-1","name":"Brandon Sanderson","libraryItems":[
			{"id":"book-2","media":{"numTracks":3,"metadata":{"title":"Warbreaker","authorName":"Brandon Sanderson","publishedYear":"2009"}}},
			{"id":"book-1","media":{"numTracks":2,"metadata":{"title":"Mistborn","authorName":"Brandon Sanderson","publishedYear":"2006"}}}
		]}`), nil
	})

	albums, err := p.ArtistAlbums("au-1")
	if err != nil {
		t.Fatalf("ArtistAlbums() error: %v", err)
	}
	if len(albums) != 2 {
		t.Fatalf("got %d albums, want 2", len(albums))
	}
	if albums[0].Name != "Mistborn" {
		t.Fatalf("albums not title-sorted: %+v", albums)
	}
	if albums[0].ID != "b:book-1" || albums[0].ArtistID != "au-1" || albums[0].Year != 2006 || albums[0].TrackCount != 2 {
		t.Fatalf("album = %+v", albums[0])
	}
}

func TestAlbumListSortAndPaging(t *testing.T) {
	p := mockProvider(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/api/libraries":
			return jsonResponse(`{"libraries":[{"id":"lib-b","name":"Audiobooks","mediaType":"book"}]}`), nil
		case "/api/libraries/lib-b/items":
			return jsonResponse(`{"total":3,"results":[
				{"id":"b1","addedAt":100,"media":{"metadata":{"title":"Cibola Burn","authorName":"James Corey"}}},
				{"id":"b2","addedAt":300,"media":{"metadata":{"title":"Artemis","authorName":"Andy Weir"}}},
				{"id":"b3","addedAt":200,"media":{"metadata":{"title":"Bobiverse","authorName":"Dennis Taylor"}}}
			]}`), nil
		default:
			t.Fatalf("unexpected path %s", req.URL.Path)
			return nil, nil
		}
	})

	byTitle, err := p.AlbumList(SortBooksByTitle, 0, 0)
	if err != nil {
		t.Fatalf("AlbumList() error: %v", err)
	}
	if len(byTitle) != 3 || byTitle[0].Name != "Artemis" || byTitle[2].Name != "Cibola Burn" {
		t.Fatalf("title order = %+v", byTitle)
	}

	byAdded, err := p.AlbumList(SortBooksByAdded, 0, 2)
	if err != nil {
		t.Fatalf("AlbumList(added) error: %v", err)
	}
	if len(byAdded) != 2 || byAdded[0].ID != "b:b2" || byAdded[1].ID != "b:b3" {
		t.Fatalf("added order = %+v", byAdded)
	}

	page2, err := p.AlbumList(SortBooksByTitle, 2, 2)
	if err != nil {
		t.Fatalf("AlbumList(page 2) error: %v", err)
	}
	if len(page2) != 1 || page2[0].Name != "Cibola Burn" {
		t.Fatalf("page 2 = %+v", page2)
	}

	if past, err := p.AlbumList(SortBooksByTitle, 99, 2); err != nil || len(past) != 0 {
		t.Fatalf("offset past end = %+v, %v", past, err)
	}

	if got := p.DefaultAlbumSort(); got != SortBooksByTitle {
		t.Fatalf("DefaultAlbumSort() = %q, want %q", got, SortBooksByTitle)
	}
	if len(p.AlbumSortTypes()) != 3 {
		t.Fatalf("AlbumSortTypes() = %+v, want 3 options", p.AlbumSortTypes())
	}
}

func TestAlbumListEmptyCatalogCached(t *testing.T) {
	calls := 0
	p := mockProvider(func(req *http.Request) (*http.Response, error) {
		calls++
		switch req.URL.Path {
		case "/api/libraries":
			return jsonResponse(`{"libraries":[{"id":"lib-b","name":"Audiobooks","mediaType":"book"}]}`), nil
		case "/api/libraries/lib-b/items":
			return jsonResponse(`{"total":0,"results":[]}`), nil
		default:
			t.Fatalf("unexpected path %s", req.URL.Path)
			return nil, nil
		}
	})

	albums, err := p.AlbumList(SortBooksByTitle, 0, 0)
	if err != nil {
		t.Fatalf("AlbumList() error: %v", err)
	}
	if len(albums) != 0 {
		t.Fatalf("got %d albums, want 0", len(albums))
	}

	if _, err := p.AlbumList(SortBooksByTitle, 0, 0); err != nil {
		t.Fatalf("cached AlbumList() error: %v", err)
	}
	if calls != 2 {
		t.Fatalf("http calls = %d, want 2 (libraries + items on the first call; second call must hit the cache)", calls)
	}
}

func TestSearchTracksExpandsHits(t *testing.T) {
	p := mockProvider(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/api/libraries":
			return jsonResponse(`{"libraries":[{"id":"lib-b","name":"Audiobooks","mediaType":"book"}]}`), nil
		case "/api/libraries/lib-b/search":
			return jsonResponse(`{"book":[{"libraryItem":{"id":"book-1","mediaType":"book"}}]}`), nil
		case "/api/items/book-1":
			return jsonResponse(bookItemJSON), nil
		default:
			t.Fatalf("unexpected path %s", req.URL.Path)
			return nil, nil
		}
	})

	tracks, err := p.SearchTracks(context.Background(), "mistborn", 50)
	if err != nil {
		t.Fatalf("SearchTracks() error: %v", err)
	}
	if len(tracks) != 2 {
		t.Fatalf("got %d tracks, want 2", len(tracks))
	}
	if tracks[0].Album != "Mistborn" {
		t.Fatalf("track = %+v", tracks[0])
	}
}

func TestSearchTracksHonoursLimit(t *testing.T) {
	p := mockProvider(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/api/libraries":
			return jsonResponse(`{"libraries":[{"id":"lib-b","name":"Audiobooks","mediaType":"book"}]}`), nil
		case "/api/libraries/lib-b/search":
			return jsonResponse(`{"book":[{"libraryItem":{"id":"book-1","mediaType":"book"}}]}`), nil
		case "/api/items/book-1":
			return jsonResponse(bookItemJSON), nil
		default:
			t.Fatalf("unexpected path %s", req.URL.Path)
			return nil, nil
		}
	})

	tracks, err := p.SearchTracks(context.Background(), "mistborn", 1)
	if err != nil {
		t.Fatalf("SearchTracks() error: %v", err)
	}
	if len(tracks) != 1 {
		t.Fatalf("got %d tracks, want 1", len(tracks))
	}
}

func TestAlbumTracksDelegatesToTracks(t *testing.T) {
	p := mockProvider(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/api/items/book-1" {
			t.Fatalf("unexpected path %s", req.URL.Path)
		}
		return jsonResponse(bookItemJSON), nil
	})

	tracks, err := p.AlbumTracks("b:book-1")
	if err != nil {
		t.Fatalf("AlbumTracks() error: %v", err)
	}
	if len(tracks) != 2 {
		t.Fatalf("got %d tracks, want 2", len(tracks))
	}
}

func TestCapabilityInterfaces(t *testing.T) {
	var p any = newProvider(NewClient("https://abs.example.com", "tok", "", "", nil))
	if _, ok := p.(provider.ArtistBrowser); !ok {
		t.Fatal("Provider does not implement provider.ArtistBrowser")
	}
	if _, ok := p.(provider.AlbumBrowser); !ok {
		t.Fatal("Provider does not implement provider.AlbumBrowser")
	}
	if _, ok := p.(provider.AlbumTrackLoader); !ok {
		t.Fatal("Provider does not implement provider.AlbumTrackLoader")
	}
	if _, ok := p.(provider.Searcher); !ok {
		t.Fatal("Provider does not implement provider.Searcher")
	}
}

func TestReportProgressSendsBookTimelinePosition(t *testing.T) {
	var gotPath, gotBody string
	p := mockProvider(func(req *http.Request) (*http.Response, error) {
		if !strings.HasPrefix(req.URL.Path, "/api/me/progress/") {
			t.Fatalf("unexpected path %s", req.URL.Path)
		}
		data, _ := io.ReadAll(req.Body)
		gotPath, gotBody = req.URL.Path, string(data)
		return statusResponse(http.StatusOK, "200 OK"), nil
	})

	track := playlist.Track{
		DurationSecs: 3600,
		ProviderMeta: map[string]string{
			provider.MetaAudiobookshelfID:     "book-1",
			provider.MetaAudiobookshelfOffset: "3600",
			provider.MetaAudiobookshelfTotal:  "7200",
		},
	}

	p.ReportProgress(track, 120*time.Second)

	if gotPath != "/api/me/progress/book-1" {
		t.Fatalf("path = %q, want /api/me/progress/book-1", gotPath)
	}
	if !strings.Contains(gotBody, `"currentTime":3720`) {
		t.Fatalf("body = %s, want currentTime 3720 (offset + position)", gotBody)
	}
	if !strings.Contains(gotBody, `"isFinished":false`) {
		t.Fatalf("body = %s, want isFinished false", gotBody)
	}
}

func TestReportNowPlayingSkipsZeroPosition(t *testing.T) {
	track := playlist.Track{
		DurationSecs: 3600,
		ProviderMeta: map[string]string{
			provider.MetaAudiobookshelfID:     "book-1",
			provider.MetaAudiobookshelfOffset: "3600",
			provider.MetaAudiobookshelfTotal:  "7200",
		},
	}

	p := mockProvider(func(req *http.Request) (*http.Response, error) {
		t.Fatal("no request expected for a zero position")
		return nil, nil
	})
	p.ReportNowPlaying(track, 0, false)

	var gotBody string
	p2 := mockProvider(func(req *http.Request) (*http.Response, error) {
		data, _ := io.ReadAll(req.Body)
		gotBody = string(data)
		return statusResponse(http.StatusOK, "200 OK"), nil
	})
	p2.ReportNowPlaying(track, 120*time.Second, false)
	if !strings.Contains(gotBody, `"currentTime":3720`) {
		t.Fatalf("body = %s, want currentTime 3720 (offset + position)", gotBody)
	}
}

func TestReportScrobbleMarksFinishedOnLastFile(t *testing.T) {
	tests := []struct {
		name         string
		offset       string
		duration     int
		elapsed      time.Duration
		wantFinished bool
	}{
		{name: "last file complete", offset: "3600", duration: 3600, elapsed: 3600 * time.Second, wantFinished: true},
		{name: "first file complete", offset: "0", duration: 3600, elapsed: 3600 * time.Second, wantFinished: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotBody string
			p := mockProvider(func(req *http.Request) (*http.Response, error) {
				data, _ := io.ReadAll(req.Body)
				gotBody = string(data)
				return statusResponse(http.StatusOK, "200 OK"), nil
			})

			track := playlist.Track{
				DurationSecs: tt.duration,
				ProviderMeta: map[string]string{
					provider.MetaAudiobookshelfID:     "book-1",
					provider.MetaAudiobookshelfOffset: tt.offset,
					provider.MetaAudiobookshelfTotal:  "7200",
				},
			}

			p.ReportScrobble(track, tt.elapsed, time.Duration(tt.duration)*time.Second, true)

			want := `"isFinished":false`
			if tt.wantFinished {
				want = `"isFinished":true`
			}
			if !strings.Contains(gotBody, want) {
				t.Fatalf("body = %s, want %s", gotBody, want)
			}
		})
	}
}

func TestReportProgressUsesEpisodePath(t *testing.T) {
	var gotPath string
	p := mockProvider(func(req *http.Request) (*http.Response, error) {
		gotPath = req.URL.Path
		return statusResponse(http.StatusOK, "200 OK"), nil
	})

	track := playlist.Track{
		DurationSecs: 1800,
		ProviderMeta: map[string]string{
			provider.MetaAudiobookshelfID:      "pod-1",
			provider.MetaAudiobookshelfEpisode: "ep-2",
		},
	}

	p.ReportProgress(track, 60*time.Second)

	if gotPath != "/api/me/progress/pod-1/ep-2" {
		t.Fatalf("path = %q, want /api/me/progress/pod-1/ep-2", gotPath)
	}
}

func TestCanReportPlayback(t *testing.T) {
	p := newProvider(NewClient("https://abs.example.com", "tok", "", "", nil))
	own := playlist.Track{ProviderMeta: map[string]string{provider.MetaAudiobookshelfID: "book-1"}}
	other := playlist.Track{ProviderMeta: map[string]string{provider.MetaJellyfinID: "jf-1"}}

	if !p.CanReportPlayback(own) {
		t.Fatal("CanReportPlayback(own) = false, want true")
	}
	if p.CanReportPlayback(other) {
		t.Fatal("CanReportPlayback(other) = true, want false")
	}
}

func TestProgressReporterInterface(t *testing.T) {
	var p any = newProvider(NewClient("https://abs.example.com", "tok", "", "", nil))
	if _, ok := p.(provider.ProgressReporter); !ok {
		t.Fatal("Provider does not implement provider.ProgressReporter")
	}
}

func bookResumeTracks() []playlist.Track {
	meta := func(offset string) map[string]string {
		return map[string]string{
			provider.MetaAudiobookshelfID:     "book-1",
			provider.MetaAudiobookshelfOffset: offset,
			provider.MetaAudiobookshelfTotal:  "7200",
		}
	}
	return []playlist.Track{
		{Path: "file1", DurationSecs: 3600, ProviderMeta: meta("0")},
		{Path: "file2", DurationSecs: 3600, ProviderMeta: meta("3600")},
	}
}

func TestResumeTargetBook(t *testing.T) {
	tests := []struct {
		name       string
		progress   string
		wantIndex  int
		wantOffset time.Duration
	}{
		{
			name:       "mid second file",
			progress:   `{"mediaProgress":[{"libraryItemId":"book-1","currentTime":4200,"duration":7200}]}`,
			wantIndex:  1,
			wantOffset: 600 * time.Second,
		},
		{
			name:       "mid first file",
			progress:   `{"mediaProgress":[{"libraryItemId":"book-1","currentTime":90,"duration":7200}]}`,
			wantIndex:  0,
			wantOffset: 90 * time.Second,
		},
		{
			name:       "finished",
			progress:   `{"mediaProgress":[{"libraryItemId":"book-1","currentTime":7200,"duration":7200,"isFinished":true}]}`,
			wantIndex:  0,
			wantOffset: 0,
		},
		{
			name:       "no progress stored",
			progress:   `{"mediaProgress":[]}`,
			wantIndex:  0,
			wantOffset: 0,
		},
		{
			name:       "other item only",
			progress:   `{"mediaProgress":[{"libraryItemId":"book-9","currentTime":500,"duration":7200}]}`,
			wantIndex:  0,
			wantOffset: 0,
		},
		{
			name:       "past end of last file",
			progress:   `{"mediaProgress":[{"libraryItemId":"book-1","currentTime":8000,"duration":7200}]}`,
			wantIndex:  1,
			wantOffset: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := mockProvider(func(req *http.Request) (*http.Response, error) {
				if req.URL.Path != "/api/me" {
					t.Fatalf("unexpected path %s", req.URL.Path)
				}
				return jsonResponse(tt.progress), nil
			})

			idx, offset := p.ResumeTarget("b:book-1", bookResumeTracks())
			if idx != tt.wantIndex || offset != tt.wantOffset {
				t.Fatalf("ResumeTarget() = (%d, %v), want (%d, %v)", idx, offset, tt.wantIndex, tt.wantOffset)
			}
		})
	}
}

func TestResumeTargetPodcastPicksInProgressEpisode(t *testing.T) {
	p := mockProvider(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/api/me" {
			t.Fatalf("unexpected path %s", req.URL.Path)
		}
		return jsonResponse(`{"mediaProgress":[
			{"libraryItemId":"pod-1","episodeId":"ep-1","currentTime":300,"duration":1800}
		]}`), nil
	})

	tracks := []playlist.Track{
		{Path: "ep2", DurationSecs: 2400, ProviderMeta: map[string]string{
			provider.MetaAudiobookshelfID:      "pod-1",
			provider.MetaAudiobookshelfEpisode: "ep-2",
		}},
		{Path: "ep1", DurationSecs: 1800, ProviderMeta: map[string]string{
			provider.MetaAudiobookshelfID:      "pod-1",
			provider.MetaAudiobookshelfEpisode: "ep-1",
		}},
	}

	idx, offset := p.ResumeTarget("p:pod-1", tracks)
	if idx != 1 || offset != 300*time.Second {
		t.Fatalf("ResumeTarget() = (%d, %v), want (1, 5m0s)", idx, offset)
	}
}

func TestResumeTargetPodcastSkipsSeekWhenDurationUnknown(t *testing.T) {
	p := mockProvider(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/api/me" {
			t.Fatalf("unexpected path %s", req.URL.Path)
		}
		return jsonResponse(`{"mediaProgress":[
			{"libraryItemId":"pod-1","episodeId":"ep-1","currentTime":300,"duration":0}
		]}`), nil
	})

	tracks := []playlist.Track{
		{Path: "ep1", DurationSecs: 0, ProviderMeta: map[string]string{
			provider.MetaAudiobookshelfID:      "pod-1",
			provider.MetaAudiobookshelfEpisode: "ep-1",
		}},
	}

	idx, offset := p.ResumeTarget("p:pod-1", tracks)
	if idx != 0 || offset != 0 {
		t.Fatalf("ResumeTarget() = (%d, %v), want (0, 0) when the target track's duration is unknown", idx, offset)
	}
}

func TestResumeTargetGuards(t *testing.T) {
	p := mockProvider(func(req *http.Request) (*http.Response, error) {
		t.Fatal("no request expected")
		return nil, nil
	})

	if idx, offset := p.ResumeTarget("b:book-1", nil); idx != 0 || offset != 0 {
		t.Fatalf("empty tracks = (%d, %v), want (0, 0)", idx, offset)
	}
	if idx, offset := p.ResumeTarget("bogus", bookResumeTracks()); idx != 0 || offset != 0 {
		t.Fatalf("bad id = (%d, %v), want (0, 0)", idx, offset)
	}
}

func TestResumeTargetInterface(t *testing.T) {
	var p any = newProvider(NewClient("https://abs.example.com", "tok", "", "", nil))
	if _, ok := p.(provider.ResumeTarget); !ok {
		t.Fatal("Provider does not implement provider.ResumeTarget")
	}
}

func TestBrowseLabels(t *testing.T) {
	p := newProvider(NewClient("https://abs.example.com", "tok", "", "", nil))
	artist, album := p.BrowseLabels()
	if artist != "Author" || album != "Book" {
		t.Fatalf("BrowseLabels() = (%q, %q), want (Author, Book)", artist, album)
	}
	var any_ any = p
	if _, ok := any_.(provider.BrowseLabeler); !ok {
		t.Fatal("Provider does not implement provider.BrowseLabeler")
	}
}

func TestSearchTracksHonoursCancellation(t *testing.T) {
	const searchPath = "/api/libraries/lib-b/search"
	tests := []struct {
		name      string
		cancelOn  string
		wantCalls int
	}{
		{name: "before the call", cancelOn: "", wantCalls: 0},
		{name: "after the library list", cancelOn: "/api/libraries", wantCalls: 1},
		{name: "after the search hits", cancelOn: searchPath, wantCalls: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			calls := 0
			p := mockProvider(func(req *http.Request) (*http.Response, error) {
				calls++
				var body string
				switch req.URL.Path {
				case "/api/libraries":
					body = `{"libraries":[{"id":"lib-b","name":"Audiobooks","mediaType":"book"}]}`
				case searchPath:
					body = `{"book":[{"libraryItem":{"id":"book-1","mediaType":"book"}}]}`
				default:
					t.Fatalf("request issued after cancellation: %s", req.URL.Path)
				}
				if req.URL.Path == tt.cancelOn {
					cancel()
				}
				return jsonResponse(body), nil
			})
			if tt.cancelOn == "" {
				cancel()
			}

			tracks, err := p.SearchTracks(ctx, "mistborn", 50)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("err = %v, want context.Canceled", err)
			}
			if len(tracks) != 0 {
				t.Fatalf("tracks = %d, want 0", len(tracks))
			}
			if calls != tt.wantCalls {
				t.Fatalf("http calls = %d, want %d", calls, tt.wantCalls)
			}
		})
	}
}

func TestReportProgressReturnsTheFailure(t *testing.T) {
	p := mockProvider(func(req *http.Request) (*http.Response, error) {
		return statusResponse(http.StatusNotFound, "404 Not Found"), nil
	})

	track := playlist.Track{
		DurationSecs: 3600,
		ProviderMeta: map[string]string{
			provider.MetaAudiobookshelfID:    "book-1",
			provider.MetaAudiobookshelfTotal: "7200",
		},
	}

	err := p.ReportProgress(track, 120*time.Second)
	if err == nil {
		t.Fatal("ReportProgress() error = nil, want the rejected write surfaced to the caller")
	}
	if !strings.Contains(err.Error(), "book-1") || !strings.Contains(err.Error(), "404") {
		t.Fatalf("error = %v, want the item id and the status", err)
	}

	// A track this provider does not own is not an error.
	if err := p.ReportProgress(playlist.Track{}, 120*time.Second); err != nil {
		t.Fatalf("ReportProgress() for a foreign track = %v, want nil", err)
	}
}
