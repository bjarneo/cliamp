package resolve

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/playlist"
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
	tracks, err := resolveM3U(t.Context(), u)
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

	tracks, err := resolveM3U(t.Context(), srv.URL+"/list.m3u")
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

func TestAudioFilesSkipsHiddenEntries(t *testing.T) {
	// The scanned directory may itself be hidden; only entries under it are
	// filtered.
	dir := filepath.Join(t.TempDir(), ".music")
	for _, rel := range []string{
		"01 Track.m4a",
		"._01 Track.m4a", // macOS AppleDouble sidecar
		".hidden/x.mp3",
		"disc2/02 Track.mp3",
		"disc2/._02 Track.mp3",
	} {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte{}, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	files, err := AudioFiles(dir, true)
	if err != nil {
		t.Fatalf("AudioFiles: %v", err)
	}
	want := []string{
		filepath.Join(dir, "01 Track.m4a"),
		filepath.Join(dir, "disc2", "02 Track.mp3"),
	}
	if !slices.Equal(files, want) {
		t.Fatalf("recursive AudioFiles = %v, want %v", files, want)
	}

	files, err = AudioFiles(dir, false)
	if err != nil {
		t.Fatalf("non-recursive AudioFiles: %v", err)
	}
	if !slices.Equal(files, want[:1]) {
		t.Fatalf("non-recursive AudioFiles = %v, want %v", files, want[:1])
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
	_, _ = ResolveYTDLBatch("https://example.com/playlist", 0, 0, "")

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

// TestYTDLRangeFlags pins the yt-dlp range flags that each entry point sends.
// yt-dlp counts from 1, and count 0 means all remaining entries.
func TestYTDLRangeFlags(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping Unix shell script test on Windows")
	}
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "ytdlp_args.log")
	script := "#!/bin/sh\necho \"$@\" > \"" + logFile + "\"\n" +
		"echo '{\"webpage_url\":\"https://example.com/v1\",\"title\":\"One\"}'\n" +
		"echo '{malformed}'\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "yt-dlp"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tmpDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	const page = "https://example.com/playlist"
	tests := []struct {
		name string
		run  func() ([]playlist.Track, error)
		want string
	}{
		{"batch all", func() ([]playlist.Track, error) { return ResolveYTDLBatch(page, 0, 0, "") }, ""},
		{"batch first 5", func() ([]playlist.Track, error) { return ResolveYTDLBatch(page, 0, 5, "") }, "--playlist-end 5"},
		{"batch from 20", func() ([]playlist.Track, error) { return ResolveYTDLBatch(page, 20, 0, "") }, "--playlist-start 21"},
		{"batch 20 to 30", func() ([]playlist.Track, error) { return ResolveYTDLBatch(page, 20, 10, "") }, "--playlist-start 21 --playlist-end 30"},
		{"page context", func() ([]playlist.Track, error) {
			tracks, entries, err := ResolveYTDLBatchPageContext(t.Context(), page, 2, 3, "")
			if err == nil && entries != 2 {
				err = fmt.Errorf("entries = %d, want 2", entries)
			}
			return tracks, err
		}, "--playlist-start 3 --playlist-end 5"},
		{"remote first items", func() ([]playlist.Track, error) {
			return resolveYTDL(t.Context(), page, YTDLRadioInitialItems)
		}, "--playlist-end 20"},
		{"remote all", func() ([]playlist.Track, error) { return resolveYTDL(t.Context(), page, 0) }, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tracks, err := tt.run()
			if err != nil {
				t.Fatal(err)
			}
			if len(tracks) != 1 || tracks[0].Path != "https://example.com/v1" {
				t.Fatalf("tracks = %+v, want the one valid entry", tracks)
			}
			logged, err := os.ReadFile(logFile)
			if err != nil {
				t.Fatal(err)
			}
			want := strings.Join(strings.Fields("--flat-playlist -j --socket-timeout 15 "+tt.want+" -- "+page), " ")
			if got := strings.Join(strings.Fields(string(logged)), " "); got != want {
				t.Fatalf("yt-dlp args = %q, want %q", got, want)
			}
		})
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

// TestResolvePLSCapsBody pins that an oversized remote PLS is rejected rather
// than silently truncated. io.LimitReader alone cuts mid-line, and parsePLS
// would turn the partial "FileN=https:/" into a track with a non-URL path,
// which the player then treats as a local file.
func TestResolvePLSCapsBody(t *testing.T) {
	oversized := buildPLS(40000) // 1,577,799 bytes, over maxPlaylistBody
	if len(oversized) <= maxPlaylistBody {
		t.Fatalf("fixture is %d bytes, needs to exceed maxPlaylistBody (%d)", len(oversized), maxPlaylistBody)
	}

	tests := []struct {
		name    string
		body    string
		wantErr bool
		want    int
	}{
		{name: "under the cap", body: buildPLS(10), want: 10},
		{name: "over the cap", body: oversized, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "audio/x-scpls")
				io.WriteString(w, tt.body)
			}))
			defer srv.Close()

			tracks, err := resolvePLS(t.Context(), srv.URL+"/stations.pls")
			if tt.wantErr {
				if err == nil {
					t.Fatalf("resolvePLS accepted a %d-byte body and returned %d tracks, want an error",
						len(tt.body), len(tracks))
				}
				if !strings.Contains(err.Error(), "exceeds") {
					t.Fatalf("error = %v, want it to name the cap", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolvePLS returned error: %v", err)
			}
			if len(tracks) != tt.want {
				t.Fatalf("got %d tracks, want %d", len(tracks), tt.want)
			}
			for i, tr := range tracks {
				if !playlist.IsURL(tr.Path) {
					t.Fatalf("tracks[%d].Path = %q, want a URL: a truncated entry must never reach the playlist", i, tr.Path)
				}
			}
		})
	}
}

