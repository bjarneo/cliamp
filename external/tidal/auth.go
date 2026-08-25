package tidal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/bjarneo/cliamp/applog"
	"github.com/bjarneo/cliamp/internal/browser"
)

const (
	deviceAuthURL   = "https://auth.tidal.com/v1/oauth2/device_authorization"
	defaultTokenURL = "https://auth.tidal.com/v1/oauth2/token"
	oauthScope      = "r_usr w_usr w_sub"
)

// authURLObserver is invoked with the device-flow URL when interactive auth
// begins. Used by the TUI to display the URL when the launched browser does
// not reach the user (containers, headless environments).
var authURLObserver atomic.Pointer[func(string)]

// SetAuthURLObserver registers a callback invoked once with the device-flow
// URL at the start of an interactive sign-in. Pass nil to remove.
func SetAuthURLObserver(fn func(string)) {
	if fn == nil {
		authURLObserver.Store(nil)
		return
	}
	authURLObserver.Store(&fn)
}

func notifyAuthURL(u string) {
	applog.Info("tidal: sign-in URL: %s", u)
	if p := authURLObserver.Load(); p != nil {
		(*p)(u)
	}
}

// deviceAuth is the device_authorization response. verificationUriComplete
// already embeds the user code (e.g. "link.tidal.com/ABCDE").
type deviceAuth struct {
	DeviceCode              string `json:"deviceCode"`
	VerificationURIComplete string `json:"verificationUriComplete"`
	ExpiresIn               int    `json:"expiresIn"`
	Interval                int    `json:"interval"`
}

// tokenResponse is the oauth2/token response. Error is set on OAuth-level
// failures (e.g. "authorization_pending" while device-flow polling).
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
}

