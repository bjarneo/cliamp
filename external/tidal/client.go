package tidal

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	apiBaseURL = "https://api.tidal.com/v1/"

	// apiUA mirrors the Android WebView user agent python-tidal sends; the
	// private API is only exercised by official clients, so a generic Go UA
	// would stand out.
	apiUA         = "Mozilla/5.0 (Linux; Android 12; wv) AppleWebKit/537.36 (KHTML, like Gecko) Version/4.0 Chrome/91.0.4472.114 Safari/537.36"
	clientVersion = "2025.7.16"
)

// maxResponseBody limits JSON API responses to 20 MB.
const maxResponseBody = 20 << 20

// pageSize is the per-request page size for paginated list endpoints. Tidal
// caps most v1 list endpoints at 100 items per request.
const pageSize = 100

// tokenSlack is how long before the recorded expiry a token is refreshed, so
// requests never race the actual expiry.
const tokenSlack = time.Minute

// client is a Tidal private-API client. Token state is guarded by mu; the
// remaining fields are not mutated after construction.
type client struct {
	clientID     string
	clientSecret string
	baseURL      string // apiBaseURL in production; overridden in tests
	tokenURL     string // defaultTokenURL in production; overridden in tests
	http         *http.Client

	mu           sync.Mutex
	accessToken  string
	refreshToken string
	tokenType    string
	expiresAt    time.Time
	userID       string
	countryCode  string
	sessionID    string
}

func newClient(clientID, clientSecret string) *client {
	return &client{
		clientID:     clientID,
		clientSecret: clientSecret,
		baseURL:      apiBaseURL,
		tokenURL:     defaultTokenURL,
		http:         &http.Client{Timeout: 30 * time.Second},
	}
}

// credsFromClient snapshots the client's token state for persistence.
func credsFromClient(c *client) *storedCreds {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.credsLocked()
}

// credsLocked is credsFromClient for callers already holding c.mu.
func (c *client) credsLocked() *storedCreds {
	return &storedCreds{
		ClientID:     c.clientID,
		ClientSecret: c.clientSecret,
		AccessToken:  c.accessToken,
		RefreshToken: c.refreshToken,
		TokenType:    c.tokenType,
		ExpiresAt:    c.expiresAt,
		UserID:       c.userID,
		CountryCode:  c.countryCode,
	}
}

// applyTokenLocked stores a token response. The refresh token is only
// replaced when the response carries one (refresh responses usually omit it).
// Callers must hold c.mu.
func (c *client) applyTokenLocked(tok tokenResponse) {
	c.accessToken = tok.AccessToken
	c.tokenType = tok.TokenType
	if c.tokenType == "" {
		c.tokenType = "Bearer"
	}
	if tok.RefreshToken != "" {
		c.refreshToken = tok.RefreshToken
	}
	c.expiresAt = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
}

// ensureToken refreshes the access token when it is missing, expired, or
// equal to staleToken (the token a request just got a 401 for). Holding mu
// across the refresh single-flights it: concurrent callers block, then see a
// fresh token that differs from their stale one and return without another
// round-trip. Rotated tokens are persisted so later launches stay silent.
func (c *client) ensureToken(ctx context.Context, staleToken string) error {
	c.mu.Lock()
	if c.accessToken != "" && c.accessToken != staleToken && time.Now().Before(c.expiresAt.Add(-tokenSlack)) {
		c.mu.Unlock()
		return nil
	}
	if c.refreshToken == "" {
		c.mu.Unlock()
		return fmt.Errorf("tidal: no refresh token stored")
	}
	tok, err := requestToken(ctx, c.http, c.tokenURL, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {c.refreshToken},
		"client_id":     {c.clientID},
		"client_secret": {c.clientSecret},
		"scope":         {oauthScope},
	})
	if err != nil {
		c.mu.Unlock()
		return fmt.Errorf("tidal: refresh token: %w", err)
	}
	c.applyTokenLocked(tok)
	creds := c.credsLocked()
	c.mu.Unlock()
	_ = saveCreds(creds)
	return nil
}

// rateLimitRetries is how many times a 429 response is retried (honoring
// Retry-After) before giving up.
const rateLimitRetries = 2

// doGet performs an authenticated GET against the private API and decodes the
// JSON response into out. A 401 triggers one forced token refresh and retry;
// a 429 is retried with backoff.
func (c *client) doGet(ctx context.Context, path string, params url.Values, out any) error {
	body, err := c.doRequest(ctx, path, params)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("tidal: %s: decode: %w", path, err)
	}
	return nil
}

