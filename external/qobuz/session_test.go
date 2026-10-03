package qobuz

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseQueryParams(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  oauthResult
	}{
		{name: "empty", query: "", want: oauthResult{}},
		{name: "user_auth_token", query: "user_auth_token=uat&user_id=7", want: oauthResult{Token: "uat", UserID: "7"}},
		{name: "token", query: "token=tok", want: oauthResult{Token: "tok"}},
		{name: "user_auth_token wins over token", query: "token=tok&user_auth_token=uat", want: oauthResult{Token: "uat"}},
		{name: "code_autorisation", query: "code_autorisation=fr", want: oauthResult{Code: "fr"}},
		{name: "code", query: "code=en", want: oauthResult{Code: "en"}},
		{name: "code_autorisation wins over code", query: "code=en&code_autorisation=fr", want: oauthResult{Code: "fr"}},
		{name: "token and code are both kept", query: "token=tok&code=en&user_id=7", want: oauthResult{Token: "tok", UserID: "7", Code: "en"}},
		{name: "empty values are ignored", query: "user_auth_token=&token=tok&code_autorisation=&code=en", want: oauthResult{Token: "tok", Code: "en"}},
		{name: "unrelated params", query: "state=x&foo=bar", want: oauthResult{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q, err := url.ParseQuery(tt.query)
			if err != nil {
				t.Fatal(err)
			}
			if got := parseQueryParams(q); got != tt.want {
				t.Errorf("parseQueryParams(%q) = %+v, want %+v", tt.query, got, tt.want)
			}
		})
	}
}

// TestCaptureOAuthRedirectPublishesURL checks that SetAuthURLObserver gets the
// sign-in URL and that the redirect to that URL completes the capture.
func TestCaptureOAuthRedirectPublishesURL(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // browser.Open finds no browser to start
	urls := make(chan string, 1)
	SetAuthURLObserver(func(u string) { urls <- u })
	t.Cleanup(func() { SetAuthURLObserver(nil) })

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	type capture struct {
		res oauthResult
		err error
	}
	done := make(chan capture, 1)
	go func() {
		res, err := captureOAuthRedirect(ctx, "app")
		done <- capture{res, err}
	}()

	var authURL string
	select {
	case authURL = <-urls:
	case <-ctx.Done():
		t.Fatal("observer got no sign-in URL")
	}
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	if got := u.Query().Get("ext_app_id"); got != "app" {
		t.Errorf("ext_app_id = %q, want app", got)
	}
	redirect, err := url.Parse(u.Query().Get("redirect_url"))
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+redirect.Port()+"/?user_auth_token=uat&user_id=7", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("redirect request: %v", err)
	}
	resp.Body.Close()

	got := <-done
	if got.err != nil {
		t.Fatalf("captureOAuthRedirect() error = %v", got.err)
	}
	if want := (oauthResult{Token: "uat", UserID: "7"}); got.res != want {
		t.Errorf("captureOAuthRedirect() = %+v, want %+v", got.res, want)
	}
}

// TestNewClientSilent runs newClientSilent against an httptest API with the
// stored credentials of each case.
func TestNewClientSilent(t *testing.T) {
	const eligible = `{"user":{"id":7,"credential":{"parameters":{"short_label":"Studio"}}}}`
	stored := storedCreds{AppID: "app", Secrets: []string{"bad", "good"}, PrivateKey: "key", UserAuthToken: "uat", UserID: "7", Label: "Old"}
	withSecret := stored
	withSecret.Secret = "stored"

	tests := []struct {
		name       string
		creds      *storedCreds // nil means no credentials file
		login      string       // user/login body
		loginCode  int
		goodSecret string // the secret that track/getFileUrl accepts
		wantErr    string
		wantSecret string
		wantProbes int32
	}{
		{name: "no stored credentials", wantErr: "no stored credentials"},
		{name: "incomplete credentials", creds: &storedCreds{AppID: "app"}, wantErr: "incomplete stored credentials"},
		{name: "token rejected", creds: &withSecret, loginCode: http.StatusUnauthorized, wantErr: "stored token rejected"},
		{name: "free account", creds: &withSecret, login: `{"user":{"id":7,"credential":{}}}`, wantErr: "not eligible"},
		{name: "stored secret", creds: &withSecret, login: eligible, wantSecret: "stored"},
		{name: "secret validated", creds: &stored, login: eligible, goodSecret: "good", wantSecret: "good", wantProbes: 2},
		{name: "no valid secret", creds: &stored, login: eligible, goodSecret: "other", wantErr: "no valid signing secret", wantProbes: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
			if tt.creds != nil {
				if err := credsFile.Save(tt.creds); err != nil {
					t.Fatal(err)
				}
			}
			var probes atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				q := r.URL.Query()
				switch r.URL.Path {
				case "/user/login":
					if q.Get("user_id") != "7" || q.Get("user_auth_token") != "uat" || q.Get("app_id") != "app" {
						t.Errorf("user/login query = %v", q)
					}
					if tt.loginCode != 0 {
						w.WriteHeader(tt.loginCode)
					}
					_, _ = io.WriteString(w, tt.login)
				case "/track/getFileUrl":
					probes.Add(1)
					want := trackFileURLSig(q.Get("track_id"), 5, q.Get("request_ts"), tt.goodSecret)
					if q.Get("request_sig") != want {
						http.Error(w, `{"status":"error"}`, http.StatusBadRequest)
						return
					}
					_, _ = io.WriteString(w, `{"url":"https://cdn.example/probe.mp3"}`)
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()

			c, err := newClientSilent(context.Background(), srv.URL+"/")
			if n := probes.Load(); n != tt.wantProbes {
				t.Errorf("secret probes = %d, want %d", n, tt.wantProbes)
			}
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("newClientSilent() error = %v, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("newClientSilent() error = %v", err)
			}
			if c.secret != tt.wantSecret || c.label != "Studio" || c.uat != "uat" || c.userID != "7" {
				t.Errorf("client = {secret %q, label %q, uat %q, userID %q}, want {%q, Studio, uat, 7}",
					c.secret, c.label, c.uat, c.userID, tt.wantSecret)
			}
			// The client saves the validated secret and the new label.
			saved, err := credsFile.Load()
			if err != nil {
				t.Fatal(err)
			}
			if saved.Secret != tt.wantSecret || saved.Label != "Studio" || saved.PrivateKey != "key" {
				t.Errorf("saved credentials = %+v, want secret %q, label Studio and private key kept", saved, tt.wantSecret)
			}
		})
	}
}
