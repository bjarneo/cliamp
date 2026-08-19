//go:build !windows

package spotify

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sync"
	"sync/atomic"

	"github.com/bjarneo/cliamp/applog"
	"github.com/bjarneo/cliamp/internal/browser"
	"github.com/bjarneo/cliamp/internal/fileutil"
	"github.com/bjarneo/cliamp/playlist"

	librespot "github.com/devgianlu/go-librespot"
	librespotPlayer "github.com/devgianlu/go-librespot/player"
	devicespb "github.com/devgianlu/go-librespot/proto/spotify/connectstate/devices"
	"github.com/devgianlu/go-librespot/session"
	"golang.org/x/oauth2"
	spotifyoauth2 "golang.org/x/oauth2/spotify"
)

// storedCreds holds persisted Spotify credentials for re-authentication.
type storedCreds struct {
	Username     string `json:"username"`
	Data         []byte `json:"data"`
	DeviceID     string `json:"device_id"`
	RefreshToken string `json:"refresh_token,omitempty"` // OAuth2 refresh token for silent re-auth
}

// CallbackPort is the fixed port for the OAuth2 callback server.
// Must match the redirect URI registered in the Spotify Developer app.
const CallbackPort = 19872

// authURLObserver is invoked with the OAuth URL when interactive auth begins.
// Set via SetAuthURLObserver. Used by the TUI to show the URL when the
// launched browser doesn't reach the user (containers, headless envs).
var authURLObserver atomic.Pointer[func(string)]

// SetAuthURLObserver registers a callback invoked once with the OAuth URL at
// the start of an interactive sign-in. Pass nil to remove.
func SetAuthURLObserver(fn func(string)) {
	if fn == nil {
		authURLObserver.Store(nil)
		return
	}
	authURLObserver.Store(&fn)
}

func notifyAuthURL(u string) {
	applog.Info("spotify: sign-in URL: %s", u)
	if p := authURLObserver.Load(); p != nil {
		(*p)(u)
	}
}

// Session manages a go-librespot session and player for Spotify integration.
type Session struct {
	mu          sync.RWMutex
	sess        *session.Session
	player      *librespotPlayer.Player
	devID       string
	clientID    string             // Spotify Developer app client ID
	tokenSource oauth2.TokenSource // auto-refreshing OAuth2 token source
}

type streamContextTransport struct {
	ctx  context.Context
	base http.RoundTripper
}

// RoundTrip attaches the stream lifetime to librespot's context-free chunk requests.
func (t streamContextTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return t.base.RoundTrip(req.Clone(t.ctx))
}

func newSpotifyStreamHTTPClient(ctx context.Context, transport http.RoundTripper) *http.Client {
	return &http.Client{Transport: streamContextTransport{ctx: ctx, base: transport}}
}

func awaitSpotifyStream(ctx context.Context, cancel context.CancelFunc, open func() (*librespotPlayer.Stream, error)) (*librespotPlayer.Stream, context.CancelFunc, error) {
	type result struct {
		stream *librespotPlayer.Stream
		err    error
	}

	// The worker must be able to release Session's read lock after a timeout.
	results := make(chan result, 1)
	go func() {
		stream, err := open()
		results <- result{stream: stream, err: err}
	}()

	select {
	case res := <-results:
		if res.err != nil {
			cancel()
			return nil, nil, res.err
		}
		return res.stream, cancel, nil
	case <-ctx.Done():
		cancel()
		return nil, nil, ctx.Err()
	}
}

// NewSession creates a go-librespot session, using stored credentials if
// available, otherwise starting an interactive OAuth2 flow.
// clientID is the Spotify Developer app client ID for Web API access.
func NewSession(ctx context.Context, clientID string) (*Session, error) {
	creds, err := loadCreds()
	if err == nil && creds.Username != "" && len(creds.Data) > 0 {
		s, err := newSessionFromStored(ctx, clientID, creds, false)
		if err == nil {
			return s, nil
		}
		// Stored credentials failed (expired/revoked), fall through to interactive.
	}
	return newInteractiveSession(ctx, clientID)
}