func (c *client) doRequest(ctx context.Context, path string, params url.Values) ([]byte, error) {
	refreshed := false
	rateRetries := 0
	for {
		if err := c.ensureToken(ctx, ""); err != nil {
			return nil, err
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
		if err != nil {
			return nil, fmt.Errorf("tidal: %s: build request: %w", path, err)
		}

		q := url.Values{}
		for k, vs := range params {
			q[k] = vs
		}
		c.mu.Lock()
		if c.countryCode != "" {
			q.Set("countryCode", c.countryCode)
		}
		if c.sessionID != "" {
			q.Set("sessionId", c.sessionID)
		}
		token := c.accessToken
		auth := c.tokenType + " " + token
		c.mu.Unlock()
		req.URL.RawQuery = q.Encode()
		req.Header.Set("User-Agent", apiUA)
		req.Header.Set("x-tidal-client-version", clientVersion)
		req.Header.Set("Authorization", auth)

		resp, err := c.http.Do(req)
		if err != nil {
			return nil, fmt.Errorf("tidal: %s: request: %w", path, err)
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("tidal: %s: read response: %w", path, err)
		}

		switch {
		case resp.StatusCode == http.StatusUnauthorized && !refreshed:
			if err := c.ensureToken(ctx, token); err != nil {
				return nil, err
			}
			refreshed = true
			continue
		case resp.StatusCode == http.StatusTooManyRequests && rateRetries < rateLimitRetries:
			rateRetries++
			select {
			case <-time.After(retryAfter(resp, rateRetries)):
				continue
			case <-ctx.Done():
				return nil, fmt.Errorf("tidal: %s: %w", path, ctx.Err())
			}
		case resp.StatusCode >= 400:
			return nil, fmt.Errorf("tidal: %s: HTTP %d: %s", path, resp.StatusCode, strings.TrimSpace(string(body)))
		}
		return body, nil
	}
}

// retryAfter returns the server-requested backoff for a 429 response, or an
// attempt-scaled default when the header is absent or unparseable.
func retryAfter(resp *http.Response, attempt int) time.Duration {
	if v := resp.Header.Get("Retry-After"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs > 0 && secs <= 60 {
			return time.Duration(secs) * time.Second
		}
	}
	return time.Duration(attempt) * time.Second
}

// loadSession fetches the current session (validating the token) and stores
// the session ID, country code, and user ID the catalog endpoints need.
func (c *client) loadSession(ctx context.Context) error {
	var out struct {
		SessionID   string      `json:"sessionId"`
		CountryCode string      `json:"countryCode"`
		UserID      json.Number `json:"userId"`
	}
	if err := c.doGet(ctx, "sessions", nil, &out); err != nil {
		return err
	}
	c.mu.Lock()
	c.sessionID = out.SessionID
	c.countryCode = out.CountryCode
	c.userID = out.UserID.String()
	c.mu.Unlock()
	return nil
}

// user returns the authenticated user's ID.
func (c *client) user() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.userID
}

// fetchList follows limit/offset pagination for a list endpoint. maxItems
// caps the total (0 = no cap). The offset advances by the number of items
// actually returned, so a short page mid-stream does not skip entries.
func fetchList[T any](ctx context.Context, c *client, path string, maxItems int) ([]T, error) {
	var all []T
	// The next offset is always the number of items fetched so far.
	for offset := 0; ; offset = len(all) {
		p := url.Values{
			"limit":  {strconv.Itoa(pageSize)},
			"offset": {strconv.Itoa(offset)},
		}

		var out apiList[T]
		if err := c.doGet(ctx, path, p, &out); err != nil {
			return nil, err
		}
		all = append(all, out.Items...)
		if maxItems > 0 && len(all) >= maxItems {
			return all[:maxItems], nil
		}
		switch {
		case len(out.Items) == 0:
			return all, nil
		case out.TotalNumberOfItems > 0 && len(all) >= out.TotalNumberOfItems:
			return all, nil
		case len(out.Items) < pageSize && out.TotalNumberOfItems <= 0:
			// Partial page with no reported total: assume the end.
			return all, nil
		}
	}
}

// unwrapFavorites strips the {created, item} wrapper of favorites responses.
func unwrapFavorites[T any](in []apiFavoriteItem[T]) []T {
	out := make([]T, len(in))
	for i, f := range in {
		out[i] = f.Item
	}
	return out
}

