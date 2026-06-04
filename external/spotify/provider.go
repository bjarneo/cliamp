//go:build !windows

package spotify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	librespot "github.com/devgianlu/go-librespot"
	"github.com/devgianlu/go-librespot/audio"
	"github.com/gopxl/beep/v2"

	"cliamp/applog"
	"cliamp/playlist"
	"cliamp/provider"
)

// Compile-time interface checks.
var (
	_ provider.Searcher        = (*SpotifyProvider)(nil)
	_ provider.PlaylistWriter  = (*SpotifyProvider)(nil)
	_ provider.PlaylistCreator = (*SpotifyProvider)(nil)
	_ provider.CustomStreamer  = (*SpotifyProvider)(nil)
	_ provider.Closer          = (*SpotifyProvider)(nil)
)

// maxResponseBody limits JSON API responses to 10 MB.
// SpotifyProvider implements playlist.Provider using the Spotify Web API
// for playlist/track metadata and go-librespot for audio streaming.
// playlistCache holds a snapshot_id and the fetched tracks for a playlist,
// allowing us to skip re-fetching playlists that haven't changed.
type playlistCache struct {
	snapshotID string
	tracks     []playlist.Track
}

type SpotifyProvider struct {
	session    *Session
	clientID   string
	bitrate    int
	userID     string // Spotify user ID, fetched lazily on first Playlists() call
	meFetched  bool   // /v1/me has been attempted this session; suppresses retry on failure
	mu         sync.Mutex
	trackCache map[string]*playlistCache // playlist ID → cache entry
	authCancel context.CancelFunc        // cancels any in-progress OAuth flow

	// Playlist list cache to avoid redundant API calls on provider switch.
	listCache   []playlist.PlaylistInfo
	listCacheAt time.Time
}

const playlistListCacheTTL = 5 * time.Minute

// New creates a SpotifyProvider. If session is nil, authentication is
// deferred until the user first selects the Spotify provider.
// bitrate sets the preferred Spotify stream quality in kbps (96, 160, or 320).
func New(session *Session, clientID string, bitrate int) *SpotifyProvider {
	return &SpotifyProvider{
		session:    session,
		clientID:   clientID,
		bitrate:    bitrate,
		trackCache: make(map[string]*playlistCache),
	}
}

// ensureSession tries to create a session using stored credentials only
// (no browser). Returns playlist.ErrNeedsAuth if interactive sign-in is needed.
func (p *SpotifyProvider) ensureSession() error {
	p.mu.Lock()
	if p.session != nil {
		p.mu.Unlock()
		return nil
	}
	clientID := p.clientID
	p.mu.Unlock()

	if clientID == "" {
		return fmt.Errorf("spotify: no client ID available")
	}
	sess, err := NewSessionSilent(context.Background(), clientID)
	if err != nil {
		return playlist.ErrNeedsAuth
	}
	p.mu.Lock()
	p.session = sess
	p.resetSessionScopedStateLocked()
	p.mu.Unlock()
	return nil
}

// Authenticate runs the interactive sign-in flow (opens browser, waits for callback).
// Any previous in-progress OAuth flow is cancelled first to free the callback port.
func (p *SpotifyProvider) Authenticate() error {
	p.mu.Lock()
	if p.session != nil {
		p.mu.Unlock()
		return nil
	}
	if p.authCancel != nil {
		p.authCancel()
		p.authCancel = nil
	}
	clientID := p.clientID
	p.mu.Unlock()

	if clientID == "" {
		return fmt.Errorf("spotify: no client ID available")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	p.mu.Lock()
	p.authCancel = cancel
	p.mu.Unlock()

	sess, err := NewSession(ctx, clientID)

	p.mu.Lock()
	p.authCancel = nil
	p.mu.Unlock()
	cancel()

	if err != nil {
		return err
	}
	p.mu.Lock()
	p.session = sess
	p.resetSessionScopedStateLocked()
	p.mu.Unlock()
	return nil
}

// Close releases the session if one was created.
func (p *SpotifyProvider) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.authCancel != nil {
		p.authCancel()
		p.authCancel = nil
	}
	if p.session != nil {
		p.session.Close()
		p.session = nil
		p.resetSessionScopedStateLocked()
	}
}

