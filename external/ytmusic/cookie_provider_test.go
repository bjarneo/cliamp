package ytmusic

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
	"github.com/bjarneo/cliamp/resolve"
)

func TestCookieProviderInterfaces(t *testing.T) {
	provs := NewCookieProviders("chrome")
	var _ playlist.Provider = provs.Music
	var _ playlist.Provider = provs.Video
	var _ playlist.Provider = provs.All
	var _ provider.Searcher = provs.Music
	var _ provider.Searcher = provs.Video
	var _ provider.Searcher = provs.All
	var _ playlist.Refresher = provs.Music
	var _ playlist.Refresher = provs.Video
	var _ playlist.Refresher = provs.All
	var _ provider.Closer = provs.Music
	var _ provider.Closer = provs.Video
	var _ provider.Closer = provs.All
}

func TestCookieProviderNames(t *testing.T) {
	provs := NewCookieProviders("chrome")
	if got := provs.Music.Name(); got != "YouTube Music" {
		t.Errorf("Music.Name() = %q, want %q", got, "YouTube Music")
	}
	if got := provs.Video.Name(); got != "YouTube" {
		t.Errorf("Video.Name() = %q, want %q", got, "YouTube")
	}
	if got := provs.All.Name(); got != "YouTube (All)" {
		t.Errorf("All.Name() = %q, want %q", got, "YouTube (All)")
	}
}

func TestCookieProviderPlaylists(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	mockPlaylists := []playlist.PlaylistInfo{
		{ID: "LM", Name: "Liked Music", TrackCount: 99},
		{ID: "LL", Name: "Liked Videos", TrackCount: 42},
		{ID: "PL111", Name: "My Playlist 1", TrackCount: 12},
		{ID: "PL222", Name: "My Playlist 2", TrackCount: 34},
	}
	fetchCount := 0
	base := &cookieBase{
		browser: "firefox",
		fetchFn: func(browser string) ([]playlist.PlaylistInfo, error) {
			fetchCount++
			if browser != "firefox" {
				t.Errorf("expected browser firefox, got %q", browser)
			}
			return mockPlaylists, nil
		},
	}

	musicProv := &CookieProvider{base: base, kind: KindMusic}
	videoProv := &CookieProvider{base: base, kind: KindVideo}
	allProv := &CookieProvider{base: base, kind: KindAll}

	// 1. Music playlists
	musicPls, err := musicProv.Playlists()
	if err != nil {
		t.Fatalf("musicProv.Playlists() error: %v", err)
	}
	if len(musicPls) != 3 {
		t.Fatalf("musicPls len = %d, want 3", len(musicPls))
	}
	if musicPls[0].ID != "LM" || musicPls[0].Name != "Liked Music" || musicPls[0].TrackCount != 99 {
		t.Errorf("musicPls[0] = %+v, want Liked Music (LM) with TrackCount 99", musicPls[0])
	}
	if musicPls[1].ID != "PL111" || musicPls[2].ID != "PL222" {
		t.Errorf("unexpected user playlists: %+v", musicPls[1:])
	}

	// 2. Video playlists (should use cached base playlists)
	videoPls, err := videoProv.Playlists()
	if err != nil {
		t.Fatalf("videoProv.Playlists() error: %v", err)
	}
	if len(videoPls) != 3 {
		t.Fatalf("videoPls len = %d, want 3", len(videoPls))
	}
	if videoPls[0].ID != "LL" || videoPls[0].Name != "Liked Videos" || videoPls[0].TrackCount != 42 {
		t.Errorf("videoPls[0] = %+v, want Liked Videos (LL) with TrackCount 42", videoPls[0])
	}

	// 3. All playlists (should use cached base playlists)
	allPls, err := allProv.Playlists()
	if err != nil {
		t.Fatalf("allProv.Playlists() error: %v", err)
	}
	if len(allPls) != 4 {
		t.Fatalf("allPls len = %d, want 4", len(allPls))
	}
	if allPls[0].ID != "LM" || allPls[0].TrackCount != 99 || allPls[1].ID != "LL" || allPls[1].TrackCount != 42 {
		t.Errorf("allPls pinned = %+v, %+v; want LM (99) and LL (42)", allPls[0], allPls[1])
	}

	if fetchCount != 1 {
		t.Errorf("fetchCount = %d, want 1 (cache miss only on first call)", fetchCount)
	}

	// 4. Test Refresh()
	musicProv.Refresh()
	_, _ = musicProv.Playlists()
	if fetchCount != 2 {
		t.Errorf("fetchCount after Refresh() = %d, want 2", fetchCount)
	}
}

