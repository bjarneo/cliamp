package lyrics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/bjarneo/cliamp/internal/httpclient"
)

// ErrNotFound is returned when no lyrics could be found from any source.
var ErrNotFound = errors.New("no lyrics found")

// Line represents a single timestamped lyrical line.
type Line struct {
	Start time.Duration
	Text  string
}

// httpClient is reused across all lyrics API calls.
var httpClient = httpclient.NewAPI(10 * time.Second)

// The base URLs of the LRCLIB and NetEase APIs. Tests point them at a local
// server.
var (
	lrclibBaseURL  = "https://lrclib.net"
	neteaseBaseURL = "http://music.163.com"
)

// sourceTimeout limits each Source that Lookup calls.
const sourceTimeout = 10 * time.Second

// maxResponseBody limits API responses to 2 MB.
const maxResponseBody = 2 << 20

type lrcResponse struct {
	SyncedLyrics string `json:"syncedLyrics"`
	PlainLyrics  string `json:"plainLyrics"`
}

type ncmSearchResponse struct {
	Result struct {
		Songs []struct {
			Id int `json:"id"`
		} `json:"songs"`
	} `json:"result"`
}

type ncmLyricResponse struct {
	Lrc struct {
		Lyric string `json:"lyric"`
	} `json:"lrc"`
}

var lrcRegex = regexp.MustCompile(`\[(\d{2,}):(\d{2})\.(\d{2,3})\](.*)`)

// cleanQuery strips noise from a search query: bracketed text like "[Official Video]",
// parenthesized text like "(Lyric Video)", and common video/audio label suffixes.
//
// The label words are only stripped when they form a genuine trailing label
// (after a dash, or as an "official video/audio" / "lyric(s) video" phrase),
// never as a bare substring. Otherwise legitimate titles like "Videotape",
// "Audioslave", or "Video Games" would be erased.
var noiseRegex = regexp.MustCompile(`(?i)(?:` +
	`\[.*?\]` + // [Official Video]
	`|\(.*?\)` + // (Lyric Video)
	`|\s*-\s*(?:official|lyric|audio|video).*` + // - Official Video
	`|\s+official(?:\s+music)?\s+(?:video|audio).*` + // Official Music Video
	`|\s+lyrics?\s+video.*` + // Lyric Video / Lyrics Video
	`)`)

func cleanQuery(str string) string {
	s := noiseRegex.ReplaceAllString(str, "")
	return strings.TrimSpace(s)
}

// Source finds the lyrics of one track in one place, such as the API of the
// provider that owns the track. It returns ErrNotFound when it has none.
type Source func(ctx context.Context) ([]Line, error)

// Lookup returns the lyrics of a track. It tries the embedded lyrics first,
// then each source in order, then LRCLIB and NetEase with artist and title.
// A source that fails is skipped. When no step finds lyrics, Lookup returns
// the last LRCLIB or NetEase error, so a network failure does not read as
// ErrNotFound. When both answer that they have none, it returns ErrNotFound.
func Lookup(ctx context.Context, embedded, artist, title string, sources ...Source) ([]Line, error) {
	if lines := ParseEmbedded(embedded); len(lines) > 0 {
		return lines, nil
	}
	for _, source := range sources {
		sourceCtx, cancel := context.WithTimeout(ctx, sourceTimeout)
		lines, err := source(sourceCtx)
		cancel()
		if err == nil && len(lines) > 0 {
			return lines, nil
		}
	}
	return fetch(ctx, artist, title)
}

