package resolve

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/playlist"
)

func TestFeedMetadata(t *testing.T) {
	const body = `<rss version="2.0" xmlns:p="http://www.itunes.com/dtds/podcast-1.0.dtd">
<channel>
  <item>
    <title> Zulu &amp; friends </title><guid isPermaLink="false"> episode-id </guid>
    <pubDate>Tue, 08 Sep 2026 09:30:00 +0200</pubDate>
    <p:duration>01:35:02</p:duration><p:episode> 42 </p:episode>
    <p:image href=" https://example.com/episode.jpg "/>
    <enclosure url="https://example.com/cover.jpg" type="image/jpeg"/>
    <enclosure url="ssh://example.com/audio.mp3" type="audio/mpeg"/>
    <enclosure url="https://example.com/first?x=1&amp;y=2" type="audio/mpeg"/>
    <enclosure url="https://example.com/second.mp3" type="audio/mpeg"/>
  </item>
  <item><title>Not playable</title><p:duration>300</p:duration></item>
  <item><title> </title><guid> </guid><enclosure url="https://example.com/untitled.mp3"/></item>
  <item><title>Alpha</title><enclosure url="http://example.com/alpha.ogg"/></item>
  <title> Channel &amp; show </title>
  <p:image href="https://example.com/channel.jpg"/>
  <description><title>Not the channel title</title><item><enclosure url="https://example.com/nested.mp3"/></item></description>
</channel></rss>`
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET without classification probes", r.Method)
		}
		if got := r.UserAgent(); got != "cliamp/1.0 (https://github.com/bjarneo/cliamp)" {
			t.Errorf("User-Agent = %q", got)
		}
		if r.URL.Path == "/show" {
			http.Redirect(w, r, "/publisher", http.StatusFound)
			return
		}
		// Feed must parse publisher XML regardless of extension or Content-Type.
		w.Header().Set("Content-Type", "application/octet-stream")
		io.WriteString(w, body)
	}))
	defer srv.Close()
	feedURL := srv.URL + "/show?token=original"
	want := []playlist.Track{
		{
			Path: "https://example.com/first?x=1&y=2", Title: "Zulu & friends",
			Artist: "Channel & show", Album: "Channel & show", Stream: true,
			DurationSecs: 5702, TrackNumber: 42, AlbumArtURL: "https://example.com/episode.jpg",
			ProviderMeta: map[string]string{"podcast.feed": feedURL, "podcast.guid": "episode-id", "podcast.published": "2026-09-08"},
		},
		{
			Path: "https://example.com/untitled.mp3", Title: "Untitled episode",
			Artist: "Channel & show", Album: "Channel & show", Stream: true,
			AlbumArtURL:  "https://example.com/channel.jpg",
			ProviderMeta: map[string]string{"podcast.feed": feedURL, "podcast.guid": "https://example.com/untitled.mp3"},
		},
		{
			Path: "http://example.com/alpha.ogg", Title: "Alpha",
			Artist: "Channel & show", Album: "Channel & show", Stream: true,
			AlbumArtURL:  "https://example.com/channel.jpg",
			ProviderMeta: map[string]string{"podcast.feed": feedURL, "podcast.guid": "http://example.com/alpha.ogg"},
		},
	}
	for _, resolve := range []struct {
		name string
		call func(string) ([]playlist.Track, error)
	}{
		{"Feed", func(u string) ([]playlist.Track, error) { return Feed(context.Background(), u) }},
		{"resolveFeed", resolveFeed},
	} {
		t.Run(resolve.name, func(t *testing.T) {
			got, err := resolve.call(feedURL)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("tracks = %#v, want %#v", got, want)
			}
		})
	}
	if got := requests.Load(); got != 4 {
		t.Errorf("requests = %d, want 4 (one GET and redirect per call)", got)
	}
}

