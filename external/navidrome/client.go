package navidrome

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	"cliamp/config"
	"cliamp/playlist"
)

// httpClient is used for all Navidrome API calls with a finite timeout.
var httpClient = &http.Client{Timeout: 30 * time.Second}

// maxResponseBody limits JSON API responses to 10 MB to prevent unbounded memory growth.
const maxResponseBody = 10 << 20

// Sort type constants for album browsing (Subsonic getAlbumList2 "type" parameter).
const (
	SortAlphabeticalByName   = "alphabeticalByName"
	SortAlphabeticalByArtist = "alphabeticalByArtist"
	SortNewest               = "newest"
	SortRecent               = "recent"
	SortFrequent             = "frequent"
	SortStarred              = "starred"
	SortByYear               = "byYear"
	SortByGenre              = "byGenre"
)

// SortTypes is the ordered list of sort modes used for cycling.
var SortTypes = []string{
	SortAlphabeticalByName,
	SortAlphabeticalByArtist,
	SortNewest,
	SortRecent,
	SortFrequent,
	SortStarred,
	SortByYear,
	SortByGenre,
}

// SortTypeLabel returns a human-readable label for a sort type constant.
func SortTypeLabel(s string) string {
	switch s {
	case SortAlphabeticalByName:
		return "Alphabetical by Name"
	case SortAlphabeticalByArtist:
		return "Alphabetical by Artist"
	case SortNewest:
		return "Newest"
	case SortRecent:
		return "Recently Played"
	case SortFrequent:
		return "Most Played"
	case SortStarred:
		return "Starred"
	case SortByYear:
		return "By Year"
	case SortByGenre:
		return "By Genre"
	default:
		return s
	}
}

// Artist represents a Navidrome/Subsonic artist entry.
type Artist struct {
	ID         string
	Name       string
	AlbumCount int
}

// Album represents a Navidrome/Subsonic album entry.
type Album struct {
	ID        string
	Name      string
	Artist    string
	ArtistID  string
	Year      int
	SongCount int
	Genre     string
}

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

// subsonicError represents an application-level error from the Subsonic API.
// The API returns HTTP 200 even for errors; the real status is in the JSON body.
type subsonicError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// checkSubsonicError inspects the decoded JSON response for an application-level
// error (e.g., wrong credentials, missing resource). Returns nil if status is "ok".
func checkSubsonicError(status string, apiErr *subsonicError) error {
	if status == "ok" || status == "" {
		return nil
	}
	if apiErr != nil && apiErr.Message != "" {
		return fmt.Errorf("navidrome: %s (code %d)", apiErr.Message, apiErr.Code)
	}
	return fmt.Errorf("navidrome: request failed (status %q)", status)
}

func (c *NavidromeClient) buildURL(endpoint string, params url.Values) string {
	// Use crypto/rand for the salt as recommended by the Subsonic API spec.
	// MD5 is required by the protocol — not a choice.
	saltBytes := make([]byte, 8)
	if _, err := io.ReadFull(rand.Reader, saltBytes); err != nil {
		// Fallback to timestamp if crypto/rand fails (should never happen).
		saltBytes = []byte(fmt.Sprintf("%d", time.Now().UnixNano()))
	}
	salt := hex.EncodeToString(saltBytes)
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
			Status    string         `json:"status"`
			Error     *subsonicError `json:"error"`
			Playlists struct {
				Playlist []struct {
					ID    string `json:"id"`
					Name  string `json:"name"`
					Count int    `json:"songCount"`
				} `json:"playlist"`
			} `json:"playlists"`
		} `json:"subsonic-response"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBody)).Decode(&result); err != nil {
		return nil, err
	}
	if err := checkSubsonicError(result.SubsonicResponse.Status, result.SubsonicResponse.Error); err != nil {
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
			Status   string         `json:"status"`
			Error    *subsonicError `json:"error"`
			Playlist struct {
				Entry []struct {
					ID          string `json:"id"`
					Title       string `json:"title"`
					Artist      string `json:"artist"`
					Album       string `json:"album"`
					Year        int    `json:"year"`
					TrackNumber int    `json:"track"`
					Genre       string `json:"genre"`
					Duration    int    `json:"duration"`
				} `json:"entry"`
			} `json:"playlist"`
		} `json:"subsonic-response"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBody)).Decode(&result); err != nil {
		return nil, err
	}
	if err := checkSubsonicError(result.SubsonicResponse.Status, result.SubsonicResponse.Error); err != nil {
		return nil, err
	}

	var tracks []playlist.Track
	for _, t := range result.SubsonicResponse.Playlist.Entry {
		tracks = append(tracks, playlist.Track{
			Path:         c.streamURL(t.ID),
			NavidromeID:  t.ID,
			Title:        t.Title,
			Artist:       t.Artist,
			Album:        t.Album,
			Year:         t.Year,
			TrackNumber:  t.TrackNumber,
			Genre:        t.Genre,
			Stream:       true,
			DurationSecs: t.Duration,
		})
	}
	return tracks, nil
}

