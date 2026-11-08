// Package plex implements a playlist.Provider for Plex Media Server.
package plex

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bjarneo/cliamp/applog"
	"github.com/bjarneo/cliamp/internal/httpclient"
	"github.com/bjarneo/cliamp/internal/netdiag"
)

// maxResponseBody limits API responses to 10 MB to prevent unbounded memory growth.
const maxResponseBody = 10 << 20

// apiClient is used for all Plex API calls with a finite timeout.
// It is distinct from httpclient.Streaming (which has no timeout) used for audio streams.
var apiClient = httpclient.NewAPI(30 * time.Second)

// Client speaks to a Plex Media Server over its HTTP API.
type Client struct {
	mu        sync.Mutex
	baseURL   string   // e.g. "http://192.168.1.10:32400"
	token     string   // X-Plex-Token used with the current server
	clientID  string   // stable device identity sent with Plex requests
	libraries []string // if non-empty, MusicSections filters to these titles (case-insensitive)
	account   *accountBinding
	machineID string // server identifier for play queue URIs, read once
}

type accountBinding struct {
	api       *AccountAPI
	token     string
	clientID  string
	serverID  string
	connected bool
}

// NewClient returns a Client for the given server URL and authentication token.
// libraries is an optional list of music library names to restrict MusicSections to.
// When not provided, all music libraries will be loaded
func NewClient(baseURL, token string, libraries ...string) *Client {
	return &Client{baseURL: baseURL, token: token, clientID: accountProduct, libraries: libraries}
}

// NewAccountClient returns a client that rediscovers the selected Plex server
// through plex.tv before its first request. baseURL is retained for display and
// as the last known connection in config; discovery supplies the live route.
func NewAccountClient(baseURL, accountToken, clientID, serverID string, libraries ...string) *Client {
	return newAccountClient(baseURL, accountToken, clientID, serverID, NewAccountAPI(), libraries...)
}

func newAccountClient(baseURL, accountToken, clientID, serverID string, api *AccountAPI, libraries ...string) *Client {
	return &Client{
		baseURL:   baseURL,
		token:     accountToken,
		clientID:  clientID,
		libraries: libraries,
		account: &accountBinding{
			api:      api,
			token:    accountToken,
			clientID: clientID,
			serverID: serverID,
		},
	}
}

func (c *Client) endpoint(ctx context.Context) (string, string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.account != nil && !c.account.connected {
		server, err := c.account.api.ConnectServer(ctx, c.account.token, c.account.clientID, c.account.serverID)
		if err != nil {
			return "", "", err
		}
		c.baseURL = server.URL
		c.token = server.AccessToken
		c.account.connected = true
	}
	return c.baseURL, c.token, nil
}

// responseHeaderTimeout bounds the wait for a server's response headers on
// one attempt. A dead route then fails in seconds instead of after the full
// request timeout, and send can move to another route. Tests shorten it.
var responseHeaderTimeout = 10 * time.Second

// send runs one request against the current server route; build makes it for
// a base URL and token. When an account-backed client cannot reach its route,
// send drops the route, rediscovers one through plex.tv and retries once, so
// a dead local, remote or relay connection does not stall playback. HTTP
// error responses are returned as they are. label names the call in errors.
func (c *Client) send(ctx context.Context, client *http.Client, label string, build func(baseURL, token string) (*http.Request, error)) (*http.Response, error) {
	for retried := false; ; retried = true {
		baseURL, token, err := c.endpoint(ctx)
		if err != nil {
			return nil, fmt.Errorf("plex: connect account server: %w", err)
		}
		req, err := build(baseURL, token)
		if err != nil {
			return nil, fmt.Errorf("plex: %s: %w", label, err)
		}
		resp, err := doWithHeaderDeadline(client, req)
		if err == nil {
			return resp, nil
		}
		// A url.Error names the request URL, and with it the token.
		if uerr, ok := errors.AsType[*url.Error](err); ok {
			err = uerr.Err
		}
		if retried || ctx.Err() != nil || !c.dropRoute(baseURL) || !retrySafe(req.Method, err) {
			return nil, fmt.Errorf("plex: %s: server unreachable: %w", label, netdiag.Explain(err))
		}
		applog.Info("plex: %s: %v; finding another server route", label, netdiag.Explain(err))
	}
}