func TestFeedPublicationDates(t *testing.T) {
	for _, tt := range []struct {
		name, pubDate, want string
	}{
		{"RFC1123Z", "<pubDate>Tue, 08 Sep 2026 09:30:00 +0200</pubDate>", "2026-09-08"},
		{"RFC1123", "<pubDate>Tue, 08 Sep 2026 09:30:00 GMT</pubDate>", "2026-09-08"},
		{"RFC822", "<pubDate>08 Sep 26 09:30 GMT</pubDate>", "2026-09-08"},
		{"RFC3339", "<pubDate>2026-09-08T09:30:00Z</pubDate>", "2026-09-08"},
		{"publisher timezone", "<pubDate>2026-09-08T00:30:00+14:00</pubDate>", "2026-09-08"},
		{"whitespace", "<pubDate> \n2026-09-08T09:30:00Z\t </pubDate>", "2026-09-08"},
		{"escaped offset", "<pubDate>Tue, 08 Sep 2026 09:30:00 &#43;0200</pubDate>", "2026-09-08"},
		{"missing", "", ""},
		{"empty", "<pubDate/>", ""},
		{"blank", "<pubDate> \n\t </pubDate>", ""},
		{"invalid", "<pubDate>not a date</pubDate>", ""},
		{"invalid calendar date", "<pubDate>2026-02-30T09:30:00Z</pubDate>", ""},
		{"escaped markup", "<pubDate>&lt;script&gt;2026-09-08&lt;/script&gt;</pubDate>", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			body := `<rss><channel><item><enclosure url="https://example.com/audio.mp3"/>` + tt.pubDate + `</item></channel></rss>`
			feedURL := serveFeed(t, body)
			tracks, err := Feed(context.Background(), feedURL)
			if err != nil || len(tracks) != 1 {
				t.Fatalf("tracks = %+v, err = %v", tracks, err)
			}
			wantMeta := map[string]string{"podcast.feed": feedURL, "podcast.guid": "https://example.com/audio.mp3"}
			if tt.want != "" {
				wantMeta["podcast.published"] = tt.want
			}
			if !reflect.DeepEqual(tracks[0].ProviderMeta, wantMeta) {
				t.Errorf("ProviderMeta = %v, want %v", tracks[0].ProviderMeta, wantMeta)
			}
			if tracks[0].Year != 0 {
				t.Errorf("Year = %d, want unchanged zero value", tracks[0].Year)
			}
		})
	}
}

func TestFeedEnclosures(t *testing.T) {
	tests := []struct {
		name, enclosures, want string
	}{
		{"missing", "", ""},
		{"empty", `<enclosure url="" type="audio/mpeg"/>`, ""},
		{"audio MIME", `<enclosure url="https://example.com/audio" type="Audio/MPEG; charset=utf-8"/>`, "https://example.com/audio"},
		{"extension and query", `<enclosure url=" HTTPS://example.com/audio.MP3?x=1&amp;y=2#part "/>`, "https://example.com/audio.MP3?x=1&y=2#part"},
		{"blank MIME", `<enclosure url="http://example.com/audio.opus" type=" "/>`, "http://example.com/audio.opus"},
		{"IPv6", `<enclosure url="http://[::1]:8000/audio.flac"/>`, "http://[::1]:8000/audio.flac"},
		{"image before audio", `<enclosure url="https://example.com/cover.jpg" type="image/jpeg"/><enclosure url="https://example.com/audio.m4a"/>`, "https://example.com/audio.m4a"},
		{"first audio wins", `<enclosure url="https://example.com/first.wav"/><enclosure url="https://example.com/second.aac"/>`, "https://example.com/first.wav"},
		{"video MIME", `<enclosure url="https://example.com/audio.mp3" type="video/mp4"/>`, ""},
		{"generic MIME", `<enclosure url="https://example.com/audio.mp3" type="application/octet-stream"/>`, "https://example.com/audio.mp3"},
		{"binary MIME", `<enclosure url="https://example.com/audio.m4a" type="binary/octet-stream"/>`, "https://example.com/audio.m4a"},
		{"OGG MIME", `<enclosure url="https://example.com/audio" type="application/ogg"/>`, "https://example.com/audio"},
		{"generic MIME unknown extension", `<enclosure url="https://example.com/audio.bin" type="application/octet-stream"/>`, ""},
		{"not audio slash", `<enclosure url="https://example.com/audio.mp3" type="audiobook"/>`, ""},
		{"invalid MIME", `<enclosure url="https://example.com/audio.mp3" type="audio/"/>`, ""},
		{"unknown extension", `<enclosure url="https://example.com/audio.bin"/>`, ""},
		{"extension only in query", `<enclosure url="https://example.com/download?file=audio.mp3"/>`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := serveFeed(t, `<rss><channel><item>`+tt.enclosures+`</item></channel></rss>`)
			tracks, err := Feed(context.Background(), u)
			if err != nil {
				t.Fatal(err)
			}
			if tt.want == "" {
				if len(tracks) != 0 {
					t.Fatalf("got %d tracks, want none", len(tracks))
				}
				return
			}
			if len(tracks) != 1 || tracks[0].Path != tt.want || tracks[0].Title != "Untitled episode" {
				t.Fatalf("tracks = %+v, want untitled episode at %q", tracks, tt.want)
			}
			if !playlist.IsURL(tracks[0].Path) {
				t.Errorf("playback does not recognize %q as an HTTP(S) URL", tracks[0].Path)
			}
		})
	}
}