// TestResolvePLSStopsReadingAtTheCap pins that the cap bounds the read itself,
// not just the size check afterwards. Without the LimitReader the whole body is
// pulled into memory before its length is ever examined, which is the condition
// the cap exists to prevent.
func TestResolvePLSStopsReadingAtTheCap(t *testing.T) {
	const served = 32 << 20 // far more than maxPlaylistBody

	var written atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/x-scpls")
		io.WriteString(w, "[playlist]\n")
		chunk := []byte(strings.Repeat("File1=https://example.com/1.mp3\n", 2048))
		for written.Load() < served {
			n, err := w.Write(chunk)
			written.Add(int64(n))
			if err != nil {
				return // client stopped reading, which is the point
			}
		}
	}))
	defer srv.Close()

	if _, err := resolvePLS(t.Context(), srv.URL+"/endless.pls"); err == nil {
		t.Fatal("resolvePLS accepted an oversized body, want an error")
	}

	// Allow generous slack for socket and proxy buffering; without the cap the
	// server drains all 32 MB.
	if got := written.Load(); got >= served/2 {
		t.Fatalf("server wrote %d bytes before the client stopped, want well under %d", got, served)
	}
}

// TestResolveM3UCapsBody pins the size rule for remote M3U bodies. A plain
// M3U over the cap is an error, as for PLS, because truncation cuts the last
// entry into a bogus track. An HLS body only decides the stream type, so a
// long VOD media playlist over the cap still plays as one stream.
func TestResolveM3UCapsBody(t *testing.T) {
	plain := func(entries int) string {
		var sb strings.Builder
		sb.WriteString("#EXTM3U\n")
		for i := 1; i <= entries; i++ {
			fmt.Fprintf(&sb, "#EXTINF:120,Track %d\nhttps://example.com/%d.mp3\n", i, i)
		}
		return sb.String()
	}
	var hls strings.Builder
	hls.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:6\n")
	for i := 0; hls.Len() <= maxPlaylistBody; i++ {
		fmt.Fprintf(&hls, "#EXTINF:6.0,\nsegment_%08d.ts\n", i)
	}
	live := hls.String()
	hls.WriteString("#EXT-X-ENDLIST\n")

	tests := []struct {
		name         string
		body         string
		wantErr      bool
		wantTracks   int
		wantRealtime bool
	}{
		{name: "plain under the cap", body: plain(10), wantTracks: 10},
		{name: "plain over the cap", body: plain(30000), wantErr: true},
		{name: "hls vod over the cap", body: hls.String(), wantTracks: 1},
		{name: "hls live over the cap", body: live, wantTracks: 1, wantRealtime: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if strings.HasSuffix(tt.name, "over the cap") && len(tt.body) <= maxPlaylistBody {
				t.Fatalf("fixture is %d bytes, needs to exceed maxPlaylistBody (%d)", len(tt.body), maxPlaylistBody)
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.WriteString(w, tt.body)
			}))
			defer srv.Close()

			u := srv.URL + "/list.m3u8"
			tracks, err := resolveM3U(t.Context(), u)
			if tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), "exceeds") {
					t.Fatalf("resolveM3U = %d tracks, err %v, want an error that names the cap", len(tracks), err)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveM3U: %v", err)
			}
			if len(tracks) != tt.wantTracks {
				t.Fatalf("got %d tracks, want %d", len(tracks), tt.wantTracks)
			}
			for i, tr := range tracks {
				if !playlist.IsURL(tr.Path) {
					t.Fatalf("tracks[%d].Path = %q, want a URL", i, tr.Path)
				}
				if tr.Realtime != tt.wantRealtime {
					t.Errorf("tracks[%d].Realtime = %v, want %v", i, tr.Realtime, tt.wantRealtime)
				}
			}
		})
	}
}