// resetSessionScopedStateLocked clears /v1/me-derived caches when the session
// changes. p.mu must be held.
func (p *SpotifyProvider) resetSessionScopedStateLocked() {
	p.userID = ""
	p.meFetched = false
}

func (p *SpotifyProvider) Name() string { return "Spotify" }

// currentUserID returns the authenticated user's Spotify ID, fetched from
// /v1/me at most once per session. Failures are remembered so a network blip
// during the first call doesn't trigger a request on every later use.
// userID is used by playlistAccessible to filter playlists the user doesn't
// own (which 403 on Tracks() for dev-mode apps).
func (p *SpotifyProvider) currentUserID(ctx context.Context) string {
	p.mu.Lock()
	if p.meFetched {
		id := p.userID
		p.mu.Unlock()
		return id
	}
	p.mu.Unlock()

	var me struct {
		ID string `json:"id"`
	}
	if resp, err := p.webAPI(ctx, "GET", "/v1/me", nil); err == nil {
		_ = decodeBody(resp, &me)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.userID = me.ID
	p.meFetched = true
	return p.userID
}

// Playlists returns the authenticated user's Spotify playlists.
// Only playlists owned by the user or marked as collaborative are returned;
// playlists saved from other users are excluded because the Spotify API
// returns 403 when trying to list their tracks.
func (p *SpotifyProvider) Playlists() ([]playlist.PlaylistInfo, error) {
	if err := p.ensureSession(); err != nil {
		return nil, err
	}

	p.mu.Lock()
	if p.listCache != nil && time.Since(p.listCacheAt) < playlistListCacheTTL {
		cached := slices.Clone(p.listCache)
		p.mu.Unlock()
		return cached, nil
	}
	p.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	userID := p.currentUserID(ctx) // empty string if fetch fails → no filtering

	var all []playlist.PlaylistInfo
	offset := 0
	limit := spotifyPlaylistPageSize

	// List of Playlists only includes created playlists by the User.
	// This doesn't include the 'Liked Songs' playlist.
	resp, err := p.webAPI(ctx, "GET", "/v1/me/tracks", nil)
	if err != nil {
		return nil, fmt.Errorf("spotify: your music: %w", err)
	}

	var result struct {
		Total int `json:"total"`
	}
	if err := decodeBody(resp, &result); err != nil {
		return nil, fmt.Errorf("spotify: parse playlists: %w", err)
	}

	// Unfortunately, the Spotify API doesn't expose the localized display name.
	// i.e. 'Liked Songs' or 'Lieblingssongs' etc.
	// For the moment, "Your Music" must sufficice without adding a localization
	// map.
	all = append(all, playlist.PlaylistInfo{
		ID:         "YOUR MUSIC",
		Name:       "Your Music",
		TrackCount: result.Total,
		Section:    "Library",
	})

	for {
		query := url.Values{
			"limit":  {fmt.Sprintf("%d", limit)},
			"offset": {fmt.Sprintf("%d", offset)},
			// Include owner.id and collaborative to filter inaccessible playlists.
			"fields": {"items(id,name,snapshot_id,collaborative,owner(id),items.total),total"},
		}

		resp, err := p.webAPI(ctx, "GET", "/v1/me/playlists", query)
		if err != nil {
			return nil, fmt.Errorf("spotify: list playlists: %w", err)
		}

		var result struct {
			Items []spotifyPlaylistItem `json:"items"`
			Total int                   `json:"total"`
		}
		if err := decodeBody(resp, &result); err != nil {
			return nil, fmt.Errorf("spotify: parse playlists: %w", err)
		}

		p.mu.Lock()
		for _, item := range result.Items {
			if !playlistAccessible(item, userID) {
				continue
			}
			count := 0
			if item.Items != nil {
				count = item.Items.Total
			}
			section := "Followed playlists"
			if userID != "" && item.Owner.ID == userID {
				section = "Your playlists"
			}
			all = append(all, playlist.PlaylistInfo{
				ID:         item.ID,
				Name:       item.Name,
				TrackCount: count,
				Section:    section,
			})
			// Update snapshot_id in cache; if it changed, invalidate cached tracks.
			if cached, ok := p.trackCache[item.ID]; ok {
				if cached.snapshotID != item.SnapshotID {
					delete(p.trackCache, item.ID)
				}
			}
			// Store snapshot_id for later cache checks in Tracks().
			if _, ok := p.trackCache[item.ID]; !ok && item.SnapshotID != "" {
				p.trackCache[item.ID] = &playlistCache{snapshotID: item.SnapshotID}
			}
		}
		p.mu.Unlock()

		if offset+limit >= result.Total {
			break
		}
		offset += limit
	}

	// Group playlists by section so the UI can emit one header per group.
	// Library first, then owned, then followed; preserve API order within.
	sectionOrder := map[string]int{
		"Library":            0,
		"Your playlists":     1,
		"Followed playlists": 2,
	}
	sort.SliceStable(all, func(i, j int) bool {
		return sectionOrder[all[i].Section] < sectionOrder[all[j].Section]
	})

	p.mu.Lock()
	p.listCache = all
	p.listCacheAt = time.Now()
	p.mu.Unlock()

	return slices.Clone(all), nil
}

// Tracks returns all tracks for the given Spotify playlist ID.
// Track.Path is set to the canonical spotify: URI for the player to resolve.
// Results are cached by snapshot_id; unchanged playlists skip the API call.
func (p *SpotifyProvider) Tracks(playlistID string) ([]playlist.Track, error) {
	if err := p.ensureSession(); err != nil {
		return nil, err
	}
	// Check cache — if we have tracks and the snapshot_id hasn't changed, return cached.
	p.mu.Lock()
	if cached, ok := p.trackCache[playlistID]; ok && cached.tracks != nil {
		tracks := slices.Clone(cached.tracks)
		p.mu.Unlock()
		return tracks, nil
	}
	p.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	var all []playlist.Track
	offset := 0
	limit := spotifyTrackPageSize

	for {
		var (
			resp *http.Response
			err  error
		)

		if playlistID == "YOUR MUSIC" {
			query := url.Values{
				"limit":  {fmt.Sprintf("%d", limit)},
				"offset": {fmt.Sprintf("%d", offset)},
			}
			resp, err = p.webAPI(ctx, "GET", "/v1/me/tracks", query)
		} else {
			query := url.Values{
				"limit":  {fmt.Sprintf("%d", limit)},
				"offset": {fmt.Sprintf("%d", offset)},
				"fields": {"items(item(id,name,type,uri,artists(name),album(name,release_date),show(name),release_date,duration_ms,track_number,is_playable,restrictions(reason))),total"},
			}
			path := fmt.Sprintf("/v1/playlists/%s/items", playlistID)
			resp, err = p.webAPI(ctx, "GET", path, query)
		}

		if err != nil {
			if strings.Contains(err.Error(), "403") {
				return nil, fmt.Errorf("spotify: playlist not accessible: only playlists you own or collaborate on can be loaded")
			}
			return nil, fmt.Errorf("spotify: list tracks: %w", err)
		}

		var result struct {
			Items []struct {
				Item  *spotifyItem `json:"item"`
				Track *spotifyItem `json:"track"`
			} `json:"items"`
			Total int `json:"total"`
		}
		if err := decodeBody(resp, &result); err != nil {
			return nil, fmt.Errorf("spotify: parse tracks: %w", err)
		}

		for _, item := range result.Items {
			t := item.Item
			if t == nil {
				t = item.Track
			}
			if t == nil || t.ID == "" {
				continue // skip local/unavailable tracks
			}
			all = append(all, trackFromItem(t))
		}

		if offset+limit >= result.Total {
			break
		}
		offset += limit
	}

	// Cache the fetched tracks.
	p.mu.Lock()
	if cached, ok := p.trackCache[playlistID]; ok {
		cached.tracks = all
	} else {
		p.trackCache[playlistID] = &playlistCache{tracks: all}
	}
	p.mu.Unlock()

	return slices.Clone(all), nil
}

// isAuthError returns true if the error is an authentication/session-related
// failure that can be resolved by re-authenticating.
func isAuthError(err error) bool {
	if err == nil {
		return false
	}

	// context.DeadlineExceeded and context.Canceled are NOT auth errors.
	// They commonly fire during rapid track skipping when a previous NewStream's
	// network fetch is interrupted, and previously caused spurious re-auth
	// attempts (which then escalated to opening a browser tab mid-skip).
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return false
	}
	var keyErr *audio.KeyProviderError
	if errors.As(err, &keyErr) {
		return true
	}
	return false
}