// retrySafe reports whether a failed request may be sent again. A read may.
// A write, such as a scrobble or a new play queue, may only when it never left
// this computer: after a timeout the server may already have applied it.
func retrySafe(method string, err error) bool {
	if method == http.MethodGet || method == http.MethodHead {
		return true
	}
	opErr, ok := errors.AsType[*net.OpError](err)
	return ok && opErr.Op == "dial"
}

// dropRoute makes the next request rediscover the route when baseURL is still
// the current one. It reports whether rediscovery is possible: manual clients
// have only their configured URL.
func (c *Client) dropRoute(baseURL string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.account == nil {
		return false
	}
	if c.baseURL == baseURL {
		c.account.connected = false
	}
	return true
}

// doWithHeaderDeadline sends req and cancels it when no response headers
// arrive within responseHeaderTimeout. The body read is not limited by it.
func doWithHeaderDeadline(client *http.Client, req *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithCancel(req.Context())
	timeout := responseHeaderTimeout
	timer := time.AfterFunc(timeout, cancel)
	resp, err := client.Do(req.WithContext(ctx))
	fired := !timer.Stop()
	if err != nil {
		cancel()
		if fired && req.Context().Err() == nil {
			return nil, fmt.Errorf("no response in %s", timeout)
		}
		return nil, err
	}
	resp.Body = cancelOnClose{ReadCloser: resp.Body, cancel: cancel}
	return resp, nil
}

// cancelOnClose releases a request context when its response body closes.
type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b cancelOnClose) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}

// RefreshConnection makes an account-backed client rediscover its route on
// the next request. Manual clients keep their configured URL unchanged.
func (c *Client) RefreshConnection() {
	c.mu.Lock()
	if c.account != nil {
		c.account.connected = false
	}
	c.mu.Unlock()
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
	AlbumKey    string // parentRatingKey
	Year        int
	TrackNumber int    // index field in Plex API
	Duration    int    // milliseconds
	PartKey     string // relative path, e.g. "/library/parts/67890/1234567890/file.flac"
	UserRating  float64
}

// Station is a radio station of a Plex music library, such as Library Radio
// or Random Album Radio. Playing one asks the server for a play queue.
type Station struct {
	Key   string // e.g. "/library/sections/3/stations/1"
	Title string
}

// Playlist represents a Plex playlist (smart or user-created) of audio tracks.
type Playlist struct {
	RatingKey    string
	Title        string
	TrackCount   int // leafCount
	DurationSecs int // duration, converted from milliseconds
	Smart        bool
}

// errNotFound reports an HTTP 404: the server has no such item.
var errNotFound = errors.New("HTTP 404 Not Found")

// get issues an authenticated GET request and decodes the JSON response into result.
func (c *Client) get(path string, params url.Values, result any) error {
	return c.request(http.MethodGet, path, params, result)
}

