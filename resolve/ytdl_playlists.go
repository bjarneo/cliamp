package resolve

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"strings"
	"time"

	"github.com/bjarneo/cliamp/internal/ytdlcookies"
	"github.com/bjarneo/cliamp/playlist"
)

// ytdlPlaylistFeedEntry holds JSON fields for a single playlist item returned
// by yt-dlp --flat-playlist on a feed/playlist URL.
type ytdlPlaylistFeedEntry struct {
	ID         string `json:"id"`
	URL        string `json:"url"`
	WebpageURL string `json:"webpage_url"`
	Title      string `json:"title"`
	Type       string `json:"_type"`
}

// parseYTDLPlaylistFeed parses newline-delimited JSON output from yt-dlp into
// a slice of playlist.PlaylistInfo entries.
func parseYTDLPlaylistFeed(r io.Reader) ([]playlist.PlaylistInfo, error) {
	var playlists []playlist.PlaylistInfo
	seen := make(map[string]bool)

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, scannerInitBufSize), scannerMaxLineSize)

	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}

		var entry ytdlPlaylistFeedEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			continue
		}

		id := strings.TrimSpace(entry.ID)
		if id == "" {
			for _, uStr := range []string{entry.WebpageURL, entry.URL} {
				if uStr == "" {
					continue
				}
				if u, err := url.Parse(uStr); err == nil && u.Query().Get("list") != "" {
					id = u.Query().Get("list")
					break
				}
				if !strings.HasPrefix(uStr, "http://") && !strings.HasPrefix(uStr, "https://") {
					id = uStr
					break
				}
			}
		}

		// YouTube feed responses often prepend "VL" ("View List") to playlist IDs (e.g. VLPL..., VLLM, VLLL).
		if strings.HasPrefix(id, "VL") && len(id) > 2 {
			id = strings.TrimPrefix(id, "VL")
		}

		if id == "" || seen[id] {
			continue
		}
		seen[id] = true

		title := strings.TrimSpace(entry.Title)
		if title == "" {
			title = id
		}

		playlists = append(playlists, playlist.PlaylistInfo{
			ID:   id,
			Name: title,
		})
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("parse playlist feed: %w", err)
	}
	return playlists, nil
}

// FetchUserPlaylists invokes yt-dlp to scrape user playlists from
// https://www.youtube.com/feed/playlists using the specified browser session.
// If browser is empty, the cookie source configured for YouTube is used.
func FetchUserPlaylists(browser string) ([]playlist.PlaylistInfo, error) {
	if _, err := exec.LookPath("yt-dlp"); err != nil {
		return nil, fmt.Errorf("yt-dlp not found in PATH — see https://github.com/yt-dlp/yt-dlp#installation")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	args := []string{"--flat-playlist", "-j", "--socket-timeout", "15"}
	b := strings.TrimSpace(browser)
	if b == "" {
		b = ytdlcookies.ForURL("https://www.youtube.com/feed/playlists")
	}
	if b != "" {
		args = append(args, "--cookies-from-browser", b)
	}
	args = append(args, "https://www.youtube.com/feed/playlists")

	cmd := exec.CommandContext(ctx, "yt-dlp", args...)
	cmd.WaitDelay = 3 * time.Second
	var stderr strings.Builder
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("yt-dlp: timed out fetching playlists (30s)")
		}
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return nil, fmt.Errorf("yt-dlp: %s", msg)
		}
		return nil, fmt.Errorf("yt-dlp: %w", err)
	}

	pls, err := parseYTDLPlaylistFeed(bytes.NewReader(stdout))
	if err != nil {
		return nil, fmt.Errorf("yt-dlp: %w", err)
	}
	return pls, nil
}