func TestCookieProviderPlaylists_NilCaching(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fetchCount := 0
	base := &cookieBase{
		browser: "chrome",
		fetchFn: func(browser string) ([]playlist.PlaylistInfo, error) {
			fetchCount++
			return nil, nil
		},
	}
	prov := &CookieProvider{base: base, kind: KindMusic}
	pls1, err := prov.Playlists()
	if err != nil {
		t.Fatalf("first Playlists() unexpected error: %v", err)
	}
	if len(pls1) != 1 { // Only pinned Liked Music
		t.Errorf("len(pls1) = %d, want 1", len(pls1))
	}
	pls2, err := prov.Playlists()
	if err != nil {
		t.Fatalf("second Playlists() unexpected error: %v", err)
	}
	if len(pls2) != 1 {
		t.Errorf("len(pls2) = %d, want 1", len(pls2))
	}
	if fetchCount != 1 {
		t.Errorf("fetchCount = %d, want 1 (nil result should be cached)", fetchCount)
	}
}

func TestCookieProviderPlaylists_Error(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	base := &cookieBase{
		browser: "chrome",
		fetchFn: func(browser string) ([]playlist.PlaylistInfo, error) {
			return nil, errors.New("yt-dlp failed")
		},
	}
	prov := &CookieProvider{base: base, kind: KindMusic}
	_, err := prov.Playlists()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "ytmusic: fetch playlists:") {
		t.Errorf("expected wrapped error containing 'ytmusic: fetch playlists:', got %v", err)
	}
}

func TestFormatPlaylistURL(t *testing.T) {
	tests := []struct {
		id      string
		isMusic bool
		want    string
	}{
		{"LM", true, "https://music.youtube.com/playlist?list=LM"},
		{"LM", false, "https://music.youtube.com/playlist?list=LM"},
		{"LL", true, "https://www.youtube.com/playlist?list=LL"},
		{"LL", false, "https://www.youtube.com/playlist?list=LL"},
		{"PL12345", true, "https://music.youtube.com/playlist?list=PL12345"},
		{"PL12345", false, "https://www.youtube.com/playlist?list=PL12345"},
		{"https://music.youtube.com/playlist?list=CUSTOM", true, "https://music.youtube.com/playlist?list=CUSTOM"},
		{"http://example.com/stream", false, "http://example.com/stream"},
	}

	for _, tt := range tests {
		got := formatPlaylistURL(tt.id, tt.isMusic)
		if got != tt.want {
			t.Errorf("formatPlaylistURL(%q, %v) = %q, want %q", tt.id, tt.isMusic, got, tt.want)
		}
	}
}

func TestCookieProviderSearchTracks_Empty(t *testing.T) {
	prov := NewCookieProvider("chrome", KindMusic)
	tracks, err := prov.SearchTracks(context.Background(), "", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tracks) != 0 {
		t.Errorf("expected 0 tracks for empty query, got %d", len(tracks))
	}
}

func TestCookieProviderTracks_EmptyID(t *testing.T) {
	prov := NewCookieProvider("chrome", KindMusic)
	_, err := prov.Tracks("")
	if err == nil {
		t.Fatal("expected error for empty playlist id, got nil")
	}
}

func TestCookieProviderTracksCaching(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	base := newCookieBase("chrome")

	mockTracks := []playlist.Track{
		{Path: "https://music.youtube.com/watch?v=123", Title: "Song 1", Artist: "Artist 1", DurationSecs: 200},
		{Path: "https://music.youtube.com/watch?v=456", Title: "Song 2", Artist: "Artist 2", DurationSecs: 180},
	}
	musicTarget := formatPlaylistURL("PL123", true)
	base.trackCache[musicTarget] = mockTracks

	prov := &CookieProvider{base: base, kind: KindMusic}
	tracks, err := prov.Tracks("PL123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tracks) != 2 {
		t.Fatalf("got %d tracks, want 2", len(tracks))
	}
	if tracks[0].Title != "Song 1" || tracks[1].Artist != "Artist 2" {
		t.Errorf("unexpected tracks from cache: %+v", tracks)
	}

	// Verify Video provider with the same playlist ID uses distinct cache key
	videoTarget := formatPlaylistURL("PL123", false)
	videoTracks := []playlist.Track{
		{Path: "https://www.youtube.com/watch?v=789", Title: "Video 1", Artist: "Channel 1", DurationSecs: 300},
	}
	base.trackCache[videoTarget] = videoTracks

	videoProv := &CookieProvider{base: base, kind: KindVideo}
	vTracks, err := videoProv.Tracks("PL123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vTracks) != 1 || vTracks[0].Title != "Video 1" {
		t.Errorf("unexpected video tracks from cache: %+v", vTracks)
	}
}

func TestCookieProviderTracksLoadsInBatches(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	base := newCookieBase("firefox")
	var starts []int
	base.resolveFn = func(_ context.Context, _ string, start, count int, browser ...string) ([]playlist.Track, int, error) {
		starts = append(starts, start)
		if count != cookiePlaylistBatchSize {
			t.Fatalf("count = %d, want %d", count, cookiePlaylistBatchSize)
		}
		if len(browser) != 1 || browser[0] != "firefox" {
			t.Fatalf("browser = %v, want firefox", browser)
		}
		n := cookiePlaylistBatchSize
		if start > 0 {
			n = 20
		} else {
			n-- // One malformed source entry was omitted from the parsed tracks.
		}
		tracks := make([]playlist.Track, n)
		for i := range tracks {
			tracks[i].Title = fmt.Sprintf("Track %d", start+i)
		}
		entries := n
		if start == 0 {
			entries = cookiePlaylistBatchSize
		}
		return tracks, entries, nil
	}

	tracks, err := (&CookieProvider{base: base, kind: KindMusic}).Tracks("PL123")
	if err != nil {
		t.Fatalf("Tracks() error: %v", err)
	}
	if len(tracks) != 119 {
		t.Fatalf("tracks = %d, want 119", len(tracks))
	}
	if !slices.Equal(starts, []int{0, cookiePlaylistBatchSize}) {
		t.Fatalf("batch starts = %v, want [0 %d]", starts, cookiePlaylistBatchSize)
	}
	if _, err := os.Stat(ytCachePath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cookie provider persisted account cache: %v", err)
	}
}

