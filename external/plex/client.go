// Package plex implements a playlist.Provider for Plex Media Server.
package plex

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// maxResponseBody limits API responses to 10 MB to prevent unbounded memory growth.
const maxResponseBody = 10 << 20

// apiClient is used for all Plex API calls with a finite timeout.
// It is distinct from httpclient.Streaming (which has no timeout) used for audio streams.
var apiClient = &http.Client{Timeout: 30 * time.Second}

// Client speaks to a Plex Media Server over its HTTP API.
type Client struct {
	baseURL string // e.g. "http://192.168.1.10:32400"
	token   string // X-Plex-Token
}

// NewClient returns a Client for the given server URL and authentication token.
func NewClient(baseURL, token string) *Client {
	return &Client{baseURL: baseURL, token: token}
}

// Section represents a Plex library section (e.g. a music library).
type Section struct {
	Key   string // numeric section ID, e.g. "3"
	Title string // display name
	Type  string // "artist" for music libraries
}

// Album represents a Plex album with its artist and track count.
type Album struct {
	RatingKey  string // unique album ID (Plex ratingKey)
	Title      string
	ArtistName string // parentTitle in the Plex API
	Year       int
	TrackCount int // leafCount
}

// Track represents a Plex track with metadata and its first streamable Part.
type Track struct {
	RatingKey   string
	Title       string
	ArtistName  string // grandparentTitle
	AlbumName   string // parentTitle
	Year        int
	TrackNumber int    // index field in Plex API
	Duration    int    // milliseconds
	PartKey     string // relative path, e.g. "/library/parts/67890/1234567890/file.flac"
}