// request issues an authenticated request and decodes the JSON response into
// result.
func (c *Client) request(method, path string, params url.Values, result any) error {
	resp, err := c.send(context.Background(), apiClient, path, func(baseURL, token string) (*http.Request, error) {
		query := url.Values{}
		for k, v := range params {
			query[k] = v
		}
		query.Set("X-Plex-Token", token)
		req, err := http.NewRequest(method, baseURL+path+"?"+query.Encode(), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("X-Plex-Product", "cliamp")
		req.Header.Set("X-Plex-Client-Identifier", c.clientID)
		return req, nil
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// ok
	case http.StatusUnauthorized:
		return fmt.Errorf("plex: token invalid or expired")
	case http.StatusNotFound:
		return fmt.Errorf("plex: %s: %w", path, errNotFound)
	default:
		return fmt.Errorf("plex: %s: HTTP %s", path, resp.Status)
	}

	if err := httpclient.ReadJSON(resp.Body, maxResponseBody, result); err != nil {
		return fmt.Errorf("plex: %s: %w", path, err)
	}
	return nil
}

// SetRating updates the current user's rating for one Plex library item.
// Plex uses 10 for a liked track and 0 to clear the like.
func (c *Client) SetRating(ctx context.Context, ratingKey string, rating float64) error {
	if ratingKey == "" {
		return fmt.Errorf("plex: rate: missing rating key")
	}
	params := url.Values{
		"identifier": {"com.plexapp.plugins.library"},
		"key":        {ratingKey},
		"rating":     {fmt.Sprintf("%g", rating)},
	}
	return c.doAction(ctx, http.MethodPut, "/:/rate", params)
}

// ReportTimeline updates Plex's view of the current playback position.
func (c *Client) ReportTimeline(ctx context.Context, ratingKey, state string, position, duration time.Duration) error {
	if ratingKey == "" {
		return fmt.Errorf("plex: timeline: missing rating key")
	}
	// Track durations are whole seconds while the position has milliseconds,
	// so in a track's last second the position can pass the duration. Plex
	// rejects such a timeline with 400 Bad Request.
	if duration > 0 {
		position = min(position, duration)
	}
	params := url.Values{
		"key":       {"/library/metadata/" + ratingKey},
		"ratingKey": {ratingKey},
		"state":     {state},
		"time":      {strconv.FormatInt(max(position.Milliseconds(), 0), 10)},
		"duration":  {strconv.FormatInt(max(duration.Milliseconds(), 0), 10)},
	}
	return c.doAction(ctx, http.MethodPost, "/:/timeline", params)
}

// Scrobble records one completed play in Plex.
func (c *Client) Scrobble(ctx context.Context, ratingKey string) error {
	if ratingKey == "" {
		return fmt.Errorf("plex: scrobble: missing rating key")
	}
	params := url.Values{
		"identifier": {"com.plexapp.plugins.library"},
		"key":        {ratingKey},
	}
	return c.doAction(ctx, http.MethodPut, "/:/scrobble", params)
}

func (c *Client) doAction(ctx context.Context, method, endpoint string, params url.Values) error {
	resp, err := c.send(ctx, apiClient, strings.TrimPrefix(endpoint, "/:/"), func(baseURL, token string) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, method, baseURL+endpoint+"?"+params.Encode(), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("X-Plex-Product", "cliamp")
		req.Header.Set("X-Plex-Client-Identifier", c.clientID)
		req.Header.Set("X-Plex-Token", token)
		return req, nil
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBody))

	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("plex: token invalid or expired")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("plex: %s: HTTP %s", strings.TrimPrefix(endpoint, "/:/"), resp.Status)
	}
	return nil
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

// MusicSections returns library sections of type "artist" (music libraries).
// When the client was constructed with a library filter, only sections whose
// title matches one of the allowed names (case-insensitive) are returned.
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
		if d.Type == "artist" && c.includeLibrary(d.Title) {
			sections = append(sections, Section{
				Key:   d.Key,
				Title: d.Title,
				Type:  d.Type,
			})
		}
	}
	return sections, nil
}

// includeLibrary reports whether the library with the given title should be
// included. When no filter is configured (libraries is empty) all libraries pass.
func (c *Client) includeLibrary(title string) bool {
	if len(c.libraries) == 0 {
		return true
	}
	for _, lib := range c.libraries {
		if strings.EqualFold(lib, title) {
			return true
		}
	}
	return false
}

// pageSize is the number of albums requested per API call.
// Plex paginates /library/sections/{id}/all; without an explicit size the
// server may return as few as one item.
const pageSize = 300

// maxPages stops a paging loop if a server never sends an empty page and
// omits totalSize.
const maxPages = 1000

