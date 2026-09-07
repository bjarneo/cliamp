package spotify

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/bjarneo/cliamp/lyrics"
	"github.com/bjarneo/cliamp/playlist"
)

// maxLyricsBody limits color-lyrics responses to 2 MB.
const maxLyricsBody = 2 << 20

// TrackIDFromPath extracts the Spotify track ID from a track path of the
// form "spotify:track:<id>". It returns "" for anything else.
func TrackIDFromPath(path string) string {
	const prefix = "spotify:track:"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	id := strings.TrimPrefix(path, prefix)
	if id == "" {
		return ""
	}
	return id
}

// TrackLyrics fetches synced lyrics for a Spotify track using Spotify's
// internal color-lyrics endpoint (the one powering the web player's lyrics
// view). This endpoint is undocumented and may change without notice; callers
// must treat failures as a signal to fall back to other lyric sources.
func (p *SpotifyProvider) TrackLyrics(ctx context.Context, trackID string) ([]lyrics.Line, error) {
	p.mu.Lock()
	sess := p.session
	p.mu.Unlock()
	if sess == nil {
		return nil, fmt.Errorf("spotify: not signed in: %w", playlist.ErrNeedsAuth)
	}
	return sess.trackLyrics(ctx, trackID)
}

func (s *Session) trackLyrics(ctx context.Context, trackID string) ([]lyrics.Line, error) {
	if trackID == "" {
		return nil, lyrics.ErrNotFound
	}

	s.mu.RLock()
	ts := s.tokenSource
	s.mu.RUnlock()
	if ts == nil {
		return nil, fmt.Errorf("spotify: web api token unavailable, run 'cliamp spotify reset' and sign in again: %w", playlist.ErrNeedsAuth)
	}
	tok, err := ts.Token()
	if err != nil {
		return nil, fmt.Errorf("refresh access token: %w", err)
	}

	u := "https://spclient.wg.spotify.com/color-lyrics/v2/track/" + url.PathEscape(trackID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u+"?format=json&market=from_token", nil)
	if err != nil {
		return nil, err
	}
	// The color-lyrics endpoint rejects tokens without this platform marker.
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	req.Header.Set("app-platform", "WebPlayer")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("spotify lyrics request: %w", err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, lyrics.ErrNotFound
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("spotify lyrics: unexpected status %d", resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxLyricsBody))
	if err != nil {
		return nil, fmt.Errorf("read spotify lyrics: %w", err)
	}
	return parseColorLyrics(data)
}

type colorLyricsResponse struct {
	Lyrics struct {
		SyncType string `json:"syncType"`
		Lines    []struct {
			StartTimeMs string `json:"startTimeMs"`
			Words       string `json:"words"`
		} `json:"lines"`
	} `json:"lyrics"`
}

// parseColorLyrics decodes a color-lyrics v2 payload into lyric lines.
// Unsynced payloads carry empty startTimeMs values and yield zero starts,
// which the UI renders in scroll mode like plain lyrics.
func parseColorLyrics(data []byte) ([]lyrics.Line, error) {
	var parsed colorLyricsResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("decode spotify lyrics: %w", err)
	}
	raw := parsed.Lyrics.Lines
	if len(raw) == 0 {
		return nil, lyrics.ErrNotFound
	}
	lines := make([]lyrics.Line, 0, len(raw))
	for _, l := range raw {
		var start time.Duration
		if ms, err := strconv.Atoi(strings.TrimSpace(l.StartTimeMs)); err == nil && ms > 0 {
			start = time.Duration(ms) * time.Millisecond
		}
		lines = append(lines, lyrics.Line{Start: start, Text: l.Words})
	}
	return lines, nil
}
