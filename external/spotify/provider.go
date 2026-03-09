//go:build !windows

package spotify

import (
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
)

// maxResponseBody limits JSON API responses to 10 MB.
const maxResponseBody = 10 << 20

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
	mu         sync.Mutex
	trackCache map[string]*playlistCache // playlist ID → cache entry
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
	p.mu.Unlock()
	return nil
}

// Authenticate runs the interactive sign-in flow (opens browser, waits for callback).
func (p *SpotifyProvider) Authenticate() error {
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
	sess, err := NewSession(context.Background(), clientID)
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.session = sess
	p.mu.Unlock()
	return nil
}

// Close releases the session if one was created.
func (p *SpotifyProvider) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.session != nil {
		p.session.Close()
		p.session = nil
	}
}

func (p *SpotifyProvider) Name() string { return "Spotify" }

// Playlists returns the authenticated user's Spotify playlists.
func (p *SpotifyProvider) Playlists() ([]playlist.PlaylistInfo, error) {
	if err := p.ensureSession(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	var all []playlist.PlaylistInfo
	offset := 0
	limit := 50

	for {
		query := url.Values{
			"limit":  {fmt.Sprintf("%d", limit)},
			"offset": {fmt.Sprintf("%d", offset)},
			// Request only the fields we need to reduce payload size and API cost.
			// Include snapshot_id for cache invalidation.
			"fields": {"items(id,name,snapshot_id,items.total),total"},
		}

		resp, err := p.webAPI(ctx, "GET", "/v1/me/playlists", query)
		if err != nil {
			return nil, fmt.Errorf("spotify: list playlists: %w", err)
		}

		var result struct {
			Items []struct {
				ID         string `json:"id"`
				Name       string `json:"name"`
				SnapshotID string `json:"snapshot_id"`
				Items      *struct {
					Total int `json:"total"`
				} `json:"items"`
			} `json:"items"`
			Total int `json:"total"`
		}
		if err := decodeBody(resp, &result); err != nil {
			return nil, fmt.Errorf("spotify: parse playlists: %w", err)
		}

		p.mu.Lock()
		for _, item := range result.Items {
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

	var all []playlist.Track
	offset := 0
	limit := 100

	for {
		query := url.Values{
			"limit":  {fmt.Sprintf("%d", limit)},
			"offset": {fmt.Sprintf("%d", offset)},
			"fields": {"items(item(id,name,artists(name),album(name,release_date),duration_ms,track_number)),total"},
		}

		path := fmt.Sprintf("/v1/playlists/%s/items", playlistID)
		resp, err := p.webAPI(ctx, "GET", path, query)
		if err != nil {
			return nil, fmt.Errorf("spotify: list tracks: %w", err)
		}

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
		var result struct {
			Items []struct {
				Item *trackObj `json:"item"`
			} `json:"items"`
			Total int `json:"total"`
		}
		if err := decodeBody(resp, &result); err != nil {
			return nil, fmt.Errorf("spotify: parse tracks: %w", err)
		}

		for _, item := range result.Items {
			t := item.Item
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

// NewStreamer creates a SpotifyStreamer for the given spotify:track:xxx URI.
// Called by the player's StreamerFactory when it encounters a Spotify URI.
//
// If the stream fails due to an auth error (e.g. expired session, AES key
// rejection), the session is torn down, credentials are cleared, and a fresh
// interactive OAuth2 flow is triggered automatically. The stream is then
// retried once with the new session.
func (p *SpotifyProvider) NewStreamer(uri string) (beep.StreamSeekCloser, beep.Format, time.Duration, error) {
	if err := p.ensureSession(); err != nil {
		return nil, beep.Format{}, 0, err
	}
	spotID, err := librespot.SpotifyIdFromUri(uri)
	if err != nil {
		return nil, beep.Format{}, 0, fmt.Errorf("spotify: invalid URI %q: %w", uri, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	stream, err := p.session.NewStream(ctx, *spotID, 320) // TODO: make bitrate configurable via config.toml
	if err != nil {
		if !isAuthError(err) {
			return nil, beep.Format{}, 0, fmt.Errorf("spotify: new stream: %w", err)
		}

		// Auth error — attempt re-authentication and retry once.
		fmt.Fprintf(os.Stderr, "spotify: stream auth error (%v), attempting re-auth...\n", err)

		reconnCtx, reconnCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer reconnCancel()

		if reconnErr := p.session.Reconnect(reconnCtx); reconnErr != nil {
			return nil, beep.Format{}, 0, fmt.Errorf("spotify: re-auth failed: %w (original: %v)", reconnErr, err)
		}

		// Retry with the fresh session.
		retryCtx, retryCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer retryCancel()

		stream, err = p.session.NewStream(retryCtx, *spotID, 320)
		if err != nil {
			return nil, beep.Format{}, 0, fmt.Errorf("spotify: new stream after re-auth: %w", err)
		}
	}

	streamer := NewSpotifyStreamer(stream)
	return streamer, streamer.Format(), streamer.Duration(), nil
}

// webAPI calls the Spotify Web API via the session with retry on 429.
func (p *SpotifyProvider) webAPI(ctx context.Context, method, path string, query url.Values) (*http.Response, error) {
	const maxRetries = 8
	for attempt := range maxRetries {
		resp, err := p.session.WebApi(ctx, method, path, query)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			resp.Body.Close()
			// Parse Retry-After header (seconds), default to exponential backoff.
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
		if resp.StatusCode != http.StatusOK {
			body, readErr := io.ReadAll(io.LimitReader(resp.Body, 512))
			resp.Body.Close()
			if readErr != nil {
				return nil, fmt.Errorf("http status %s (failed to read body: %v)", resp.Status, readErr)
			}
			return nil, fmt.Errorf("http status %s: %s", resp.Status, string(body))
		}
		return resp, nil
	}
	return nil, fmt.Errorf("http status 429 after %d retries", maxRetries)
}

// decodeBody reads and decodes a JSON response body, then closes it.
func decodeBody(resp *http.Response, v any) error {
	defer resp.Body.Close()
	return json.NewDecoder(io.LimitReader(resp.Body, maxResponseBody)).Decode(v)
}
