// Package subsonicapi implements the Subsonic/OpenSubsonic protocol client
// shared by the Navidrome and Bandcamp providers, parameterized by a dialect
// (the internal/embyapi pattern). The transport is token-auth GETs with JSON
// responses; per-server quirks live entirely in dialect.go.
package subsonicapi

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bjarneo/cliamp/internal/httpclient"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// Compile-time interface checks.
var (
	_ provider.ArtistBrowser       = (*Client)(nil)
	_ provider.AlbumBrowser        = (*Client)(nil)
	_ provider.AlbumTrackLoader    = (*Client)(nil)
	_ provider.AlbumSortSaver      = (*Client)(nil)
	_ provider.PlaybackReporter    = (*Client)(nil)
	_ provider.Searcher            = (*Client)(nil)
	_ provider.PlaylistCreator     = (*Client)(nil)
	_ provider.PlaylistWriter      = (*Client)(nil)
	_ provider.PlaylistBatchWriter = (*Client)(nil)
)

// defaultTimeout bounds every JSON API call. The streaming path has its own
// client; this never applies to audio.
const defaultTimeout = 30 * time.Second

// maxResponseBody limits JSON API responses to 128 MB to prevent unbounded
// memory growth. A getPlaylist response runs roughly 1.2 KB per entry, so this
// leaves room for playlists of ~100k tracks.
const maxResponseBody = 128 << 20

// retryAfterCap bounds how long a 429 Retry-After can make us sleep before
// the single retry.
const retryAfterCap = 5 * time.Second

// after is time.After, a variable so tests can skip the real backoff.
var after = time.After

// ErrBadCredentials marks an error whose most likely cause is rejected
// authentication, so providers can fail the pane into the credential-error
// surface instead of degrading gracefully. Match with errors.Is.
var ErrBadCredentials = errors.New("bad credentials")

// pingEndpoint is the connectivity check; authProbeEndpoint is the
// authenticated call that proves credentials. A dialect that infers rejected
// credentials from an ambiguous HTTP status keys on the latter, so the two
// must name the same endpoint.
const (
	pingEndpoint      = "ping.view"
	authProbeEndpoint = "getPlaylists"
)

// credentialsError carries a user-facing message while matching
// ErrBadCredentials, without appending the sentinel's text to the message.
// Only a protocol-level rejection (Subsonic error 40) builds one: a bare
// HTTP status is too ambiguous to condemn a user's credentials with.
type credentialsError struct{ msg string }

func (e credentialsError) Error() string        { return e.msg }
func (e credentialsError) Is(target error) bool { return target == ErrBadCredentials }

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

// Config carries the per-server settings for a Client.
type Config struct {
	BaseURL          string // e.g. "https://music.example.com"; trailing slashes are trimmed
	User             string
	Password         string
	BrowseSort       string // album browse sort; empty uses the dialect default
	StreamFormat     string // requested stream format; empty lets the server decide, "raw" requests the original
	ScrobbleDisabled bool   // user opt-out on top of dialect support
	// SaveSort persists a browse-sort change (config-file I/O stays in the
	// adapter packages). Nil makes SaveAlbumSort in-memory only.
	SaveSort func(sortType string) error
}

// Client implements playlist.Provider for a Subsonic-family server.
type Client struct {
	d             dialect
	url           string
	user          string
	password      string
	browseSort    string
	streamFormat  string
	scrobbleOff   bool
	saveSort      func(string) error
	httpClient    *http.Client
	mu            sync.Mutex
	playlistCache []playlist.PlaylistInfo
	trackCache    map[string][]playlist.Track
	unsupported   map[string]error // endpoints the server rejects, memoized until Refresh
}