// URISchemes returns the URI prefixes handled by this provider.
// Implements provider.CustomStreamer.
func (p *SpotifyProvider) URISchemes() []string { return []string{"spotify:"} }

// NewStreamer creates a SpotifyStreamer for the given spotify: URI (track or
// episode).
// If the stream fails due to an auth error (e.g. expired session, AES key
// rejection), the player tries a silent reconnect from cached credentials.
// If that fails — or the retry still hits an auth error — the streamer
// surfaces playlist.ErrNeedsAuth so the UI can prompt the user to sign in.
// We deliberately do NOT auto-launch a browser-based OAuth flow from this
// path: rapid track skipping can produce transient stream errors and a
// browser tab popping up mid-skip.
//
// Implements provider.CustomStreamer.
func (p *SpotifyProvider) NewStreamer(uri string) (beep.StreamSeekCloser, beep.Format, time.Duration, error) {
	if err := p.ensureSession(); err != nil {
		return nil, beep.Format{}, 0, err
	}
	spotID, err := librespot.SpotifyIdFromUri(uri)
	if err != nil {
		return nil, beep.Format{}, 0, fmt.Errorf("spotify: invalid URI %q: %w", uri, err)
	}

	tryStream := func() (*spotifyStreamer, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		stream, err := p.session.NewStream(ctx, *spotID, p.bitrate)
		if err != nil {
			return nil, err
		}
		return newSpotifyStreamer(stream), nil
	}

	s, err := tryStream()
	if err == nil {
		return s, s.Format(), s.Duration(), nil
	}
	if !isAuthError(err) {
		return nil, beep.Format{}, 0, fmt.Errorf("spotify: new stream: %w", err)
	}

	// Auth error — try a silent reconnect from cached credentials.
	applog.UserWarn("spotify: stream auth error (%v), attempting silent reconnect...", err)

	reconnCtx, reconnCancel := context.WithTimeout(context.Background(), 30*time.Second)
	reconnErr := p.session.Reconnect(reconnCtx)
	reconnCancel()

	if reconnErr != nil {
		applog.UserWarn("spotify: silent reconnect failed (%v); sign-in required", reconnErr)
		return nil, beep.Format{}, 0, fmt.Errorf("spotify: stream auth error, silent reconnect failed: %w", playlist.ErrNeedsAuth)
	}

	s, err = tryStream()
	if err == nil {
		return s, s.Format(), s.Duration(), nil
	}
	if !isAuthError(err) {
		return nil, beep.Format{}, 0, fmt.Errorf("spotify: new stream after silent reconnect: %w", err)
	}

	// Still failing after a silent reconnect — surface ErrNeedsAuth so the
	// UI can prompt the user to sign in. Do NOT open a browser from here.
	applog.UserWarn("spotify: stream still failing after silent reconnect (%v); sign-in required", err)
	return nil, beep.Format{}, 0, fmt.Errorf("spotify: stream auth error after silent reconnect: %w", playlist.ErrNeedsAuth)
}

