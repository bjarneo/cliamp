// Package embyapi implements the shared Emby/Jellyfin HTTP client. The two
// servers speak nearly the same API; the few differences (auth header scheme,
// ping endpoint, user-id discovery, error prefix, metadata key) are isolated
// in a dialect so emby and jellyfin can be thin wrappers over one client.
package embyapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"cliamp/playlist"
	"cliamp/provider"
)

var defaultHTTPClient = &http.Client{Timeout: 30 * time.Second}

// maxResponseBody limits API responses to 10 MB to prevent unbounded memory growth.
const maxResponseBody = 10 << 20

// Client speaks to an Emby or Jellyfin server over its HTTP API.
type Client struct {
	baseURL    string
	user       string
	password   string
	deviceID   string
	dialect    dialect
	httpClient *http.Client

	// mu guards the lazily-populated fields below, which are read and written
	// from concurrent tea.Cmd goroutines. It is never held across network I/O.
	mu         sync.Mutex
	token      string
	userID     string
	albumCache []Album // cached after first Albums() call
}

// NewEmbyClient returns a Client configured for an Emby server.
func NewEmbyClient(baseURL, token, userID, user, password string) *Client {
	return newClient(baseURL, token, userID, user, password, embyDialect{})
}

// NewJellyfinClient returns a Client configured for a Jellyfin server.
func NewJellyfinClient(baseURL, token, userID, user, password string) *Client {
	return newClient(baseURL, token, userID, user, password, jellyfinDialect{})
}

func newClient(baseURL, token, userID, user, password string, d dialect) *Client {
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		token:      token,
		userID:     userID,
		user:       user,
		password:   password,
		deviceID:   "cliamp",
		dialect:    d,
		httpClient: defaultHTTPClient,
	}
}

// SetHTTPClient overrides the HTTP client used for requests. Mainly for tests
// that inject a custom transport.
func (c *Client) SetHTTPClient(hc *http.Client) { c.httpClient = hc }

// authToken returns the current bearer token under the mutex.
func (c *Client) authToken() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.token
}

func (c *Client) setUserID(id string) {
	c.mu.Lock()
	c.userID = id
	c.mu.Unlock()
}

// ClearCache discards the cached album list so the next Albums call re-fetches.
func (c *Client) ClearCache() {
	c.mu.Lock()
	c.albumCache = nil
	c.mu.Unlock()
}

// MetaKey returns the playlist.Track ProviderMeta key for this server's item IDs.
func (c *Client) MetaKey() string { return c.dialect.metaKey() }

// Library represents a music library view.
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

// Album represents an album entry.
type Album struct {
	ID         string
	Name       string
	Artist     string
	ArtistID   string
	Year       int
	TrackCount int
}

// Track represents a track entry.
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
	var raw json.RawMessage
	return c.get(c.dialect.pingPath(), nil, &raw)
}