func newClient(d dialect, cfg Config) *Client {
	// On a server whose accepted sorts are known in full, a browse_sort
	// outside that set (a [navidrome] value copied into [bandcamp], say)
	// would render an empty album list with no error — fall back instead.
	// Servers that take more types than the picker lists keep the value.
	sort := cfg.BrowseSort
	if sort != "" && d.sortListExhaustive() &&
		!slices.ContainsFunc(d.albumSortTypes(), func(t provider.SortType) bool { return t.ID == sort }) {
		sort = ""
	}
	if sort == "" {
		sort = d.defaultSort()
	}
	return &Client{
		d:            d,
		url:          strings.TrimRight(cfg.BaseURL, "/"),
		user:         cfg.User,
		password:     cfg.Password,
		browseSort:   sort,
		streamFormat: strings.ToLower(strings.TrimSpace(cfg.StreamFormat)),
		scrobbleOff:  cfg.ScrobbleDisabled || !d.scrobbleSupported(),
		saveSort:     cfg.SaveSort,
		httpClient:   &http.Client{Timeout: defaultTimeout},
	}
}

// prefix is the lowercase provider name used in error messages.
func (c *Client) prefix() string {
	return strings.ToLower(c.d.name())
}

// BaseURL returns the resolved API base URL.
func (c *Client) BaseURL() string {
	return c.url
}

func (c *Client) Name() string {
	return c.d.name()
}

// Ping verifies connectivity via the Subsonic ping.view endpoint. Note that
// some servers (Bandcamp) answer ping without validating credentials — use
// ValidateAuth for a real credential check.
func (c *Client) Ping() error {
	return c.ping(context.Background())
}

func (c *Client) ping(ctx context.Context) error {
	var dummy struct{}
	return c.subsonicGet(ctx, pingEndpoint, nil, &dummy)
}

// ValidateAuth verifies credentials with an authenticated data call
// (getPlaylists). Unlike Ping, this fails on wrong credentials everywhere.
// It bypasses and does not populate the playlist cache.
func (c *Client) ValidateAuth() error { return c.ValidateAuthContext(context.Background()) }

// ValidateAuthContext is ValidateAuth bound to ctx.
func (c *Client) ValidateAuthContext(ctx context.Context) error {
	var dummy struct{}
	return c.subsonicGet(ctx, authProbeEndpoint, nil, &dummy)
}

// PingContext is Ping bound to ctx.
func (c *Client) PingContext(ctx context.Context) error { return c.ping(ctx) }

func (c *Client) AlbumSortTypes() []provider.SortType {
	return c.d.albumSortTypes()
}

func (c *Client) DefaultAlbumSort() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.browseSort
}

func (c *Client) SaveAlbumSort(sortType string) error {
	if sortType == "" {
		sortType = c.d.defaultSort()
	}
	c.mu.Lock()
	c.browseSort = sortType
	c.mu.Unlock()
	if c.saveSort == nil {
		return nil
	}
	return c.saveSort(sortType)
}

// subsonicError represents an application-level error from the Subsonic API.
// The API returns HTTP 200 even for errors; the real status is in the JSON body.
type subsonicError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// checkSubsonicError inspects the decoded JSON response for an application-level
// error (e.g., wrong credentials, missing resource). Returns nil if status is "ok".
func checkSubsonicError(prefix, status string, apiErr *subsonicError) error {
	if status == "ok" || status == "" {
		return nil
	}
	if apiErr != nil && apiErr.Message != "" {
		// Code 40 is the spec's "wrong username or password".
		if apiErr.Code == 40 {
			return credentialsError{msg: fmt.Sprintf("%s: %s (code %d)", prefix, apiErr.Message, apiErr.Code)}
		}
		return fmt.Errorf("%s: %s (code %d)", prefix, apiErr.Message, apiErr.Code)
	}
	return fmt.Errorf("%s: request failed (status %q)", prefix, status)
}

