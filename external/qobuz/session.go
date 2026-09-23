package qobuz

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/bjarneo/cliamp/applog"
	"github.com/bjarneo/cliamp/internal/browser"
)

// authURLObserver is invoked with the OAuth URL when interactive auth begins.
// Used by the TUI to display the URL when the launched browser does not reach
// the user (containers, headless environments).
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
	applog.Info("qobuz: sign-in URL: %s", u)
	if p := authURLObserver.Load(); p != nil {
		(*p)(u)
	}
}

// oauthResult holds the data captured from a Qobuz OAuth redirect.
type oauthResult struct {
	Token  string
	UserID string
	Code   string
}

// newClientSilent builds an authenticated client from stored credentials only.
// It never opens a browser; if no usable credentials exist it returns an error.
func newClientSilent(ctx context.Context) (*client, error) {
	creds, err := loadCreds()
	if err != nil {
		return nil, fmt.Errorf("qobuz: no stored credentials: %w", err)
	}
	if creds.AppID == "" || creds.UserAuthToken == "" {
		return nil, fmt.Errorf("qobuz: incomplete stored credentials")
	}

	c := newClient(creds.AppID, creds.Secrets)
	c.secret = creds.Secret
	c.uat = creds.UserAuthToken
	c.userID = creds.UserID
	c.label = creds.Label
	if c.secret == "" {
		if err := c.validateSecret(ctx); err != nil {
			return nil, err
		}
	}

	if err := c.authWithToken(ctx, creds.UserID, creds.UserAuthToken); err != nil {
		return nil, fmt.Errorf("qobuz: stored token rejected: %w", err)
	}
	// Re-persist in case the validated secret or label changed.
	_ = saveCreds(credsFromClient(c, creds.PrivateKey))
	return c, nil
}

// newClientInteractive scrapes fresh credentials from the Qobuz web player and
// runs the interactive OAuth browser flow, persisting the result on success.
func newClientInteractive(ctx context.Context) (*client, error) {
	appID, secrets, privateKey, err := scrapeCredentials(ctx)
	if err != nil {
		return nil, fmt.Errorf("qobuz: scrape credentials: %w", err)
	}

	c := newClient(appID, secrets)

	// OAuth first: the secret-validation request is signed with the candidate
	// secret and requires a valid user_auth_token, so the browser sign-in must
	// complete before validateSecret runs.
	result, err := captureOAuthRedirect(ctx, appID)
	if err != nil {
		return nil, err
	}
	if err := c.loginWithOAuth(ctx, result, privateKey); err != nil {
		return nil, fmt.Errorf("qobuz: OAuth login: %w", err)
	}
	if err := c.validateSecret(ctx); err != nil {
		return nil, err
	}

	if err := saveCreds(credsFromClient(c, privateKey)); err != nil {
		applog.UserError("qobuz: failed to save credentials: %v", err)
	}
	return c, nil
}

func credsFromClient(c *client, privateKey string) *storedCreds {
	return &storedCreds{
		AppID:         c.appID,
		Secrets:       c.secrets,
		Secret:        c.secret,
		PrivateKey:    privateKey,
		UserAuthToken: c.uat,
		UserID:        c.userID,
		Label:         c.label,
	}
}

// oauthCallbackHTML is shown in the browser once the redirect is captured.
const oauthCallbackHTML = `<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>cliamp</title></head>
<body style="font-family:system-ui;display:flex;justify-content:center;align-items:center;height:100vh;margin:0;background:#1a1a2e;color:#e0e0e0">
<div style="text-align:center">
<h2>Signed in to Qobuz</h2>
<p>You can close this tab now.</p>
<script>setTimeout(function(){window.close()},1500)</script>
</div></body></html>`

// captureOAuthRedirect starts a local HTTP server on a random port, opens the
// Qobuz OAuth URL in the browser, and waits for the redirect carrying the
// token or code. Qobuz accepts any localhost redirect_url, so a random port is
// fine.
func captureOAuthRedirect(ctx context.Context, appID string) (oauthResult, error) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return oauthResult{}, fmt.Errorf("qobuz: open local port: %w", err)
	}
	defer lis.Close()
	port := lis.Addr().(*net.TCPAddr).Port

	// Qobuz validates the redirect_url host server-side and only accepts
	// "localhost" (not 127.0.0.1). We still bind 127.0.0.1 below; browsers
	// fall back from localhost ([::1]) to 127.0.0.1, so the capture works.
	// This matches the proven SofusA/qobine reference flow.
	//
	// Note: after authorizing, Qobuz shows a "you are signed in, you can leave
	// this page" screen with a Back button rather than redirecting back. The
	// user must click Back to fire the redirect (see docs/qobuz.md). This is
	// Qobuz's behavior; URL-encoding the redirect_url does not change it.
	authURL := fmt.Sprintf(
		"https://www.qobuz.com/signin/oauth?ext_app_id=%s&redirect_url=http://localhost:%d",
		appID, port,
	)
	notifyAuthURL(authURL)

	resultCh := make(chan oauthResult, 1)
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			applog.Debug("qobuz: oauth redirect received: %s", r.URL.RequestURI())
			res := parseQueryParams(r.URL.Query())
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(oauthCallbackHTML))
			if res.Token != "" || res.Code != "" {
				select {
				case resultCh <- res:
				default:
				}
			} else {
				applog.Debug("qobuz: oauth redirect had no token/code param")
			}
		}),
	}
	go func() { _ = srv.Serve(lis) }()
	defer func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	_ = browser.Open(authURL) // best-effort; user can open the URL manually

	select {
	case res := <-resultCh:
		return res, nil
	case <-ctx.Done():
		return oauthResult{}, fmt.Errorf("qobuz: authentication cancelled: %w", ctx.Err())
	case <-time.After(5 * time.Minute):
		return oauthResult{}, errors.New("qobuz: timed out waiting for OAuth redirect")
	}
}

// parseQueryParams extracts token/code/user_id from a Qobuz redirect. Qobuz has
// used several parameter names across auth flow versions.
func parseQueryParams(params url.Values) oauthResult {
	var res oauthResult
	if t := params.Get("user_auth_token"); t != "" {
		res.Token = t
	}
	if t := params.Get("token"); t != "" && res.Token == "" {
		res.Token = t
	}
	if uid := params.Get("user_id"); uid != "" {
		res.UserID = uid
	}
	if code := params.Get("code_autorisation"); code != "" { // French spelling, Qobuz's actual param
		res.Code = code
	}
	if code := params.Get("code"); code != "" && res.Code == "" {
		res.Code = code
	}
	return res
}
