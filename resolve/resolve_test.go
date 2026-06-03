package resolve

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
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
