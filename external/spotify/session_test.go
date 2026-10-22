package spotify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	librespotPlayer "github.com/devgianlu/go-librespot/player"
	"golang.org/x/oauth2"

	"github.com/bjarneo/cliamp/playlist"
)

type tokenSourceFunc func() (*oauth2.Token, error)

func (f tokenSourceFunc) Token() (*oauth2.Token, error) { return f() }

func TestCallbackAddressUsesIPv4Loopback(t *testing.T) {
	if got, want := callbackAddress(), "127.0.0.1:19872"; got != want {
		t.Fatalf("callbackAddress() = %q, want %q", got, want)
	}
}

func TestAwaitSpotifyStreamTimeoutCancelsTransportAndReleasesReadLock(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const timeout = 10 * time.Millisecond
		setupCtx, setupCancel := context.WithTimeout(t.Context(), timeout)
		defer setupCancel()

		streamCtx, streamCancel := context.WithCancel(context.Background())
		requestStarted := make(chan struct{})
		requestCanceled := make(chan error, 1)
		client := newSpotifyStreamHTTPClient(streamCtx, roundTripFunc(func(req *http.Request) (*http.Response, error) {
			close(requestStarted)
			<-req.Context().Done()
			requestCanceled <- req.Context().Err()
			return nil, req.Context().Err()
		}))

		var session Session
		openDone := make(chan struct{})
		start := time.Now()
		stream, cancel, err := awaitSpotifyStream(setupCtx, streamCancel, func() (*librespotPlayer.Stream, error) {
			session.mu.RLock()
			defer close(openDone)
			defer session.mu.RUnlock()

			req, err := http.NewRequest(http.MethodGet, "https://audio.example/initial", nil)
			if err != nil {
				return nil, err
			}
			_, err = client.Do(req)
			return nil, err
		})

		if stream != nil || cancel != nil {
			t.Fatalf("awaitSpotifyStream() = (%v, %v), want nil stream and cancel", stream, cancel)
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("awaitSpotifyStream() error = %v, want context.DeadlineExceeded", err)
		}
		if elapsed := time.Since(start); elapsed != timeout {
			t.Errorf("awaitSpotifyStream() returned after %v, want %v", elapsed, timeout)
		}

		synctest.Wait()
		select {
		case <-requestStarted:
		default:
			t.Fatal("transport request did not start")
		}
		select {
		case err := <-requestCanceled:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("transport context error = %v, want context.Canceled", err)
			}
		default:
			t.Fatal("transport request was not canceled")
		}
		select {
		case <-openDone:
		default:
			t.Fatal("stream setup goroutine did not exit")
		}
		if !session.mu.TryLock() {
			t.Fatal("stream setup retained the session read lock")
		}
		session.mu.Unlock()
	})
}

func TestAwaitSpotifyStreamRecoversSetupPanic(t *testing.T) {
	streamCtx, streamCancel := context.WithCancel(context.Background())
	var session Session

	stream, cancel, err := awaitSpotifyStream(t.Context(), streamCancel, func() (*librespotPlayer.Stream, error) {
		session.mu.RLock()
		defer session.mu.RUnlock()
		var params *struct{ LoudnessDb float32 }
		_ = params.LoudnessDb // nil dereference, as in librespot v0.9.0 normalisation
		return nil, nil
	})

	if stream != nil || cancel != nil {
		t.Fatalf("awaitSpotifyStream() = (%v, %v), want nil stream and cancel", stream, cancel)
	}
	if err == nil {
		t.Fatal("awaitSpotifyStream() error = nil, want recovered panic")
	}
	if streamCtx.Err() == nil {
		t.Error("stream context was not canceled")
	}
	if !session.mu.TryLock() {
		t.Fatal("stream setup retained the session read lock")
	}
	session.mu.Unlock()
}

