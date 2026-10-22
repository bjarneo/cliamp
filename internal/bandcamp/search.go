// Package bandcamp searches Bandcamp for tracks. Bandcamp publishes no search
// API, so SearchTracks posts to the endpoint behind the search box of
// bandcamp.com, which can change without notice. The tracks keep their page
// URLs, and yt-dlp plays them.
package bandcamp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bjarneo/cliamp/internal/httpclient"
	"github.com/bjarneo/cliamp/playlist"
)

// searchURL is the endpoint of the bandcamp.com search box. Tests replace it.
var searchURL = "https://bandcamp.com/api/bcsearch_public_api/1/autocomplete_elastic"

var httpClient = httpclient.NewAPI(15 * time.Second)

// maxResponseBytes bounds a search response. Bandcamp sends at most 50
// results.
const maxResponseBytes = 2 << 20

type searchRequest struct {
	SearchText   string `json:"search_text"`
	SearchFilter string `json:"search_filter"` // "t" finds tracks
	FullPage     bool   `json:"full_page"`
	FanID        *int64 `json:"fan_id"`
}

type searchResponse struct {
	Auto struct {
		Results []searchResult `json:"results"`
	} `json:"auto"`
}

type searchResult struct {
	Name        string `json:"name"`
	BandName    string `json:"band_name"`
	AlbumName   string `json:"album_name"`
	ItemURLPath string `json:"item_url_path"`
}

// SearchTracks returns up to limit tracks that match query, or every match
// when limit is 0.
func SearchTracks(ctx context.Context, query string, limit int) ([]playlist.Track, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, nil
	}
	body, err := json.Marshal(searchRequest{SearchText: q, SearchFilter: "t", FullPage: true})
	if err != nil {
		return nil, fmt.Errorf("bandcamp: encode search: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, searchURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("bandcamp: create search request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bandcamp: search: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bandcamp: search: HTTP %d", resp.StatusCode)
	}
	var page searchResponse
	if err := httpclient.ReadJSON(resp.Body, maxResponseBytes, &page); err != nil {
		return nil, fmt.Errorf("bandcamp: decode search: %w", err)
	}

	var tracks []playlist.Track
	for _, r := range page.Auto.Results {
		if limit > 0 && len(tracks) == limit {
			break
		}
		if !isTrackPage(r.ItemURLPath) {
			continue
		}
		tracks = append(tracks, playlist.Track{
			Path:   r.ItemURLPath,
			Title:  r.Name,
			Artist: r.BandName,
			Album:  r.AlbumName,
			Stream: true,
		})
	}
	return tracks, nil
}

// isTrackPage reports whether raw is the page of a track on a bandcamp.com
// subdomain. cliamp hands only those hosts to yt-dlp, so a release on an
// artist's own domain could not play.
func isTrackPage(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" {
		return false
	}
	return strings.HasSuffix(strings.ToLower(u.Hostname()), ".bandcamp.com") && strings.HasPrefix(u.Path, "/track/")
}
