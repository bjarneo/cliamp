package ytmusic

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/bjarneo/cliamp/applog"
	"github.com/bjarneo/cliamp/internal/authurl"
	"github.com/bjarneo/cliamp/internal/browser"
	"github.com/bjarneo/cliamp/internal/credstore"
	"github.com/bjarneo/cliamp/internal/httpclient"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"
	"google.golang.org/api/youtube/v3"
)

// storedCreds holds persisted YouTube Music credentials for re-authentication.
type storedCreds struct {
	RefreshToken string `json:"refresh_token"`
}

// credsFile holds the stored YouTube Music credentials.
var credsFile = credstore.File[storedCreds]{Name: "ytmusic_credentials.json"}

// CredsPath returns the absolute path to the stored YouTube Music credentials
// file.
func CredsPath() (string, error) { return credsFile.Path() }

// DeleteCreds removes the stored YouTube Music credentials file. Returns true
// if a file was removed, false if it did not exist.
func DeleteCreds() (bool, error) { return credsFile.Delete() }

// CallbackPort is the fixed port for the OAuth2 callback server.
// Must match the redirect URI registered in the Google Cloud console.
const CallbackPort = 19873

// oauthHTTPClient sends OAuth token requests. The timeout stops a stalled
// token endpoint from blocking a provider call without limit.
var oauthHTTPClient = httpclient.NewAPI(30 * time.Second)

// oauthContext makes oauth2 send its token requests through oauthHTTPClient.
func oauthContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, oauth2.HTTPClient, oauthHTTPClient)
}

// authURLObserver receives the OAuth URL when interactive auth begins.
var authURLObserver authurl.Observer

// SetAuthURLObserver registers a callback invoked once with the OAuth URL at
// the start of an interactive sign-in. Pass nil to remove.
func SetAuthURLObserver(fn func(string)) { authURLObserver.Set(fn) }

// Session manages a YouTube Data API v3 service for YouTube Music integration.
type Session struct {
	mu           sync.Mutex
	clientID     string
	clientSecret string
	service      *youtube.Service
	tokenSource  oauth2.TokenSource
	cacheScope   string
}

// oauthScopes are the YouTube API scopes needed for cliamp.
var oauthScopes = []string{
	"https://www.googleapis.com/auth/youtube.readonly",
}

// googleOAuthConfig returns the OAuth2 config for the given client ID and secret.
// Google Desktop OAuth requires both a client_id and client_secret (unlike Spotify
// which supports PKCE-only public clients).
func googleOAuthConfig(clientID, clientSecret string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  fmt.Sprintf("http://127.0.0.1:%d/callback", CallbackPort),
		Scopes:       oauthScopes,
		Endpoint:     google.Endpoint,
	}
}

// NewSession creates a YouTube API session, using stored credentials if
// available, otherwise starting an interactive OAuth2 flow.
func NewSession(ctx context.Context, clientID, clientSecret string) (*Session, error) {
	creds, err := credsFile.Load()
	if err == nil && creds.RefreshToken != "" {
		s, err := newSessionFromStored(ctx, clientID, clientSecret, creds)
		if err == nil {
			return s, nil
		}
		// Stored credentials failed, fall through to interactive.
	}
	return newInteractiveSession(ctx, clientID, clientSecret)
}

// NewSessionSilent is like NewSession but only uses stored credentials.
// Returns an error if interactive auth is required.
func NewSessionSilent(ctx context.Context, clientID, clientSecret string) (*Session, error) {
	creds, err := credsFile.Load()
	if err != nil || creds.RefreshToken == "" {
		return nil, fmt.Errorf("no stored credentials")
	}
	return newSessionFromStored(ctx, clientID, clientSecret, creds)
}

// newSessionFromStored creates a session from stored credentials via silent refresh.
func newSessionFromStored(ctx context.Context, clientID, clientSecret string, creds *storedCreds) (*Session, error) {
	token, err := silentTokenRefresh(ctx, clientID, clientSecret, creds.RefreshToken)
	if err != nil {
		return nil, fmt.Errorf("ytmusic: silent refresh: %w", err)
	}

	// Re-save credentials (refresh token may have been rotated).
	refreshToken := creds.RefreshToken
	if token.RefreshToken != "" {
		refreshToken = token.RefreshToken
		if err := credsFile.Save(&storedCreds{RefreshToken: refreshToken}); err != nil {
			applog.UserError("ytmusic: failed to save credentials: %v", err)
		}
	}

	return newTokenSession(ctx, clientID, clientSecret, token, refreshToken)
}

// newTokenSession builds a Session around token. ctx bounds only the service
// setup. oauth2 keeps the context of a token source for every later refresh,
// so the token source gets a context that does not end. The Data API client
// sends its requests through the transport of oauthHTTPClient, so they
// follow the same proxy rules. It does not use the timeout of
// oauthHTTPClient.
func newTokenSession(ctx context.Context, clientID, clientSecret string, token *oauth2.Token, cacheIdentity string) (*Session, error) {
	ts := googleOAuthConfig(clientID, clientSecret).TokenSource(oauthContext(context.Background()), token)

	apiClient := &http.Client{Transport: &oauth2.Transport{Base: oauthHTTPClient.Transport, Source: ts}}
	svc, err := youtube.NewService(ctx, option.WithHTTPClient(apiClient))
	if err != nil {
		return nil, fmt.Errorf("ytmusic: create service: %w", err)
	}

	return &Session{
		clientID:     clientID,
		clientSecret: clientSecret,
		service:      svc,
		tokenSource:  ts,
		cacheScope:   oauthCacheScope(clientID, cacheIdentity),
	}, nil
}