// postForm POSTs a form to an auth endpoint and decodes the JSON response.
// OAuth-level errors arrive with 4xx status codes and a JSON body, so those
// are decoded rather than treated as transport failures.
func postForm(ctx context.Context, httpc *http.Client, endpoint string, form url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("tidal: build auth request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", apiUA)

	resp, err := httpc.Do(req)
	if err != nil {
		return fmt.Errorf("tidal: auth request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return fmt.Errorf("tidal: read auth response: %w", err)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("tidal: HTTP %d: decode auth response: %w", resp.StatusCode, err)
	}
	return nil
}

// Sentinel errors for OAuth-level failures. errAuthPending and errSlowDown
// drive the device-flow poll loop (RFC 8628 error codes); errAuthRevoked
// means the refresh token was revoked or expired and the user must sign in
// again interactively.
var (
	errAuthPending = errors.New("tidal: authorization pending")
	errSlowDown    = errors.New("tidal: polling too fast")
	errAuthRevoked = errors.New("tidal: authorization revoked")
)

// requestToken calls the token endpoint. authorization_pending and slow_down
// are surfaced as sentinels so the device-flow poll loop can continue; other
// OAuth errors are returned as plain errors.
func requestToken(ctx context.Context, httpc *http.Client, endpoint string, form url.Values) (tokenResponse, error) {
	var tok tokenResponse
	if err := postForm(ctx, httpc, endpoint, form, &tok); err != nil {
		return tokenResponse{}, err
	}
	switch tok.Error {
	case "":
	case "authorization_pending":
		return tokenResponse{}, errAuthPending
	case "slow_down":
		return tokenResponse{}, errSlowDown
	case "invalid_grant", "expired_token":
		return tokenResponse{}, fmt.Errorf("%w: %s: %s", errAuthRevoked, tok.Error, tok.ErrorDesc)
	default:
		return tokenResponse{}, fmt.Errorf("tidal: %s: %s", tok.Error, tok.ErrorDesc)
	}
	if tok.AccessToken == "" {
		return tokenResponse{}, errors.New("tidal: token response missing access_token")
	}
	return tok, nil
}

// newClientSilent builds an authenticated client from stored credentials only.
// It never opens a browser; if no usable credentials exist it returns an error.
func newClientSilent(ctx context.Context) (*client, error) {
	creds, err := loadCreds()
	if err != nil {
		return nil, fmt.Errorf("tidal: no stored credentials: %w", err)
	}
	if creds.ClientID == "" || creds.RefreshToken == "" {
		return nil, fmt.Errorf("tidal: incomplete stored credentials")
	}

	c := newClient(creds.ClientID, creds.ClientSecret)
	c.accessToken = creds.AccessToken
	c.refreshToken = creds.RefreshToken
	c.tokenType = creds.TokenType
	c.expiresAt = creds.ExpiresAt
	c.userID = creds.UserID
	c.countryCode = creds.CountryCode

	if err := c.ensureToken(ctx, ""); err != nil {
		return nil, err
	}
	// Validates the token and refreshes session/country data.
	if err := c.loadSession(ctx); err != nil {
		return nil, fmt.Errorf("tidal: stored token rejected: %w", err)
	}
	_ = saveCreds(credsFromClient(c))
	return c, nil
}

// newClientInteractive runs the OAuth device flow: it shows a link.tidal.com
// URL (and opens it in a browser, best-effort), then polls the token endpoint
// until the user approves the device, persisting the result on success.
func newClientInteractive(ctx context.Context, clientID, clientSecret string) (*client, error) {
	c := newClient(clientID, clientSecret)

	var da deviceAuth
	err := postForm(ctx, c.http, deviceAuthURL, url.Values{
		"client_id": {clientID},
		"scope":     {oauthScope},
	}, &da)
	if err != nil {
		return nil, err
	}
	if da.DeviceCode == "" || da.VerificationURIComplete == "" {
		return nil, errors.New("tidal: device authorization failed (client_id may be revoked; set [tidal] client_id in config.toml)")
	}

	authURL := da.VerificationURIComplete
	if !strings.HasPrefix(authURL, "http") {
		authURL = "https://" + authURL
	}
	notifyAuthURL(authURL)
	_ = browser.Open(authURL) // best-effort; user can open the URL manually

	tok, err := pollDeviceToken(ctx, c.http, c.tokenURL, clientID, clientSecret, da)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.applyTokenLocked(tok)
	c.mu.Unlock()

	if err := c.loadSession(ctx); err != nil {
		return nil, fmt.Errorf("tidal: load session: %w", err)
	}
	if err := saveCreds(credsFromClient(c)); err != nil {
		applog.UserError("tidal: failed to save credentials: %v", err)
	}
	return c, nil
}

// pollDeviceToken polls the token endpoint until the user approves the device
// authorization, it expires, or ctx is cancelled.
func pollDeviceToken(ctx context.Context, httpc *http.Client, endpoint, clientID, clientSecret string, da deviceAuth) (tokenResponse, error) {
	form := url.Values{
		"grant_type":    {"urn:ietf:params:oauth:grant-type:device_code"},
		"device_code":   {da.DeviceCode},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"scope":         {oauthScope},
	}

	// RFC 8628 §3.2: clients MUST default to 5 seconds when the server
	// provides no polling interval.
	interval := time.Duration(da.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	expiry := time.Duration(da.ExpiresIn) * time.Second
	if expiry <= 0 {
		expiry = 5 * time.Minute
	}
	deadline := time.Now().Add(expiry)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return tokenResponse{}, fmt.Errorf("tidal: authentication cancelled: %w", ctx.Err())
		case <-ticker.C:
		}
		if time.Now().After(deadline) {
			return tokenResponse{}, errors.New("tidal: device authorization expired before approval")
		}
		tok, err := requestToken(ctx, httpc, endpoint, form)
		switch {
		case errors.Is(err, errAuthPending):
			continue
		case errors.Is(err, errSlowDown):
			// RFC 8628: back off by widening the poll interval.
			interval += 5 * time.Second
			ticker.Reset(interval)
			continue
		case err != nil:
			return tokenResponse{}, err
		}
		return tok, nil
	}
}
