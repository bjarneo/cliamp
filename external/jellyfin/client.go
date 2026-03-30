package jellyfin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"

	"cliamp/playlist"
	"cliamp/provider"
)

var apiClient = &http.Client{Timeout: 30 * time.Second}

// maxResponseBody limits API responses to 10 MB to prevent unbounded memory growth.
const maxResponseBody = 10 << 20

// Client speaks to a Jellyfin server over its HTTP API.
type Client struct {
	baseURL    string
	token      string
	userID     string
	user       string
	password   string
	deviceID   string
	albumCache []Album // cached after first Albums() call
}

// NewClient returns a Client for the given server URL and API token.
func NewClient(baseURL, token, userID, user, password string) *Client {
	return &Client{
		baseURL:  strings.TrimRight(baseURL, "/"),
		token:    token,
		userID:   userID,
		user:     user,
		password: password,
		deviceID: "cliamp",
	}
}

// Library represents a Jellyfin music library view.
type Library struct {
	ID   string
	Name string
}

const (
	SortAlbumsByName   = "name"
	SortAlbumsByArtist = "artist"
	SortAlbumsByYear   = "year"
)

var albumSortTypes = []provider.SortType{
	{ID: SortAlbumsByName, Label: "Alphabetical by Name"},
	{ID: SortAlbumsByArtist, Label: "Alphabetical by Artist"},
	{ID: SortAlbumsByYear, Label: "By Year"},
}

// Album represents a Jellyfin album entry.
type Album struct {
	ID         string
	Name       string
	Artist     string
	ArtistID   string
	Year       int
	TrackCount int
}

// Track represents a Jellyfin track entry.
type Track struct {
	ID           string
	Name         string
	Artist       string
	Album        string
	Year         int
	TrackNumber  int
	DurationSecs int
}

type userDTO struct {
	ID   string `json:"Id"`
	Name string `json:"Name"`
}

type itemsResponseDTO struct {
	Items            []itemDTO `json:"Items"`
	TotalRecordCount int       `json:"TotalRecordCount"`
}

type itemDTO struct {
	ID             string      `json:"Id"`
	Name           string      `json:"Name"`
	Type           string      `json:"Type"`
	CollectionType string      `json:"CollectionType,omitempty"`
	Album          string      `json:"Album,omitempty"`
	AlbumArtist    string      `json:"AlbumArtist,omitempty"`
	AlbumArtists   []nameIDDTO `json:"AlbumArtists,omitempty"`
	Artists        []string    `json:"Artists,omitempty"`
	ArtistItems    []nameIDDTO `json:"ArtistItems,omitempty"`
	ProductionYear int         `json:"ProductionYear,omitempty"`
	ChildCount     int         `json:"ChildCount,omitempty"`
	IndexNumber    int         `json:"IndexNumber,omitempty"`
	RunTimeTicks   int64       `json:"RunTimeTicks,omitempty"`
}

type nameIDDTO struct {
	ID   string `json:"Id"`
	Name string `json:"Name"`
}

type authResponseDTO struct {
	User struct {
		ID string `json:"Id"`
	} `json:"User"`
	AccessToken string `json:"AccessToken"`
}

type playbackInfo struct {
	CanSeek       bool   `json:"CanSeek"`
	ItemID        string `json:"ItemId"`
	IsPaused      bool   `json:"IsPaused"`
	IsMuted       bool   `json:"IsMuted"`
	PositionTicks int64  `json:"PositionTicks,omitempty"`
	PlayMethod    string `json:"PlayMethod,omitempty"`
}

type playbackStopInfo struct {
	ItemID        string `json:"ItemId"`
	PositionTicks int64  `json:"PositionTicks,omitempty"`
	Failed        bool   `json:"Failed"`
}

// Ping checks that the server is reachable and the token is accepted.
func (c *Client) Ping() error {
	var u userDTO
	return c.get("/Users/Me", nil, &u)
}

// UserID returns the active user id, discovering it lazily when needed.
func (c *Client) UserID() (string, error) {
	if c.userID != "" {
		return c.userID, nil
	}
	if err := c.ensureAuth(); err != nil {
		return "", err
	}
	if c.userID != "" {
		return c.userID, nil
	}

	var u userDTO
	if err := c.get("/Users/Me", nil, &u); err != nil {
		return "", err
	}
	if u.ID == "" {
		return "", fmt.Errorf("jellyfin: current user response missing id")
	}
	c.userID = u.ID
	return c.userID, nil
}

