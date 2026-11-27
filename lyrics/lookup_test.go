package lyrics

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/internal/httpclient"
)

// lyricsAPI holds the replies of a fake LRCLIB and a fake NetEase. A nil
// handler answers 404.
type lyricsAPI struct {
	lrclib http.HandlerFunc // /api/search on LRCLIB
	search http.HandlerFunc // /api/search/get/web on NetEase
	lyric  http.HandlerFunc // /api/song/lyric on NetEase
}

// apiLog records the requests that reach the fake APIs.
type apiLog struct {
	mu        sync.Mutex
	paths     []string
	query     string
	userAgent string
}

func (l *apiLog) record(r *http.Request) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.paths = append(l.paths, r.URL.Path)
	if r.URL.Path == "/api/search" {
		l.query = r.URL.Query().Get("q")
		l.userAgent = r.Header.Get("User-Agent")
	}
}

// serveLyrics starts the fake APIs and points the base URLs at them. With
// down set, both base URLs point at a closed server.
func serveLyrics(t *testing.T, api lyricsAPI, down bool) *apiLog {
	t.Helper()
	log := &apiLog{}
	handle := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			log.record(r)
			if h == nil {
				http.NotFound(w, r)
				return
			}
			h(w, r)
		}
	}
	lrclib := http.NewServeMux()
	lrclib.HandleFunc("/api/search", handle(api.lrclib))
	netease := http.NewServeMux()
	netease.HandleFunc("/api/search/get/web", handle(api.search))
	netease.HandleFunc("/api/song/lyric", handle(api.lyric))
	lrclibSrv := httptest.NewServer(lrclib)
	neteaseSrv := httptest.NewServer(netease)
	t.Cleanup(lrclibSrv.Close)
	t.Cleanup(neteaseSrv.Close)

	oldLRCLIB, oldNetEase := lrclibBaseURL, neteaseBaseURL
	t.Cleanup(func() { lrclibBaseURL, neteaseBaseURL = oldLRCLIB, oldNetEase })
	lrclibBaseURL, neteaseBaseURL = lrclibSrv.URL, neteaseSrv.URL
	if down {
		closed := httptest.NewServer(http.NotFoundHandler())
		closed.Close()
		lrclibBaseURL, neteaseBaseURL = closed.URL, closed.URL
	}
	return log
}

func reply(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
}

func status(code int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, http.StatusText(code), code)
	}
}

func source(lines []Line, err error) Source {
	return func(context.Context) ([]Line, error) { return lines, err }
}