// Artists returns all artists from the server, flattening the index structure.
func (c *NavidromeClient) Artists() ([]Artist, error) {
	resp, err := httpClient.Get(c.buildURL("getArtists", nil))
	if err != nil {
		return nil, fmt.Errorf("navidrome: getArtists: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("navidrome: getArtists: http status %s", resp.Status)
	}

	var result struct {
		SubsonicResponse struct {
			Status  string         `json:"status"`
			Error   *subsonicError `json:"error"`
			Artists struct {
				Index []struct {
					Artist []struct {
						ID         string `json:"id"`
						Name       string `json:"name"`
						AlbumCount int    `json:"albumCount"`
					} `json:"artist"`
				} `json:"index"`
			} `json:"artists"`
		} `json:"subsonic-response"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBody)).Decode(&result); err != nil {
		return nil, fmt.Errorf("navidrome: getArtists: %w", err)
	}
	if err := checkSubsonicError(result.SubsonicResponse.Status, result.SubsonicResponse.Error); err != nil {
		return nil, err
	}

	var artists []Artist
	for _, idx := range result.SubsonicResponse.Artists.Index {
		for _, a := range idx.Artist {
			artists = append(artists, Artist{
				ID:         a.ID,
				Name:       a.Name,
				AlbumCount: a.AlbumCount,
			})
		}
	}
	return artists, nil
}

// ArtistAlbums returns all albums for the given artist ID.
func (c *NavidromeClient) ArtistAlbums(artistID string) ([]Album, error) {
	resp, err := httpClient.Get(c.buildURL("getArtist", url.Values{"id": {artistID}}))
	if err != nil {
		return nil, fmt.Errorf("navidrome: getArtist: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("navidrome: getArtist: http status %s", resp.Status)
	}

	var result struct {
		SubsonicResponse struct {
			Status string         `json:"status"`
			Error  *subsonicError `json:"error"`
			Artist struct {
				Album []struct {
					ID        string `json:"id"`
					Name      string `json:"name"`
					Artist    string `json:"artist"`
					ArtistID  string `json:"artistId"`
					Year      int    `json:"year"`
					SongCount int    `json:"songCount"`
					Genre     string `json:"genre"`
				} `json:"album"`
			} `json:"artist"`
		} `json:"subsonic-response"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBody)).Decode(&result); err != nil {
		return nil, fmt.Errorf("navidrome: getArtist: %w", err)
	}
	if err := checkSubsonicError(result.SubsonicResponse.Status, result.SubsonicResponse.Error); err != nil {
		return nil, err
	}

	var albums []Album
	for _, a := range result.SubsonicResponse.Artist.Album {
		albums = append(albums, Album{
			ID:        a.ID,
			Name:      a.Name,
			Artist:    a.Artist,
			ArtistID:  a.ArtistID,
			Year:      a.Year,
			SongCount: a.SongCount,
			Genre:     a.Genre,
		})
	}
	return albums, nil
}