// MusicLibraries returns all user views whose collection type is music.
func (c *Client) MusicLibraries() ([]Library, error) {
	userID, err := c.UserID()
	if err != nil {
		return nil, err
	}

	var resp itemsResponseDTO
	if err := c.get("/Users/"+url.PathEscape(userID)+"/Views", nil, &resp); err != nil {
		return nil, err
	}

	var libs []Library
	for _, it := range resp.Items {
		if strings.EqualFold(it.CollectionType, "music") {
			libs = append(libs, Library{ID: it.ID, Name: it.Name})
		}
	}
	return libs, nil
}

// Albums returns all albums across every Jellyfin music library.
// Results are cached after the first successful call.
func (c *Client) Albums() ([]Album, error) {
	if c.albumCache != nil {
		return c.albumCache, nil
	}

	libs, err := c.MusicLibraries()
	if err != nil {
		return nil, err
	}

	var out []Album
	for _, lib := range libs {
		albums, err := c.AlbumsByLibrary(lib.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, albums...)
	}
	c.albumCache = out
	return out, nil
}

// Artists returns a derived artist list built from the server's album catalog.
func (c *Client) Artists() ([]provider.ArtistInfo, error) {
	albums, err := c.Albums()
	if err != nil {
		return nil, err
	}

	type artistKey struct {
		id   string
		name string
	}
	seen := make(map[artistKey]*provider.ArtistInfo)
	for _, album := range albums {
		key := artistKey{id: canonicalArtistID(album.ArtistID, album.Artist), name: album.Artist}
		if key.id == "" && key.name == "" {
			continue
		}
		info, ok := seen[key]
		if !ok {
			info = &provider.ArtistInfo{
				ID:   key.id,
				Name: key.name,
			}
			seen[key] = info
		}
		info.AlbumCount++
	}

	artists := make([]provider.ArtistInfo, 0, len(seen))
	for _, artist := range seen {
		artists = append(artists, *artist)
	}
	sort.Slice(artists, func(i, j int) bool {
		return strings.ToLower(artists[i].Name) < strings.ToLower(artists[j].Name)
	})
	return artists, nil
}

// ArtistAlbums returns all albums for one artist, derived from the full album list.
func (c *Client) ArtistAlbums(artistID string) ([]provider.AlbumInfo, error) {
	albums, err := c.Albums()
	if err != nil {
		return nil, err
	}

	var out []provider.AlbumInfo
	for _, album := range albums {
		if artistID != "" && album.ArtistID != artistID {
			if canonicalArtistID(album.ArtistID, album.Artist) != artistID {
				continue
			}
		}
		out = append(out, provider.AlbumInfo{
			ID:         album.ID,
			Name:       album.Name,
			Artist:     album.Artist,
			ArtistID:   canonicalArtistID(album.ArtistID, album.Artist),
			Year:       album.Year,
			TrackCount: album.TrackCount,
		})
	}
	sortAlbums(out, SortAlbumsByName)
	return out, nil
}

// AlbumList returns one page from the full album catalog, sorted client-side.
func (c *Client) AlbumList(sortType string, offset, size int) ([]provider.AlbumInfo, error) {
	albums, err := c.Albums()
	if err != nil {
		return nil, err
	}

	out := make([]provider.AlbumInfo, 0, len(albums))
	for _, album := range albums {
		out = append(out, provider.AlbumInfo{
			ID:         album.ID,
			Name:       album.Name,
			Artist:     album.Artist,
			ArtistID:   canonicalArtistID(album.ArtistID, album.Artist),
			Year:       album.Year,
			TrackCount: album.TrackCount,
		})
	}

	sortAlbums(out, sortType)
	if offset >= len(out) {
		return nil, nil
	}
	end := len(out)
	if size > 0 && offset+size < end {
		end = offset + size
	}
	return out[offset:end], nil
}

func (c *Client) AlbumSortTypes() []provider.SortType {
	return albumSortTypes
}

func (c *Client) DefaultAlbumSort() string {
	return SortAlbumsByName
}

// AlbumsByLibrary returns all albums under one Jellyfin music library view.
func (c *Client) AlbumsByLibrary(libraryID string) ([]Album, error) {
	userID, err := c.UserID()
	if err != nil {
		return nil, err
	}

	params := url.Values{
		"userId":                 {userID},
		"parentId":               {libraryID},
		"recursive":              {"true"},
		"includeItemTypes":       {"MusicAlbum"},
		"sortBy":                 {"SortName"},
		"sortOrder":              {"Ascending"},
		"enableTotalRecordCount": {"false"},
	}

	var resp itemsResponseDTO
	if err := c.get("/Items", params, &resp); err != nil {
		return nil, err
	}

	out := make([]Album, 0, len(resp.Items))
	for _, it := range resp.Items {
		out = append(out, albumFromItem(it))
	}
	return out, nil
}