func (c *Client) buildURL(endpoint string, params url.Values) string {
	if sfx := c.d.endpointSuffix(); sfx != "" && !strings.HasSuffix(endpoint, sfx) {
		endpoint += sfx
	}
	// Use crypto/rand for the salt as recommended by the Subsonic API spec.
	// MD5 is required by the protocol — not a choice.
	saltBytes := make([]byte, 8)
	if _, err := io.ReadFull(rand.Reader, saltBytes); err != nil {
		// Fallback to timestamp if crypto/rand fails (should never happen).
		saltBytes = fmt.Appendf(nil, "%d", time.Now().UnixNano())
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
	params.Set("v", c.d.apiVersion())
	params.Set("c", "cliamp")
	params.Set("f", "json")

	return fmt.Sprintf("%s/rest/%s?%s", c.url, endpoint, params.Encode())
}

// get performs a GET with the cliamp User-Agent, retrying once on HTTP 429
// with the server's Retry-After honored (capped at retryAfterCap). Transport
// errors are sanitized: the URL query carries auth tokens and must never
// reach error strings (footer, logs, probe output).
func (c *Client) get(ctx context.Context, rawURL string) (*http.Response, error) {
	do := func() (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, SanitizeURLError(err)
		}
		// Same User-Agent as the stream download, so Navidrome registers one
		// player for cliamp instead of one per User-Agent.
		req.Header.Set("User-Agent", httpclient.UserAgent)
		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, SanitizeURLError(err)
		}
		return resp, nil
	}
	resp, err := do()
	if err != nil || resp.StatusCode != http.StatusTooManyRequests {
		return resp, err
	}
	delay := retryDelay(resp.Header.Get("Retry-After"))
	resp.Body.Close()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-after(delay):
	}
	return do()
}

// retryDelay converts a Retry-After header (seconds form) into a bounded
// sleep: 1s when absent or unparsable, never more than retryAfterCap. The
// clamp happens before the Duration multiply so huge values cannot overflow.
func retryDelay(header string) time.Duration {
	v, err := strconv.Atoi(header)
	if err != nil || v <= 0 {
		return time.Second
	}
	if capSecs := int(retryAfterCap / time.Second); v > capSecs {
		v = capSecs
	}
	return time.Duration(v) * time.Second
}

// SanitizeURLError strips the query string (auth token, salt) out of a
// *url.Error before it can be printed anywhere.
func SanitizeURLError(err error) error {
	var ue *url.Error
	if !errors.As(err, &ue) {
		return err
	}
	if u, perr := url.Parse(ue.URL); perr == nil {
		u.RawQuery = ""
		// Keep the *url.Error wrapper so callers can still reach Timeout()
		// and Temporary() through errors.As.
		return &url.Error{Op: ue.Op, URL: u.Redacted(), Err: ue.Err}
	}
	return ue.Err
}

