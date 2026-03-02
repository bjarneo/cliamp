package navidrome

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"

	"cliamp/config"
	"cliamp/playlist"
)

// httpClient is used for all Navidrome API calls with a finite timeout.
var httpClient = &http.Client{Timeout: 30 * time.Second}

// NavidromeClient implements playlist.Provider for a Navidrome/Subsonic server.
type NavidromeClient struct {
	url      string
	user     string
	password string
}

// New creates a NavidromeClient with the given server credentials.
func New(serverURL, user, password string) *NavidromeClient {
	return &NavidromeClient{url: serverURL, user: user, password: password}
}

// NewFromEnv creates a NavidromeClient from NAVIDROME_URL, NAVIDROME_USER,
// and NAVIDROME_PASS environment variables. Returns nil if any are unset.
func NewFromEnv() *NavidromeClient {
	u := os.Getenv("NAVIDROME_URL")
	user := os.Getenv("NAVIDROME_USER")
	pass := os.Getenv("NAVIDROME_PASS")
	if u == "" || user == "" || pass == "" {
		return nil
	}
	return New(u, user, pass)
}

// NewFromConfig creates a NavidromeClient from a config.NavidromeConfig value.
// Returns nil if any of the required fields (URL, User, Password) are empty.
func NewFromConfig(cfg config.NavidromeConfig) *NavidromeClient {
	if !cfg.IsSet() {
		return nil
	}
	return New(cfg.URL, cfg.User, cfg.Password)
}

func (c *NavidromeClient) Name() string {
	return "Navidrome"
}

func (c *NavidromeClient) buildURL(endpoint string, params url.Values) string {
	salt := fmt.Sprintf("%d", time.Now().UnixNano())
	hash := md5.Sum([]byte(c.password + salt))
	token := hex.EncodeToString(hash[:])

	if params == nil {
		params = url.Values{}
	}
	params.Set("u", c.user)
	params.Set("t", token)
	params.Set("s", salt)
	params.Set("v", "1.0.0")
	params.Set("c", "cliamp")
	params.Set("f", "json")

	return fmt.Sprintf("%s/rest/%s?%s", c.url, endpoint, params.Encode())
}

func (c *NavidromeClient) Playlists() ([]playlist.PlaylistInfo, error) {
	resp, err := httpClient.Get(c.buildURL("getPlaylists", nil))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("navidrome: http status %s", resp.Status)
	}

	var result struct {
		SubsonicResponse struct {
			Playlists struct {
				Playlist []struct {
					ID    string `json:"id"`
					Name  string `json:"name"`
					Count int    `json:"songCount"`
				} `json:"playlist"`
			} `json:"playlists"`
		} `json:"subsonic-response"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	var lists []playlist.PlaylistInfo
	for _, p := range result.SubsonicResponse.Playlists.Playlist {
		lists = append(lists, playlist.PlaylistInfo{
			ID:         p.ID,
			Name:       p.Name,
			TrackCount: p.Count,
		})
	}
	return lists, nil
}

func (c *NavidromeClient) Tracks(id string) ([]playlist.Track, error) {
	resp, err := httpClient.Get(c.buildURL("getPlaylist", url.Values{"id": {id}}))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("navidrome: http status %s", resp.Status)
	}

	var result struct {
		SubsonicResponse struct {
			Playlist struct {
				Entry []struct {
					ID     string `json:"id"`
					Title  string `json:"title"`
					Artist string `json:"artist"`
				} `json:"entry"`
			} `json:"playlist"`
		} `json:"subsonic-response"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	var tracks []playlist.Track
	for _, t := range result.SubsonicResponse.Playlist.Entry {
		tracks = append(tracks, playlist.Track{
			Path:   c.streamURL(t.ID),
			Title:  t.Title,
			Artist: t.Artist,
			Stream: true,
		})
	}
	return tracks, nil
}

// streamURL generates the authenticated streaming URL for a track ID.
func (c *NavidromeClient) streamURL(id string) string {
	return c.buildURL("stream", url.Values{"id": {id}, "format": {"mp3"}})
}