// Tracks returns all audio tracks contained by an album item.
func (c *Client) Tracks(albumID string) ([]Track, error) {
	userID, err := c.UserID()
	if err != nil {
		return nil, err
	}

	params := url.Values{
		"userId":                 {userID},
		"parentId":               {albumID},
		"includeItemTypes":       {"Audio"},
		"sortBy":                 {"ParentIndexNumber,IndexNumber,SortName"},
		"sortOrder":              {"Ascending"},
		"fields":                 {"RunTimeTicks"},
		"enableTotalRecordCount": {"false"},
	}

	var resp itemsResponseDTO
	if err := c.get("/Items", params, &resp); err != nil {
		return nil, err
	}

	out := make([]Track, 0, len(resp.Items))
	for _, it := range resp.Items {
		out = append(out, trackFromItem(it))
	}
	return out, nil
}

// IsStreamURL reports whether the given URL looks like a Jellyfin item download
// endpoint. Used by the player to route these URLs through the buffered ffmpeg
// pipeline instead of native HTTP streaming.
func IsStreamURL(path string) bool {
	u, err := url.Parse(path)
	if err != nil {
		return false
	}
	p := strings.ToLower(u.Path)
	return strings.Contains(p, "/items/") && strings.HasSuffix(p, "/download")
}

// StreamURL returns an authenticated Jellyfin audio URL for a track item.
func (c *Client) StreamURL(itemID string) string {
	_ = c.ensureAuth()
	v := url.Values{
		"api_key": {c.token},
	}

	// Use the direct item download route rather than the Audio controller.
	// On the live Jellyfin server used for validation, the Audio endpoints
	// returned 200 with an empty body, while Download returned the original
	// FLAC/MP3 bytes with byte-range support.
	u := c.baseURL + path.Join("/", "Items", itemID, "Download")
	if enc := v.Encode(); enc != "" {
		u += "?" + enc
	}
	return u
}

func (c *Client) ReportNowPlaying(track playlist.Track, position time.Duration, canSeek bool) error {
	return c.postJSON("/Sessions/Playing", playbackInfo{
		CanSeek:       canSeek,
		ItemID:        track.Meta(provider.MetaJellyfinID),
		IsPaused:      false,
		IsMuted:       false,
		PositionTicks: toTicks(position),
		PlayMethod:    "DirectPlay",
	})
}

func (c *Client) ReportScrobble(track playlist.Track, elapsed time.Duration, canSeek bool) error {
	progress := playbackInfo{
		CanSeek:       canSeek,
		ItemID:        track.Meta(provider.MetaJellyfinID),
		IsPaused:      false,
		IsMuted:       false,
		PositionTicks: toTicks(elapsed),
		PlayMethod:    "DirectPlay",
	}
	if err := c.postJSON("/Sessions/Playing/Progress", progress); err != nil {
		return err
	}
	return c.postJSON("/Sessions/Playing/Stopped", playbackStopInfo{
		ItemID:        track.Meta(provider.MetaJellyfinID),
		PositionTicks: toTicks(elapsed),
		Failed:        false,
	})
}