// userPlaylists returns the user's own playlists plus playlists they have
// favorited ("subscribed to"), which the plain /playlists endpoint omits.
func (c *client) userPlaylists(ctx context.Context) ([]apiPlaylist, error) {
	items, err := fetchList[apiPlaylistItem](ctx, c, "users/"+c.user()+"/playlistsAndFavoritePlaylists", 0)
	if err != nil {
		return nil, err
	}
	out := make([]apiPlaylist, len(items))
	for i, it := range items {
		out[i] = it.Playlist
	}
	return out, nil
}

// playlistTracks returns the tracks of a playlist, following pagination.
func (c *client) playlistTracks(ctx context.Context, playlistUUID string) ([]apiTrack, error) {
	return fetchList[apiTrack](ctx, c, "playlists/"+url.PathEscape(playlistUUID)+"/tracks", 0)
}

// favoriteTracks returns the user's favorite tracks, capped at maxItems.
func (c *client) favoriteTracks(ctx context.Context, maxItems int) ([]apiTrack, error) {
	items, err := fetchList[apiFavoriteItem[apiTrack]](ctx, c, "users/"+c.user()+"/favorites/tracks", maxItems)
	if err != nil {
		return nil, err
	}
	return unwrapFavorites(items), nil
}

// favoriteAlbums returns one page of the user's favorite albums.
func (c *client) favoriteAlbums(ctx context.Context, offset, limit int) ([]apiAlbum, error) {
	if limit <= 0 || limit > pageSize {
		limit = pageSize
	}
	params := url.Values{
		"limit":  {strconv.Itoa(limit)},
		"offset": {strconv.Itoa(offset)},
	}
	var out apiList[apiFavoriteItem[apiAlbum]]
	if err := c.doGet(ctx, "users/"+c.user()+"/favorites/albums", params, &out); err != nil {
		return nil, err
	}
	return unwrapFavorites(out.Items), nil
}

// favoriteArtists returns all of the user's favorite artists.
func (c *client) favoriteArtists(ctx context.Context) ([]apiArtist, error) {
	items, err := fetchList[apiFavoriteItem[apiArtist]](ctx, c, "users/"+c.user()+"/favorites/artists", 0)
	if err != nil {
		return nil, err
	}
	return unwrapFavorites(items), nil
}

// artistAlbums returns the albums of an artist, following pagination.
func (c *client) artistAlbums(ctx context.Context, artistID string) ([]apiAlbum, error) {
	return fetchList[apiAlbum](ctx, c, "artists/"+url.PathEscape(artistID)+"/albums", 0)
}

// albumGet returns an album's metadata.
func (c *client) albumGet(ctx context.Context, albumID string) (apiAlbum, error) {
	var out apiAlbum
	if err := c.doGet(ctx, "albums/"+url.PathEscape(albumID), nil, &out); err != nil {
		return apiAlbum{}, err
	}
	return out, nil
}

// albumTracks returns the tracks of an album.
func (c *client) albumTracks(ctx context.Context, albumID string) ([]apiTrack, error) {
	return fetchList[apiTrack](ctx, c, "albums/"+url.PathEscape(albumID)+"/tracks", 0)
}

// searchTracks searches the Tidal catalog for tracks.
func (c *client) searchTracks(ctx context.Context, query string, limit int) ([]apiTrack, error) {
	if limit <= 0 || limit > pageSize {
		limit = pageSize
	}
	params := url.Values{
		"query": {query},
		"limit": {strconv.Itoa(limit)},
	}
	var out apiList[apiTrack]
	if err := c.doGet(ctx, "search/tracks", params, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// searchAlbums searches the Tidal catalog for albums.
func (c *client) searchAlbums(ctx context.Context, query string, limit int) ([]apiAlbum, error) {
	if limit <= 0 || limit > pageSize {
		limit = pageSize
	}
	params := url.Values{
		"query": {query},
		"limit": {strconv.Itoa(limit)},
	}
	var out apiList[apiAlbum]
	if err := c.doGet(ctx, "search/albums", params, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// playbackInfo returns the playback manifest for a track at the given quality.
func (c *client) playbackInfo(ctx context.Context, trackID, quality string) (apiPlaybackInfo, error) {
	params := url.Values{
		"playbackmode":      {"STREAM"},
		"audioquality":      {quality},
		"assetpresentation": {"FULL"},
	}
	var out apiPlaybackInfo
	if err := c.doGet(ctx, "tracks/"+url.PathEscape(trackID)+"/playbackinfopostpaywall", params, &out); err != nil {
		return apiPlaybackInfo{}, err
	}
	return out, nil
}