// Albums returns all albums in the given music section (identified by its key).
// It requests type=9 (album) directly rather than walking artists, and paginates
// through the full result set using X-Plex-Container-Start / Size.
func (c *Client) Albums(sectionKey string) ([]Album, error) {
	type albumPage struct {
		MediaContainer struct {
			// TotalSize is a pointer so a server that omits it is
			// distinguishable from one that reports zero.
			TotalSize *int `json:"totalSize"`
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
	var prevFirstKey string
	for offset, n := 0, 0; n < maxPages; n++ {
		params := url.Values{
			"type":                   {"9"}, // 9 = album
			"X-Plex-Container-Start": {fmt.Sprintf("%d", offset)},
			"X-Plex-Container-Size":  {fmt.Sprintf("%d", pageSize)},
		}
		var page albumPage
		if err := c.get("/library/sections/"+sectionKey+"/all", params, &page); err != nil {
			return nil, err
		}
		// A page that starts with the album that started the page before
		// it means that the server ignored the start. Each further page
		// would repeat the first one.
		if md := page.MediaContainer.Metadata; len(md) > 0 {
			if md[0].RatingKey != "" && md[0].RatingKey == prevFirstKey {
				return nil, fmt.Errorf("plex: section %s albums: server ignored X-Plex-Container-Start %d", sectionKey, offset)
			}
			prevFirstKey = md[0].RatingKey
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
		// Advance by what the server actually returned, not by what was
		// asked for: Plex may answer with fewer items than pageSize.
		count := len(page.MediaContainer.Metadata)
		if count == 0 {
			break
		}
		offset += count
		if page.MediaContainer.TotalSize != nil && offset >= *page.MediaContainer.TotalSize {
			break
		}
	}
	return albums, nil
}

// Playlists returns all audio playlists on the server. Smart playlists and
// user-created playlists are both included; non-audio playlists (video, photo)
// are filtered out. The Plex JSON API returns playlist entries under
// MediaContainer.Metadata, each carrying a "type" of "playlist".
func (c *Client) Playlists() ([]Playlist, error) {
	var result struct {
		MediaContainer struct {
			Metadata []struct {
				RatingKey    string `json:"ratingKey"`
				Title        string `json:"title"`
				Smart        bool   `json:"smart"`
				PlaylistType string `json:"playlistType"`
				LeafCount    int    `json:"leafCount"`
				Duration     int    `json:"duration"`
			} `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	if err := c.get("/playlists", nil, &result); err != nil {
		return nil, fmt.Errorf("plex: list playlists: %w", err)
	}

	var playlists []Playlist
	for _, m := range result.MediaContainer.Metadata {
		if m.PlaylistType != "audio" {
			continue
		}
		playlists = append(playlists, Playlist{
			RatingKey:    m.RatingKey,
			Title:        m.Title,
			TrackCount:   m.LeafCount,
			DurationSecs: m.Duration / 1000,
			Smart:        m.Smart,
		})
	}
	return playlists, nil
}

// playlistPageSize is the number of playlist items requested per API call.
// Playlists can contain tens of thousands of tracks, so a larger page keeps
// the request count low while staying well under the 10 MB response cap.
const playlistPageSize = 1000

// PlaylistTracks returns all tracks in the given playlist (identified by its
// ratingKey). Playlist items share the track JSON shape, so trackFromJSON is
// reused. Results are paginated with X-Plex-Container-Start / Size.
func (c *Client) PlaylistTracks(playlistRatingKey string) ([]Track, error) {
	type trackPage struct {
		MediaContainer struct {
			TotalSize *int `json:"totalSize"`
			Metadata  []struct {
				trackJSON
				// PlaylistItemID tells two entries of one track apart.
				PlaylistItemID int64 `json:"playlistItemID"`
			} `json:"Metadata"`
		} `json:"MediaContainer"`
	}

	var tracks []Track
	var prevFirstKey string
	for offset, n := 0, 0; n < maxPages; n++ {
		params := url.Values{
			"X-Plex-Container-Start": {fmt.Sprintf("%d", offset)},
			"X-Plex-Container-Size":  {fmt.Sprintf("%d", playlistPageSize)},
		}
		var page trackPage
		if err := c.get("/playlists/"+playlistRatingKey+"/items", params, &page); err != nil {
			return nil, fmt.Errorf("plex: playlist %s items: %w", playlistRatingKey, err)
		}
		// A playlist can hold one track twice, so the entry ID marks the
		// first item when the server sends it. A repeated first item means
		// that the server ignored the start.
		if md := page.MediaContainer.Metadata; len(md) > 0 {
			key := md[0].RatingKey
			if md[0].PlaylistItemID != 0 {
				key = strconv.FormatInt(md[0].PlaylistItemID, 10)
			}
			if key != "" && key == prevFirstKey {
				return nil, fmt.Errorf("plex: playlist %s items: server ignored X-Plex-Container-Start %d", playlistRatingKey, offset)
			}
			prevFirstKey = key
		}
		for _, m := range page.MediaContainer.Metadata {
			tracks = append(tracks, trackFromJSON(m.trackJSON))
		}
		count := len(page.MediaContainer.Metadata)
		if count == 0 {
			break
		}
		offset += count
		if page.MediaContainer.TotalSize != nil && offset >= *page.MediaContainer.TotalSize {
			break
		}
	}
	return tracks, nil
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

// Track returns one track by its Plex ratingKey. It restores the stream-part
// mapping for a logical Plex URI after cliamp restarts.
func (c *Client) Track(ratingKey string) (Track, error) {
	var result struct {
		MediaContainer struct {
			Metadata []trackJSON `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	if err := c.get("/library/metadata/"+ratingKey, nil, &result); err != nil {
		return Track{}, err
	}
	if len(result.MediaContainer.Metadata) == 0 {
		return Track{}, fmt.Errorf("plex: track %s not found", ratingKey)
	}
	return trackFromJSON(result.MediaContainer.Metadata[0]), nil
}

// TracksByKey returns the tracks with the given ratingKeys in one request.
// Keys the server does not know are left out.
func (c *Client) TracksByKey(ratingKeys []string) ([]Track, error) {
	if len(ratingKeys) == 0 {
		return nil, nil
	}
	var result struct {
		MediaContainer struct {
			Metadata []trackJSON `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	err := c.get("/library/metadata/"+strings.Join(ratingKeys, ","), nil, &result)
	if errors.Is(err, errNotFound) {
		// Plex answers 404 only when none of the keys exists; a batch with
		// some removed tracks returns the others.
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	tracks := make([]Track, 0, len(result.MediaContainer.Metadata))
	for _, item := range result.MediaContainer.Metadata {
		tracks = append(tracks, trackFromJSON(item))
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

// OpenPart opens the original file of a track part, with the token in a
// request header. The caller closes the body.
func (c *Client) OpenPart(ctx context.Context, partKey string) (io.ReadCloser, error) {
	if !strings.HasPrefix(partKey, "/library/parts/") {
		return nil, fmt.Errorf("plex: download: invalid part")
	}
	resp, err := c.send(ctx, httpclient.Streaming, "download", func(baseURL, token string) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+partKey+"?download=1", nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("X-Plex-Product", "cliamp")
		req.Header.Set("X-Plex-Client-Identifier", c.clientID)
		req.Header.Set("X-Plex-Token", token)
		return req, nil
	})
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized {
			return nil, fmt.Errorf("plex: token invalid or expired")
		}
		return nil, fmt.Errorf("plex: download: HTTP %s", resp.Status)
	}
	return resp.Body, nil
}

// StreamURL returns the authenticated HTTP URL for streaming a track part.
// partKey is the relative path from the Part element, e.g. "/library/parts/…/file.flac".
// The token is appended as a query parameter; Plex accepts it in either header or query form.
//
// download=1 asks for the original file, as Plexamp does. Without it Plex
// checks the part against the client's direct-play profile; cliamp has none,
// so Plex falls back to a generic profile without FLAC and answers 500 for
// every FLAC whose audio stream it has analyzed.
func (c *Client) StreamURL(partKey string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.baseURL + partKey + "?download=1&X-Plex-Token=" + url.QueryEscape(c.token)
}

// IsStreamURL reports whether the given URL looks like a Plex library part
// endpoint. Used by the player to route these URLs through the buffered
// navBuffer + ffmpeg pipeline instead of native HTTP streaming.
func IsStreamURL(urlStr string) bool {
	u, err := url.Parse(urlStr)
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(u.Path), "/library/parts/")
}

// trackJSON is the shared JSON structure for track responses (children and search).
type trackJSON struct {
	RatingKey        string  `json:"ratingKey"`
	Title            string  `json:"title"`
	GrandparentTitle string  `json:"grandparentTitle"` // artist
	ParentTitle      string  `json:"parentTitle"`      // album
	ParentRatingKey  string  `json:"parentRatingKey"`  // album
	Year             int     `json:"year"`
	ParentYear       int     `json:"parentYear"` // album year; tracks rarely carry their own
	Index            int     `json:"index"`      // track number within album
	Duration         int     `json:"duration"`   // milliseconds
	UserRating       float64 `json:"userRating"`
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
	year := m.Year
	if year == 0 {
		year = m.ParentYear
	}
	return Track{
		RatingKey:   m.RatingKey,
		Title:       m.Title,
		ArtistName:  m.GrandparentTitle,
		AlbumName:   m.ParentTitle,
		AlbumKey:    m.ParentRatingKey,
		Year:        year,
		TrackNumber: m.Index,
		Duration:    m.Duration,
		PartKey:     partKey,
		UserRating:  m.UserRating,
	}
}

// Stations returns the radio stations of a music section from its hubs.
func (c *Client) Stations(sectionKey string) ([]Station, error) {
	var result struct {
		MediaContainer struct {
			Hub []struct {
				Type     string `json:"type"`
				Metadata []struct {
					Key   string `json:"key"`
					Title string `json:"title"`
				} `json:"Metadata"`
			} `json:"Hub"`
		} `json:"MediaContainer"`
	}
	params := url.Values{"count": {"20"}, "includeStations": {"1"}}
	if err := c.get("/hubs/sections/"+sectionKey, params, &result); err != nil {
		return nil, fmt.Errorf("plex: section %s stations: %w", sectionKey, err)
	}
	var stations []Station
	for _, hub := range result.MediaContainer.Hub {
		if hub.Type != "station" {
			continue
		}
		for _, m := range hub.Metadata {
			if m.Key != "" && m.Title != "" {
				stations = append(stations, Station{Key: m.Key, Title: m.Title})
			}
		}
	}
	return stations, nil
}

// PlayQueuePage is one window of a server play queue. LastItemID is the
// playQueueItemID of the last track, where the next window starts.
type PlayQueuePage struct {
	QueueID    int
	LastItemID int
	Tracks     []Track
}

// StationTracks starts a play queue for the station at stationKey and returns
// its first limit tracks. Each call starts a new queue, so it returns a fresh
// selection; QueueTracksAfter continues it.
func (c *Client) StationTracks(stationKey string, limit int) (PlayQueuePage, error) {
	machineID, err := c.machineIdentifier()
	if err != nil {
		return PlayQueuePage{}, err
	}
	var created struct {
		MediaContainer struct {
			PlayQueueID int `json:"playQueueID"`
		} `json:"MediaContainer"`
	}
	params := url.Values{
		"type": {"audio"},
		"uri":  {"server://" + machineID + "/com.plexapp.plugins.library" + stationKey},
	}
	if err := c.request(http.MethodPost, "/playQueues", params, &created); err != nil {
		return PlayQueuePage{}, fmt.Errorf("plex: start station: %w", err)
	}
	if created.MediaContainer.PlayQueueID == 0 {
		return PlayQueuePage{}, fmt.Errorf("plex: start station: no play queue")
	}
	// The window counts the selected first item as well.
	return c.playQueuePage(created.MediaContainer.PlayQueueID, url.Values{"window": {strconv.Itoa(limit - 1)}})
}

// QueueTracksAfter returns up to limit tracks of a play queue after the item
// afterItemID. An empty page means the queue has ended.
func (c *Client) QueueTracksAfter(queueID, afterItemID, limit int) (PlayQueuePage, error) {
	return c.playQueuePage(queueID, url.Values{
		"window":        {strconv.Itoa(limit)},
		"center":        {strconv.Itoa(afterItemID)},
		"includeBefore": {"0"},
		"includeAfter":  {"1"},
	})
}

func (c *Client) playQueuePage(queueID int, params url.Values) (PlayQueuePage, error) {
	var queue struct {
		MediaContainer struct {
			Metadata []struct {
				trackJSON
				PlayQueueItemID int `json:"playQueueItemID"`
			} `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	if err := c.get("/playQueues/"+strconv.Itoa(queueID), params, &queue); err != nil {
		return PlayQueuePage{}, fmt.Errorf("plex: play queue: %w", err)
	}
	page := PlayQueuePage{QueueID: queueID}
	for _, m := range queue.MediaContainer.Metadata {
		page.Tracks = append(page.Tracks, trackFromJSON(m.trackJSON))
		page.LastItemID = m.PlayQueueItemID
	}
	return page, nil
}

// machineIdentifier returns the server's identifier, which play queue URIs
// name. It is read once from /identity.
func (c *Client) machineIdentifier() (string, error) {
	c.mu.Lock()
	id := c.machineID
	c.mu.Unlock()
	if id != "" {
		return id, nil
	}
	var result struct {
		MediaContainer struct {
			MachineIdentifier string `json:"machineIdentifier"`
		} `json:"MediaContainer"`
	}
	if err := c.get("/identity", nil, &result); err != nil {
		return "", fmt.Errorf("plex: server identity: %w", err)
	}
	if result.MediaContainer.MachineIdentifier == "" {
		return "", fmt.Errorf("plex: server identity: no machine identifier")
	}
	c.mu.Lock()
	c.machineID = result.MediaContainer.MachineIdentifier
	c.mu.Unlock()
	return result.MediaContainer.MachineIdentifier, nil
}
