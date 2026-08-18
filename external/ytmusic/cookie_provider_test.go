package ytmusic

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

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

	// Pre-populate disk cache with tracks using resolved target URL
	dc := base.ensureDiskCache()
	mockTracks := []playlist.Track{
		{Path: "https://music.youtube.com/watch?v=123", Title: "Song 1", Artist: "Artist 1", DurationSecs: 200},
		{Path: "https://music.youtube.com/watch?v=456", Title: "Song 2", Artist: "Artist 2", DurationSecs: 180},
	}
	musicTarget := formatPlaylistURL("PL123", true)
	dc.setTracks(musicTarget, mockTracks)
	saveSnapshot(dc.snapshot())

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
	dc.setTracks(videoTarget, videoTracks)
	saveSnapshot(dc.snapshot())

	videoProv := &CookieProvider{base: base, kind: KindVideo}
	vTracks, err := videoProv.Tracks("PL123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vTracks) != 1 || vTracks[0].Title != "Video 1" {
		t.Errorf("unexpected video tracks from cache: %+v", vTracks)
	}
}

func TestNewCookieProvidersDoesNotMutateGlobalCookies(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping Unix shell script test on Windows")
	}
	t.Cleanup(func() { resolve.SetYTDLCookiesFrom("") })

	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "ytdlp_args.log")
	fakeYTDL := filepath.Join(tmpDir, "yt-dlp")

	script := "#!/bin/sh\necho \"$@\" > \"" + logFile + "\"\n"
	if err := os.WriteFile(fakeYTDL, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", tmpDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	// Baseline global cookie configured by another provider (e.g. SoundCloud)
	resolve.SetYTDLCookiesFrom("firefox")

	// Initializing YouTube Music cookie providers should NOT overwrite global cookies
	_ = NewCookieProviders("chrome")
	_ = NewCookieProvider("chrome", KindMusic)

	// Caller relying on global cookies (e.g. SoundCloud) should still get firefox
	_, _ = resolve.ResolveYTDLBatch("https://soundcloud.com/user/tracks", 0, 0)
	logged, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logged), "--cookies-from-browser firefox") {
		t.Errorf("expected global cookies 'firefox' to remain unchanged, got: %s", string(logged))
	}
	if strings.Contains(string(logged), "--cookies-from-browser chrome") {
		t.Errorf("global cookies was corrupted with 'chrome': %s", string(logged))
	}
}