// silentTokenRefresh uses a stored refresh token to get a new access token
// without opening a browser.
func silentTokenRefresh(ctx context.Context, clientID, clientSecret, refreshToken string) (*oauth2.Token, error) {
	conf := googleOAuthConfig(clientID, clientSecret)
	src := conf.TokenSource(oauthContext(ctx), &oauth2.Token{RefreshToken: refreshToken})
	return src.Token()
}

// newInteractiveSession performs an OAuth2 flow to authenticate.
func newInteractiveSession(ctx context.Context, clientID, clientSecret string) (*Session, error) {
	token, err := doOAuth(ctx, clientID, clientSecret)
	if err != nil {
		return nil, err
	}

	// Persist refresh token for future sessions.
	if err := credsFile.Save(&storedCreds{RefreshToken: token.RefreshToken}); err != nil {
		applog.UserError("ytmusic: failed to save credentials: %v", err)
	}
	cacheIdentity := token.RefreshToken
	if cacheIdentity == "" {
		cacheIdentity = token.AccessToken
	}

	return newTokenSession(ctx, clientID, clientSecret, token, cacheIdentity)
}

// oauthCallbackHTML is the response sent to the browser after a successful OAuth2 callback.
const oauthCallbackHTML = `<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>cliamp</title></head>
<body style="font-family:system-ui;display:flex;justify-content:center;align-items:center;height:100vh;margin:0;background:#1a1a2e;color:#e0e0e0">
<div style="text-align:center">
<h2>Authenticated!</h2>
<p>You can close this tab now.</p>
<script>setTimeout(function(){window.close()},1500)</script>
</div></body></html>`

// oauthCallbackErrorHTML is the response sent to the browser when Google
// reports an authorization error, for example when the user denies access.
const oauthCallbackErrorHTML = `<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>cliamp</title></head>
<body style="font-family:system-ui;display:flex;justify-content:center;align-items:center;height:100vh;margin:0;background:#1a1a2e;color:#e0e0e0">
<div style="text-align:center">
<h2>Sign-in failed</h2>
<p>Return to cliamp to try again.</p>
</div></body></html>`

// oauthResult is the outcome of an OAuth callback: an authorization code, or
// the error code that Google sent in place of it.
type oauthResult struct {
	code    string
	errCode string
}

// oauthCallbackHandler passes the code or the error of a callback that
// carries state to resultCh. It rejects other states and callbacks without a
// code or an error. It drops a result when resultCh is full, so a repeated
// callback never blocks.
func oauthCallbackHandler(state string, resultCh chan<- oauthResult) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if query.Get("state") != state {
			http.Error(w, "invalid OAuth state", http.StatusBadRequest)
			return
		}
		result := oauthResult{code: query.Get("code"), errCode: query.Get("error")}
		page := oauthCallbackHTML
		switch {
		case result.errCode != "":
			result.code = ""
			page = oauthCallbackErrorHTML
		case result.code == "":
			http.Error(w, "OAuth callback contains no code", http.StatusBadRequest)
			return
		}
		select {
		case resultCh <- result:
		default:
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(page))
	})
}

// doOAuth performs an OAuth2 flow: starts localhost server, opens browser,
// exchanges code for token. The context controls cancellation — if ctx is
// cancelled (e.g. the user retries auth), the listener is closed and the
// function returns promptly, freeing the callback port.
func doOAuth(ctx context.Context, clientID, clientSecret string) (*oauth2.Token, error) {
	lis, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", CallbackPort))
	if err != nil {
		return nil, fmt.Errorf("ytmusic: listen on port %d (is another instance running?): %w", CallbackPort, err)
	}
	defer lis.Close() // always release the port

	oauthConf := googleOAuthConfig(clientID, clientSecret)

	verifier := oauth2.GenerateVerifier()
	state := oauth2.GenerateVerifier()
	authURL := oauthConf.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier), oauth2.AccessTypeOffline)

	resultCh := make(chan oauthResult, 1)
	// Close each connection after its response. The browser then cannot
	// send the callback of a later sign-in to this server after it stops.
	srv := &http.Server{Handler: oauthCallbackHandler(state, resultCh)}
	srv.SetKeepAlivesEnabled(false)
	go func() {
		if err := srv.Serve(lis); err != nil && !errors.Is(err, net.ErrClosed) {
			applog.UserError("ytmusic: auth callback server error: %v", err)
		}
	}()

	authURLObserver.Notify("ytmusic", authURL)
	_ = browser.Open(authURL) // best-effort — user can open the URL manually if this fails

	var result oauthResult
	select {
	case result = <-resultCh:
	case <-ctx.Done():
		return nil, fmt.Errorf("ytmusic: authentication cancelled: %w", ctx.Err())
	}
	if result.errCode != "" {
		return nil, fmt.Errorf("ytmusic: authorization: %s", result.errCode)
	}

	token, err := oauthConf.Exchange(oauthContext(ctx), result.code, oauth2.VerifierOption(verifier))
	if err != nil {
		return nil, fmt.Errorf("ytmusic: token exchange: %w", err)
	}

	applog.Info("ytmusic: authenticated")
	return token, nil
}

// Service returns the YouTube API service, holding the lock briefly.
func (s *Session) Service() *youtube.Service {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.service
}

// Close is a no-op for YouTube Music sessions (no persistent connections).
func (s *Session) Close() {}