func TestFeedURLSafety(t *testing.T) {
	for _, raw := range []string{
		"", "/local/audio.mp3", "relative.mp3", "//example.com/audio.mp3",
		"file:///etc/passwd", "ssh://example.com/audio.mp3", "data:audio/mpeg,AAAA",
		"ftp://example.com/audio.mp3", "javascript:alert(1)", "https:audio.mp3",
		"https:///audio.mp3", "https://:443/audio.mp3", "https://example.com:bad/audio.mp3",
		"https://user:password@example.com/audio.mp3", "https://@example.com/audio.mp3",
		"https://example.com/%zz", "https://exam ple.com/audio.mp3", "https://example.com/a\nb.mp3",
	} {
		t.Run(raw, func(t *testing.T) {
			if tracks, err := Feed(context.Background(), raw); err == nil || !strings.Contains(err.Error(), "feed URL") || tracks != nil {
				t.Fatalf("Feed(%q) = %+v, %v; want URL validation error", raw, tracks, err)
			}
			escaped := html.EscapeString(raw)
			body := fmt.Sprintf(`<rss xmlns:i="%s"><channel>
<i:image href="%s"/>
<item><enclosure url="%s" type="audio/mpeg"/></item>
<item><enclosure url="https://example.com/safe.mp3"/><i:image href="%s"/></item>
</channel></rss>`, itunesNS, escaped, escaped, escaped)
			tracks, err := Feed(context.Background(), serveFeed(t, body))
			if err != nil {
				t.Fatal(err)
			}
			if len(tracks) != 1 || tracks[0].Path != "https://example.com/safe.mp3" || tracks[0].AlbumArtURL != "" {
				t.Fatalf("unsafe enclosure or artwork survived: %+v", tracks)
			}
		})
	}
}

func TestFeedArtwork(t *testing.T) {
	tests := []struct {
		name, channel, episode, want string
	}{
		{"RSS image", `<image><title>Image title</title><url> https://example.com/rss.jpg </url></image>`, "", "https://example.com/rss.jpg"},
		{"iTunes image", `<i:image href="https://example.com/show.jpg"/>`, "", "https://example.com/show.jpg"},
		{"episode override", `<i:image href="https://example.com/show.jpg"/>`, `<i:image href="HTTPS://example.com/episode.jpg"/>`, "https://example.com/episode.jpg"},
		{"invalid episode fallback", `<i:image href="https://example.com/show.jpg"/>`, `<i:image href="/relative.jpg"/>`, "https://example.com/show.jpg"},
		{"invalid channel fallback", `<i:image href="file:///cover.jpg"/><image><url>https://example.com/rss.jpg</url></image>`, "", "https://example.com/rss.jpg"},
		{"first valid episode image", "", `<i:image href="/relative.jpg"/><i:image href="https://example.com/episode.jpg"/>`, "https://example.com/episode.jpg"},
		{"unrelated namespace", "", `<other:image xmlns:other="urn:other" href="https://example.com/wrong.jpg"/>`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := fmt.Sprintf(`<rss xmlns:i="%s"><channel><title>Show</title>%s<item><enclosure url="https://example.com/audio.mp3"/>%s</item></channel></rss>`, itunesNS, tt.channel, tt.episode)
			tracks, err := Feed(context.Background(), serveFeed(t, body))
			if err != nil || len(tracks) != 1 {
				t.Fatalf("tracks = %+v, err = %v", tracks, err)
			}
			if tracks[0].AlbumArtURL != tt.want || tracks[0].Artist != "Show" {
				t.Errorf("track = %+v, want artwork %q and artist Show", tracks[0], tt.want)
			}
		})
	}
}

