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
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	librespot "github.com/devgianlu/go-librespot"
	"github.com/devgianlu/go-librespot/audio"
	"github.com/gopxl/beep/v2"

	"cliamp/playlist"
	"cliamp/provider"
)

// Compile-time interface checks.
var (
	_ provider.Searcher       = (*SpotifyProvider)(nil)
	_ provider.PlaylistWriter = (*SpotifyProvider)(nil)
	_ provider.PlaylistCreator = (*SpotifyProvider)(nil)
	_ provider.CustomStreamer  = (*SpotifyProvider)(nil)
	_ provider.Closer          = (*SpotifyProvider)(nil)
)

// maxResponseBody limits JSON API responses to 10 MB.
const maxResponseBody = 10 << 20

// Pagination limits for the Spotify Web API.
const (
	spotifyPlaylistPageSize = 50
	spotifyTrackPageSize    = 100
)

// spotifyBitrate is the audio quality for Spotify streams (kbps).
// TODO: make bitrate configurable via config.toml
const spotifyBitrate = 320

// spotifyPlaylistItem is the raw playlist object returned by /v1/me/playlists.
type spotifyPlaylistItem struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	SnapshotID    string `json:"snapshot_id"`
	Collaborative bool   `json:"collaborative"`
	Owner         struct {
		ID string `json:"id"`
	} `json:"owner"`
	Items *struct {
		Total int `json:"total"`
	} `json:"items"`
}

// playlistAccessible reports whether the playlist should be shown to the user.
// Playlists saved from other users (not owned, not collaborative) are excluded
// because the Spotify API returns 403 when listing their tracks.
// When userID is empty (fetch failed), all playlists are included as a fallback.
func playlistAccessible(item spotifyPlaylistItem, userID string) bool {
	if userID == "" {
		return true
	}
	return item.Owner.ID == userID || item.Collaborative
}

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
	userID     string // Spotify user ID, fetched lazily on first Playlists() call
	mu         sync.Mutex
	trackCache map[string]*playlistCache // playlist ID → cache entry
	authCancel context.CancelFunc        // cancels any in-progress OAuth flow
}

// New creates a SpotifyProvider. If session is nil, authentication is
// deferred until the user first selects the Spotify provider.
func New(session *Session, clientID string) *SpotifyProvider {
	return &SpotifyProvider{
		session:    session,
		clientID:   clientID,
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
	p.userID = ""
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
	p.userID = ""
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
		p.userID = ""
	}
}

func (p *SpotifyProvider) Name() string { return "Spotify" }

// currentUserID fetches and caches the authenticated user's Spotify ID.
func (p *SpotifyProvider) currentUserID(ctx context.Context) string {
	p.mu.Lock()
	id := p.userID
	p.mu.Unlock()
	if id != "" {
		return id
	}
	resp, err := p.webAPI(ctx, "GET", "/v1/me", nil)
	if err != nil {
		return ""
	}
	var me struct {
		ID string `json:"id"`
	}
	if err := decodeBody(resp, &me); err != nil || me.ID == "" {
		return ""
	}
	p.mu.Lock()
	p.userID = me.ID
	p.mu.Unlock()
	return me.ID
}