func TestIsInvalidGrant(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"plain error", errors.New("network blip"), false},
		{"oauth invalid_grant", &oauth2.RetrieveError{ErrorCode: "invalid_grant"}, true},
		{"oauth invalid_request", &oauth2.RetrieveError{ErrorCode: "invalid_request"}, false},
		{"wrapped invalid_grant", fmt.Errorf("refresh failed: %w", &oauth2.RetrieveError{ErrorCode: "invalid_grant"}), true},
		{"wrapped non-oauth", fmt.Errorf("refresh failed: %w", errors.New("transport error")), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isInvalidGrant(tt.err)
			if got != tt.want {
				t.Errorf("isInvalidGrant(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestInteractiveOAuthFlows(t *testing.T) {
	tests := []struct {
		name     string
		clientID string
		want     []oauthFlow
	}{
		{
			name:     "keymaster uses one flow",
			clientID: PlaybackClientID,
			want:     []oauthFlow{{name: "web api and playback", clientID: PlaybackClientID, scopes: oauthScopes}},
		},
		{
			name:     "built-in web api client authorizes playback through keymaster",
			clientID: DefaultClientID,
			want: []oauthFlow{
				{name: "web api", clientID: DefaultClientID, scopes: webAPIScopes(DefaultClientID)},
				{name: "playback", clientID: PlaybackClientID, scopes: playbackOAuthScopes},
			},
		},
		{
			name:     "custom client authorizes playback through keymaster",
			clientID: "custom-client",
			want: []oauthFlow{
				{name: "web api", clientID: "custom-client", scopes: webAPIScopes("custom-client")},
				{name: "playback", clientID: PlaybackClientID, scopes: playbackOAuthScopes},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := interactiveOAuthFlows(tt.clientID)
			if len(got) != len(tt.want) {
				t.Fatalf("OAuth flows = %d, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if got[i].name != tt.want[i].name || got[i].clientID != tt.want[i].clientID || !slices.Equal(got[i].scopes, tt.want[i].scopes) {
					t.Errorf("OAuth flow %d = (%q, %q, %v), want (%q, %q, %v)", i, got[i].name, got[i].clientID, got[i].scopes, tt.want[i].name, tt.want[i].clientID, tt.want[i].scopes)
				}
			}
		})
	}
}

func TestOAuthCallbackHandlerChainsFlows(t *testing.T) {
	pending := []pendingOAuthFlow{
		{state: "web-state"},
		{state: "playback-state", authURL: "https://accounts.spotify.com/playback"},
	}
	callbacks := make(chan oauthCallback, len(pending))
	handler := oauthCallbackHandler(pending, callbacks)

	webResponse := httptest.NewRecorder()
	handler.ServeHTTP(webResponse, httptest.NewRequest(http.MethodGet, "/login?state=web-state&code=web-code", nil))
	if webResponse.Code != http.StatusFound {
		t.Fatalf("web callback status = %d, want %d", webResponse.Code, http.StatusFound)
	}
	if location := webResponse.Header().Get("Location"); location != pending[1].authURL {
		t.Errorf("web callback location = %q, want %q", location, pending[1].authURL)
	}
	if callback := <-callbacks; callback.flow != 0 || callback.code != "web-code" || callback.err != nil {
		t.Errorf("web callback = %+v, want flow 0 with web-code", callback)
	}

	// Browser retries must not submit the same authorization code twice.
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/login?state=web-state&code=web-code", nil))
	if len(callbacks) != 0 {
		t.Fatalf("duplicate callback queued %d extra result(s)", len(callbacks))
	}

	playbackResponse := httptest.NewRecorder()
	handler.ServeHTTP(playbackResponse, httptest.NewRequest(http.MethodGet, "/login?state=playback-state&code=playback-code", nil))
	if playbackResponse.Code != http.StatusOK {
		t.Fatalf("playback callback status = %d, want %d", playbackResponse.Code, http.StatusOK)
	}
	if callback := <-callbacks; callback.flow != 1 || callback.code != "playback-code" || callback.err != nil {
		t.Errorf("playback callback = %+v, want flow 1 with playback-code", callback)
	}
}

func TestOAuthCallbackHandlerRejectsUnknownState(t *testing.T) {
	callbacks := make(chan oauthCallback, 1)
	handler := oauthCallbackHandler([]pendingOAuthFlow{{state: "known"}}, callbacks)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/login?state=unknown&code=code", nil))

	if response.Code != http.StatusBadRequest {
		t.Errorf("unknown state status = %d, want %d", response.Code, http.StatusBadRequest)
	}
	if len(callbacks) != 0 {
		t.Errorf("unknown state queued %d callback(s), want 0", len(callbacks))
	}
}

func TestPersistingTokenSourcePersistsRotationOnce(t *testing.T) {
	var persisted []string
	source := &persistingTokenSource{
		source:       tokenSourceFunc(func() (*oauth2.Token, error) { return &oauth2.Token{AccessToken: "access", RefreshToken: "new"}, nil }),
		refreshToken: "old",
		persist: func(refreshToken string) error {
			persisted = append(persisted, refreshToken)
			return nil
		},
	}

	for range 2 {
		token, err := source.Token()
		if err != nil {
			t.Fatalf("Token() error = %v", err)
		}
		if token.AccessToken != "access" {
			t.Errorf("access token = %q, want access", token.AccessToken)
		}
	}
	if !slices.Equal(persisted, []string{"new"}) {
		t.Errorf("persisted refresh tokens = %v, want [new]", persisted)
	}
}

func TestPersistingTokenSourceDoesNotBlockOnSaveFailure(t *testing.T) {
	wantErr := errors.New("disk full")
	attempts := 0
	source := &persistingTokenSource{
		source:       tokenSourceFunc(func() (*oauth2.Token, error) { return &oauth2.Token{AccessToken: "access", RefreshToken: "new"}, nil }),
		refreshToken: "old",
		persist: func(string) error {
			attempts++
			return wantErr
		},
	}

	for range 2 {
		token, err := source.Token()
		if err != nil {
			t.Fatalf("Token() error = %v, want successful access despite persistence failure", err)
		}
		if token.AccessToken != "access" {
			t.Errorf("access token = %q, want access", token.AccessToken)
		}
	}
	if attempts != 1 {
		t.Errorf("persistence attempts = %d, want 1 for one rotated token", attempts)
	}
}

func TestWebAPITokenSourcePersistsRotatedRefreshToken(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stored := storedCreds{
		Username:     "user",
		Data:         []byte("playback-credential"),
		DeviceID:     "device",
		RefreshToken: "old",
	}
	if err := credsFile.Save(&stored); err != nil {
		t.Fatal(err)
	}

	source := webAPITokenSource("client", &oauth2.Token{
		AccessToken:  "access",
		RefreshToken: "new",
		Expiry:       time.Now().Add(time.Hour),
	}, stored)
	if _, err := source.Token(); err != nil {
		t.Fatalf("Token() error = %v", err)
	}

	got, err := credsFile.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.Username != stored.Username || got.DeviceID != stored.DeviceID || !slices.Equal(got.Data, stored.Data) {
		t.Errorf("non-OAuth credentials changed: got %+v, want username/device/data from %+v", got, stored)
	}
	if got.RefreshToken != "new" {
		t.Errorf("refresh token = %q, want new", got.RefreshToken)
	}
	path, err := CredsPath()
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if mode := info.Mode().Perm(); mode != 0o600 {
			t.Errorf("credentials mode = %o, want 600", mode)
		}
	}
}

func TestDeleteCreds(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	t.Run("missing file", func(t *testing.T) {
		removed, err := DeleteCreds()
		if err != nil {
			t.Errorf("DeleteCreds() on missing file returned %v, want nil", err)
		}
		if removed {
			t.Error("DeleteCreds() reported removed=true for missing file")
		}
	})

	t.Run("removes existing file", func(t *testing.T) {
		dir := filepath.Join(home, ".config", "cliamp")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "spotify_credentials.json")
		if err := os.WriteFile(path, []byte(`{"username":"x"}`), 0o600); err != nil {
			t.Fatal(err)
		}

		removed, err := DeleteCreds()
		if err != nil {
			t.Fatalf("DeleteCreds() = %v, want nil", err)
		}
		if !removed {
			t.Error("DeleteCreds() reported removed=false after removing file")
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("file still exists after DeleteCreds: stat err = %v", err)
		}
	})
}

func TestCredsPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got, err := CredsPath()
	if err != nil {
		t.Fatalf("CredsPath() error = %v", err)
	}
	want := filepath.Join(home, ".config", "cliamp", "spotify_credentials.json")
	if got != want {
		t.Errorf("CredsPath() = %q, want %q", got, want)
	}
}

// TestLoadStoredCredsFile checks that credentials an earlier release wrote
// still load, so an upgrade keeps the user signed in.
func TestLoadStoredCredsFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLIAMP_CONFIG_DIR", dir)
	data := `{"username":"user","data":"cGxheQ==","device_id":"device","refresh_token":"refresh"}`
	if err := os.WriteFile(filepath.Join(dir, "spotify_credentials.json"), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := credsFile.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := storedCreds{Username: "user", Data: []byte("play"), DeviceID: "device", RefreshToken: "refresh"}
	if got.Username != want.Username || !slices.Equal(got.Data, want.Data) || got.DeviceID != want.DeviceID || got.RefreshToken != want.RefreshToken {
		t.Errorf("Load() = %+v, want %+v", got, want)
	}
}

func TestSessionBearer(t *testing.T) {
	t.Parallel()
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	refreshErr := errors.New("token endpoint down")
	tests := []struct {
		name      string
		ctx       context.Context
		source    func() (*oauth2.Token, error) // nil means no token source
		want      string
		wantErr   error
		wantCalls int
	}{
		{
			name:      "token",
			ctx:       context.Background(),
			source:    func() (*oauth2.Token, error) { return &oauth2.Token{AccessToken: "token"}, nil },
			want:      "token",
			wantCalls: 1,
		},
		{name: "no token source", ctx: context.Background(), wantErr: playlist.ErrNeedsAuth},
		{
			name:      "refresh fails",
			ctx:       context.Background(),
			source:    func() (*oauth2.Token, error) { return nil, refreshErr },
			wantErr:   refreshErr,
			wantCalls: 1,
		},
		{
			name:      "refresh token revoked",
			ctx:       context.Background(),
			source:    func() (*oauth2.Token, error) { return nil, &oauth2.RetrieveError{ErrorCode: "invalid_grant"} },
			wantErr:   playlist.ErrNeedsAuth,
			wantCalls: 1,
		},
		{
			name:    "ended context sends no refresh",
			ctx:     cancelled,
			source:  func() (*oauth2.Token, error) { return &oauth2.Token{AccessToken: "token"}, nil },
			wantErr: context.Canceled,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			s := &Session{}
			if tt.source != nil {
				s.tokenSource = tokenSourceFunc(func() (*oauth2.Token, error) {
					calls++
					return tt.source()
				})
			}
			got, err := s.bearer(tt.ctx)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("bearer() error = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want || calls != tt.wantCalls {
				t.Errorf("bearer() = %q after %d token calls, want %q after %d", got, calls, tt.want, tt.wantCalls)
			}
		})
	}
}