// NewSessionSilent is like NewSession but only uses stored credentials.
// Returns an error if interactive auth is required.
func NewSessionSilent(ctx context.Context, clientID string) (*Session, error) {
	creds, err := loadCreds()
	if err != nil || creds.Username == "" || len(creds.Data) == 0 {
		return nil, fmt.Errorf("no stored credentials")
	}
	return newSessionFromStored(ctx, clientID, creds, true)
}

// newSessionFromStored creates a session from stored credentials.
// When silentOnly is true, it will not fall back to browser-based auth
// if the silent token refresh fails.
func newSessionFromStored(ctx context.Context, clientID string, creds *storedCreds, silentOnly bool) (*Session, error) {
	devID := creds.DeviceID
	if devID == "" {
		devID = generateDeviceID()
	}

	sess, err := session.NewSessionFromOptions(ctx, &session.Options{
		Log:        &librespot.NullLogger{},
		DeviceType: devicespb.DeviceType_COMPUTER,
		DeviceId:   devID,
		Credentials: session.StoredCredentials{
			Username: creds.Username,
			Data:     creds.Data,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("spotify: stored auth: %w", err)
	}

	// For stored credentials, we need a fresh Web API token via OAuth2.
	// The spclient's login5 token is NOT suitable for Web API calls.
	// Try silent refresh first (no browser), fall back to interactive.
	var oauthToken *oauth2.Token
	var refreshErr error
	if creds.RefreshToken != "" {
		token, err := silentTokenRefresh(clientID, creds.RefreshToken)
		if err == nil {
			oauthToken = token
		} else {
			refreshErr = err
		}
	}
	// Dead refresh tokens (invalid_grant) never recover — clear so we don't
	// repeat the same failure on every launch.
	if isInvalidGrant(refreshErr) {
		applog.UserError("spotify: stored refresh token is invalid; clearing credentials, please sign in again")
		if _, err := DeleteCreds(); err != nil {
			applog.Warn("spotify: failed to clear stored credentials: %v", err)
		}
		sess.Close()
		return nil, fmt.Errorf("spotify: %w", playlist.ErrNeedsAuth)
	}
	if oauthToken == nil {
		if silentOnly {
			// Continue without a token source — already-loaded tracks still stream
			// via spclient; new Web API calls will return ErrNeedsAuth.
			applog.UserError("spotify: stored auth no longer valid; run 'cliamp spotify reset' or sign in again to fix")
			s := &Session{sess: sess, devID: devID, clientID: clientID}
			if err := saveCreds(&storedCreds{
				Username:     sess.Username(),
				Data:         sess.StoredCredentials(),
				DeviceID:     devID,
				RefreshToken: creds.RefreshToken, // preserve for next attempt
			}); err != nil {
				applog.UserError("spotify: failed to save credentials: %v", err)
			}
			if err := s.initPlayer(); err != nil {
				sess.Close()
				return nil, err
			}
			return s, nil
		}
		token, err := doWebAPIAuth(ctx, clientID)
		if err != nil {
			sess.Close()
			return nil, fmt.Errorf("stored session needs fresh Web API token: %w", err)
		}
		oauthToken = token
	}

	// Re-save credentials (including refresh token for next launch).
	stored := storedCreds{
		Username:     sess.Username(),
		Data:         sess.StoredCredentials(),
		DeviceID:     devID,
		RefreshToken: oauthToken.RefreshToken,
	}
	if err := saveCreds(&stored); err != nil {
		applog.UserError("spotify: failed to save credentials: %v", err)
	}

	s := &Session{
		sess:        sess,
		devID:       devID,
		clientID:    clientID,
		tokenSource: webAPITokenSource(clientID, oauthToken, stored),
	}

	if err := s.initPlayer(); err != nil {
		sess.Close()
		return nil, err
	}
	return s, nil
}

// oauthScopes are the Spotify Web API scopes needed for cliamp.
// See: https://developer.spotify.com/documentation/web-api/concepts/scopes
var oauthScopes = []string{
	// Playlist browsing
	"playlist-read-collaborative",
	"playlist-read-private",
	// Playlist modification (save queue, create playlists)
	"playlist-modify-public",
	"playlist-modify-private",
	// Streaming audio
	"streaming",
	// Library (liked songs, saved albums)
	"user-library-read",
	"user-library-modify",
	// User profile
	"user-read-private",
	// Playback state (current track, queue)
	"user-read-playback-state",
	"user-modify-playback-state",
	"user-read-currently-playing",
	// Recently played / top tracks
	"user-read-recently-played",
	"user-top-read",
	// Following (artists, users)
	"user-follow-read",
	"user-follow-modify",
}

// playbackOAuthScopes are requested through Spotify's keymaster client. Since
// August 2026, login5 rejects playback credentials minted by any other client.
var playbackOAuthScopes = []string{"streaming"}

// spotifyOAuthConfig returns the OAuth2 config for the given client ID.
func spotifyOAuthConfig(clientID string, scopes []string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:    clientID,
		RedirectURL: fmt.Sprintf("http://127.0.0.1:%d/login", CallbackPort),
		Scopes:      scopes,
		Endpoint:    spotifyoauth2.Endpoint,
	}
}

// silentTokenRefresh uses a stored refresh token to get a new access token
// without opening a browser.
func silentTokenRefresh(clientID, refreshToken string) (*oauth2.Token, error) {
	conf := spotifyOAuthConfig(clientID, oauthScopes)
	src := conf.TokenSource(context.Background(), &oauth2.Token{RefreshToken: refreshToken})
	return src.Token()
}

// persistingTokenSource saves refresh-token rotations without making a usable
// access token fail when credential persistence itself fails.
type persistingTokenSource struct {
	source       oauth2.TokenSource
	persist      func(string) error
	mu           sync.Mutex
	refreshToken string
}

func (s *persistingTokenSource) Token() (*oauth2.Token, error) {
	token, err := s.source.Token()
	if err != nil {
		return nil, err
	}
	if token.RefreshToken == "" {
		return token, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if token.RefreshToken == s.refreshToken {
		return token, nil
	}
	s.refreshToken = token.RefreshToken
	if err := s.persist(token.RefreshToken); err != nil {
		applog.UserError("spotify: failed to persist rotated refresh token: %v", err)
	}
	return token, nil
}

func webAPITokenSource(clientID string, token *oauth2.Token, creds storedCreds) oauth2.TokenSource {
	conf := spotifyOAuthConfig(clientID, oauthScopes)
	source := conf.TokenSource(context.Background(), token)
	return &persistingTokenSource{
		source:       source,
		refreshToken: creds.RefreshToken,
		persist: func(refreshToken string) error {
			creds.RefreshToken = refreshToken
			return saveCreds(&creds)
		},
	}
}

// isInvalidGrant reports whether err is an OAuth2 invalid_grant response
// from the token endpoint, indicating the refresh token is dead and
// retrying with the same token will not succeed.
func isInvalidGrant(err error) bool {
	var rerr *oauth2.RetrieveError
	if !errors.As(err, &rerr) {
		return false
	}
	return rerr.ErrorCode == "invalid_grant"
}

// oauthCallbackHTML is the response sent to the browser after a successful OAuth2 callback.
const oauthCallbackHTML = `<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>cliamp</title></head>
<body style="font-family:system-ui;display:flex;justify-content:center;align-items:center;height:100vh;margin:0;background:#1a1a2e;color:#e0e0e0">
<div style="text-align:center">
<h2>✅ Authenticated!</h2>
<p>You can close this tab now.</p>
<script>setTimeout(function(){window.close()},1500)</script>
</div></body></html>`

type oauthFlow struct {
	name     string
	clientID string
	scopes   []string
}

type pendingOAuthFlow struct {
	oauthFlow
	config   *oauth2.Config
	verifier string
	state    string
	authURL  string
}

type oauthCallback struct {
	flow int
	code string
	err  error
}

func oauthCallbackHandler(pending []pendingOAuthFlow, callbackCh chan<- oauthCallback) http.Handler {
	byState := make(map[string]int, len(pending))
	for i, flow := range pending {
		byState[flow.state] = i
	}

	seen := make(map[string]bool, len(pending))
	var mu sync.Mutex
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state := r.URL.Query().Get("state")
		flow, ok := byState[state]
		if !ok {
			http.Error(w, "invalid OAuth state", http.StatusBadRequest)
			return
		}

		code := r.URL.Query().Get("code")
		oauthErr := r.URL.Query().Get("error")
		if code == "" && oauthErr == "" {
			http.Error(w, "OAuth callback contains no code", http.StatusBadRequest)
			return
		}

		mu.Lock()
		first := !seen[state]
		seen[state] = true
		mu.Unlock()
		if first {
			result := oauthCallback{flow: flow, code: code}
			if oauthErr != "" {
				result.err = fmt.Errorf("spotify returned %s", oauthErr)
			}
			callbackCh <- result
		}

		if oauthErr != "" {
			http.Error(w, "Spotify authorization failed", http.StatusBadRequest)
			return
		}
		if flow+1 < len(pending) {
			http.Redirect(w, r, pending[flow+1].authURL, http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(oauthCallbackHTML))
	})
}

// performOAuth2PKCE runs one OAuth2 PKCE flow and returns its token.
func performOAuth2PKCE(ctx context.Context, clientID string, scopes []string) (*oauth2.Token, error) {
	tokens, err := performOAuth2PKCEFlows(ctx, []oauthFlow{{name: "web api", clientID: clientID, scopes: scopes}})
	if err != nil {
		return nil, err
	}
	return tokens[0], nil
}

// performOAuth2PKCEFlows opens one browser journey for one or more OAuth
// authorizations. Each callback redirects the same tab to the next flow, so a
// custom Web API client can be followed by keymaster playback authorization
// without relying on the browser to foreground a second tab.
func performOAuth2PKCEFlows(ctx context.Context, flows []oauthFlow) ([]*oauth2.Token, error) {
	if len(flows) == 0 {
		return nil, fmt.Errorf("no OAuth flows configured")
	}

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", CallbackPort))
	if err != nil {
		return nil, fmt.Errorf("listen on port %d: %w", CallbackPort, err)
	}
	defer lis.Close() // always release the port

	pending := make([]pendingOAuthFlow, len(flows))
	for i, flow := range flows {
		conf := spotifyOAuthConfig(flow.clientID, flow.scopes)
		verifier := oauth2.GenerateVerifier()
		state := oauth2.GenerateVerifier()
		pending[i] = pendingOAuthFlow{
			oauthFlow: flow,
			config:    conf,
			verifier:  verifier,
			state:     state,
			authURL:   conf.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier)),
		}
	}

	callbackCh := make(chan oauthCallback, len(flows))
	go func() {
		if err := http.Serve(lis, oauthCallbackHandler(pending, callbackCh)); err != nil && !errors.Is(err, net.ErrClosed) {
			applog.UserError("spotify: auth callback server error: %v", err)
		}
	}()

	notifyAuthURL(pending[0].authURL)
	_ = browser.Open(pending[0].authURL) // best-effort — user can open the URL manually if this fails

	tokens := make([]*oauth2.Token, len(pending))
	for remaining := len(pending); remaining > 0; remaining-- {
		select {
		case result := <-callbackCh:
			flow := pending[result.flow]
			if result.err != nil {
				return nil, fmt.Errorf("%s authorization: %w", flow.name, result.err)
			}
			token, err := flow.config.Exchange(ctx, result.code, oauth2.VerifierOption(flow.verifier))
			if err != nil {
				return nil, fmt.Errorf("%s token exchange: %w", flow.name, err)
			}
			tokens[result.flow] = token
		case <-ctx.Done():
			return nil, fmt.Errorf("authentication cancelled: %w", ctx.Err())
		}
	}
	return tokens, nil
}