func buildPLS(entries int) string {
	var sb strings.Builder
	sb.WriteString("[playlist]\n")
	for i := 1; i <= entries; i++ {
		fmt.Fprintf(&sb, "File%d=https://example.com/%d.mp3\n", i, i)
	}
	return sb.String()
}

// TestRemoteRequiresClassifiedURLs documents why URL exists: Remote's default
// arm assumes its input was already classified as a feed, so handing it a raw
// stream address parses the audio as XML.
func TestRemoteRequiresClassifiedURLs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		io.WriteString(w, "\xff\xfb\x90\x00not xml")
	}))
	defer srv.Close()

	if _, err := Remote([]string{srv.URL + "/live"}); err == nil {
		t.Fatal("Remote accepted an unclassified stream URL; URL is no longer needed")
	}
}

// TestClassifyRemote pins the one URL list that Args and Remote share,
// including the order rules between overlapping predicates.
func TestClassifyRemote(t *testing.T) {
	tests := []struct {
		url  string
		want remoteKind
	}{
		{"https://www.xiaoyuzhoufm.com/episode/abc123", kindXiaoyuzhou},
		{"https://music.youtube.com/watch?v=abc", kindYouTubeMusic},
		{"https://music.youtube.com/playlist?list=PLx", kindYouTubeMusic},
		{"https://www.youtube.com/watch?v=abc", kindYouTube},
		{"https://youtu.be/abc", kindYouTube},
		{"ytsearch5:lofi", kindYTDL},
		{"scsearch:artist", kindYTDL},
		{"https://soundcloud.com/artist/track", kindYTDL},
		{"https://www.mixcloud.com/creator/show/", kindYTDL},
		{"https://artist.bandcamp.com/album/name", kindYTDL},
		{"https://example.com/podcast.rss", kindFeed},
		{"https://example.com/feed.XML", kindFeed},
		{"https://example.com/list.m3u8", kindM3U},
		{"https://example.com/list.m3u", kindM3U},
		{"https://example.com/stations.pls", kindPLS},
		// Order rules: a yt-dlp site wins over a file extension.
		{"https://www.youtube.com/list.m3u8", kindYouTube},
		{"https://soundcloud.com/artist/feed.xml", kindYTDL},
		{"https://music.youtube.com/stations.pls", kindYouTubeMusic},
		// No resolver claims these. Args sniffs them for a feed.
		{"https://example.com/stream.mp3", kindStream},
		{"https://radio.example/live", kindStream},
		{"https://www.xiaoyuzhoufm.com/podcast/abc123", kindStream},
	}
	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			if got := classifyRemote(tt.url); got != tt.want {
				t.Fatalf("classifyRemote(%q) = %d, want %d", tt.url, got, tt.want)
			}
			if tt.want == kindStream {
				return // Args would send a HEAD request to sniff for a feed.
			}
			r, err := Args([]string{tt.url})
			if err != nil {
				t.Fatalf("Args: %v", err)
			}
			if len(r.Pending) != 1 || r.Pending[0] != tt.url || len(r.Tracks) != 0 {
				t.Fatalf("Args = %+v, want %q pending for Remote", r, tt.url)
			}
		})
	}
}

// TestURLContextCancelsRemoteFetch pins that URLContext stops a slow remote
// resolve or feed sniff when the caller cancels, well before the client
// limits of 30 s and 5 s.
func TestURLContextCancelsRemoteFetch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping Unix shell script test on Windows")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
	}))
	defer srv.Close()
	target, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	oldClient, oldSniff := httpClient, sniffClient
	httpClient = &http.Client{
		Timeout:   30 * time.Second,
		Transport: rewriteHostTransport{target: target, rt: http.DefaultTransport},
	}
	sniffClient = &http.Client{
		Timeout:   5 * time.Second,
		Transport: rewriteHostTransport{target: target, rt: http.DefaultTransport},
	}
	t.Cleanup(func() { httpClient, sniffClient = oldClient, oldSniff })

	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "yt-dlp"), []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tmpDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	for _, rawURL := range []string{
		"https://example.com/list.m3u",
		"https://example.com/stations.pls",
		"https://example.com/podcast.rss",
		"https://www.xiaoyuzhoufm.com/episode/abc123",
		"ytsearch:slow query",
		"https://example.com/live", // no resolver claims it, so Args sniffs it
	} {
		t.Run(rawURL, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			time.AfterFunc(100*time.Millisecond, cancel)
			start := time.Now()
			_, err := URLContext(ctx, rawURL)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("URLContext error = %v, want context.Canceled", err)
			}
			if elapsed := time.Since(start); elapsed > 5*time.Second {
				t.Fatalf("URLContext returned after %v, want it to stop at the cancel", elapsed)
			}
		})
	}
}