func TestLookup(t *testing.T) {
	sourceLine := []Line{{Start: time.Second, Text: "from the source"}}
	neteaseSongs := reply(`{"result":{"songs":[{"id":42}]}}`)
	tests := []struct {
		name          string
		embedded      string
		artist, title string
		sources       []Source
		api           lyricsAPI
		down          bool
		want          []Line
		wantNotFound  bool // the error is ErrNotFound
		wantErr       bool // the error is some other error
		wantErrIs     error
		noRequests    bool // no request may reach the APIs
		wantPaths     []string
		wantQuery     string
	}{
		{
			name:     "embedded synced lyrics come first",
			embedded: "[00:05.00]Embedded",
			artist:   "Artist", title: "Song",
			sources:    []Source{source(sourceLine, nil)},
			want:       []Line{{Start: 5 * time.Second, Text: "Embedded"}},
			noRequests: true,
		},
		{
			name:     "embedded plain lyrics come first",
			embedded: "One\nTwo",
			artist:   "Artist", title: "Song",
			want:       []Line{{Text: "One"}, {Text: "Two"}},
			noRequests: true,
		},
		{
			name:   "a source comes before the network",
			artist: "Artist", title: "Song",
			sources:    []Source{source(nil, ErrNotFound), source(sourceLine, nil)},
			want:       sourceLine,
			noRequests: true,
		},
		{
			name:   "a failed source is skipped",
			artist: "Artist", title: "Song",
			sources:   []Source{source(nil, errors.New("signed out"))},
			api:       lyricsAPI{lrclib: reply(`[{"syncedLyrics":"[00:05.00]Hello","plainLyrics":"Plain text"}]`)},
			want:      []Line{{Start: 5 * time.Second, Text: "Hello"}},
			wantPaths: []string{"/api/search"},
		},
		{
			name:   "LRCLIB plain lyrics when no synced lyrics",
			artist: "Artist", title: "Song",
			api:  lyricsAPI{lrclib: reply(`[{"syncedLyrics":"","plainLyrics":"Line one\nLine two"}]`)},
			want: []Line{{Text: "Line one"}, {Text: "Line two"}},
		},
		{
			name:   "LRCLIB skips unparseable synced lyrics to use later synced result",
			artist: "Artist", title: "Song",
			api: lyricsAPI{lrclib: reply(`[
				{"syncedLyrics":"[ti:Only metadata]\nno timestamps","plainLyrics":"Ignored plain"},
				{"syncedLyrics":"[00:08.00]Second synced","plainLyrics":""}
			]`)},
			want: []Line{{Start: 8 * time.Second, Text: "Second synced"}},
		},
		{
			name:   "LRCLIB scans all results for plain lyrics fallback",
			artist: "Artist", title: "Song",
			api: lyricsAPI{lrclib: reply(`[
				{"syncedLyrics":"not lrc","plainLyrics":"   "},
				{"syncedLyrics":"","plainLyrics":"Later line one\nLater line two"}
			]`)},
			want: []Line{{Text: "Later line one"}, {Text: "Later line two"}},
		},
		{
			name:   "NetEase runs when LRCLIB synced lyrics have no timestamps and no plain fallback",
			artist: "Artist", title: "Song",
			api: lyricsAPI{
				lrclib: reply(`[{"syncedLyrics":"[ar:Artist]","plainLyrics":""}]`),
				search: neteaseSongs,
				lyric:  reply(`{"lrc":{"lyric":"[00:12.00]NetEase fallback"}}`),
			},
			want:      []Line{{Start: 12 * time.Second, Text: "NetEase fallback"}},
			wantPaths: []string{"/api/search", "/api/search/get/web", "/api/song/lyric"},
		},
		{
			name:   "NetEase after an empty LRCLIB answer",
			artist: "Artist", title: "Song",
			api: lyricsAPI{
				lrclib: reply(`[]`),
				search: neteaseSongs,
				lyric:  reply(`{"lrc":{"lyric":"[00:10.00]From NetEase"}}`),
			},
			want:      []Line{{Start: 10 * time.Second, Text: "From NetEase"}},
			wantPaths: []string{"/api/search", "/api/search/get/web", "/api/song/lyric"},
		},
		{
			name:   "NetEase after an LRCLIB error",
			artist: "Artist", title: "Song",
			api: lyricsAPI{
				lrclib: status(http.StatusInternalServerError),
				search: neteaseSongs,
				lyric:  reply(`{"lrc":{"lyric":"[00:10.00]From NetEase"}}`),
			},
			want: []Line{{Start: 10 * time.Second, Text: "From NetEase"}},
		},
		{
			name:   "both sources have none",
			artist: "Artist", title: "Song",
			api:          lyricsAPI{lrclib: reply(`[]`), search: reply(`{"result":{"songs":[]}}`)},
			wantNotFound: true,
		},
		{
			name:   "a NetEase search result that is not an object has no songs",
			artist: "Artist", title: "Song",
			api:          lyricsAPI{lrclib: reply(`[]`), search: reply(`{"result":"35b1748964af"}`)},
			wantNotFound: true,
			wantPaths:    []string{"/api/search", "/api/search/get/web"},
		},
		{
			name:   "a NetEase song id that is not a number has no songs",
			artist: "Artist", title: "Song",
			api:          lyricsAPI{lrclib: reply(`[]`), search: reply(`{"result":{"songs":[{"id":"42"}]}}`)},
			wantNotFound: true,
			wantPaths:    []string{"/api/search", "/api/search/get/web"},
		},
		{
			name:   "a NetEase search reply that is not JSON is an error",
			artist: "Artist", title: "Song",
			api:     lyricsAPI{lrclib: reply(`[]`), search: reply(`<html>`)},
			wantErr: true,
		},
		{
			name:   "an LRCLIB error is not a missing lyric",
			artist: "Artist", title: "Song",
			api:     lyricsAPI{lrclib: status(http.StatusInternalServerError), search: reply(`{"result":{"songs":[]}}`)},
			wantErr: true,
		},
		{
			name:   "the NetEase lyric status is checked",
			artist: "Artist", title: "Song",
			api:     lyricsAPI{lrclib: reply(`[]`), search: neteaseSongs, lyric: status(http.StatusBadGateway)},
			wantErr: true,
		},
		{
			name:   "an oversized LRCLIB answer is an error",
			artist: "Artist", title: "Song",
			api: lyricsAPI{
				lrclib: reply(`[{"plainLyrics":"` + strings.Repeat("x", maxResponseBody) + `"}]`),
				search: reply(`{"result":{"songs":[]}}`),
			},
			wantErr:   true,
			wantErrIs: httpclient.ErrTooLarge,
		},
		{
			name:   "unreachable APIs return the network error",
			artist: "Artist", title: "Song",
			down:    true,
			wantErr: true,
		},
		{
			name:         "no artist and no title",
			wantNotFound: true,
			noRequests:   true,
		},
		{
			name:   "a title with a dash gives the query artist",
			artist: "Uploader Channel", title: "Real Artist - Real Song (Official Video)",
			api:       lyricsAPI{lrclib: reply(`[{"syncedLyrics":"[00:01.00]ok"}]`)},
			want:      []Line{{Start: time.Second, Text: "ok"}},
			wantQuery: "Real Artist Real Song",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log := serveLyrics(t, tt.api, tt.down)
			got, err := Lookup(context.Background(), tt.embedded, tt.artist, tt.title, tt.sources...)
			switch {
			case tt.wantNotFound:
				if !errors.Is(err, ErrNotFound) {
					t.Fatalf("err = %v, want ErrNotFound", err)
				}
			case tt.wantErr:
				if err == nil || errors.Is(err, ErrNotFound) {
					t.Fatalf("err = %v, want an error that is not ErrNotFound", err)
				}
				if tt.wantErrIs != nil && !errors.Is(err, tt.wantErrIs) {
					t.Fatalf("err = %v, want %v", err, tt.wantErrIs)
				}
			case err != nil:
				t.Fatalf("err = %v, want nil", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("lines = %+v, want %+v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("lines = %+v, want %+v", got, tt.want)
				}
			}
			log.mu.Lock()
			defer log.mu.Unlock()
			if tt.noRequests && len(log.paths) != 0 {
				t.Fatalf("requests = %v, want none", log.paths)
			}
			if tt.wantPaths != nil && strings.Join(log.paths, " ") != strings.Join(tt.wantPaths, " ") {
				t.Fatalf("requests = %v, want %v", log.paths, tt.wantPaths)
			}
			if tt.wantQuery != "" && log.query != tt.wantQuery {
				t.Fatalf("query = %q, want %q", log.query, tt.wantQuery)
			}
			if slices.Contains(log.paths, "/api/search") && log.userAgent != httpclient.UserAgent {
				t.Fatalf("LRCLIB User-Agent = %q, want %q", log.userAgent, httpclient.UserAgent)
			}
		})
	}
}