// doWebAPIAuth performs an OAuth2 PKCE flow to get a fresh Web API access token.
// Opens a browser for user consent, returns the full token (including refresh token).
func doWebAPIAuth(ctx context.Context, clientID string) (*oauth2.Token, error) {
	token, err := performOAuth2PKCE(ctx, clientID, oauthScopes)
	if err != nil {
		return nil, err
	}
	fmt.Println("Spotify: Web API token refreshed.")
	return token, nil
}

// interactiveOAuthFlows returns the authorization sequence for a new session.
// The built-in client already is keymaster, so it needs only one flow.
func interactiveOAuthFlows(clientID string) []oauthFlow {
	if clientID == DefaultClientID {
		return []oauthFlow{{name: "web api and playback", clientID: clientID, scopes: oauthScopes}}
	}
	return []oauthFlow{
		{name: "web api", clientID: clientID, scopes: oauthScopes},
		{name: "playback", clientID: DefaultClientID, scopes: playbackOAuthScopes},
	}
}

func newInteractiveSession(ctx context.Context, clientID string) (*Session, error) {
	devID := generateDeviceID()

	tokens, err := performOAuth2PKCEFlows(ctx, interactiveOAuthFlows(clientID))
	if err != nil {
		return nil, fmt.Errorf("spotify: %w", err)
	}
	webToken := tokens[0]
	playbackToken := tokens[len(tokens)-1]

	username, _ := playbackToken.Extra("username").(string)
	accessToken := playbackToken.AccessToken

	// Create go-librespot session using the keymaster-issued playback token.
	sess, err := session.NewSessionFromOptions(ctx, &session.Options{
		Log:        &librespot.NullLogger{},
		DeviceType: devicespb.DeviceType_COMPUTER,
		DeviceId:   devID,
		Credentials: session.SpotifyTokenCredentials{
			Username: username,
			Token:    accessToken,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("spotify: session from token: %w", err)
	}

	// Persist stored credentials + refresh token for future sessions.
	stored := storedCreds{
		Username:     sess.Username(),
		Data:         sess.StoredCredentials(),
		DeviceID:     devID,
		RefreshToken: webToken.RefreshToken,
	}
	if err := saveCreds(&stored); err != nil {
		applog.UserError("spotify: failed to save credentials: %v", err)
	}

	s := &Session{
		sess:        sess,
		devID:       devID,
		clientID:    clientID,
		tokenSource: webAPITokenSource(clientID, webToken, stored),
	}
	if err := s.initPlayer(); err != nil {
		sess.Close()
		return nil, err
	}
	return s, nil
}

// initPlayer creates the go-librespot player. We only use NewStream() for
// decoded AudioSources — audio output is routed through cliamp's Beep pipeline,
// not go-librespot's output backend.
func (s *Session) initPlayer() error {
	// go-librespot uses this for media restriction checks but Premium
	// accounts can play all tracks regardless.
	countryCode := "US"
	p, err := librespotPlayer.NewPlayer(&librespotPlayer.Options{
		Spclient:             s.sess.Spclient(),
		AudioKey:             s.sess.AudioKey(),
		Events:               s.sess.Events(),
		Log:                  &librespot.NullLogger{},
		CountryCode:          &countryCode,
		NormalisationEnabled: true,
		AudioBackend:         "pipe",
		AudioOutputPipe:      os.DevNull,
	})
	if err != nil {
		return fmt.Errorf("spotify: player init: %w", err)
	}
	s.player = p
	return nil
}

// NewStream creates a decoded audio stream for the given Spotify track ID. The
// caller must retain and invoke the returned cancel function for the lifetime
// of a successful stream. ctx bounds setup independently of that lifetime.
//
// Holds s.mu.RLock() across the librespot network call. Multiple concurrent
// NewStream / webApi callers can run in parallel (RLock is shared), so rapid
// track skipping does not serialize. reconnect() and Close() take the full
// Lock and will wait for in-flight callers to finish before tearing down the
// player — without this, the swap could call oldPlayer.Close() while we are
// still reading from it.
func (s *Session) NewStream(ctx context.Context, spotID librespot.SpotifyId, bitrate int) (*librespotPlayer.Stream, context.CancelFunc, error) {
	streamCtx, cancel := context.WithCancel(context.Background())
	client := newSpotifyStreamHTTPClient(streamCtx, http.DefaultTransport)

	return awaitSpotifyStream(ctx, cancel, func() (*librespotPlayer.Stream, error) {
		s.mu.RLock()
		defer s.mu.RUnlock()
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if s.player == nil {
			return nil, fmt.Errorf("spotify: session closed")
		}
		return s.player.NewStream(ctx, client, spotID, bitrate, 0)
	})
}

// webApiWithBody calls the Spotify Web API using the OAuth2 access token.
//
// The spclient/login5 token from librespot is NOT accepted by the Web API
// for endpoints like /v1/search and /v1/me/playlists — Spotify returns
// misleading errors ("Invalid limit", 429) instead of a clear auth failure.
// So if there is no OAuth2 token source, fail loudly with ErrNeedsAuth
// rather than attempting the call with the wrong token.
func (s *Session) webApiWithBody(ctx context.Context, method, path string, query url.Values, body io.Reader, contentType string) (*http.Response, error) {
	s.mu.RLock()
	ts := s.tokenSource
	s.mu.RUnlock()

	if ts == nil {
		return nil, fmt.Errorf("spotify: web api token unavailable, run 'cliamp spotify reset' and sign in again: %w", playlist.ErrNeedsAuth)
	}
	tok, err := ts.Token()
	if err != nil {
		return nil, fmt.Errorf("refresh access token: %w", err)
	}
	token := tok.AccessToken

	u, _ := url.Parse("https://api.spotify.com")
	u = u.JoinPath(path)
	if query != nil {
		u.RawQuery = query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	return http.DefaultClient.Do(req)
}

// Close releases all session and player resources.
func (s *Session) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.player != nil {
		s.player.Close()
	}
	if s.sess != nil {
		s.sess.Close()
	}
}

// Reconnect rebuilds the session from stored credentials (no browser).
// Returns an error if stored credentials are missing or the refresh fails.
func (s *Session) Reconnect(ctx context.Context) error {
	return s.reconnect(ctx, NewSessionSilent)
}

// ReconnectInteractive forces a fresh browser-based OAuth2 flow.
// Stored credentials are preserved until the new session succeeds —
// newInteractiveSession overwrites them via saveCreds on success.
func (s *Session) ReconnectInteractive(ctx context.Context) error {
	return s.reconnect(ctx, newInteractiveSession)
}

// reconnect replaces the live session using the provided builder function.
// The new session is established before tearing down the old one to avoid a
// window where s.sess/s.player are nil (which would crash concurrent callers).
//
// The swap-and-teardown phase is done under s.mu (full Lock), which waits for
// any in-flight NewStream / webApi RLockers to drain. This guarantees that
// oldPlayer.Close() is never called while a NewStream is still using the
// old player pointer.
func (s *Session) reconnect(ctx context.Context, build func(context.Context, string) (*Session, error)) error {
	s.mu.RLock()
	clientID := s.clientID
	s.mu.RUnlock()

	newSess, err := build(ctx, clientID)
	if err != nil {
		return fmt.Errorf("spotify: reconnect: %w", err)
	}

	// Swap and tear down the old session under a single write lock so
	// in-flight NewStream / webApi calls finish before oldPlayer.Close()
	// runs. The expensive build() above happened lock-free.
	s.mu.Lock()
	oldPlayer := s.player
	oldSess := s.sess
	s.sess = newSess.sess
	s.player = newSess.player
	s.devID = newSess.devID
	s.tokenSource = newSess.tokenSource
	if oldPlayer != nil {
		oldPlayer.Close()
	}
	if oldSess != nil {
		oldSess.Close()
	}
	s.mu.Unlock()

	// Prevent newSess.Close() from tearing down the resources we just adopted.
	newSess.mu.Lock()
	newSess.sess = nil
	newSess.player = nil
	newSess.mu.Unlock()

	const reauthMsg = "spotify: re-authenticated successfully"
	applog.Info(reauthMsg)
	applog.Status(reauthMsg)
	return nil
}

func generateDeviceID() string {
	b := make([]byte, 20)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func loadCreds() (*storedCreds, error) {
	path, err := CredsPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var creds storedCreds
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, err
	}
	return &creds, nil
}

func saveCreds(creds *storedCreds) error {
	path, err := CredsPath()
	if err != nil {
		return err
	}
	data, err := json.Marshal(creds)
	if err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(path, data, 0o600)
}