// Playlists returns the authenticated user's Spotify playlists.
// Only playlists owned by the user or marked as collaborative are returned;
// playlists saved from other users are excluded because the Spotify API
// returns 403 when trying to list their tracks.
func (p *SpotifyProvider) Playlists() ([]playlist.PlaylistInfo, error) {
	if err := p.ensureSession(); err != nil {
		return nil, err
	}
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
	p.mu.Lock()
	all = append(all, playlist.PlaylistInfo{
		ID:         "YOUR MUSIC",
		Name:       "Your Music",
		TrackCount: result.Total,
	})
	p.mu.Unlock()

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
			all = append(all, playlist.PlaylistInfo{
				ID:         item.ID,
				Name:       item.Name,
				TrackCount: count,
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

	return all, nil
}

// Tracks returns all tracks for the given Spotify playlist ID.
// Track.Path is set to a spotify:track:<id> URI for the player to resolve.
// Results are cached by snapshot_id; unchanged playlists skip the API call.
func (p *SpotifyProvider) Tracks(playlistID string) ([]playlist.Track, error) {
	if err := p.ensureSession(); err != nil {
		return nil, err
	}
	// Check cache — if we have tracks and the snapshot_id hasn't changed, return cached.
	p.mu.Lock()
	if cached, ok := p.trackCache[playlistID]; ok && cached.tracks != nil {
		tracks := cached.tracks
		p.mu.Unlock()
		return tracks, nil
	}
	p.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	type trackObj struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Artists []struct {
			Name string `json:"name"`
		} `json:"artists"`
		Album struct {
			Name        string `json:"name"`
			ReleaseDate string `json:"release_date"`
		} `json:"album"`
		DurationMs  int `json:"duration_ms"`
		TrackNumber int `json:"track_number"`
	}

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
				"limit":  {fmt.Sprintf("%d", min(50, limit))},
				"offset": {fmt.Sprintf("%d", offset)},
			}
			resp, err = p.webAPI(ctx, "GET", "/v1/me/tracks", query)
		} else {
			query := url.Values{
				"limit":  {fmt.Sprintf("%d", limit)},
				"offset": {fmt.Sprintf("%d", offset)},
				"fields": {"items(item(id,name,artists(name),album(name,release_date),duration_ms,track_number)),total"},
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
				Item  *trackObj `json:"item"`
				Track *trackObj `json:"track"`
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

			artists := make([]string, len(t.Artists))
			for i, a := range t.Artists {
				artists[i] = a.Name
			}

			var year int
			if len(t.Album.ReleaseDate) >= 4 {
				if y, err := strconv.Atoi(t.Album.ReleaseDate[:4]); err == nil {
					year = y
				}
			}

			all = append(all, playlist.Track{
				Path:         fmt.Sprintf("spotify:track:%s", t.ID),
				Title:        t.Name,
				Artist:       strings.Join(artists, ", "),
				Album:        t.Album.Name,
				Year:         year,
				Stream:       false, // must be false: true causes togglePlayPause to stop+restart instead of pause/resume
				DurationSecs: t.DurationMs / 1000,
				TrackNumber:  t.TrackNumber,
			})
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

	return all, nil
}

// isAuthError returns true if the error is an authentication/session-related
// failure that can be resolved by re-authenticating.
func isAuthError(err error) bool {
	var keyErr *audio.KeyProviderError
	if errors.As(err, &keyErr) {
		return true
	}
	// Catch wrapped context errors from a dead session.
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	return false
}

// URISchemes returns the URI prefixes handled by this provider.
// Implements provider.CustomStreamer.
func (p *SpotifyProvider) URISchemes() []string { return []string{"spotify:"} }

// NewStreamer creates a SpotifyStreamer for the given spotify:track:xxx URI.
// If the stream fails due to an auth error (e.g. expired session, AES key
// rejection), the player first tries a silent reconnect from cached credentials.
// If that fails or the retry still hits an auth error, it falls back to an
// interactive OAuth2 flow and retries once more.
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
		stream, err := p.session.NewStream(ctx, *spotID, spotifyBitrate)
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

	// Auth error — try silent reconnect first.
	fmt.Fprintf(os.Stderr, "spotify: stream auth error (%v), attempting silent reconnect...\n", err)

	reconnCtx, reconnCancel := context.WithTimeout(context.Background(), 2*time.Minute)
	reconnErr := p.session.Reconnect(reconnCtx)
	reconnCancel()

	if reconnErr == nil {
		s, err = tryStream()
		if err == nil {
			return s, s.Format(), s.Duration(), nil
		}
		if !isAuthError(err) {
			return nil, beep.Format{}, 0, fmt.Errorf("spotify: new stream after silent reconnect: %w", err)
		}
		fmt.Fprintf(os.Stderr, "spotify: stream still failing after silent reconnect (%v), falling back to interactive...\n", err)
	} else {
		fmt.Fprintf(os.Stderr, "spotify: silent reconnect failed (%v), falling back to interactive...\n", reconnErr)
	}

	interactiveCtx, interactiveCancel := context.WithTimeout(context.Background(), 2*time.Minute)
	interactiveErr := p.session.ReconnectInteractive(interactiveCtx)
	interactiveCancel()

	if interactiveErr != nil {
		return nil, beep.Format{}, 0, fmt.Errorf("spotify: interactive reconnect failed: %w (original: %v)", interactiveErr, err)
	}

	s, err = tryStream()
	if err != nil {
		return nil, beep.Format{}, 0, fmt.Errorf("spotify: new stream after interactive reconnect: %w", err)
	}
	return s, s.Format(), s.Duration(), nil
}