// webAPI calls the Spotify Web API via the session with retry on 429.
func (p *SpotifyProvider) webAPI(ctx context.Context, method, path string, query url.Values) (*http.Response, error) {
	return p.webAPIWithBody(ctx, method, path, query, nil, "", http.StatusOK)
}

// webAPIWithBody is like webAPI but accepts an optional request body, content type,
// and a set of acceptable HTTP status codes (e.g. 200, 201). Retries 429 with
// exponential backoff (honoring Retry-After when present).
func (p *SpotifyProvider) webAPIWithBody(ctx context.Context, method, path string, query url.Values, body io.Reader, contentType string, acceptStatus ...int) (*http.Response, error) {
	const maxRetries = 8

	// Buffer the body so it can be replayed on retry.
	var bodyBytes []byte
	if body != nil {
		var err error
		bodyBytes, err = io.ReadAll(body)
		if err != nil {
			return nil, fmt.Errorf("read request body: %w", err)
		}
	}

	for attempt := range maxRetries {
		var reqBody io.Reader
		if bodyBytes != nil {
			reqBody = bytes.NewReader(bodyBytes)
		}

		resp, err := p.session.webApiWithBody(ctx, method, path, query, reqBody, contentType)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			resp.Body.Close()
			// On the last attempt there's no retry after the wait, so don't
			// sleep (up to 128s) just to give up; fail now.
			if attempt == maxRetries-1 {
				break
			}
			wait := time.Duration(1<<uint(attempt)) * time.Second
			if ra := resp.Header.Get("Retry-After"); ra != "" {
				if secs, err := strconv.Atoi(ra); err == nil && secs > 0 {
					wait = time.Duration(secs) * time.Second
				}
			}
			applog.UserWarn("spotify: web api rate-limited on %s, retrying in %v (attempt %d/%d)", path, wait, attempt+1, maxRetries)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(wait):
				continue
			}
		}

		ok := slices.Contains(acceptStatus, resp.StatusCode)
		if !ok {
			respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 512))
			resp.Body.Close()
			if readErr != nil {
				return nil, fmt.Errorf("http status %s (failed to read body: %v)", resp.Status, readErr)
			}
			return nil, fmt.Errorf("http status %s: %s", resp.Status, string(respBody))
		}
		return resp, nil
	}
	return nil, fmt.Errorf("spotify: web api rate-limited on %s after %d retries (try re-authenticating)", path, maxRetries)
}