// AlbumList returns a page of albums sorted by sortType.
// offset and size control pagination; size should be ≤ 500.
func (c *NavidromeClient) AlbumList(sortType string, offset, size int) ([]Album, error) {
	if sortType == "" {
		sortType = SortAlphabeticalByName
	}
	params := url.Values{
		"type":   {sortType},
		"offset": {fmt.Sprintf("%d", offset)},
		"size":   {fmt.Sprintf("%d", size)},
	}
	resp, err := httpClient.Get(c.buildURL("getAlbumList2", params))
	if err != nil {
		return nil, fmt.Errorf("navidrome: getAlbumList2: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("navidrome: getAlbumList2: http status %s", resp.Status)
	}

	var result struct {
		SubsonicResponse struct {
			Status     string         `json:"status"`
			Error      *subsonicError `json:"error"`
			AlbumList2 struct {
				Album []struct {
					ID        string `json:"id"`
					Name      string `json:"name"`
					Artist    string `json:"artist"`
					ArtistID  string `json:"artistId"`
					Year      int    `json:"year"`
					SongCount int    `json:"songCount"`
					Genre     string `json:"genre"`
				} `json:"album"`
			} `json:"albumList2"`
		} `json:"subsonic-response"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBody)).Decode(&result); err != nil {
		return nil, fmt.Errorf("navidrome: getAlbumList2: %w", err)
	}
	if err := checkSubsonicError(result.SubsonicResponse.Status, result.SubsonicResponse.Error); err != nil {
		return nil, err
	}

	var albums []Album
	for _, a := range result.SubsonicResponse.AlbumList2.Album {
		albums = append(albums, Album{
			ID:        a.ID,
			Name:      a.Name,
			Artist:    a.Artist,
			ArtistID:  a.ArtistID,
			Year:      a.Year,
			SongCount: a.SongCount,
			Genre:     a.Genre,
		})
	}
	return albums, nil
}

// AlbumTracks returns all tracks for the given album ID with full metadata.
func (c *NavidromeClient) AlbumTracks(albumID string) ([]playlist.Track, error) {
	resp, err := httpClient.Get(c.buildURL("getAlbum", url.Values{"id": {albumID}}))
	if err != nil {
		return nil, fmt.Errorf("navidrome: getAlbum: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("navidrome: getAlbum: http status %s", resp.Status)
	}

	var result struct {
		SubsonicResponse struct {
			Status string         `json:"status"`
			Error  *subsonicError `json:"error"`
			Album  struct {
				Song []struct {
					ID          string `json:"id"`
					Title       string `json:"title"`
					Artist      string `json:"artist"`
					Album       string `json:"album"`
					Year        int    `json:"year"`
					TrackNumber int    `json:"track"`
					Genre       string `json:"genre"`
					Duration    int    `json:"duration"`
				} `json:"song"`
			} `json:"album"`
		} `json:"subsonic-response"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBody)).Decode(&result); err != nil {
		return nil, fmt.Errorf("navidrome: getAlbum: %w", err)
	}
	if err := checkSubsonicError(result.SubsonicResponse.Status, result.SubsonicResponse.Error); err != nil {
		return nil, err
	}

	var tracks []playlist.Track
	for _, s := range result.SubsonicResponse.Album.Song {
		tracks = append(tracks, playlist.Track{
			Path:         c.streamURL(s.ID),
			NavidromeID:  s.ID,
			Title:        s.Title,
			Artist:       s.Artist,
			Album:        s.Album,
			Year:         s.Year,
			TrackNumber:  s.TrackNumber,
			Genre:        s.Genre,
			Stream:       true,
			DurationSecs: s.Duration,
		})
	}
	return tracks, nil
}

// streamURL generates the authenticated streaming URL for a track ID.
// format=raw (Subsonic API 1.9.0+) instructs the server to return the original
// file without transcoding, giving a genuine Content-Length and preserving
// audio quality (FLAC, OPUS, AAC, MP3 — whatever is stored).
func (c *NavidromeClient) streamURL(id string) string {
	return c.buildURL("stream", url.Values{"id": {id}, "format": {"raw"}})
}

// Scrobble reports playback of a track to the Subsonic server.
// If submission is false, it registers a "now playing" notification only.
// If submission is true, it records a full play (updates play count, last.fm, etc.).
// The call is best-effort: errors are silently discarded.
func (c *NavidromeClient) Scrobble(id string, submission bool) {
	params := url.Values{
		"id":         {id},
		"submission": {fmt.Sprintf("%t", submission)},
	}
	if submission {
		// Pass the current wall-clock time in milliseconds as required by
		// the spec for submission=true (Subsonic API 1.8.0+).
		params.Set("time", fmt.Sprintf("%d", time.Now().UnixMilli()))
	}
	resp, err := httpClient.Get(c.buildURL("scrobble", params))
	if err != nil {
		return // fire-and-forget; ignore network errors
	}
	resp.Body.Close()
}
