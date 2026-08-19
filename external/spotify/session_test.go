//go:build !windows

package spotify

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	librespotPlayer "github.com/devgianlu/go-librespot/player"
	"golang.org/x/oauth2"
)

type tokenSourceFunc func() (*oauth2.Token, error)

func (f tokenSourceFunc) Token() (*oauth2.Token, error) { return f() }

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
			name:     "built-in client uses one flow",
			clientID: DefaultClientID,
			want:     []oauthFlow{{name: "web api and playback", clientID: DefaultClientID, scopes: oauthScopes}},
		},
		{
			name:     "custom client authorizes playback through keymaster",
			clientID: "custom-client",
			want: []oauthFlow{
				{name: "web api", clientID: "custom-client", scopes: oauthScopes},
				{name: "playback", clientID: DefaultClientID, scopes: playbackOAuthScopes},
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
	if err := saveCreds(&stored); err != nil {
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

	got, err := loadCreds()
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
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("credentials mode = %o, want 600", mode)
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