// friendlySearchError rewrites Spotify's misleading 400 "Invalid limit" reply
// from /v1/search into something a user can act on. Since Nov 27, 2024 Spotify
// returns this error for developer apps registered in Development Mode — the
// rest of the API (playback, playlists, library) keeps working, but the
// catalog endpoints (/v1/search etc.) are blocked. The limit value is fine.
func friendlySearchError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if strings.Contains(msg, "400") && strings.Contains(msg, "Invalid limit") {
		return fmt.Errorf("spotify: search blocked — your client_id is too new. Spotify's Nov 27 2024 change blocks /v1/search for apps in Development Mode (the rest of cliamp still works on your app). Remove client_id from [spotify] in config.toml to use the built-in fallback for search, or apply for Extended Quota Mode")
	}
	return fmt.Errorf("spotify: search: %w", err)
}

// SearchTracks searches Spotify for tracks and podcast episodes, returning up
// to limit results of each. Episodes (e.g. podcasts) are routed through their
// spotify:episode: URI so they play correctly.
// limit is clamped to Spotify's accepted range of 1..50.
func (p *SpotifyProvider) SearchTracks(ctx context.Context, query string, limit int) ([]playlist.Track, error) {
	if err := p.ensureSession(); err != nil {
		return nil, err
	}

	if limit < 1 {
		limit = 1
	} else if limit > 50 {
		limit = 50
	}

	// No market parameter: when the request carries a user OAuth token, Spotify
	// implicitly scopes results to the account's country.
	q := url.Values{
		"q":     {query},
		"type":  {"track,episode"},
		"limit": {fmt.Sprintf("%d", limit)},
	}

	resp, err := p.webAPI(ctx, "GET", "/v1/search", q)
	if err != nil {
		return nil, friendlySearchError(err)
	}

	var result struct {
		Tracks struct {
			Items []*spotifyItem `json:"items"`
		} `json:"tracks"`
		Episodes struct {
			Items []*spotifyItem `json:"items"`
		} `json:"episodes"`
	}
	if err := decodeBody(resp, &result); err != nil {
		return nil, fmt.Errorf("spotify: parse search: %w", err)
	}

	var tracks []playlist.Track
	for _, items := range [][]*spotifyItem{result.Tracks.Items, result.Episodes.Items} {
		for _, t := range items {
			if t == nil || t.ID == "" {
				continue // skip null/unavailable results
			}
			tracks = append(tracks, trackFromItem(t))
		}
	}
	return tracks, nil
}