func TestFeedDurationsAndEpisodeNumbers(t *testing.T) {
	tests := []struct {
		duration, episode string
		seconds, number   int
	}{
		{"01:35:02", "42", 5702, 42},
		{"36:12", " 7 ", 2172, 7},
		{" 2712 ", "", 2712, 0},
		{"61.9", "0", 61, 0},
		{"1:01.5", "invalid", 61, 0},
		{"", "-3", 0, 0},
		{"nonsense", "1.5", 0, 0},
		{"1:2:3:4", "9999999999999999999999999999", 0, 0},
		{"-10", "1", 0, 1},
	}
	for _, tt := range tests {
		t.Run(tt.duration+"/"+tt.episode, func(t *testing.T) {
			body := fmt.Sprintf(`<rss xmlns:i="%s"><channel><item><enclosure url="https://example.com/audio.mp3"/><i:duration>%s</i:duration><i:episode>%s</i:episode></item></channel></rss>`, itunesNS, tt.duration, tt.episode)
			tracks, err := Feed(context.Background(), serveFeed(t, body))
			if err != nil || len(tracks) != 1 {
				t.Fatalf("tracks = %+v, err = %v", tracks, err)
			}
			if tracks[0].DurationSecs != tt.seconds || tracks[0].TrackNumber != tt.number {
				t.Errorf("duration = %d, number = %d; want %d, %d", tracks[0].DurationSecs, tracks[0].TrackNumber, tt.seconds, tt.number)
			}
		})
	}
}

func TestFeedEncodings(t *testing.T) {
	tests := []struct {
		encoding, prefix, title, want string
	}{
		{"UTF-8", "", "Caf\u00e9", "Caf\u00e9"},
		{"UTF-8", "\xef\xbb\xbf", "Caf\u00e9", "Caf\u00e9"},
		{"ISO-8859-1", "", "Caf\xe9", "Caf\u00e9"},
		{"windows-1252", "", "Publisher\x92s show", "Publisher\u2019s show"},
	}
	for _, tt := range tests {
		t.Run(tt.encoding+tt.prefix, func(t *testing.T) {
			body := tt.prefix + fmt.Sprintf(`<?xml version="1.0" encoding="%s"?><rss><channel><title>%s</title><item><title>%s</title><enclosure url="https://example.com/audio.mp3"/></item></channel></rss>`, tt.encoding, tt.title, tt.title)
			tracks, err := Feed(context.Background(), serveFeed(t, body))
			if err != nil || len(tracks) != 1 {
				t.Fatalf("tracks = %+v, err = %v", tracks, err)
			}
			if tracks[0].Title != tt.want || tracks[0].Artist != tt.want || tracks[0].Album != tt.want {
				t.Errorf("decoded metadata = %+v, want %q", tracks[0], tt.want)
			}
		})
	}
}