func TestCookieProviderStopsTrackLoad(t *testing.T) {
	tests := []struct {
		name string
		stop func(*CookieProvider)
	}{
		{name: "refresh", stop: func(p *CookieProvider) { p.Refresh() }},
		{name: "close", stop: func(p *CookieProvider) { p.Close() }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := newCookieBase("firefox")
			started := make(chan struct{})
			base.resolveFn = func(ctx context.Context, _ string, _, _ int, _ ...string) ([]playlist.Track, int, error) {
				close(started)
				<-ctx.Done()
				return nil, 0, ctx.Err()
			}
			prov := &CookieProvider{base: base, kind: KindMusic}
			done := make(chan error, 1)
			go func() {
				_, err := prov.Tracks("PL123")
				done <- err
			}()

			<-started
			tt.stop(prov)
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("Tracks() error = %v, want context.Canceled", err)
				}
			case <-time.After(time.Second):
				t.Fatal("track load did not stop")
			}
		})
	}
}

func TestCookieProviderRefreshRejectsStaleResults(t *testing.T) {
	t.Run("playlists", func(t *testing.T) {
		base := newCookieBase("firefox")
		started := make(chan struct{})
		release := make(chan struct{})
		base.fetchFn = func(string) ([]playlist.PlaylistInfo, error) {
			close(started)
			<-release
			return []playlist.PlaylistInfo{{ID: "private"}}, nil
		}
		done := make(chan struct{})
		go func() {
			_, _ = base.fetchPlaylists()
			close(done)
		}()

		<-started
		base.refresh()
		close(release)
		<-done
		base.mu.Lock()
		defer base.mu.Unlock()
		if base.playlists != nil {
			t.Fatalf("stale playlists cached after refresh: %v", base.playlists)
		}
	})

	t.Run("tracks", func(t *testing.T) {
		base := newCookieBase("firefox")
		started := make(chan struct{})
		release := make(chan struct{})
		base.resolveFn = func(context.Context, string, int, int, ...string) ([]playlist.Track, int, error) {
			close(started)
			<-release
			return []playlist.Track{{Title: "Private"}}, 1, nil
		}
		done := make(chan struct{})
		go func() {
			_, _ = base.fetchTracks("private")
			close(done)
		}()

		<-started
		base.refresh()
		close(release)
		<-done
		base.mu.Lock()
		defer base.mu.Unlock()
		if _, ok := base.trackCache["private"]; ok {
			t.Fatal("stale tracks cached after refresh")
		}
	})
}

func TestCookieProviderSearchTracksHonorsCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping Unix shell script test on Windows")
	}
	tmpDir := t.TempDir()
	fakeYTDL := filepath.Join(tmpDir, "yt-dlp")
	if err := os.WriteFile(fakeYTDL, []byte("#!/bin/sh\nexec sleep 10\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tmpDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewCookieProvider("chrome", KindMusic).SearchTracks(ctx, "query", 10)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("SearchTracks() error = %v, want context.Canceled", err)
	}
}

func TestNewCookieProvidersDoesNotMutateOtherHostCookies(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping Unix shell script test on Windows")
	}
	t.Cleanup(func() { resolve.SetYTDLCookiesForHost("soundcloud.com", "") })

	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "ytdlp_args.log")
	fakeYTDL := filepath.Join(tmpDir, "yt-dlp")

	script := "#!/bin/sh\necho \"$@\" > \"" + logFile + "\"\n"
	if err := os.WriteFile(fakeYTDL, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", tmpDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	// Configure a cookie source for another provider.
	resolve.SetYTDLCookiesForHost("soundcloud.com", "firefox")

	// Initializing YouTube Music cookie providers must not affect SoundCloud.
	_ = NewCookieProviders("chrome")
	_ = NewCookieProvider("chrome", KindMusic)

	_, _ = resolve.ResolveYTDLBatch("https://soundcloud.com/user/tracks", 0, 0)
	logged, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logged), "--cookies-from-browser firefox") {
		t.Errorf("expected SoundCloud cookies 'firefox' to remain unchanged, got: %s", string(logged))
	}
	if strings.Contains(string(logged), "--cookies-from-browser chrome") {
		t.Errorf("SoundCloud cookies were replaced with 'chrome': %s", string(logged))
	}
}