// AddTrackToPlaylist adds a track to an existing Spotify playlist.
// The track's Path is used as the Spotify URI (e.g. "spotify:track:..." or
// "spotify:episode:..."); the Spotify API accepts either.
// Implements provider.PlaylistWriter.
func (p *SpotifyProvider) AddTrackToPlaylist(ctx context.Context, playlistID string, track playlist.Track) error {
	trackURI := track.Path
	if err := p.ensureSession(); err != nil {
		return err
	}

	body, _ := json.Marshal(map[string]any{"uris": []string{trackURI}})
	path := fmt.Sprintf("/v1/playlists/%s/tracks", playlistID)

	resp, err := p.webAPIWithBody(ctx, "POST", path, nil, bytes.NewReader(body), "application/json", http.StatusOK, http.StatusCreated)
	if err != nil {
		return fmt.Errorf("spotify: add track: %w", err)
	}
	resp.Body.Close()

	// Invalidate caches for this playlist.
	p.mu.Lock()
	delete(p.trackCache, playlistID)
	p.listCache = nil
	p.mu.Unlock()

	return nil
}

// CreatePlaylist creates a new private Spotify playlist and returns its ID.
func (p *SpotifyProvider) CreatePlaylist(ctx context.Context, name string) (string, error) {
	if err := p.ensureSession(); err != nil {
		return "", err
	}

	userID := p.currentUserID(ctx)
	if userID == "" {
		return "", fmt.Errorf("spotify: could not determine user ID")
	}

	body, _ := json.Marshal(map[string]any{"name": name, "public": false})
	path := fmt.Sprintf("/v1/users/%s/playlists", userID)

	resp, err := p.webAPIWithBody(ctx, "POST", path, nil, bytes.NewReader(body), "application/json", http.StatusOK, http.StatusCreated)
	if err != nil {
		return "", fmt.Errorf("spotify: create playlist: %w", err)
	}

	var result struct {
		ID string `json:"id"`
	}
	if err := decodeBody(resp, &result); err != nil {
		return "", fmt.Errorf("spotify: parse created playlist: %w", err)
	}

	// Invalidate playlist list cache.
	p.mu.Lock()
	p.listCache = nil
	p.mu.Unlock()

	return result.ID, nil
}

// decodeBody reads and decodes a JSON response body, then closes it.
func decodeBody(resp *http.Response, v any) error {
	defer resp.Body.Close()
	return json.NewDecoder(io.LimitReader(resp.Body, maxResponseBody)).Decode(v)
}