// subsonicGet performs a GET to the Subsonic API endpoint, decodes the JSON
// response into result, and checks for both HTTP and API-level errors.
// Endpoints the dialect marked unsupported short-circuit until Refresh.
func (c *Client) subsonicGet(ctx context.Context, endpoint string, params url.Values, result any) error {
	prefix := c.prefix()
	// The memo records endpoints the server does not implement, which the
	// dialect recognizes by a route-level signature; parameters do not
	// change that verdict (Bandcamp answers bad parameters with a proper ok
	// envelope, verified live 2026-08-25). The ping health check is the one
	// exclusion: it doubles as the outage detector, so a single transient
	// answer must not disable it until Refresh.
	memoizable := endpoint != pingEndpoint
	if memoizable {
		c.mu.Lock()
		memoized, ok := c.unsupported[endpoint]
		c.mu.Unlock()
		if ok {
			return memoized
		}
	}
	resp, err := c.get(ctx, c.buildURL(endpoint, params))
	if err != nil {
		return fmt.Errorf("%s: %s: %w", prefix, endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return c.d.httpError(endpoint, resp.StatusCode, resp.Status)
	}
	// Read one byte past the cap so an oversized response is reported rather
	// than silently truncated: io.LimitReader alone would cut mid-object and
	// hand json.Unmarshal a partial body, which fails as the opaque
	// "unexpected end of JSON input".
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody+1))
	if err != nil {
		return fmt.Errorf("%s: %s: %w", prefix, endpoint, err)
	}
	if len(body) > maxResponseBody {
		return fmt.Errorf("%s: %s: response exceeds %d bytes", prefix, endpoint, maxResponseBody)
	}
	// Check for API-level errors.
	var env struct {
		SubsonicResponse *struct {
			Status string         `json:"status"`
			Error  *subsonicError `json:"error"`
		} `json:"subsonic-response"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("%s: %s: %w", prefix, endpoint, err)
	}
	if env.SubsonicResponse == nil {
		if uerr := c.d.missingEnvelope(endpoint, body); uerr != nil {
			if memoizable {
				c.mu.Lock()
				if c.unsupported == nil {
					c.unsupported = make(map[string]error)
				}
				c.unsupported[endpoint] = uerr
				c.mu.Unlock()
			}
			return uerr
		}
		// Valid JSON without the protocol envelope is not a Subsonic answer
		// at all (a proxy error page, a captive portal). Decoding it into
		// result would leave every field zero and report success.
		return fmt.Errorf("%s: %s: response is not a Subsonic reply (no subsonic-response envelope)", prefix, endpoint)
	} else if err := checkSubsonicError(prefix, env.SubsonicResponse.Status, env.SubsonicResponse.Error); err != nil {
		return err
	}
	return json.Unmarshal(body, result)
}

func (c *Client) Playlists() ([]playlist.PlaylistInfo, error) {
	c.mu.Lock()
	if c.playlistCache != nil {
		cached := slices.Clone(c.playlistCache)
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
	if err := c.subsonicGet(context.Background(), "getPlaylists", nil, &result); err != nil {
		return nil, err
	}

	lists := make([]playlist.PlaylistInfo, 0, len(result.SubsonicResponse.Playlists.Playlist))
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

	return slices.Clone(lists), nil
}

func (c *Client) Tracks(id string) ([]playlist.Track, error) {
	c.mu.Lock()
	if c.trackCache != nil {
		if cached, ok := c.trackCache[id]; ok {
			c.mu.Unlock()
			return slices.Clone(cached), nil
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
	if err := c.subsonicGet(context.Background(), "getPlaylist", url.Values{"id": {id}}, &result); err != nil {
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

	return slices.Clone(tracks), nil
}

// Refresh clears cached playlist/track data and the unsupported-endpoint memo
// so the next call re-fetches from the server. Implements playlist.Refresher.
func (c *Client) Refresh() {
	c.mu.Lock()
	c.playlistCache = nil
	c.trackCache = nil
	c.unsupported = nil
	c.mu.Unlock()
}

// clearPlaylistMemo forgets "unimplemented" verdicts for the playlist
// endpoints only. A successful mutation proves those routes serve; it says
// nothing about getArtists or getAlbumList2, whose verdicts must survive.
// Callers hold c.mu.
func (c *Client) clearPlaylistMemo() {
	for _, endpoint := range []string{"getPlaylists", "getPlaylist", "createPlaylist", "updatePlaylist"} {
		delete(c.unsupported, endpoint)
	}
}

// Artists returns all artists from the server, flattening the index structure.
func (c *Client) Artists() ([]provider.ArtistInfo, error) {
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
	if err := c.subsonicGet(context.Background(), "getArtists", nil, &result); err != nil {
		return nil, err
	}

	var artists []provider.ArtistInfo
	for _, idx := range result.SubsonicResponse.Artists.Index {
		for _, a := range idx.Artist {
			artists = append(artists, provider.ArtistInfo{
				ID:         a.ID,
				Name:       a.Name,
				AlbumCount: a.AlbumCount,
			})
		}
	}
	return artists, nil
}

// ArtistAlbums returns all albums for the given artist ID.
func (c *Client) ArtistAlbums(artistID string) ([]provider.AlbumInfo, error) {
	var result struct {
		SubsonicResponse struct {
			Artist struct {
				Album []subsonicAlbum `json:"album"`
			} `json:"artist"`
		} `json:"subsonic-response"`
	}
	if err := c.subsonicGet(context.Background(), "getArtist", url.Values{"id": {artistID}}, &result); err != nil {
		return nil, err
	}

	var albums []provider.AlbumInfo
	for _, a := range result.SubsonicResponse.Artist.Album {
		albums = append(albums, albumFromSubsonic(a))
	}
	return albums, nil
}

// AlbumList returns a page of albums sorted by sortType.
// offset and size control pagination; size should be ≤ 500.
func (c *Client) AlbumList(sortType string, offset, size int) ([]provider.AlbumInfo, error) {
	return c.AlbumListContext(context.Background(), sortType, offset, size)
}

// AlbumListContext is AlbumList bound to ctx.
func (c *Client) AlbumListContext(ctx context.Context, sortType string, offset, size int) ([]provider.AlbumInfo, error) {
	if sortType == "" {
		sortType = c.DefaultAlbumSort()
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
	if err := c.subsonicGet(ctx, "getAlbumList2", params, &result); err != nil {
		return nil, err
	}

	var albums []provider.AlbumInfo
	for _, a := range result.SubsonicResponse.AlbumList2.Album {
		albums = append(albums, albumFromSubsonic(a))
	}
	return albums, nil
}

// AlbumTracks returns all tracks for the given album ID with full metadata.
func (c *Client) AlbumTracks(albumID string) ([]playlist.Track, error) {
	return c.AlbumTracksContext(context.Background(), albumID)
}

// AlbumTracksContext is AlbumTracks bound to ctx.
func (c *Client) AlbumTracksContext(ctx context.Context, albumID string) ([]playlist.Track, error) {
	var result struct {
		SubsonicResponse struct {
			Album struct {
				Song []subsonicSong `json:"song"`
			} `json:"album"`
		} `json:"subsonic-response"`
	}
	if err := c.subsonicGet(ctx, "getAlbum", url.Values{"id": {albumID}}, &result); err != nil {
		return nil, err
	}

	var tracks []playlist.Track
	for _, s := range result.SubsonicResponse.Album.Song {
		tracks = append(tracks, c.songToTrack(s))
	}
	return tracks, nil
}

// SearchTracks searches the Subsonic library for songs matching query
// using the search3.view endpoint. Implements provider.Searcher.
func (c *Client) SearchTracks(ctx context.Context, query string, limit int) ([]playlist.Track, error) {
	return c.SearchTracksFrom(ctx, query, 0, limit)
}

// SearchTracksFrom is SearchTracks with a song offset, for paging through
// large result sets (an empty query lists the whole library on Subsonic
// servers that support it).
func (c *Client) SearchTracksFrom(ctx context.Context, query string, offset, limit int) ([]playlist.Track, error) {
	if limit <= 0 {
		limit = 50
	}
	params := url.Values{
		"query":       {query},
		"songCount":   {strconv.Itoa(limit)},
		"songOffset":  {strconv.Itoa(offset)},
		"albumCount":  {"0"},
		"artistCount": {"0"},
	}
	var result struct {
		SubsonicResponse struct {
			SearchResult3 struct {
				Song []subsonicSong `json:"song"`
			} `json:"searchResult3"`
		} `json:"subsonic-response"`
	}
	if err := c.subsonicGet(ctx, "search3", params, &result); err != nil {
		return nil, err
	}
	tracks := make([]playlist.Track, 0, len(result.SubsonicResponse.SearchResult3.Song))
	for _, s := range result.SubsonicResponse.SearchResult3.Song {
		tracks = append(tracks, c.songToTrack(s))
	}
	return tracks, nil
}

// CreatePlaylist creates a new playlist and returns its ID.
// Implements provider.PlaylistCreator.
func (c *Client) CreatePlaylist(ctx context.Context, name string) (string, error) {
	var result struct {
		SubsonicResponse struct {
			Playlist struct {
				ID string `json:"id"`
			} `json:"playlist"`
		} `json:"subsonic-response"`
	}
	if err := c.subsonicGet(ctx, "createPlaylist", url.Values{"name": {name}}, &result); err != nil {
		return "", err
	}
	c.mu.Lock()
	c.playlistCache = nil
	c.clearPlaylistMemo() // a working mutation proves the playlist endpoints serve
	c.mu.Unlock()
	if result.SubsonicResponse.Playlist.ID == "" {
		return "", fmt.Errorf("%s: createPlaylist: server did not return a playlist id", c.prefix())
	}
	return result.SubsonicResponse.Playlist.ID, nil
}

// AddTrackToPlaylist appends one track to an existing playlist.
// Implements provider.PlaylistWriter.
func (c *Client) AddTrackToPlaylist(ctx context.Context, playlistID string, track playlist.Track) error {
	added, _, err := c.AddTracksToPlaylist(ctx, playlistID, []playlist.Track{track})
	if err != nil {
		return err
	}
	if added == 0 {
		return fmt.Errorf("%s: track has no %s id", c.prefix(), c.d.name())
	}
	return nil
}

// AddTracksToPlaylist appends tracks to an existing playlist in one
// updatePlaylist call. Tracks that did not originate from this provider
// (no matching ProviderMeta id) are skipped. Implements
// provider.PlaylistBatchWriter.
func (c *Client) AddTracksToPlaylist(ctx context.Context, playlistID string, tracks []playlist.Track) (added, skipped int, err error) {
	var ids []string
	for _, t := range tracks {
		id := t.Meta(c.d.metaKey())
		if id == "" {
			skipped++
			continue
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return 0, skipped, nil
	}
	// Invalidate after any attempt: a request that failed client-side (cut
	// response, cancelled context) may still have been applied by the
	// server. Only a confirmed add proves the playlist endpoints serve.
	defer func() {
		c.mu.Lock()
		c.playlistCache = nil
		delete(c.trackCache, playlistID)
		if added > 0 {
			c.clearPlaylistMemo()
		}
		c.mu.Unlock()
	}()
	var dummy struct{}
	if c.d.singleSongPlaylistAdd() {
		// One call per song means a failure can land mid-way. Say how far it
		// got: callers that surface only the error would otherwise invite a
		// retry that appends the first tracks a second time.
		for _, id := range ids {
			params := url.Values{"playlistId": {playlistID}, "songIdToAdd": {id}}
			if err := c.subsonicGet(ctx, "updatePlaylist", params, &dummy); err != nil {
				if added > 0 {
					return added, skipped, fmt.Errorf("%s: added %d of %d tracks before failing (retrying would duplicate them): %w", c.prefix(), added, len(ids), err)
				}
				return added, skipped, err
			}
			added++
		}
		return added, skipped, nil
	}
	params := url.Values{"playlistId": {playlistID}}
	for _, id := range ids {
		params.Add("songIdToAdd", id)
	}
	if err := c.subsonicGet(ctx, "updatePlaylist", params, &dummy); err != nil {
		return 0, skipped, err
	}
	return len(ids), skipped, nil
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

func (c *Client) songToTrack(s subsonicSong) playlist.Track {
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
		ProviderMeta: map[string]string{c.d.metaKey(): s.ID},
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

func albumFromSubsonic(a subsonicAlbum) provider.AlbumInfo {
	return provider.AlbumInfo{
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
// Without a format parameter the server applies its own transcoding policy
// (Navidrome: the player's setting, else the original file); format=raw
// (Subsonic 1.9.0+) always requests the original. The token in the URL is
// password-derived and never expires, so the URL is safe to keep in
// Track.Path; servers that redirect to short-lived CDN URLs (Bandcamp) are
// re-followed on every GET, so the post-redirect URL must never be cached.
func (c *Client) streamURL(id string) string {
	params := url.Values{"id": {id}}
	if c.streamFormat != "" {
		params.Set("format", c.streamFormat)
	}
	return c.buildURL("stream", params)
}

func (c *Client) CanReportPlayback(track playlist.Track) bool {
	return !c.scrobbleOff && track.Meta(c.d.metaKey()) != ""
}

func (c *Client) ReportNowPlaying(track playlist.Track, _ time.Duration, _ bool) error {
	return c.scrobble(track.Meta(c.d.metaKey()), false)
}

func (c *Client) ReportScrobble(track playlist.Track, _, _ time.Duration, _ bool) error {
	return c.scrobble(track.Meta(c.d.metaKey()), true)
}

// scrobble reports playback of a track to the Subsonic server.
// If submission is false, it registers a "now playing" notification only.
// If submission is true, it records a full play (updates play count, last.fm, etc.).
// The call is best-effort: the error is returned for logging, never acted on.
func (c *Client) scrobble(id string, submission bool) error {
	if id == "" {
		return nil
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
	resp, err := c.get(context.Background(), c.buildURL("scrobble", params))
	if err != nil {
		return fmt.Errorf("%s: scrobble: %w", c.prefix(), err)
	}
	resp.Body.Close()
	return nil
}