// UserID returns the active user id, discovering it lazily when needed.
func (c *Client) UserID() (string, error) {
	c.mu.Lock()
	id := c.userID
	c.mu.Unlock()
	if id != "" {
		return id, nil
	}
	if err := c.ensureAuth(); err != nil {
		return "", err
	}
	c.mu.Lock()
	id = c.userID
	c.mu.Unlock()
	if id != "" {
		return id, nil
	}
	return c.dialect.discoverUserID(c)
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

// Albums returns all albums across every music library.
// Results are cached after the first successful call.
func (c *Client) Albums() ([]Album, error) {
	c.mu.Lock()
	cached := c.albumCache
	c.mu.Unlock()
	if cached != nil {
		return cached, nil
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
	c.mu.Lock()
	c.albumCache = out
	c.mu.Unlock()
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
	if offset < 0 {
		offset = 0
	}
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

// AlbumsByLibrary returns all albums under one music library view.
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

// Search searches the user's audio library for tracks matching query and
// returns up to limit results.
func (c *Client) Search(query string, limit int) ([]Track, error) {
	userID, err := c.UserID()
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}

	params := url.Values{
		"userId":                 {userID},
		"searchTerm":             {query},
		"includeItemTypes":       {"Audio"},
		"recursive":              {"true"},
		"limit":                  {strconv.Itoa(limit)},
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

// IsStreamURL reports whether the given URL looks like an item download
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

// StreamURL returns an authenticated audio URL for a track item.
func (c *Client) StreamURL(itemID string) string {
	_ = c.ensureAuth()
	v := url.Values{
		"api_key": {c.authToken()},
	}

	// Use the direct item download route rather than the Audio controller.
	// On the live servers used for validation, the Audio endpoints returned
	// 200 with an empty body, while Download returned the original FLAC/MP3
	// bytes with byte-range support.
	u := c.baseURL + path.Join("/", "Items", itemID, "Download")
	if enc := v.Encode(); enc != "" {
		u += "?" + enc
	}
	return u
}

func (c *Client) ReportNowPlaying(track playlist.Track, position time.Duration, canSeek bool) error {
	return c.postJSON("/Sessions/Playing", playbackInfo{
		CanSeek:       canSeek,
		ItemID:        track.Meta(c.dialect.metaKey()),
		IsPaused:      false,
		IsMuted:       false,
		PositionTicks: toTicks(position),
		PlayMethod:    "DirectPlay",
	})
}

func (c *Client) ReportScrobble(track playlist.Track, elapsed time.Duration, canSeek bool) error {
	progress := playbackInfo{
		CanSeek:       canSeek,
		ItemID:        track.Meta(c.dialect.metaKey()),
		IsPaused:      false,
		IsMuted:       false,
		PositionTicks: toTicks(elapsed),
		PlayMethod:    "DirectPlay",
	}
	if err := c.postJSON("/Sessions/Playing/Progress", progress); err != nil {
		return err
	}
	return c.postJSON("/Sessions/Playing/Stopped", playbackStopInfo{
		ItemID:        track.Meta(c.dialect.metaKey()),
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

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %s: %w", c.dialect.name(), p, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	default:
		return fmt.Errorf("%s: %s: http status %s", c.dialect.name(), p, resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return fmt.Errorf("%s: %s: %w", c.dialect.name(), p, err)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("%s: %s: %w", c.dialect.name(), p, err)
	}
	return nil
}

func (c *Client) postJSON(p string, payload any) error {
	if err := c.ensureAuth(); err != nil {
		return fmt.Errorf("%s: %s: %w", c.dialect.name(), p, err)
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("%s: %s: %w", c.dialect.name(), p, err)
	}

	req, err := c.newRequestWithBody(http.MethodPost, p, nil, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%s: %s: %w", c.dialect.name(), p, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %s: %w", c.dialect.name(), p, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("%s: %s: http status %s", c.dialect.name(), p, resp.Status)
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBody))
	return nil
}

func (c *Client) ensureAuth() error {
	c.mu.Lock()
	have := c.token != ""
	c.mu.Unlock()
	if have {
		return nil
	}
	if c.user == "" || c.password == "" {
		return fmt.Errorf("%s: missing token or user/password", c.dialect.name())
	}

	body, err := json.Marshal(map[string]string{
		"Username": c.user,
		"Pw":       c.password,
	})
	if err != nil {
		return fmt.Errorf("%s: auth: %w", c.dialect.name(), err)
	}

	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/Users/AuthenticateByName", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%s: auth: %w", c.dialect.name(), err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	c.dialect.applyAuth(req, "", "", c.deviceID)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s: auth: %w", c.dialect.name(), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: auth: http status %s", c.dialect.name(), resp.Status)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return fmt.Errorf("%s: auth: %w", c.dialect.name(), err)
	}

	var out authResponseDTO
	if err := json.Unmarshal(data, &out); err != nil {
		return fmt.Errorf("%s: auth: %w", c.dialect.name(), err)
	}
	if out.AccessToken == "" {
		return fmt.Errorf("%s: auth: missing access token", c.dialect.name())
	}
	c.mu.Lock()
	c.token = out.AccessToken
	if c.userID == "" {
		c.userID = out.User.ID
	}
	c.mu.Unlock()
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
		return nil, fmt.Errorf("%s: %s: %w", c.dialect.name(), p, err)
	}
	req.Header.Set("Accept", "application/json")
	c.mu.Lock()
	token, userID := c.token, c.userID
	c.mu.Unlock()
	c.dialect.applyAuth(req, token, userID, c.deviceID)
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