// fetch requests lyrics for the given artist and title.
// It tries LRCLIB first, then falls back to NetEase Cloud Music.
//
// For YouTube/SoundCloud tracks where Artist is the uploader and Title
// contains "Artist - Song", the title is split to build a better query.
func fetch(ctx context.Context, artist, title string) ([]Line, error) {
	if artist == "" && title == "" {
		return nil, ErrNotFound
	}

	// YouTube titles often embed the real artist: "Artist - Song (Official Video)".
	// If the title contains " - ", prefer that split over the uploader name.
	if a, t, ok := strings.Cut(title, " - "); ok {
		a = cleanQuery(strings.TrimSpace(a))
		t = cleanQuery(strings.TrimSpace(t))
		if a != "" && t != "" {
			artist = a
			title = t
		}
	}

	query := cleanQuery(artist) + " " + cleanQuery(title)
	query = strings.TrimSpace(query)
	if query == "" {
		query = artist + " " + title
	}

	var lastErr error
	for _, source := range []func(context.Context, string) ([]Line, error){fetchLRCLIB, fetchNetEase} {
		lines, err := source(ctx, query)
		if err == nil && len(lines) > 0 {
			return lines, nil
		}
		if err != nil && !errors.Is(err, ErrNotFound) {
			lastErr = err
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, ErrNotFound
}

// ParseEmbedded converts lyrics read from local file tags into display lines.
// Timestamped LRC data remains synced; plain text is returned as scrollable
// lines at timestamp 0.
func ParseEmbedded(data string) []Line {
	data = strings.TrimSpace(data)
	if data == "" {
		return nil
	}
	lines := parseLRC(data)
	if len(lines) > 0 {
		return lines
	}
	for raw := range strings.SplitSeq(data, "\n") {
		lines = append(lines, Line{Start: 0, Text: strings.TrimSpace(raw)})
	}
	return lines
}

func fetchLRCLIB(ctx context.Context, query string) ([]Line, error) {
	searchURL := lrclibBaseURL + "/api/search?q=" + url.QueryEscape(query)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("lrclib: %s", resp.Status)
	}

	var results []lrcResponse
	if err := httpclient.ReadJSON(resp.Body, maxResponseBody, &results); err != nil {
		return nil, err
	}

	if len(results) == 0 {
		return nil, ErrNotFound
	}

	// Prefer synced lyrics.
	for _, r := range results {
		if r.SyncedLyrics != "" {
			return parseLRC(r.SyncedLyrics), nil
		}
	}

	// Fallback to plain lyrics (all lines at timestamp 0).
	if results[0].PlainLyrics != "" {
		var lines []Line
		for raw := range strings.SplitSeq(results[0].PlainLyrics, "\n") {
			lines = append(lines, Line{Start: 0, Text: strings.TrimSpace(raw)})
		}
		return lines, nil
	}

	return nil, ErrNotFound
}

func fetchNetEase(ctx context.Context, query string) ([]Line, error) {
	data := url.Values{}
	data.Set("s", query)
	data.Set("type", "1")
	data.Set("limit", "1")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, neteaseBaseURL+"/api/search/get/web", strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", "http://music.163.com")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("netease: %s", resp.Status)
	}

	var searchRes ncmSearchResponse
	if err := httpclient.ReadJSON(resp.Body, maxResponseBody, &searchRes); err != nil {
		// NetEase answers some searches with a result that is not a song
		// list, such as a string. That reply has no song for the query.
		var typeErr *json.UnmarshalTypeError
		if errors.As(err, &typeErr) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	if len(searchRes.Result.Songs) == 0 {
		return nil, ErrNotFound
	}

	songID := searchRes.Result.Songs[0].Id
	lyricURL := fmt.Sprintf("%s/api/song/lyric?id=%d&lv=1&kv=1&tv=-1", neteaseBaseURL, songID)

	lreq, err := http.NewRequestWithContext(ctx, http.MethodGet, lyricURL, nil)
	if err != nil {
		return nil, err
	}
	lresp, err := httpClient.Do(lreq)
	if err != nil {
		return nil, err
	}
	defer lresp.Body.Close()

	if lresp.StatusCode != 200 {
		return nil, fmt.Errorf("netease: %s", lresp.Status)
	}

	var lyricRes ncmLyricResponse
	if err := httpclient.ReadJSON(lresp.Body, maxResponseBody, &lyricRes); err != nil {
		return nil, err
	}

	if lyricRes.Lrc.Lyric == "" {
		return nil, ErrNotFound
	}

	return parseLRC(lyricRes.Lrc.Lyric), nil
}

// parseLRC converts standard LRC string blocks into a slice of timestamped Lines.
func parseLRC(data string) []Line {
	var lines []Line
	for raw := range strings.SplitSeq(data, "\n") {
		matches := lrcRegex.FindStringSubmatch(raw)
		if len(matches) == 5 {
			mins, _ := strconv.Atoi(matches[1])
			secs, _ := strconv.Atoi(matches[2])
			ms, _ := strconv.Atoi(matches[3])

			// If LRC millisecond part is hundredths (2 chars), scale it.
			if len(matches[3]) == 2 {
				ms *= 10
			}

			start := time.Duration(mins)*time.Minute + time.Duration(secs)*time.Second + time.Duration(ms)*time.Millisecond
			text := strings.TrimSpace(matches[4])
			lines = append(lines, Line{Start: start, Text: text})
		}
	}
	return lines
}
