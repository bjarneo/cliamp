package ytmusic

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// setOAuthTransport sends OAuth token requests of the test through rt.
func setOAuthTransport(t *testing.T, timeout time.Duration, rt http.RoundTripper) {
	t.Helper()
	original := oauthHTTPClient
	oauthHTTPClient = &http.Client{Timeout: timeout, Transport: rt}
	t.Cleanup(func() { oauthHTTPClient = original })
}

func TestSilentSessionTimesOut(t *testing.T) {
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
	if err := credsFile.Save(&storedCreds{RefreshToken: "refresh"}); err != nil {
		t.Fatal(err)
	}
	setOAuthTransport(t, 50*time.Millisecond, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		<-req.Context().Done()
		return nil, req.Context().Err()
	}))

	done := make(chan error, 1)
	go func() {
		_, err := NewSessionSilent(context.Background(), "client", "secret")
		done <- err
	}()
	select {
	case err := <-done:
		var urlErr *url.Error
		if !errors.As(err, &urlErr) || !urlErr.Timeout() {
			t.Fatalf("NewSessionSilent() error = %v, want a client timeout", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("NewSessionSilent() did not time out")
	}
}

// TestSessionTokenRefreshOutlivesSetupContext checks that a session still
// refreshes its access token after the context that built it ends.
func TestSessionTokenRefreshOutlivesSetupContext(t *testing.T) {
	setOAuthTransport(t, 5*time.Second, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if err := req.Context().Err(); err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"access_token":"new","token_type":"Bearer","expires_in":3600}`)),
			Request:    req,
		}, nil
	}))

	ctx, cancel := context.WithCancel(context.Background())
	expired := &oauth2.Token{AccessToken: "old", RefreshToken: "refresh", Expiry: time.Now().Add(-time.Hour)}
	s, err := newTokenSession(ctx, "client", "secret", expired, "refresh")
	if err != nil {
		t.Fatalf("newTokenSession() error = %v", err)
	}
	cancel()

	token, err := s.tokenSource.Token()
	if err != nil {
		t.Fatalf("Token() after the setup context ended: %v", err)
	}
	if token.AccessToken != "new" {
		t.Errorf("access token = %q, want new", token.AccessToken)
	}
}

// TestInteractiveSessionRefreshesAfterSignIn checks that a session from the
// browser sign-in still refreshes its access token after initSession cancels
// the sign-in context.
func TestInteractiveSessionRefreshesAfterSignIn(t *testing.T) {
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
	t.Setenv("PATH", t.TempDir()) // keep browser.Open from starting a real browser
	var requests atomic.Int32
	setOAuthTransport(t, 5*time.Second, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if err := req.Context().Err(); err != nil {
			return nil, err
		}
		// The first token expires at once, so the next Token call refreshes it.
		n := requests.Add(1)
		body := fmt.Sprintf(`{"access_token":"access-%d","refresh_token":"refresh","token_type":"Bearer","expires_in":1}`, n)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    req,
		}, nil
	}))
	urls := make(chan string, 1)
	SetAuthURLObserver(func(u string) { urls <- u })
	t.Cleanup(func() { SetAuthURLObserver(nil) })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	type result struct {
		s   *Session
		err error
	}
	done := make(chan result, 1)
	go func() {
		s, err := newInteractiveSession(ctx, "client", "secret")
		done <- result{s, err}
	}()

	var authURL string
	select {
	case authURL = <-urls:
	case r := <-done:
		t.Skipf("callback port unavailable: %v", r.err)
	}
	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	callback := fmt.Sprintf("http://127.0.0.1:%d/callback?state=%s&code=abc",
		CallbackPort, url.QueryEscape(parsed.Query().Get("state")))
	resp, err := http.Get(callback)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	var r result
	select {
	case r = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("newInteractiveSession() did not return after the callback")
	}
	if r.err != nil {
		t.Fatalf("newInteractiveSession() error = %v", r.err)
	}
	cancel() // initSession cancels the sign-in context when the sign-in ends

	token, err := r.s.tokenSource.Token()
	if err != nil {
		t.Fatalf("Token() after the sign-in context ended: %v", err)
	}
	if token.AccessToken != "access-2" {
		t.Errorf("access token = %q, want the refreshed access-2", token.AccessToken)
	}
}

// TestDataAPIRequestsKeepNoClientTimeout checks that the Data API client
// shares only the transport of oauthHTTPClient. A Data API request that
// takes longer than the token client timeout still succeeds.
func TestDataAPIRequestsKeepNoClientTimeout(t *testing.T) {
	const tokenTimeout = 50 * time.Millisecond
	setOAuthTransport(t, tokenTimeout, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		select {
		case <-time.After(4 * tokenTimeout):
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"items":[]}`)),
			Request:    req,
		}, nil
	}))

	token := &oauth2.Token{AccessToken: "token", Expiry: time.Now().Add(time.Hour)}
	s, err := newTokenSession(context.Background(), "client", "secret", token, "refresh")
	if err != nil {
		t.Fatalf("newTokenSession() error = %v", err)
	}
	if _, err := s.Service().Playlists.List([]string{"snippet"}).Mine(true).Do(); err != nil {
		t.Fatalf("Playlists.List().Do() error = %v, want no client timeout", err)
	}
}
