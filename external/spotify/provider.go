package spotify

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// Compile-time interface checks.
var (
	_ provider.Searcher             = (*SpotifyProvider)(nil)
	_ provider.PlaylistWriter       = (*SpotifyProvider)(nil)
	_ provider.PlaylistCreator      = (*SpotifyProvider)(nil)
	_ provider.CustomStreamer       = (*SpotifyProvider)(nil)
	_ provider.Closer               = (*SpotifyProvider)(nil)
	_ provider.TrackPager           = (*SpotifyProvider)(nil)
	_ playlist.Refresher            = (*SpotifyProvider)(nil)
	_ provider.PlaylistTargetFilter = (*SpotifyProvider)(nil)
	_ playlist.Authenticator        = (*SpotifyProvider)(nil)
	_ provider.Relater              = (*SpotifyProvider)(nil)
)

// SpotifyProvider implements playlist.Provider using the Spotify Web API
// for playlist/track metadata and go-librespot for audio streaming.
type SpotifyProvider struct {
	session    *Session
	clientID   string
	bitrate    int
	userID     string // Spotify user ID, fetched lazily on first Playlists() call
	meFetched  bool   // /v1/me has been attempted this session; suppresses retry on failure
	mu         sync.Mutex
	trackCache map[string]*playlistCache // playlist ID → cache entry
	pending    map[string]*pendingTracks
	authCancel context.CancelFunc // cancels any in-progress OAuth flow
	authGen    uint64             // counts sign-ins, so a call clears only its own authCancel

	// Playlist list cache to avoid redundant API calls on provider switch.
	listCache   []playlist.PlaylistInfo
	listCacheAt time.Time
	writable    map[string]bool // playlist IDs the user owns or collaborates on
}

// New creates a SpotifyProvider. If session is nil, authentication is
// deferred until the user first selects the Spotify provider.
// bitrate sets the preferred Spotify stream quality in kbps (96, 160, or 320).
func New(session *Session, clientID string, bitrate int) *SpotifyProvider {
	return &SpotifyProvider{
		session:    session,
		clientID:   clientID,
		bitrate:    bitrate,
		trackCache: make(map[string]*playlistCache),
		pending:    make(map[string]*pendingTracks),
		writable:   make(map[string]bool),
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
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sess, err := NewSessionSilent(ctx, clientID)
	if err != nil {
		return playlist.ErrNeedsAuth
	}
	p.mu.Lock()
	p.session = sess
	p.resetSessionScopedStateLocked()
	p.mu.Unlock()
	return nil
}

// signIn runs the interactive sign-in. It reconnects existing when it is not
// nil and then returns no new session. Tests replace it.
var signIn = func(ctx context.Context, clientID string, existing *Session) (*Session, error) {
	if existing != nil {
		return nil, existing.ReconnectInteractive(ctx)
	}
	return NewSession(ctx, clientID)
}

// Authenticate runs the interactive sign-in flow (opens browser, waits for callback).
// Any previous in-progress OAuth flow is cancelled first to free the callback port.
//
// The UI asks for sign-in only after a call returned ErrNeedsAuth. When a
// session already exists at that point, its Web API token is missing or its
// stream keys were rejected, so the session is rebuilt through the browser.
func (p *SpotifyProvider) Authenticate() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Cancel the old flow and register this one in one lock hold, so a
	// newer call or Close always finds the flow that runs.
	p.mu.Lock()
	clientID := p.clientID
	if clientID == "" {
		p.mu.Unlock()
		return fmt.Errorf("spotify: no client ID available")
	}
	if p.authCancel != nil {
		p.authCancel()
	}
	p.authGen++
	gen := p.authGen
	p.authCancel = cancel
	existing := p.session
	p.mu.Unlock()

	sess, err := signIn(ctx, clientID, existing)

	p.mu.Lock()
	if p.authGen == gen {
		p.authCancel = nil
	}
	p.mu.Unlock()

	if err != nil {
		return err
	}
	p.mu.Lock()
	if sess != nil {
		p.session = sess
	}
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

// Refresh drops the cached playlist list and track lists, so the next load
// reads them from Spotify. Implements playlist.Refresher.
func (p *SpotifyProvider) Refresh() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.listCache = nil
	p.trackCache = make(map[string]*playlistCache)
	clear(p.writable)
}

// resetSessionScopedStateLocked clears /v1/me-derived caches when the session
// changes. The playlist list goes too, because playlist ownership decides
// which playlists are writable. p.mu must be held.
func (p *SpotifyProvider) resetSessionScopedStateLocked() {
	p.userID = ""
	p.meFetched = false
	p.listCache = nil
	clear(p.writable)
}

func (p *SpotifyProvider) Name() string { return "Spotify" }
