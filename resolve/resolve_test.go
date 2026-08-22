package resolve

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestArgsTreatsXiaoyuzhouEpisodeAsPending(t *testing.T) {
	url := "https://www.xiaoyuzhoufm.com/episode/69a13b07a22480add648dd03?s=eyJ1IjogIjYxODEzNmZiZTBmNWU3MjNiYjk2MmE5MiJ9"

	got, err := Args([]string{url})
	if err != nil {
		t.Fatalf("Args returned error: %v", err)
	}
	if len(got.Tracks) != 0 {
		t.Fatalf("Args returned %d immediate tracks, want 0", len(got.Tracks))
	}
	if len(got.Pending) != 1 || got.Pending[0] != url {
		t.Fatalf("Args pending = %#v, want [%q]", got.Pending, url)
	}
}

func TestRemoteResolvesXiaoyuzhouEpisodeHTML(t *testing.T) {
	const episodeURL = "https://www.xiaoyuzhoufm.com/episode/69a13b07a22480add648dd03?s=eyJ1IjogIjYxODEzNmZiZTBmNWU3MjNiYjk2MmE5MiJ9"
	const audioURL = "https://media.xyzcdn.net/65d322815c5cc49b4db454a8/lqbqTgipk04QFSwIMACyGNK655rR.m4a"
	const title = "周轶君对话张艾嘉：我从不刻意标榜“女性”"
	const podcast = "山下声"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/episode/69a13b07a22480add648dd03" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.Error(w, "unexpected path", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!DOCTYPE html>
<html><head>
<script name="schema:podcast-show" type="application/ld+json">{
  "@context":"https://schema.org/",
  "@type":"PodcastEpisode",
  "url":"https://www.xiaoyuzhoufm.com/episode/69a13b07a22480add648dd03",
  "name":"` + title + `",
  "timeRequired":"PT106M",
  "associatedMedia":{"@type":"MediaObject","contentUrl":"` + audioURL + `"},
  "partOfSeries":{"@type":"PodcastSeries","name":"` + podcast + `","url":"https://www.xiaoyuzhoufm.com/podcast/65d322815c5cc49b4db454a8"}
}</script>
</head><body></body></html>`))
	}))
	defer srv.Close()

	target, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parsing test server URL: %v", err)
	}

	oldClient := httpClient
	httpClient = &http.Client{
		Timeout:   30 * time.Second,
		Transport: rewriteHostTransport{target: target, rt: http.DefaultTransport},
	}
	defer func() {
		httpClient = oldClient
	}()

	tracks, err := Remote([]string{episodeURL})
	if err != nil {
		t.Fatalf("Remote returned error: %v", err)
	}
	if len(tracks) != 1 {
		t.Fatalf("Remote returned %d tracks, want 1", len(tracks))
	}
	track := tracks[0]
	if track.Path != audioURL {
		t.Fatalf("track.Path = %q, want %q", track.Path, audioURL)
	}
	if track.Title != title {
		t.Fatalf("track.Title = %q, want %q", track.Title, title)
	}
	if track.Artist != podcast {
		t.Fatalf("track.Artist = %q, want %q", track.Artist, podcast)
	}
	if !track.Stream {
		t.Fatalf("track.Stream = false, want true")
	}
	if track.DurationSecs != 106*60 {
		t.Fatalf("track.DurationSecs = %d, want %d", track.DurationSecs, 106*60)
	}
}

func TestParseXiaoyuzhouOgAudioTakesPrecedence(t *testing.T) {
	const audioURL = "https://media.xyzcdn.net/audio.m4a"
	const title = "Test Episode"

	doc := `<!DOCTYPE html>
<html><head>
<meta property="og:audio" content="` + audioURL + `">
<meta property="og:title" content="` + title + `">
</head><body></body></html>`

	track, err := parseXiaoyuzhouEpisodeHTML("https://www.xiaoyuzhoufm.com/episode/abc", doc)
	if err != nil {
		t.Fatalf("parseXiaoyuzhouEpisodeHTML returned error: %v", err)
	}
	if track.Path != audioURL {
		t.Fatalf("track.Path = %q, want %q", track.Path, audioURL)
	}
	if track.Title != title {
		t.Fatalf("track.Title = %q, want %q", track.Title, title)
	}
}

func TestParseItunesDuration(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		// Plain seconds
		{"3600", 3600},
		{"90", 90},
		{"0", 0},
		// Fractional seconds
		{"3661.5", 3661},
		{"90.9", 90},
		// MM:SS
		{"1:30", 90},
		{"87:05", 5225},
		// HH:MM:SS
		{"1:27:05", 5225},
		{"0:01:30", 90},
		// Whitespace
		{" 3600 ", 3600},
		// Empty
		{"", 0},
		// Invalid — return 0
		{"abc", 0},
		{"12:xx", 0},
		{"1:2:xx", 0},
		// Negative — clamp to 0
		{"-1", 0},
		{"0:-10", 0},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseItunesDuration(tt.input)
			if got != tt.want {
				t.Errorf("parseItunesDuration(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

type rewriteHostTransport struct {
	target *url.URL
	rt     http.RoundTripper
}

func (t rewriteHostTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = t.target.Scheme
	clone.URL.Host = t.target.Host
	clone.Host = t.target.Host
	return t.rt.RoundTrip(clone)
}

func TestIsHLSPlaylist(t *testing.T) {
	master := "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1000000\nchunklist_abc.m3u8\n"
	media := "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:6\n#EXT-X-MEDIA-SEQUENCE:42\n#EXTINF:6.0,\nmedia_42.ts\n"
	simple := "#EXTM3U\n#EXTINF:-1,Radio\nhttp://radio.example.com/stream\n"

	if !isHLSPlaylist([]byte(master)) {
		t.Error("master playlist should be detected as HLS")
	}
	if !isHLSPlaylist([]byte(media)) {
		t.Error("media playlist should be detected as HLS")
	}
	if isHLSPlaylist([]byte(simple)) {
		t.Error("plain radio M3U must NOT be detected as HLS")
	}
}

func TestResolveM3U_HLS_ReturnsSingleStream(t *testing.T) {
	const master = "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1000000\nchunklist_abc.m3u8\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		_, _ = io.WriteString(w, master)
	}))
	defer srv.Close()

	u := srv.URL + "/primary/gaucha_rbs.sdp/playlist.m3u8"
	tracks, err := resolveM3U(u)
	if err != nil {
		t.Fatalf("resolveM3U: %v", err)
	}
	if len(tracks) != 1 {
		t.Fatalf("got %d tracks, want 1 (HLS = single stream)", len(tracks))
	}
	if tracks[0].Path != u {
		t.Errorf("Path = %q, want original URL %q", tracks[0].Path, u)
	}
	if !tracks[0].Stream {
		t.Error("Stream should be true")
	}
	if !tracks[0].Realtime {
		t.Error("Realtime should be true (no #EXT-X-ENDLIST)")
	}
}

func TestResolveM3U_PlainPlaylist_StillParsesTracks(t *testing.T) {
	const pl = "#EXTM3U\n#EXTINF:-1,A\nhttp://x/a.mp3\n#EXTINF:-1,B\nhttp://x/b.mp3\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, pl)
	}))
	defer srv.Close()

	tracks, err := resolveM3U(srv.URL + "/list.m3u")
	if err != nil {
		t.Fatalf("resolveM3U: %v", err)
	}
	if len(tracks) != 2 {
		t.Fatalf("got %d tracks, want 2 (regression guard)", len(tracks))
	}
}

func TestAudioFilesSkipsUnreadableSubdir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Chmod does not map to Unix directory permissions on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("permission checks do not apply to root")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.mp3"), []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(dir, "locked")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(locked, "b.mp3"), []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(locked, "c.ogg"), []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })

	files, err := AudioFiles(dir, true)
	if err != nil {
		t.Fatalf("AudioFiles: %v", err)
	}
	if len(files) != 1 || filepath.Base(files[0]) != "a.mp3" {
		t.Fatalf("AudioFiles = %v, want only a.mp3 (unreadable subdir must be skipped)", files)
	}

	// Non-recursive mode must behave the same way (no abort either).
	if files, err := AudioFiles(dir, false); err != nil || len(files) != 1 {
		t.Fatalf("non-recursive AudioFiles = %v err=%v, want only a.mp3", files, err)
	}
}

func TestResolveYTDLBatchCookieSelection(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping Unix shell script test on Windows")
	}
	t.Cleanup(func() { SetYTDLCookiesForHost("example.com", "") })

	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "ytdlp_args.log")
	fakeYTDL := filepath.Join(tmpDir, "yt-dlp")

	script := "#!/bin/sh\necho \"$@\" > \"" + logFile + "\"\n"
	if err := os.WriteFile(fakeYTDL, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", tmpDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	// 1. Fall back to cookies configured for the URL's host.
	SetYTDLCookiesForHost("example.com", "firefox")
	_, _ = ResolveYTDLBatch("https://example.com/playlist", 0, 0)

	logged, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logged), "--cookies-from-browser firefox") {
		t.Errorf("expected host cookies 'firefox' in args, got: %s", string(logged))
	}

	// 2. An explicit browser overrides the host cookie source.
	_, _ = ResolveYTDLBatch("https://example.com/playlist", 0, 0, "chrome")
	logged, err = os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logged), "--cookies-from-browser chrome") {
		t.Errorf("expected explicit browser 'chrome' in args, got: %s", string(logged))
	}
	if strings.Contains(string(logged), "--cookies-from-browser firefox") {
		t.Errorf("did not expect host cookies 'firefox' in args, got: %s", string(logged))
	}
}

func TestParseYTDLTracksCountsMalformedEntries(t *testing.T) {
	input := strings.Join([]string{
		`{"webpage_url":"https://example.com/one","title":"One"}`,
		`{malformed}`,
		`{"title":"Missing URL"}`,
	}, "\n")

	tracks, entries, err := parseYTDLTracks(strings.NewReader(input))
	if err != nil {
		t.Fatalf("parseYTDLTracks() error: %v", err)
	}
	if entries != 3 {
		t.Fatalf("source entries = %d, want 3", entries)
	}
	if len(tracks) != 1 || tracks[0].Title != "One" {
		t.Fatalf("tracks = %+v, want one valid track", tracks)
	}
}
