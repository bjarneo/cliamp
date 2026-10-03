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
	"strconv"
	"strings"
	"time"

	"github.com/bjarneo/cliamp/applog"
	"github.com/bjarneo/cliamp/playlist"
)

// webAPI calls the Spotify Web API via the session with retry on 429.
func (p *SpotifyProvider) webAPI(ctx context.Context, method, path string, query url.Values) (*http.Response, error) {
	return p.webAPIWithRetry(ctx, method, path, query, nil, "", http.StatusOK)
}

// webAPIWithRetry is like webAPI but accepts an optional request body, content type,
// and a set of acceptable HTTP status codes (e.g. 200, 201). Retries 429 with
// exponential backoff (honoring Retry-After when present). Each attempt goes
// through Session.webAPIOnce.
func (p *SpotifyProvider) webAPIWithRetry(ctx context.Context, method, path string, query url.Values, body io.Reader, contentType string, acceptStatus ...int) (*http.Response, error) {
	const maxRetries = 8

	// Capture the session once: Close can clear p.session while a request
	// still runs.
	p.mu.Lock()
	sess := p.session
	p.mu.Unlock()
	if sess == nil {
		return nil, playlist.ErrNeedsAuth
	}

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

		resp, err := sess.webAPIOnce(ctx, method, path, query, reqBody, contentType)
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
			// A long Retry-After means the app has no quota left for hours.
			// Waiting would only end in a timeout, so report it now.
			if wait > maxRateLimitWait || waitExceedsDeadline(ctx, wait) {
				return nil, p.rateLimitError(path, fmt.Sprintf("Spotify asks to wait %v", wait))
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
			apiErr := newAPIError(resp.StatusCode, respBody)
			// Spotify rejected the access token, so ask for sign-in.
			if apiErr.status == http.StatusUnauthorized {
				return nil, fmt.Errorf("%w: %w", playlist.ErrNeedsAuth, apiErr)
			}
			return nil, apiErr
		}
		return resp, nil
	}
	return nil, p.rateLimitError(path, fmt.Sprintf("still limited after %d retries", maxRetries))
}

// webAPIOnce sends one Web API request with the OAuth2 access token. It does
// not retry a 429 and does not check the status, so provider code calls
// SpotifyProvider.webAPIWithRetry instead.
//
// The spclient/login5 token from librespot is NOT accepted by the Web API
// for endpoints like /v1/search and /v1/me/playlists — Spotify returns
// misleading errors ("Invalid limit", 429) instead of a clear auth failure.
// So if there is no OAuth2 token source, fail loudly with ErrNeedsAuth
// rather than attempting the call with the wrong token.
func (s *Session) webAPIOnce(ctx context.Context, method, path string, query url.Values, body io.Reader, contentType string) (*http.Response, error) {
	token, err := s.bearer(ctx)
	if err != nil {
		return nil, err
	}

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

	return s.webClient().Do(req)
}

// bearer returns the OAuth2 access token for Web API and lyrics requests. The
// token source refreshes an expired token. When ctx has ended, bearer returns
// ctx.Err() and sends no refresh request. A session without a token source,
// or with a revoked refresh token, returns playlist.ErrNeedsAuth.
func (s *Session) bearer(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	s.mu.RLock()
	ts := s.tokenSource
	s.mu.RUnlock()
	if ts == nil {
		return "", fmt.Errorf("spotify: web api token unavailable, sign in again: %w", playlist.ErrNeedsAuth)
	}
	tok, err := ts.Token()
	if isInvalidGrant(err) {
		return "", fmt.Errorf("spotify: web api token revoked, sign in again: %w", playlist.ErrNeedsAuth)
	}
	if err != nil {
		return "", fmt.Errorf("refresh access token: %w", err)
	}
	return tok.AccessToken, nil
}

// maxRateLimitWait is the longest Retry-After that webAPIWithRetry waits for.
const maxRateLimitWait = time.Minute

// waitExceedsDeadline reports whether ctx expires before wait ends.
func waitExceedsDeadline(ctx context.Context, wait time.Duration) bool {
	deadline, ok := ctx.Deadline()
	return ok && time.Until(deadline) < wait
}

// rateLimitError explains a 429 that retries cannot fix. Signing in again
// does not help, because Spotify limits the app, not the account.
func (p *SpotifyProvider) rateLimitError(path, detail string) error {
	hint := "try again later"
	if p.clientID == DefaultClientID {
		hint = "the built-in client ID shares its quota with other apps, so set client_id in [spotify] to get your own quota"
	}
	return fmt.Errorf("spotify: web api rate-limited on %s, %s: %s", path, detail, hint)
}

// apiError is a Web API response with a status the caller did not accept.
type apiError struct {
	status  int
	message string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("http status %d: %s", e.status, e.message)
}

// newAPIError reads the message from a Spotify error body. Web API errors
// use {"error":{"message":...}}, and OAuth errors use error_description.
func newAPIError(status int, body []byte) *apiError {
	var parsed struct {
		Error json.RawMessage `json:"error"`
		Desc  string          `json:"error_description"`
	}
	msg := strings.TrimSpace(string(body))
	if json.Unmarshal(body, &parsed) == nil {
		var nested struct {
			Message string `json:"message"`
		}
		var flat string
		switch {
		case json.Unmarshal(parsed.Error, &nested) == nil && nested.Message != "":
			msg = nested.Message
		case parsed.Desc != "":
			msg = parsed.Desc
		case json.Unmarshal(parsed.Error, &flat) == nil && flat != "":
			msg = flat
		}
	}
	if msg == "" {
		msg = http.StatusText(status)
	}
	return &apiError{status: status, message: msg}
}

// hasStatus reports whether err wraps an apiError with the given status.
func hasStatus(err error, status int) bool {
	var apiErr *apiError
	return errors.As(err, &apiErr) && apiErr.status == status
}

// decodeBody reads and decodes a JSON response body, then closes it.
func decodeBody(resp *http.Response, v any) error {
	defer resp.Body.Close()
	return json.NewDecoder(io.LimitReader(resp.Body, maxResponseBody)).Decode(v)
}
