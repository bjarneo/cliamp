package lyrics

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ErrNotFound is returned when no lyrics could be found from any source.
var ErrNotFound = errors.New("no lyrics found")

// Line represents a single timestamped lyrical line.
type Line struct {
	Start time.Duration
	Text  string
}

// httpClient is reused across all lyrics API calls.
var httpClient = &http.Client{Timeout: 10 * time.Second}

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
var noiseRegex = regexp.MustCompile(`(?i)(?:\[.*?\]|\(.*?\)|-?\s*(?:official|lyric|audio|video).*)`)

func cleanQuery(str string) string {
	s := noiseRegex.ReplaceAllString(str, "")
	return strings.TrimSpace(s)
}

// Fetch requests lyrics for the given artist and title.
// It tries LRCLIB first, then falls back to NetEase Cloud Music.
// Returns ErrNotFound if neither source has lyrics.
//
// For YouTube/SoundCloud tracks where Artist is the uploader and Title
// contains "Artist - Song", the title is split to build a better query.
func Fetch(artist, title string) ([]Line, error) {
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

	// Try LRCLIB first.
	lines, err := fetchLRCLIB(query)
	if err == nil && len(lines) > 0 {
		return lines, nil
	}

	// Fallback to NetEase Cloud Music.
	lines, err = fetchNetEase(query)
	if err == nil && len(lines) > 0 {
		return lines, nil
	}

	return nil, ErrNotFound
}

func fetchLRCLIB(query string) ([]Line, error) {
	searchURL := fmt.Sprintf("https://lrclib.net/api/search?q=%s", url.QueryEscape(query))

	req, err := http.NewRequest("GET", searchURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "cliamp")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("lrclib: %s", resp.Status)
	}

	var results []lrcResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBody)).Decode(&results); err != nil {
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

func fetchNetEase(query string) ([]Line, error) {
	data := url.Values{}
	data.Set("s", query)
	data.Set("type", "1")
	data.Set("limit", "1")

	req, err := http.NewRequest("POST", "http://music.163.com/api/search/get/web", strings.NewReader(data.Encode()))
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
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBody)).Decode(&searchRes); err != nil {
		return nil, err
	}

	if len(searchRes.Result.Songs) == 0 {
		return nil, ErrNotFound
	}

	songID := searchRes.Result.Songs[0].Id
	lyricURL := fmt.Sprintf("http://music.163.com/api/song/lyric?id=%d&lv=1&kv=1&tv=-1", songID)

	lresp, err := httpClient.Get(lyricURL)
	if err != nil {
		return nil, err
	}
	defer lresp.Body.Close()

	var lyricRes ncmLyricResponse
	if err := json.NewDecoder(io.LimitReader(lresp.Body, maxResponseBody)).Decode(&lyricRes); err != nil {
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