func TestFeedErrors(t *testing.T) {
	const emptyRSS = `<rss><channel/></rss>`
	tests := []struct {
		name, body, wantErr string
		status              int
	}{
		{"valid empty RSS", emptyRSS, "", 200},
		{"valid comments", `<!--before-->` + emptyRSS + `<!--after-->`, "", 200},
		{"no playable episodes", `<rss><channel><item><title>Only text</title></item></channel></rss>`, "", 200},
		{"empty", "", "parsing feed", 200},
		{"text", "not XML", "parsing feed", 200},
		{"HTML", `<html><body>Not RSS</body></html>`, "parsing feed", 200},
		{"Atom", `<feed xmlns="http://www.w3.org/2005/Atom"/>`, "parsing feed", 200},
		{"missing channel", `<rss/>`, "parsing feed", 200},
		{"nested channel", `<rss><wrapper><channel/></wrapper></rss>`, "parsing feed", 200},
		{"wrong namespace", `<rss xmlns="urn:other"><channel/></rss>`, "parsing feed", 200},
		{"multiple channels", `<rss><channel/><channel/></rss>`, "parsing feed", 200},
		{"multiple roots", emptyRSS + emptyRSS, "parsing feed", 200},
		{"trailing text", emptyRSS + "not XML", "parsing feed", 200},
		{"truncated", `<rss><channel><item>`, "parsing feed", 200},
		{"malformed enclosure", `<rss><channel><item><enclosure></item></channel></rss>`, "parsing feed", 200},
		{"malformed after episode", `<rss><channel><item><enclosure url="https://example.com/audio.mp3"/></item></wrong>`, "parsing feed", 200},
		{"unknown entity", `<rss><channel><title>&unknown;</title></channel></rss>`, "parsing feed", 200},
		{"invalid UTF-8", "<rss><channel><title>\xff</title></channel></rss>", "parsing feed", 200},
		{"unknown charset", `<?xml version="1.0" encoding="not-an-encoding"?>` + emptyRSS, "parsing feed", 200},
		{"not found", emptyRSS, "http status 404", 404},
		{"server error", emptyRSS, "http status 500", 500},
		{"no content", "", "http status 204", 204},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				io.WriteString(w, tt.body)
			}))
			defer srv.Close()
			tracks, err := Feed(context.Background(), srv.URL)
			if tt.wantErr == "" {
				if err != nil || len(tracks) != 0 {
					t.Fatalf("tracks = %+v, err = %v; want empty valid feed", tracks, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tt.wantErr) || tracks != nil {
				t.Fatalf("tracks = %+v, err = %v; want nil tracks and error containing %q", tracks, err, tt.wantErr)
			}
		})
	}
}

func TestFeedRedirectSafety(t *testing.T) {
	for _, target := range []string{"http://user:secret@%s/blocked", "http://@%s/blocked", "ftp://%s/blocked", "file:///blocked"} {
		t.Run(target, func(t *testing.T) {
			var requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.URL.Path == "/blocked" {
					io.WriteString(w, `<rss><channel/></rss>`)
					return
				}
				http.Redirect(w, r, strings.ReplaceAll(target, "%s", r.Host), http.StatusFound)
			}))
			defer srv.Close()
			if _, err := Feed(context.Background(), srv.URL); err == nil {
				t.Fatal("accepted unsafe redirect")
			}
			if got := requests.Load(); got != 1 {
				t.Errorf("made %d requests, want only the original request", got)
			}
		})
	}
}

func TestFeedHTTPSErrorsAndRedirectLimit(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/loop":
			http.Redirect(w, r, "/loop", http.StatusFound)
		case "/short":
			w.Header().Set("Content-Length", "1000")
			io.WriteString(w, `<rss><channel/></rss>`)
		default:
			io.WriteString(w, `<rss><channel/></rss>`)
		}
	}))
	defer srv.Close()
	oldClient := httpClient
	client := *httpClient
	client.Transport = &uaTransport{rt: srv.Client().Transport}
	httpClient = &client
	defer func() { httpClient = oldClient }()
	if client.Timeout != 30*time.Second {
		t.Fatalf("client timeout = %v, want 30s", client.Timeout)
	}
	if _, err := Feed(context.Background(), srv.URL); err != nil {
		t.Fatalf("HTTPS feed: %v", err)
	}
	if _, err := Feed(context.Background(), srv.URL+"/loop"); err == nil || !strings.Contains(err.Error(), "10 redirects") {
		t.Fatalf("redirect loop error = %v", err)
	}
	if _, err := Feed(context.Background(), srv.URL+"/short"); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("short response error = %v, want unexpected EOF", err)
	}
	srv.Close()
	if _, err := Feed(context.Background(), srv.URL); err == nil || !strings.Contains(err.Error(), "fetching feed") {
		t.Fatalf("closed server error = %v", err)
	}
}