// TestPerformOAuthReturnsAuthorizationError checks that a denied sign-in
// ends the wait at once. The retry reuses the HTTP client, as a browser
// does, so a kept-alive connection to the first callback server must not
// swallow the second callback.
func TestPerformOAuthReturnsAuthorizationError(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // keep browser.Open from starting a real browser
	urls := make(chan string, 1)
	SetAuthURLObserver(func(u string) { urls <- u })
	t.Cleanup(func() { SetAuthURLObserver(nil) })
	client := &http.Client{}
	flows := []oauthFlow{{name: "web api", clientID: "client", scopes: oauthScopes}}

	for _, attempt := range []string{"first sign-in", "retry after a denial"} {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		t.Cleanup(cancel)
		errCh := make(chan error, 1)
		go func() {
			_, err := performOAuth2PKCEFlows(ctx, flows)
			errCh <- err
		}()

		var authURL string
		select {
		case authURL = <-urls:
		case err := <-errCh:
			t.Skipf("%s: callback port unavailable: %v", attempt, err)
		}
		parsed, err := url.Parse(authURL)
		if err != nil {
			t.Fatal(err)
		}
		callback := fmt.Sprintf("http://%s/login?state=%s&error=access_denied",
			callbackAddress(), url.QueryEscape(parsed.Query().Get("state")))
		resp, err := client.Get(callback)
		if err != nil {
			t.Fatalf("%s: %v", attempt, err)
		}
		// Read the whole body, so the client can keep the connection.
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()

		select {
		case err := <-errCh:
			if err == nil || !strings.Contains(err.Error(), "access_denied") {
				t.Fatalf("%s: performOAuth2PKCEFlows() error = %v, want access_denied", attempt, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: performOAuth2PKCEFlows() still waits after the error callback", attempt)
		}
	}
}

func TestBuiltInClientIDsAreDistinct(t *testing.T) {
	// Routing Web API calls through keymaster puts them in a quota pool shared
	// with every librespot-based player, which is what caused the 429s these
	// two constants were split to fix.
	if DefaultClientID == PlaybackClientID {
		t.Fatal("web api and playback client IDs must stay distinct")
	}
	if !allowsFullSearchPage(DefaultClientID) {
		t.Error("built-in web api client ID is not in Development Mode, want a full search page")
	}
	if !allowsFullSearchPage(PlaybackClientID) {
		t.Error("keymaster is not in Development Mode, want a full search page")
	}
	if allowsFullSearchPage("custom-client") {
		t.Error("a user-supplied client ID must be assumed to be in Development Mode")
	}
}

func TestWebAPIScopesExcludeStreamingForNonKeymasterClients(t *testing.T) {
	// login5 rejects playback credentials minted by anything but keymaster, so
	// asking a separate Web API client for "streaming" buys nothing and shows
	// the user a permission the client cannot use.
	got := webAPIScopes(DefaultClientID)
	if slices.Contains(got, "streaming") {
		t.Error(`webAPIScopes(DefaultClientID) contains "streaming", want it dropped`)
	}
	if len(got) != len(oauthScopes)-1 {
		t.Errorf("webAPIScopes(DefaultClientID) dropped %d scopes, want 1", len(oauthScopes)-len(got))
	}
	for _, scope := range oauthScopes {
		if scope != "streaming" && !slices.Contains(got, scope) {
			t.Errorf("webAPIScopes(DefaultClientID) is missing %q", scope)
		}
	}

	// Keymaster authorizes playback in the same flow, so it keeps the full set.
	if keymaster := webAPIScopes(PlaybackClientID); !slices.Equal(keymaster, oauthScopes) {
		t.Errorf("webAPIScopes(PlaybackClientID) = %v, want the full scope set", keymaster)
	}
}
