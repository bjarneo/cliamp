package resolve

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/bjarneo/cliamp/playlist"
)

var (
	scClientIDMu        sync.Mutex
	scClientID          string
	scRejectedClientIDs = make(map[string]struct{})
	scAPIBase           = "https://api-v2.soundcloud.com"
	scHomepageURL       = "https://soundcloud.com/"
)

var (
	scScriptSrcRe = regexp.MustCompile(`<script[^>]+src="([^"]+)"`)
	scClientIDRe  = regexp.MustCompile(`client_id\s*:\s*"([0-9a-zA-Z]{32})"`)
	scTrackIDRe   = regexp.MustCompile(`api-v2\.soundcloud\.com/tracks/(\d+)`)
)

// soundCloudClientID returns a cached or newly fetched client ID for SoundCloud's v2 API.
func soundCloudClientID(ctx context.Context) string {
	scClientIDMu.Lock()
	clientID := scClientID
	_, rejected := scRejectedClientIDs[clientID]
	scClientIDMu.Unlock()
	if clientID != "" && !rejected {
		return clientID
	}

	// 1. Try reading from yt-dlp cache if available
	if home, err := os.UserHomeDir(); err == nil {
		cachePath := filepath.Join(home, ".cache", "yt-dlp", "soundcloud", "client_id.json")
		if data, err := os.ReadFile(cachePath); err == nil {
			var cached struct {
				Data string `json:"data"`
			}
			if err := json.Unmarshal(data, &cached); err == nil && cached.Data != "" {
				if clientID = rememberSoundCloudClientID(cached.Data); clientID != "" {
					return clientID
				}
			}
		}
	}

	// 2. Fetch from soundcloud.com directly
	req, err := http.NewRequestWithContext(ctx, "GET", scHomepageURL, nil)
	if err != nil {
		return ""
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return ""
	}

	matches := scScriptSrcRe.FindAllStringSubmatch(string(body), -1)
	// Iterate in reverse: client_id is typically defined in the last bundle scripts
	for i := len(matches) - 1; i >= 0; i-- {
		scriptURL := matches[i][1]
		sReq, err := http.NewRequestWithContext(ctx, "GET", scriptURL, nil)
		if err != nil {
			continue
		}
		sResp, err := httpClient.Do(sReq)
		if err != nil {
			continue
		}
		sBody, _ := io.ReadAll(io.LimitReader(sResp.Body, 2*1024*1024))
		sResp.Body.Close()

		if m := scClientIDRe.FindStringSubmatch(string(sBody)); len(m) > 1 {
			return rememberSoundCloudClientID(m[1])
		}
	}
	return ""
}

// rememberSoundCloudClientID publishes a discovered ID unless SoundCloud has
// already rejected it. The mutex protects state only; discovery I/O stays
// outside it so each caller remains cancellable through its own context.
func rememberSoundCloudClientID(clientID string) string {
	if clientID == "" {
		return ""
	}
	scClientIDMu.Lock()
	defer scClientIDMu.Unlock()
	if _, rejected := scRejectedClientIDs[clientID]; rejected {
		return ""
	}
	if scClientID != "" {
		if _, rejected := scRejectedClientIDs[scClientID]; !rejected {
			return scClientID
		}
	}
	scClientID = clientID
	return scClientID
}

// rejectSoundCloudClientID removes an ID from memory and marks it so the
// yt-dlp disk cache cannot return it again after an authentication failure.
func rejectSoundCloudClientID(clientID string) {
	if clientID == "" {
		return
	}
	scClientIDMu.Lock()
	scRejectedClientIDs[clientID] = struct{}{}
	if scClientID == clientID {
		scClientID = ""
	}
	scClientIDMu.Unlock()
}

type scTrackInfo struct {
	ID           int64  `json:"id"`
	Title        string `json:"title"`
	PermalinkURL string `json:"permalink_url"`
	Duration     int64  `json:"duration"` // milliseconds
	User         struct {
		Username string `json:"username"`
	} `json:"user"`
}

// enrichSoundCloudTracks hydrates track metadata for SoundCloud entries whose
// info was omitted in yt-dlp flat-playlist mode (represented as api-v2 track IDs).
func enrichSoundCloudTracks(ctx context.Context, tracks []playlist.Track) {
	type pendingTrack struct {
		trackIdx int
		trackID  string
	}
	var pending []pendingTrack

	for i, t := range tracks {
		if m := scTrackIDRe.FindStringSubmatch(t.Path); len(m) > 1 {
			pending = append(pending, pendingTrack{trackIdx: i, trackID: m[1]})
		}
	}

	if len(pending) == 0 {
		return
	}

	cid := soundCloudClientID(ctx)
	if cid == "" {
		return
	}

	const chunkSize = 50
	for i := 0; i < len(pending); i += chunkSize {
		if ctx.Err() != nil {
			return
		}
		if cid == "" {
			return
		}
		end := i + chunkSize
		if end > len(pending) {
			end = len(pending)
		}
		chunk := pending[i:end]
		ids := make([]string, len(chunk))
		idToPending := make(map[string]int, len(chunk))
		for j, p := range chunk {
			ids[j] = p.trackID
			idToPending[p.trackID] = p.trackIdx
		}

		var list []scTrackInfo
		for attempt := 0; attempt < 2; attempt++ {
			apiURL := fmt.Sprintf("%s/tracks?ids=%s&client_id=%s", scAPIBase, strings.Join(ids, ","), cid)
			req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
			if err != nil {
				break
			}
			resp, err := httpClient.Do(req)
			if err != nil {
				break
			}
			if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
				resp.Body.Close()
				rejectSoundCloudClientID(cid)
				cid = ""
				if attempt == 0 {
					cid = soundCloudClientID(ctx)
					if cid != "" {
						continue
					}
				}
				break
			}
			if resp.StatusCode != http.StatusOK {
				resp.Body.Close()
				break
			}
			err = json.NewDecoder(resp.Body).Decode(&list)
			resp.Body.Close()
			if err != nil {
				list = nil
			}
			break
		}
		if list == nil {
			continue
		}

		for _, item := range list {
			strID := fmt.Sprintf("%d", item.ID)
			if idx, ok := idToPending[strID]; ok && idx >= 0 && idx < len(tracks) {
				if item.Title != "" {
					tracks[idx].Title = item.Title
				}
				if item.User.Username != "" {
					tracks[idx].Artist = item.User.Username
				}
				if item.PermalinkURL != "" {
					tracks[idx].Path = item.PermalinkURL
				}
				if item.Duration > 0 {
					tracks[idx].DurationSecs = int(item.Duration / 1000)
				}
			}
		}
	}
}
