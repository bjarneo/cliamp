package qobuz

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	apiBaseURL     = "https://www.qobuz.com/api.json/0.2/"
	apiUA          = "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:83.0) Gecko/20100101 Firefox/83.0"
	defaultQuality = 6
)

// validQuality reports whether quality is one of the Qobuz format_id values
// cliamp accepts.
//
//	5  = MP3 320kbps
//	6  = FLAC 16-bit/44.1kHz (CD)
//	7  = FLAC 24-bit up to 96kHz
//	27 = FLAC 24-bit up to 192kHz (Hi-Res)
func validQuality(quality int) bool {
	switch quality {
	case 5, 6, 7, 27:
		return true
	default:
		return false
	}
}

// maxResponseBody limits JSON API responses to 20 MB.
const maxResponseBody = 20 << 20

// client is a Qobuz API client. It is safe for concurrent use once
// authenticated (its fields are not mutated after login).
type client struct {
	appID   string
	secrets []string // candidate signing secrets to validate
	secret  string   // validated signing secret (set by validateSecret)
	uat     string   // user_auth_token
	userID  string
	label   string // subscription tier short label
	http    *http.Client
}

func newClient(appID string, secrets []string) *client {
	return &client{
		appID:   appID,
		secrets: secrets,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// doRequest performs a Qobuz API request and returns the raw response body.
func (c *client) doRequest(ctx context.Context, method, endpoint string, params url.Values, body string) ([]byte, error) {
	var reqBody io.Reader
	if body != "" {
		reqBody = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, apiBaseURL+endpoint, reqBody)
	if err != nil {
		return nil, fmt.Errorf("qobuz: %s: build request: %w", endpoint, err)
	}
	req.Header.Set("User-Agent", apiUA)
	req.Header.Set("X-App-Id", c.appID)
	if body != "" {
		// Request bodies are always form-encoded (user/login, oauth/callback).
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=UTF-8")
	} else {
		req.Header.Set("Content-Type", "application/json;charset=UTF-8")
	}
	if c.uat != "" {
		req.Header.Set("X-User-Auth-Token", c.uat)
	}
	if params != nil {
		req.URL.RawQuery = params.Encode()
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("qobuz: %s: request: %w", endpoint, err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return nil, fmt.Errorf("qobuz: %s: read response: %w", endpoint, err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("qobuz: %s: HTTP %d: %s", endpoint, resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return respBody, nil
}

// doGet performs a GET and decodes the JSON response into out.
func (c *client) doGet(ctx context.Context, endpoint string, params url.Values, out any) error {
	body, err := c.doRequest(ctx, http.MethodGet, endpoint, params, "")
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("qobuz: %s: decode: %w", endpoint, err)
	}
	return nil
}

// authWithToken authenticates using a user_id + user_auth_token obtained via
// OAuth and populates the client's user info.
func (c *client) authWithToken(ctx context.Context, userID, userAuthToken string) error {
	params := url.Values{
		"user_id":         {userID},
		"user_auth_token": {userAuthToken},
		"app_id":          {c.appID},
	}
	var info loginResponse
	if err := c.doGet(ctx, "user/login", params, &info); err != nil {
		return err
	}
	c.uat = userAuthToken
	c.userID = userID
	return c.applyUserInfo(info)
}

// loginResponse is the shape of user/login and oauth/callback responses.
type loginResponse struct {
	UserAuthToken string `json:"user_auth_token"`
	User          struct {
		ID         json.Number `json:"id"`
		Credential struct {
			Parameters *struct {
				ShortLabel string `json:"short_label"`
			} `json:"parameters"`
		} `json:"credential"`
	} `json:"user"`
}

func (c *client) applyUserInfo(info loginResponse) error {
	if info.User.Credential.Parameters == nil {
		return fmt.Errorf("qobuz: account is not eligible for streaming (free accounts cannot stream)")
	}
	if c.uat == "" {
		c.uat = info.UserAuthToken
	}
	if c.userID == "" && info.User.ID != "" {
		c.userID = info.User.ID.String()
	}
	c.label = info.User.Credential.Parameters.ShortLabel
	return nil
}

// loginWithOAuth completes authentication from an OAuth redirect result. Qobuz
// may return either a user_auth_token directly or a code that must be exchanged.
func (c *client) loginWithOAuth(ctx context.Context, result oauthResult, privateKey string) error {
	if result.Token != "" {
		c.uat = result.Token
		if result.UserID != "" {
			c.userID = result.UserID
		}
		return c.loadOAuthUserInfo(ctx, "with OAuth token")
	}
	if result.Code != "" {
		return c.exchangeOAuthCode(ctx, result.Code, privateKey)
	}
	return fmt.Errorf("qobuz: OAuth redirect contained neither token nor code")
}

// exchangeOAuthCode exchanges an OAuth code for a token. Qobuz has used
// different parameter names and HTTP methods over time, so all combinations of
// (GET|POST) x ("code"|"code_autorisation") are tried.
func (c *client) exchangeOAuthCode(ctx context.Context, code, privateKey string) error {
	type attempt struct {
		method    string
		paramName string
	}
	attempts := []attempt{
		{http.MethodGet, "code"},
		{http.MethodPost, "code"},
		{http.MethodGet, "code_autorisation"},
		{http.MethodPost, "code_autorisation"},
	}

	var lastErr error
	for _, a := range attempts {
		params := url.Values{
			a.paramName: {code},
			"app_id":    {c.appID},
		}
		if privateKey != "" {
			params.Set("private_key", privateKey)
		}

		var body []byte
		var err error
		if a.method == http.MethodGet {
			body, err = c.doRequest(ctx, http.MethodGet, "oauth/callback", params, "")
		} else {
			body, err = c.doRequest(ctx, http.MethodPost, "oauth/callback", nil, params.Encode())
		}
		if err != nil {
			lastErr = err
			continue
		}

		var resp struct {
			Token string `json:"token"`
			loginResponse
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			lastErr = err
			continue
		}
		if resp.Token == "" {
			if resp.User.Credential.Parameters != nil {
				return c.applyUserInfo(resp.loginResponse)
			}
			lastErr = fmt.Errorf("qobuz: no token in oauth/callback response")
			continue
		}

		c.uat = resp.Token
		return c.loadOAuthUserInfo(ctx, "after OAuth")
	}
	return fmt.Errorf("qobuz: oauth code exchange failed: %w", lastErr)
}

func (c *client) loadOAuthUserInfo(ctx context.Context, phase string) error {
	body, err := c.doRequest(ctx, http.MethodPost, "user/login", nil, "extra=partner")
	if err != nil {
		return fmt.Errorf("qobuz: user/login %s: %w", phase, err)
	}
	var info loginResponse
	if err := json.Unmarshal(body, &info); err != nil {
		return fmt.Errorf("qobuz: decode user/login: %w", err)
	}
	return c.applyUserInfo(info)
}

// validateSecret picks the first signing secret that the API accepts and stores
// it on the client. It must be called before any signed request (getFileUrl,
// favorites).
func (c *client) validateSecret(ctx context.Context) error {
	if c.secret != "" {
		return nil
	}
	for _, secret := range c.secrets {
		if secret == "" {
			continue
		}
		// 5966783 is a known public track id used purely to probe the secret.
		if _, err := c.trackFileURL(ctx, "5966783", 5, secret); err == nil {
			c.secret = secret
			return nil
		}
	}
	return fmt.Errorf("qobuz: no valid signing secret found")
}

// apiFileURL is the track/getFileUrl response.
//
// We use track/getFileUrl (the legacy endpoint) on purpose: it returns a plain
// "url" pointing at a complete FLAC/MP3 file that the buffered ffmpeg pipeline
// can stream directly. The current web player instead uses /file/url +
// /session/start, which returns segmented, AES-128-CTR-encrypted CMAF (qbz-1)
// requiring a full key-derivation + per-frame decryption pipeline. Per the
// SofusA/qobine reverse-engineering notes, getFileUrl "may still work but the
// web player now uses /file/url", so this is a known, monitored assumption.
type apiFileURL struct {
	URL          string  `json:"url"`
	FormatID     int     `json:"format_id"`
	MimeType     string  `json:"mime_type"`
	Duration     int     `json:"duration"`
	SamplingRate float64 `json:"sampling_rate"`
	BitDepth     int     `json:"bit_depth"`
}

// trackFileURLSig computes the request_sig for track/getFileUrl. Qobuz signs
// the concatenation of the endpoint path, the params in alphabetical order, the
// request timestamp and the app secret. The exact layout matters: a change here
// silently breaks streaming, so it's pinned by a test.
func trackFileURLSig(trackID string, formatID int, ts, secret string) string {
	raw := fmt.Sprintf("trackgetFileUrlformat_id%dintentstreamtrack_id%s%s%s",
		formatID, trackID, ts, secret)
	return md5hex(raw)
}

// trackFileURL returns a signed streaming URL for the given track. If
// secretOverride is empty, the validated client secret is used.
func (c *client) trackFileURL(ctx context.Context, trackID string, formatID int, secretOverride string) (apiFileURL, error) {
	if !validQuality(formatID) {
		return apiFileURL{}, fmt.Errorf("qobuz: invalid quality %d (choose 5, 6, 7 or 27)", formatID)
	}
	secret := secretOverride
	if secret == "" {
		secret = c.secret
	}
	unix := strconv.FormatInt(time.Now().Unix(), 10)
	params := url.Values{
		"request_ts":  {unix},
		"request_sig": {trackFileURLSig(trackID, formatID, unix, secret)},
		"track_id":    {trackID},
		"format_id":   {strconv.Itoa(formatID)},
		"intent":      {"stream"},
	}
	var out apiFileURL
	if err := c.doGet(ctx, "track/getFileUrl", params, &out); err != nil {
		return apiFileURL{}, err
	}
	return out, nil
}

// userPlaylists returns the authenticated user's playlists.
func (c *client) userPlaylists(ctx context.Context) ([]apiPlaylist, error) {
	var out struct {
		Playlists apiPlaylistList `json:"playlists"`
	}
	params := url.Values{"limit": {"500"}, "offset": {"0"}}
	if err := c.doGet(ctx, "playlist/getUserPlaylists", params, &out); err != nil {
		return nil, err
	}
	return out.Playlists.Items, nil
}

// playlistTracks returns the tracks of a playlist, following pagination.
func (c *client) playlistTracks(ctx context.Context, playlistID string) ([]apiTrack, error) {
	const pageSize = 500
	var all []apiTrack
	for offset := 0; ; offset += pageSize {
		var out apiPlaylist
		params := url.Values{
			"playlist_id": {playlistID},
			"extra":       {"tracks"},
			"limit":       {strconv.Itoa(pageSize)},
			"offset":      {strconv.Itoa(offset)},
		}
		if err := c.doGet(ctx, "playlist/get", params, &out); err != nil {
			return nil, err
		}
		if out.Tracks == nil || len(out.Tracks.Items) == 0 {
			break
		}
		all = append(all, out.Tracks.Items...)
		if offset+pageSize >= out.Tracks.Total {
			break
		}
	}
	return all, nil
}

// albumTracks returns the tracks of an album along with the album metadata.
func (c *client) albumGet(ctx context.Context, albumID string) (apiAlbum, error) {
	var out apiAlbum
	if err := c.doGet(ctx, "album/get", url.Values{"album_id": {albumID}}, &out); err != nil {
		return apiAlbum{}, err
	}
	return out, nil
}

// signedFavorites builds the request_ts/request_sig params for favorites calls.
//
// Note: favorite/getUserFavorites signs only object+method+ts+secret; the
// query params (type/limit/offset) are deliberately NOT folded into the
// signature, matching the working qobuz-dl-go behavior. The qobine reference
// documents a generic "sorted params" rule, but getUserFavorites does not
// require it in practice; do not add the params to rawSig.
func (c *client) favoriteParams(favType string, offset, limit int) url.Values {
	unix := strconv.FormatInt(time.Now().Unix(), 10)
	rawSig := "favoritegetUserFavorites" + unix + c.secret
	return url.Values{
		"app_id":          {c.appID},
		"user_auth_token": {c.uat},
		"type":            {favType},
		"request_ts":      {unix},
		"request_sig":     {md5hex(rawSig)},
		"limit":           {strconv.Itoa(limit)},
		"offset":          {strconv.Itoa(offset)},
	}
}

// favoriteTracks returns the user's favorite tracks.
func (c *client) favoriteTracks(ctx context.Context, offset, limit int) ([]apiTrack, error) {
	var out struct {
		Tracks apiTrackList `json:"tracks"`
	}
	if err := c.doGet(ctx, "favorite/getUserFavorites", c.favoriteParams("tracks", offset, limit), &out); err != nil {
		return nil, err
	}
	return out.Tracks.Items, nil
}

// favoriteAlbums returns the user's favorite albums.
func (c *client) favoriteAlbums(ctx context.Context, offset, limit int) ([]apiAlbum, error) {
	var out struct {
		Albums apiAlbumList `json:"albums"`
	}
	if err := c.doGet(ctx, "favorite/getUserFavorites", c.favoriteParams("albums", offset, limit), &out); err != nil {
		return nil, err
	}
	return out.Albums.Items, nil
}

// favoriteArtists returns the user's favorite artists.
func (c *client) favoriteArtists(ctx context.Context, offset, limit int) ([]apiArtist, error) {
	var out struct {
		Artists apiArtistList `json:"artists"`
	}
	if err := c.doGet(ctx, "favorite/getUserFavorites", c.favoriteParams("artists", offset, limit), &out); err != nil {
		return nil, err
	}
	return out.Artists.Items, nil
}

// artistAlbums returns the albums for an artist, following pagination.
func (c *client) artistAlbums(ctx context.Context, artistID string) ([]apiAlbum, error) {
	const pageSize = 500
	var all []apiAlbum
	for offset := 0; ; offset += pageSize {
		var out struct {
			apiArtist
			Albums apiAlbumList `json:"albums"`
		}
		params := url.Values{
			"app_id":    {c.appID},
			"artist_id": {artistID},
			"extra":     {"albums"},
			"limit":     {strconv.Itoa(pageSize)},
			"offset":    {strconv.Itoa(offset)},
		}
		if err := c.doGet(ctx, "artist/get", params, &out); err != nil {
			return nil, err
		}
		if len(out.Albums.Items) == 0 {
			break
		}
		all = append(all, out.Albums.Items...)
		if offset+pageSize >= out.Albums.Total {
			break
		}
	}
	return all, nil
}

// searchTracks searches the Qobuz catalog for tracks.
func (c *client) searchTracks(ctx context.Context, query string, limit int) ([]apiTrack, error) {
	var out struct {
		Tracks apiTrackList `json:"tracks"`
	}
	params := url.Values{"query": {query}, "limit": {strconv.Itoa(limit)}}
	if err := c.doGet(ctx, "track/search", params, &out); err != nil {
		return nil, err
	}
	return out.Tracks.Items, nil
}
