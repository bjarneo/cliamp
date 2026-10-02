package ytmusic

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestSaveCredsWritesPrivateFile(t *testing.T) {
	tests := []struct {
		name     string
		existing os.FileMode // 0 means no file exists before the save
	}{
		{name: "new file"},
		{name: "replace private file", existing: 0o600},
		{name: "replace readable file", existing: 0o644},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
			path, err := credsFile.Path()
			if err != nil {
				t.Fatal(err)
			}
			if tt.existing != 0 {
				if err := os.WriteFile(path, []byte(`{"refresh_token":"old"}`), tt.existing); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(path, tt.existing); err != nil {
					t.Fatal(err)
				}
			}

			if err := credsFile.Save(&storedCreds{RefreshToken: "new"}); err != nil {
				t.Fatalf("credsFile.Save() error = %v", err)
			}
			got, err := credsFile.Load()
			if err != nil {
				t.Fatalf("credsFile.Load() error = %v", err)
			}
			if got.RefreshToken != "new" {
				t.Errorf("refresh token = %q, want new", got.RefreshToken)
			}
			if runtime.GOOS == "windows" {
				return // Windows does not report Unix permission bits.
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if perm := info.Mode().Perm(); perm != 0o600 {
				t.Errorf("credentials mode = %o, want 600", perm)
			}
		})
	}
}

// TestLoadStoredCredsFile checks that credentials an earlier release wrote
// still load, so an upgrade keeps the user signed in.
func TestLoadStoredCredsFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLIAMP_CONFIG_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "ytmusic_credentials.json"), []byte(`{"refresh_token":"refresh"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := credsFile.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.RefreshToken != "refresh" {
		t.Errorf("refresh token = %q, want refresh", got.RefreshToken)
	}
}

func TestOAuthCallbackHandler(t *testing.T) {
	const state = "expected-state"
	tests := []struct {
		name       string
		queries    []string
		wantStatus []int
		want       oauthResult // the zero value means the handler must not pass a result
	}{
		{
			name:       "valid callback",
			queries:    []string{"state=expected-state&code=abc"},
			wantStatus: []int{http.StatusOK},
			want:       oauthResult{code: "abc"},
		},
		{
			name:       "wrong state",
			queries:    []string{"state=other&code=abc"},
			wantStatus: []int{http.StatusBadRequest},
		},
		{
			name:       "missing state",
			queries:    []string{"code=abc"},
			wantStatus: []int{http.StatusBadRequest},
		},
		{
			name:       "missing code",
			queries:    []string{"state=expected-state"},
			wantStatus: []int{http.StatusBadRequest},
		},
		{
			name:       "duplicate callback",
			queries:    []string{"state=expected-state&code=first", "state=expected-state&code=second", "state=expected-state&code=third"},
			wantStatus: []int{http.StatusOK, http.StatusOK, http.StatusOK},
			want:       oauthResult{code: "first"},
		},
		{
			name:       "access denied",
			queries:    []string{"state=expected-state&error=access_denied"},
			wantStatus: []int{http.StatusOK},
			want:       oauthResult{errCode: "access_denied"},
		},
		{
			name:       "error with a code",
			queries:    []string{"state=expected-state&code=abc&error=server_error"},
			wantStatus: []int{http.StatusOK},
			want:       oauthResult{errCode: "server_error"},
		},
		{
			name:       "error with wrong state",
			queries:    []string{"state=other&error=access_denied"},
			wantStatus: []int{http.StatusBadRequest},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resultCh := make(chan oauthResult, 1)
			handler := oauthCallbackHandler(state, resultCh)
			for i, query := range tt.queries {
				rec := httptest.NewRecorder()
				done := make(chan struct{})
				go func() {
					defer close(done)
					handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/callback?"+query, nil))
				}()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Fatalf("callback %d blocked", i)
				}
				if rec.Code != tt.wantStatus[i] {
					t.Errorf("callback %d status = %d, want %d", i, rec.Code, tt.wantStatus[i])
				}
			}

			var got oauthResult
			select {
			case got = <-resultCh:
			default:
			}
			if got != tt.want {
				t.Errorf("result = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestDoOAuthReturnsAuthorizationError checks that a denied sign-in ends the
// wait at once instead of after the sign-in timeout. The retry reuses the
// HTTP client, as a browser does, so a kept-alive connection to the first
// callback server must not swallow the second callback.
func TestDoOAuthReturnsAuthorizationError(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // keep browser.Open from starting a real browser
	urls := make(chan string, 1)
	SetAuthURLObserver(func(u string) { urls <- u })
	t.Cleanup(func() { SetAuthURLObserver(nil) })
	client := &http.Client{}

	for _, attempt := range []string{"first sign-in", "retry after a denial"} {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		t.Cleanup(cancel)
		errCh := make(chan error, 1)
		go func() {
			_, err := doOAuth(ctx, "id", "secret")
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
		callback := fmt.Sprintf("http://127.0.0.1:%d/callback?state=%s&error=access_denied",
			CallbackPort, url.QueryEscape(parsed.Query().Get("state")))
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
				t.Fatalf("%s: doOAuth() error = %v, want access_denied", attempt, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: doOAuth() still waits after the error callback", attempt)
		}
	}
}