// get issues an authenticated GET request and decodes the JSON response into result.
func (c *Client) get(path string, params url.Values, result any) error {
	if params == nil {
		params = url.Values{}
	}
	params.Set("X-Plex-Token", c.token)

	req, err := http.NewRequest(http.MethodGet, c.baseURL+path+"?"+params.Encode(), nil)
	if err != nil {
		return fmt.Errorf("plex: %s: %w", path, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Plex-Product", "cliamp")
	req.Header.Set("X-Plex-Client-Identifier", "cliamp")

	resp, err := apiClient.Do(req)
	if err != nil {
		return fmt.Errorf("plex: %s: server unreachable: %w", path, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// ok
	case http.StatusUnauthorized:
		return fmt.Errorf("plex: token invalid or expired")
	default:
		return fmt.Errorf("plex: %s: HTTP %s", path, resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return fmt.Errorf("plex: %s: %w", path, err)
	}
	return json.Unmarshal(body, result)
}

// Ping checks that the server is reachable and the token is valid.
// Returns a descriptive error on failure.
func (c *Client) Ping() error {
	var result struct {
		MediaContainer struct {
			FriendlyName string `json:"friendlyName"`
		} `json:"MediaContainer"`
	}
	return c.get("/", nil, &result)
}

// MusicSections returns all library sections of type "artist" (music libraries).
func (c *Client) MusicSections() ([]Section, error) {
	var result struct {
		MediaContainer struct {
			Directory []struct {
				Key   string `json:"key"`
				Type  string `json:"type"`
				Title string `json:"title"`
			} `json:"Directory"`
		} `json:"MediaContainer"`
	}
	if err := c.get("/library/sections", nil, &result); err != nil {
		return nil, err
	}

	var sections []Section
	for _, d := range result.MediaContainer.Directory {
		if d.Type == "artist" {
			sections = append(sections, Section{
				Key:   d.Key,
				Title: d.Title,
				Type:  d.Type,
			})
		}
	}
	return sections, nil
}

// pageSize is the number of albums requested per API call.
// Plex paginates /library/sections/{id}/all; without an explicit size the
// server may return as few as one item.
const pageSize = 300

// Albums returns all albums in the given music section (identified by its key).
// It requests type=9 (album) directly rather than walking artists, and paginates
// through the full result set using X-Plex-Container-Start / Size.
func (c *Client) Albums(sectionKey string) ([]Album, error) {
	type albumPage struct {
		MediaContainer struct {
			TotalSize int `json:"totalSize"`
			Metadata  []struct {
				RatingKey   string `json:"ratingKey"`
				Title       string `json:"title"`
				ParentTitle string `json:"parentTitle"` // artist name
				Year        int    `json:"year"`
				LeafCount   int    `json:"leafCount"` // track count
			} `json:"Metadata"`
		} `json:"MediaContainer"`
	}

	var albums []Album
	for offset := 0; ; offset += pageSize {
		params := url.Values{
			"type":                   {"9"}, // 9 = album
			"X-Plex-Container-Start": {fmt.Sprintf("%d", offset)},
			"X-Plex-Container-Size":  {fmt.Sprintf("%d", pageSize)},
		}
		var page albumPage
		if err := c.get("/library/sections/"+sectionKey+"/all", params, &page); err != nil {
			return nil, err
		}
		for _, m := range page.MediaContainer.Metadata {
			albums = append(albums, Album{
				RatingKey:  m.RatingKey,
				Title:      m.Title,
				ArtistName: m.ParentTitle,
				Year:       m.Year,
				TrackCount: m.LeafCount,
			})
		}
		// Stop when we've fetched everything.
		if offset+pageSize >= page.MediaContainer.TotalSize {
			break
		}
	}
	return albums, nil
}

// Tracks returns all tracks in the given album (identified by its ratingKey).
func (c *Client) Tracks(albumRatingKey string) ([]Track, error) {
	var result struct {
		MediaContainer struct {
			Metadata []trackJSON `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	if err := c.get("/library/metadata/"+albumRatingKey+"/children", nil, &result); err != nil {
		return nil, err
	}

	tracks := make([]Track, 0, len(result.MediaContainer.Metadata))
	for _, m := range result.MediaContainer.Metadata {
		tracks = append(tracks, trackFromJSON(m))
	}
	return tracks, nil
}

// Search searches the music library for tracks matching query.
// Returns nil without making an HTTP call when query is empty.
func (c *Client) Search(query string) ([]Track, error) {
	if query == "" {
		return nil, nil
	}
	var result struct {
		MediaContainer struct {
			Metadata []trackJSON `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	params := url.Values{
		"query": {query},
		"type":  {"10"}, // 10 = track
	}
	if err := c.get("/library/search", params, &result); err != nil {
		return nil, err
	}

	tracks := make([]Track, 0, len(result.MediaContainer.Metadata))
	for _, m := range result.MediaContainer.Metadata {
		tracks = append(tracks, trackFromJSON(m))
	}
	return tracks, nil
}

// StreamURL returns the authenticated HTTP URL for streaming a track part.
// partKey is the relative path from the Part element, e.g. "/library/parts/…/file.flac".
// The token is appended as a query parameter; Plex accepts it in either header or query form.
func (c *Client) StreamURL(partKey string) string {
	return c.baseURL + partKey + "?X-Plex-Token=" + url.QueryEscape(c.token)
}

// trackJSON is the shared JSON structure for track responses (children and search).
type trackJSON struct {
	RatingKey        string `json:"ratingKey"`
	Title            string `json:"title"`
	GrandparentTitle string `json:"grandparentTitle"` // artist
	ParentTitle      string `json:"parentTitle"`      // album
	Year             int    `json:"year"`
	Index            int    `json:"index"`    // track number within album
	Duration         int    `json:"duration"` // milliseconds
	Media            []struct {
		Part []struct {
			Key string `json:"key"`
		} `json:"Part"`
	} `json:"Media"`
}

func trackFromJSON(m trackJSON) Track {
	var partKey string
	if len(m.Media) > 0 && len(m.Media[0].Part) > 0 {
		partKey = m.Media[0].Part[0].Key
	}
	return Track{
		RatingKey:   m.RatingKey,
		Title:       m.Title,
		ArtistName:  m.GrandparentTitle,
		AlbumName:   m.ParentTitle,
		Year:        m.Year,
		TrackNumber: m.Index,
		Duration:    m.Duration,
		PartKey:     partKey,
	}
}