func (c *Client) get(p string, params url.Values, out any) error {
	if err := c.ensureAuth(); err != nil {
		return err
	}

	req, err := c.newRequest(http.MethodGet, p, params)
	if err != nil {
		return err
	}

	resp, err := apiClient.Do(req)
	if err != nil {
		return fmt.Errorf("jellyfin: %s: %w", p, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	default:
		return fmt.Errorf("jellyfin: %s: http status %s", p, resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return fmt.Errorf("jellyfin: %s: %w", p, err)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("jellyfin: %s: %w", p, err)
	}
	return nil
}

func (c *Client) postJSON(p string, payload any) error {
	if err := c.ensureAuth(); err != nil {
		return err
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := c.newRequestWithBody(http.MethodPost, p, nil, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := apiClient.Do(req)
	if err != nil {
		return fmt.Errorf("jellyfin: %s: %w", p, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("jellyfin: %s: http status %s", p, resp.Status)
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBody))
	return nil
}

func (c *Client) ensureAuth() error {
	if c.token != "" {
		return nil
	}
	if c.user == "" || c.password == "" {
		return fmt.Errorf("jellyfin: missing token or user/password")
	}

	body, err := json.Marshal(map[string]string{
		"Username": c.user,
		"Pw":       c.password,
	})
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/Users/AuthenticateByName", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Emby-Authorization",
		fmt.Sprintf(`MediaBrowser Client="cliamp", Device="cliamp", DeviceId="%s", Version="dev"`, c.deviceID))

	resp, err := apiClient.Do(req)
	if err != nil {
		return fmt.Errorf("jellyfin: auth: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("jellyfin: auth: http status %s", resp.Status)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return fmt.Errorf("jellyfin: auth: %w", err)
	}

	var out authResponseDTO
	if err := json.Unmarshal(data, &out); err != nil {
		return fmt.Errorf("jellyfin: auth: %w", err)
	}
	if out.AccessToken == "" {
		return fmt.Errorf("jellyfin: auth: missing access token")
	}
	c.token = out.AccessToken
	if c.userID == "" {
		c.userID = out.User.ID
	}
	return nil
}

func (c *Client) newRequest(method, p string, params url.Values) (*http.Request, error) {
	return c.newRequestWithBody(method, p, params, nil)
}

func (c *Client) newRequestWithBody(method, p string, params url.Values, body io.Reader) (*http.Request, error) {
	u := c.baseURL + p
	if len(params) > 0 {
		u += "?" + params.Encode()
	}

	req, err := http.NewRequest(method, u, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if c.token != "" {
		req.Header.Set("X-Emby-Token", c.token)
	}
	req.Header.Set("X-Emby-Authorization",
		fmt.Sprintf(`MediaBrowser Client="cliamp", Device="cliamp", DeviceId="%s", Version="dev"`, c.deviceID))
	return req, nil
}

func albumFromItem(it itemDTO) Album {
	a := Album{
		ID:         it.ID,
		Name:       it.Name,
		Artist:     it.AlbumArtist,
		Year:       it.ProductionYear,
		TrackCount: it.ChildCount,
	}
	if len(it.AlbumArtists) > 0 {
		if a.Artist == "" {
			a.Artist = it.AlbumArtists[0].Name
		}
		a.ArtistID = it.AlbumArtists[0].ID
	}
	if a.Artist == "" && len(it.ArtistItems) > 0 {
		a.Artist = it.ArtistItems[0].Name
		a.ArtistID = it.ArtistItems[0].ID
	}
	return a
}

func trackFromItem(it itemDTO) Track {
	t := Track{
		ID:           it.ID,
		Name:         it.Name,
		Album:        it.Album,
		Year:         it.ProductionYear,
		TrackNumber:  it.IndexNumber,
		DurationSecs: int(it.RunTimeTicks / 10_000_000),
	}
	if len(it.Artists) > 0 {
		t.Artist = it.Artists[0]
	} else if len(it.ArtistItems) > 0 {
		t.Artist = it.ArtistItems[0].Name
	}
	return t
}

func sortAlbums(albums []provider.AlbumInfo, sortType string) {
	switch sortType {
	case "", SortAlbumsByName:
		sort.Slice(albums, func(i, j int) bool {
			if strings.EqualFold(albums[i].Name, albums[j].Name) {
				return strings.ToLower(albums[i].Artist) < strings.ToLower(albums[j].Artist)
			}
			return strings.ToLower(albums[i].Name) < strings.ToLower(albums[j].Name)
		})
	case SortAlbumsByArtist:
		sort.Slice(albums, func(i, j int) bool {
			if strings.EqualFold(albums[i].Artist, albums[j].Artist) {
				return strings.ToLower(albums[i].Name) < strings.ToLower(albums[j].Name)
			}
			return strings.ToLower(albums[i].Artist) < strings.ToLower(albums[j].Artist)
		})
	case SortAlbumsByYear:
		sort.Slice(albums, func(i, j int) bool {
			if albums[i].Year == albums[j].Year {
				return strings.ToLower(albums[i].Name) < strings.ToLower(albums[j].Name)
			}
			return albums[i].Year > albums[j].Year
		})
	default:
		sortAlbums(albums, SortAlbumsByName)
	}
}

func canonicalArtistID(id, name string) string {
	if id != "" {
		return id
	}
	if name == "" {
		return ""
	}
	return "name:" + strings.ToLower(name)
}

func toTicks(d time.Duration) int64 {
	if d <= 0 {
		return 0
	}
	return d.Nanoseconds() / 100
}