func TestFeedContextCancellation(t *testing.T) {
	for _, stage := range []string{"before request", "waiting for headers", "reading body"} {
		t.Run(stage, func(t *testing.T) {
			started := make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if stage == "reading body" {
					io.WriteString(w, `<rss><channel>`)
					w.(http.Flusher).Flush()
				}
				close(started)
				<-r.Context().Done()
			}))
			defer srv.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if stage == "before request" {
				cancel()
			}
			result := make(chan error, 1)
			go func() {
				_, err := Feed(ctx, srv.URL)
				result <- err
			}()
			if stage != "before request" {
				select {
				case <-started:
				case <-time.After(5 * time.Second):
					t.Fatal("request did not start")
				}
				cancel()
			}
			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("error = %v, want context.Canceled", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Feed did not honor cancellation")
			}
		})
	}
}

func TestFeedLimitAndOrder(t *testing.T) {
	var body strings.Builder
	body.WriteString(`<rss><channel><title>Show</title>`)
	for i := 305; i > 0; i-- {
		body.WriteString(`<item><title>Skip</title></item><item><enclosure url="file:///skip.mp3" type="audio/mpeg"/></item>`)
		fmt.Fprintf(&body, `<item><title>Episode %d</title><enclosure url="https://example.com/%d.mp3"/></item>`, i, i)
	}
	body.WriteString(`</channel></rss>`)
	tracks, err := Feed(context.Background(), serveFeed(t, body.String()))
	if err != nil || len(tracks) != 300 {
		t.Fatalf("got %d tracks, err = %v; want 300", len(tracks), err)
	}
	for i, track := range tracks {
		if track.Title != fmt.Sprintf("Episode %d", 305-i) || track.Path != fmt.Sprintf("https://example.com/%d.mp3", 305-i) {
			t.Errorf("track %d = %+v, want episode %d", i, track, 305-i)
		}
	}
}

func TestFeedStopsAfterLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `<rss><channel>`)
		for range 300 {
			io.WriteString(w, `<item><enclosure url="https://example.com/audio.mp3"/></item>`)
		}
		io.WriteString(w, `</malformed-tail>`)
		w.(http.Flusher).Flush()
		// Do not finish the response: a whole-document read would hang here.
		<-r.Context().Done()
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tracks, err := Feed(ctx, srv.URL)
	if err != nil || len(tracks) != 300 {
		t.Fatalf("got %d tracks, err = %v; want 300 without reading the tail", len(tracks), err)
	}
}

func TestFeedBodyLimit(t *testing.T) {
	const prefix = `<rss><channel><item><enclosure url="https://example.com/audio.mp3"/></item><description>`
	const suffix = `</description></channel></rss>`
	for _, size := range []int{maxFeedBody, maxFeedBody + 1} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Length", strconv.Itoa(size))
				io.WriteString(w, prefix)
				chunk := strings.Repeat(" ", 8192)
				for remaining := size - len(prefix) - len(suffix); remaining > 0; {
					n := min(remaining, len(chunk))
					if _, err := io.WriteString(w, chunk[:n]); err != nil {
						return
					}
					remaining -= n
				}
				io.WriteString(w, suffix)
			}))
			defer srv.Close()
			tracks, err := Feed(context.Background(), srv.URL)
			if size == maxFeedBody {
				if err != nil || len(tracks) != 1 {
					t.Fatalf("exactly 32 MiB: got %d tracks, err = %v", len(tracks), err)
				}
			} else if err == nil || tracks != nil {
				t.Fatalf("oversized feed: tracks = %+v, err = %v; want nil tracks and error", tracks, err)
			}
		})
	}
}

func serveFeed(t *testing.T, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/feed"
}