// webAPI calls the Spotify Web API via the session with retry on 429.
func (p *SpotifyProvider) webAPI(ctx context.Context, method, path string, query url.Values) (*http.Response, error) {
	return p.webAPIWithBody(ctx, method, path, query, nil, "", http.StatusOK)
}

// webAPIWithBody is like webAPI but accepts an optional request body, content type,
// and a set of acceptable HTTP status codes (e.g. 200, 201).
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
			wait := time.Duration(1<<uint(attempt)) * time.Second
			if ra := resp.Header.Get("Retry-After"); ra != "" {
				if secs, err := strconv.Atoi(ra); err == nil && secs > 0 {
					wait = time.Duration(secs) * time.Second
				}
			}
			fmt.Fprintf(os.Stderr, "spotify: rate limited on %s, retrying in %v (attempt %d/%d)\n", path, wait, attempt+1, maxRetries)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(wait):
				continue
			}
		}

		ok := false
		for _, code := range acceptStatus {
			if resp.StatusCode == code {
				ok = true
				break
			}
		}
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
	return nil, fmt.Errorf("http status 429 after %d retries", maxRetries)
}

// SearchTracks searches for tracks on Spotify and returns up to limit results.
func (p *SpotifyProvider) SearchTracks(ctx context.Context, query string, limit int) ([]playlist.Track, error) {
	if err := p.ensureSession(); err != nil {
		return nil, err
	}

	q := url.Values{
		"q":     {query},
		"type":  {"track"},
		"limit": {fmt.Sprintf("%d", limit)},
	}

	resp, err := p.webAPI(ctx, "GET", "/v1/search", q)
	if err != nil {
		return nil, fmt.Errorf("spotify: search: %w", err)
	}

	var result struct {
		Tracks struct {
			Items []struct {
				ID      string `json:"id"`
				Name    string `json:"name"`
				Artists []struct {
					Name string `json:"name"`
				} `json:"artists"`
				Album struct {
					Name        string `json:"name"`
					ReleaseDate string `json:"release_date"`
				} `json:"album"`
				DurationMs int `json:"duration_ms"`
			} `json:"items"`
		} `json:"tracks"`
	}
	if err := decodeBody(resp, &result); err != nil {
		return nil, fmt.Errorf("spotify: parse search: %w", err)
	}

	var tracks []playlist.Track
	for _, t := range result.Tracks.Items {
		if t.ID == "" {
			continue
		}
		artists := make([]string, len(t.Artists))
		for i, a := range t.Artists {
			artists[i] = a.Name
		}
		var year int
		if len(t.Album.ReleaseDate) >= 4 {
			if y, err := strconv.Atoi(t.Album.ReleaseDate[:4]); err == nil {
				year = y
			}
		}
		tracks = append(tracks, playlist.Track{
			Path:         fmt.Sprintf("spotify:track:%s", t.ID),
			Title:        t.Name,
			Artist:       strings.Join(artists, ", "),
			Album:        t.Album.Name,
			Year:         year,
			DurationSecs: t.DurationMs / 1000,
		})
	}
	return tracks, nil
}

// AddTrackToPlaylist adds a track to an existing Spotify playlist.
// The track's Path is used as the Spotify URI (e.g. "spotify:track:xxx").
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

	// Invalidate cache for this playlist.
	p.mu.Lock()
	delete(p.trackCache, playlistID)
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
	return result.ID, nil
}

// decodeBody reads and decodes a JSON response body, then closes it.
func decodeBody(resp *http.Response, v any) error {
	defer resp.Body.Close()
	return json.NewDecoder(io.LimitReader(resp.Body, maxResponseBody)).Decode(v)
}
