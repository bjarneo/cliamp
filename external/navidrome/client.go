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
	"strings"
	"sync"
	"time"

	"cliamp/config"
	"cliamp/playlist"
	"cliamp/provider"
)

// Compile-time interface checks.
var (
	_ provider.ArtistBrowser    = (*NavidromeClient)(nil)
	_ provider.AlbumBrowser     = (*NavidromeClient)(nil)
	_ provider.AlbumTrackLoader = (*NavidromeClient)(nil)
	_ provider.AlbumSortSaver   = (*NavidromeClient)(nil)
	_ provider.PlaybackReporter = (*NavidromeClient)(nil)
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

// IsSubsonicStreamURL reports whether path is a Subsonic stream or download
// endpoint. Used by the player to select the buffered download pipeline.
func IsSubsonicStreamURL(path string) bool {
	u, err := url.Parse(path)
	if err != nil {
		return false
	}
	p := strings.ToLower(u.Path)
	return strings.HasSuffix(p, "/rest/stream") ||
		strings.HasSuffix(p, "/rest/stream.view") ||
		strings.HasSuffix(p, "/rest/download") ||
		strings.HasSuffix(p, "/rest/download.view")
}

// Artist is a Navidrome/Subsonic artist — aliased to the provider type.
type Artist = provider.ArtistInfo

// Album is a Navidrome/Subsonic album — aliased to the provider type.
type Album = provider.AlbumInfo

// albumSortTypes is the static list of sort options for album browsing.
var albumSortTypes = []provider.SortType{
	{ID: SortAlphabeticalByName, Label: "Alphabetical by Name"},
	{ID: SortAlphabeticalByArtist, Label: "Alphabetical by Artist"},
	{ID: SortNewest, Label: "Newest"},
	{ID: SortRecent, Label: "Recently Played"},
	{ID: SortFrequent, Label: "Most Played"},
	{ID: SortStarred, Label: "Starred"},
	{ID: SortByYear, Label: "By Year"},
	{ID: SortByGenre, Label: "By Genre"},
}

// NavidromeClient implements playlist.Provider for a Navidrome/Subsonic server.
type NavidromeClient struct {
	url              string
	user             string
	password         string
	browseSort       string
	scrobbleDisabled bool
	mu               sync.Mutex
	playlistCache    []playlist.PlaylistInfo
	trackCache       map[string][]playlist.Track
}

// New creates a NavidromeClient with the given server credentials.
func New(serverURL, user, password string) *NavidromeClient {
	return &NavidromeClient{
		url:        serverURL,
		user:       user,
		password:   password,
		browseSort: SortAlphabeticalByName,
	}
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
	client := New(cfg.URL, cfg.User, cfg.Password)
	if cfg.BrowseSort != "" {
		client.browseSort = cfg.BrowseSort
	}
	client.scrobbleDisabled = cfg.ScrobbleDisabled
	return client
}

func (c *NavidromeClient) Name() string {
	return "Navidrome"
}

func (c *NavidromeClient) AlbumSortTypes() []provider.SortType {
	return albumSortTypes
}

func (c *NavidromeClient) DefaultAlbumSort() string {
	if c.browseSort != "" {
		return c.browseSort
	}
	return SortAlphabeticalByName
}

func (c *NavidromeClient) SaveAlbumSort(sortType string) error {
	if sortType == "" {
		sortType = SortAlphabeticalByName
	}
	c.browseSort = sortType
	return config.SaveNavidromeSort(sortType)
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

// subsonicGet performs a GET to the Subsonic API endpoint, decodes the JSON
// response into result, and checks for both HTTP and API-level errors.
func (c *NavidromeClient) subsonicGet(endpoint string, params url.Values, result any) error {
	resp, err := httpClient.Get(c.buildURL(endpoint, params))
	if err != nil {
		return fmt.Errorf("navidrome: %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("navidrome: %s: http status %s", endpoint, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return fmt.Errorf("navidrome: %s: %w", endpoint, err)
	}
	// Check for API-level errors.
	var env struct {
		SubsonicResponse struct {
			Status string         `json:"status"`
			Error  *subsonicError `json:"error"`
		} `json:"subsonic-response"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("navidrome: %s: %w", endpoint, err)
	}
	if err := checkSubsonicError(env.SubsonicResponse.Status, env.SubsonicResponse.Error); err != nil {
		return err
	}
	return json.Unmarshal(body, result)
}

func (c *NavidromeClient) Playlists() ([]playlist.PlaylistInfo, error) {
	c.mu.Lock()
	if c.playlistCache != nil {
		cached := c.playlistCache
		c.mu.Unlock()
		return cached, nil
	}
	c.mu.Unlock()

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
	if err := c.subsonicGet("getPlaylists", nil, &result); err != nil {
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

	c.mu.Lock()
	c.playlistCache = lists
	c.mu.Unlock()

	return lists, nil
}

func (c *NavidromeClient) Tracks(id string) ([]playlist.Track, error) {
	c.mu.Lock()
	if c.trackCache != nil {
		if cached, ok := c.trackCache[id]; ok {
			c.mu.Unlock()
			return cached, nil
		}
	}
	c.mu.Unlock()

	var result struct {
		SubsonicResponse struct {
			Playlist struct {
				Entry []subsonicSong `json:"entry"`
			} `json:"playlist"`
		} `json:"subsonic-response"`
	}
	if err := c.subsonicGet("getPlaylist", url.Values{"id": {id}}, &result); err != nil {
		return nil, err
	}

	var tracks []playlist.Track
	for _, t := range result.SubsonicResponse.Playlist.Entry {
		tracks = append(tracks, c.songToTrack(t))
	}

	c.mu.Lock()
	if c.trackCache == nil {
		c.trackCache = make(map[string][]playlist.Track)
	}
	c.trackCache[id] = tracks
	c.mu.Unlock()

	return tracks, nil
}

// Artists returns all artists from the server, flattening the index structure.
func (c *NavidromeClient) Artists() ([]Artist, error) {
	var result struct {
		SubsonicResponse struct {
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
	if err := c.subsonicGet("getArtists", nil, &result); err != nil {
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
	var result struct {
		SubsonicResponse struct {
			Artist struct {
				Album []subsonicAlbum `json:"album"`
			} `json:"artist"`
		} `json:"subsonic-response"`
	}
	if err := c.subsonicGet("getArtist", url.Values{"id": {artistID}}, &result); err != nil {
		return nil, err
	}

	var albums []Album
	for _, a := range result.SubsonicResponse.Artist.Album {
		albums = append(albums, albumFromSubsonic(a))
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
	var result struct {
		SubsonicResponse struct {
			AlbumList2 struct {
				Album []subsonicAlbum `json:"album"`
			} `json:"albumList2"`
		} `json:"subsonic-response"`
	}
	if err := c.subsonicGet("getAlbumList2", params, &result); err != nil {
		return nil, err
	}

	var albums []Album
	for _, a := range result.SubsonicResponse.AlbumList2.Album {
		albums = append(albums, albumFromSubsonic(a))
	}
	return albums, nil
}

// AlbumTracks returns all tracks for the given album ID with full metadata.
func (c *NavidromeClient) AlbumTracks(albumID string) ([]playlist.Track, error) {
	var result struct {
		SubsonicResponse struct {
			Album struct {
				Song []subsonicSong `json:"song"`
			} `json:"album"`
		} `json:"subsonic-response"`
	}
	if err := c.subsonicGet("getAlbum", url.Values{"id": {albumID}}, &result); err != nil {
		return nil, err
	}

	var tracks []playlist.Track
	for _, s := range result.SubsonicResponse.Album.Song {
		tracks = append(tracks, c.songToTrack(s))
	}
	return tracks, nil
}

// subsonicSong holds the common JSON fields returned by the Subsonic API
// for tracks in both getPlaylist and getAlbum responses.
type subsonicSong struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Artist      string `json:"artist"`
	Album       string `json:"album"`
	Year        int    `json:"year"`
	TrackNumber int    `json:"track"`
	Genre       string `json:"genre"`
	Duration    int    `json:"duration"`
}

func (c *NavidromeClient) songToTrack(s subsonicSong) playlist.Track {
	return playlist.Track{
		Path:         c.streamURL(s.ID),
		Title:        s.Title,
		Artist:       s.Artist,
		Album:        s.Album,
		Year:         s.Year,
		TrackNumber:  s.TrackNumber,
		Genre:        s.Genre,
		Stream:       true,
		DurationSecs: s.Duration,
		ProviderMeta: map[string]string{provider.MetaNavidromeID: s.ID},
	}
}

// subsonicAlbum holds the common JSON fields returned by the Subsonic API
// for albums in both getArtist and getAlbumList2 responses.
type subsonicAlbum struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Artist    string `json:"artist"`
	ArtistID  string `json:"artistId"`
	Year      int    `json:"year"`
	SongCount int    `json:"songCount"`
	Genre     string `json:"genre"`
}

func albumFromSubsonic(a subsonicAlbum) Album {
	return Album{
		ID:         a.ID,
		Name:       a.Name,
		Artist:     a.Artist,
		ArtistID:   a.ArtistID,
		Year:       a.Year,
		TrackCount: a.SongCount,
		Genre:      a.Genre,
	}
}

// streamURL generates the authenticated streaming URL for a track ID.
// format=raw (Subsonic API 1.9.0+) instructs the server to return the original
// file without transcoding, giving a genuine Content-Length and preserving
// audio quality (FLAC, OPUS, AAC, MP3 — whatever is stored).
func (c *NavidromeClient) streamURL(id string) string {
	return c.buildURL("stream", url.Values{"id": {id}, "format": {"raw"}})
}

func (c *NavidromeClient) CanReportPlayback(track playlist.Track) bool {
	return !c.scrobbleDisabled && track.Meta(provider.MetaNavidromeID) != ""
}

func (c *NavidromeClient) ReportNowPlaying(track playlist.Track, _ time.Duration, _ bool) {
	c.scrobble(track.Meta(provider.MetaNavidromeID), false)
}

func (c *NavidromeClient) ReportScrobble(track playlist.Track, _, _ time.Duration, _ bool) {
	c.scrobble(track.Meta(provider.MetaNavidromeID), true)
}

// scrobble reports playback of a track to the Subsonic server.
// If submission is false, it registers a "now playing" notification only.
// If submission is true, it records a full play (updates play count, last.fm, etc.).
// The call is best-effort: errors are silently discarded.
func (c *NavidromeClient) scrobble(id string, submission bool) {
	if id == "" {
		return
	}
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