// Each source gets its own time limit, so a source that hangs does not
// stop the lookup.
func TestLookupLimitsEachSource(t *testing.T) {
	serveLyrics(t, lyricsAPI{lrclib: reply(`[{"syncedLyrics":"[00:01.00]ok"}]`)}, false)
	var deadline time.Time
	hang := func(ctx context.Context) ([]Line, error) {
		deadline, _ = ctx.Deadline()
		return nil, ctx.Err()
	}
	start := time.Now()
	lines, err := Lookup(context.Background(), "", "Artist", "Song", hang)
	if err != nil || len(lines) != 1 {
		t.Fatalf("Lookup = %+v, %v, want the LRCLIB line", lines, err)
	}
	if deadline.IsZero() || deadline.Sub(start) > sourceTimeout+time.Second {
		t.Fatalf("source deadline = %v after the start, want at most %v", deadline.Sub(start), sourceTimeout)
	}
}

// A canceled lookup stops and reports the cancel.
func TestLookupStopsWhenCanceled(t *testing.T) {
	tests := []struct {
		name           string
		cancelBefore   bool
		cancelInSource int // 1-based index of source that cancels ctx, 0 for none
		artist, title  string
		sourceCount    int
		wantCalls      int
	}{
		{
			name:         "canceled before sources skips all sources and fetch",
			cancelBefore: true,
			artist:       "Artist",
			title:        "Song",
			sourceCount:  2,
			wantCalls:    0,
		},
		{
			name:           "canceled in first source skips remaining sources and fetch",
			cancelInSource: 1,
			artist:         "Artist",
			title:          "Song",
			sourceCount:    2,
			wantCalls:      1,
		},
		{
			name:           "canceled in last source skips fetch even with empty metadata",
			cancelInSource: 1,
			artist:         "",
			title:          "",
			sourceCount:    1,
			wantCalls:      1,
		},
		{
			name:         "canceled with no sources skips fetch",
			cancelBefore: true,
			artist:       "Artist",
			title:        "Song",
			sourceCount:  0,
			wantCalls:    0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log := serveLyrics(t, lyricsAPI{lrclib: reply(`[{"syncedLyrics":"[00:01.00]ok"}]`)}, false)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tt.cancelBefore {
				cancel()
			}

			calls := 0
			sources := make([]Source, tt.sourceCount)
			for i := range tt.sourceCount {
				idx := i + 1
				sources[i] = func(context.Context) ([]Line, error) {
					calls++
					if idx == tt.cancelInSource {
						cancel()
					}
					return nil, ErrNotFound
				}
			}

			if _, err := Lookup(ctx, "", tt.artist, tt.title, sources...); !errors.Is(err, context.Canceled) {
				t.Fatalf("err = %v, want context.Canceled", err)
			}
			if calls != tt.wantCalls {
				t.Errorf("source calls = %d, want %d", calls, tt.wantCalls)
			}
			log.mu.Lock()
			gotPaths := slices.Clone(log.paths)
			log.mu.Unlock()
			if len(gotPaths) != 0 {
				t.Errorf("API requests = %v, want none", gotPaths)
			}
		})
	}
}

func TestNetEaseUsesHTTPSDefaultAndReferer(t *testing.T) {
	if neteaseBaseURL != "https://music.163.com" {
		t.Fatalf("neteaseBaseURL = %q, want https://music.163.com", neteaseBaseURL)
	}
	var gotReferer string
	serveLyrics(t, lyricsAPI{
		lrclib: reply(`[]`),
		search: func(w http.ResponseWriter, r *http.Request) {
			gotReferer = r.Header.Get("Referer")
			reply(`{"result":{"songs":[{"id":1}]}}`)(w, r)
		},
		lyric: reply(`{"lrc":{"lyric":"[00:01.00]ok"}}`),
	}, false)

	lines, err := Lookup(context.Background(), "", "Artist", "Song")
	if err != nil || len(lines) != 1 {
		t.Fatalf("Lookup() = %+v, %v", lines, err)
	}
	if gotReferer != "https://music.163.com" {
		t.Fatalf("Referer = %q, want https://music.163.com", gotReferer)
	}
}
