package lyrics

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Line represents a single timestamped lyrical line
type Line struct {
	Start time.Duration
	Text  string
}

type lrcResponse struct {
	SyncedLyrics string `json:"syncedLyrics"`
	PlainLyrics  string `json:"plainLyrics"`
}

var lrcRegex = regexp.MustCompile(`\[(\d{2,}):(\d{2})\.(\d{2,3})\](.*)`)
var noiseRegex = regexp.MustCompile(`(?i)(?:\[.*?\]|\(.*?\)|-?\s*(?:official|lyric|audio|video).*)`)

func clean(str string) string {
	s := noiseRegex.ReplaceAllString(str, "")
	return strings.TrimSpace(s)
}

// Fetch requests lyrics from lrclib.net for the given artist and title.
func Fetch(artist, title string) ([]Line, error) {
	if artist == "" || title == "" {
		return nil, fmt.Errorf("artist and title are required")
	}

	query := clean(artist) + " " + clean(title)
	query = strings.TrimSpace(query)
	if query == "" {
		query = artist + " " + title
	}

	searchURL := fmt.Sprintf("https://lrclib.net/api/search?q=%s", url.QueryEscape(query))

	req, err := http.NewRequest("GET", searchURL, nil)
	if err != nil {
		return nil, err
	}

	// LRCLIB politely asks callers to provide a User-Agent
	req.Header.Set("User-Agent", "cliamp")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("lrclib API error: %s", resp.Status)
	}

	var results []lrcResponse
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return nil, err
	}

	if len(results) == 0 {
		return nil, fmt.Errorf("no lyrics found online")
	}

	for _, best := range results {
		if best.SyncedLyrics != "" {
			return ParseLRC(best.SyncedLyrics), nil
		}
	}

	// Fallback to plain lyrics formatted as start-of-song, if synced isn't available
	best := results[0]
	if best.PlainLyrics != "" {
		var lines []Line
		for _, raw := range strings.Split(best.PlainLyrics, "\n") {
			lines = append(lines, Line{Start: 0, Text: strings.TrimSpace(raw)})
		}
		return lines, nil
	}

	return nil, fmt.Errorf("lyrics missing in response")
}

// ParseLRC converts standard LRC string blocks into a slice of timestamped Lines.
func ParseLRC(data string) []Line {
	var lines []Line
	for _, raw := range strings.Split(data, "\n") {
		matches := lrcRegex.FindStringSubmatch(raw)
		if len(matches) == 5 {
			mins, _ := strconv.Atoi(matches[1])
			secs, _ := strconv.Atoi(matches[2])
			ms, _ := strconv.Atoi(matches[3])

			// If LRC millisecond part is hundredths (2 chars), scale it
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
