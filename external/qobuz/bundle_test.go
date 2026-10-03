package qobuz

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func TestBundlePrivateKeyScraped(t *testing.T) {
	b := &bundle{content: `foo privateKey: "scrapedKey123" bar`}
	if got := b.privateKey(); got != "scrapedKey123" {
		t.Fatalf("privateKey() = %q, want scraped value", got)
	}
}

func TestBundlePrivateKeyFallback(t *testing.T) {
	b := &bundle{content: `no key here at all`}
	if got := b.privateKey(); got != fallbackPrivateKey {
		t.Fatalf("privateKey() = %q, want fallback %q", got, fallbackPrivateKey)
	}
}

func TestBundleAppID(t *testing.T) {
	b := &bundle{content: `x=production:{api:{appId:"798273057",appSecret:"05a4851e74ee47fda346f50cfdfc4f09"}}`}
	got, err := b.appID()
	if err != nil {
		t.Fatalf("appID() error = %v", err)
	}
	if got != "798273057" {
		t.Fatalf("appID() = %q, want 798273057", got)
	}
}

// seedParts returns the seed, info and extras strings that secrets() joins
// and decodes back to secret. It encodes secret without padding and appends
// the 44 characters that secrets() trims.
func seedParts(t *testing.T, secret string) (seed, info, extras string) {
	t.Helper()
	joined := base64.RawStdEncoding.EncodeToString([]byte(secret)) + strings.Repeat("x", 44)
	if strings.ContainsAny(joined, "+/") {
		t.Fatalf("encoded secret %q has characters that the bundle regex does not match", secret)
	}
	return joined[:10], joined[10:30], joined[30:]
}

func TestBundleSecrets(t *testing.T) {
	// 32 bytes encode to 43 characters, so secrets() must pad them. 33 bytes
	// encode to 44 characters and need no padding.
	const berlin = "0123456789abcdef0123456789abcdef"
	const london = "fedcba9876543210fedcba9876543210x"
	bSeed, bInfo, bExtras := seedParts(t, berlin)
	lSeed, lInfo, lExtras := seedParts(t, london)
	full := fmt.Sprintf(`a.initialSeed("%s",window.utimezone.berlin),b.initialSeed("%s",window.utimezone.london);`+
		`{name:"Europe/Berlin",info:"%s",extras:"%s"},{name:"Europe/London",info:"%s",extras:"%s"},`+
		`{name:"Europe/Paris",info:"unused",extras:"unused"}`,
		bSeed, lSeed, bInfo, bExtras, lInfo, lExtras)

	tests := []struct {
		name    string
		content string
		want    map[string]string
		wantErr string
	}{
		{
			name:    "two timezones",
			content: full,
			want:    map[string]string{"berlin": berlin, "london": london},
		},
		{
			name:    "no seeds",
			content: `{name:"Europe/Berlin",info:"abc",extras:"def"}`,
			wantErr: "no seeds found",
		},
		{
			name:    "seed without info is too short",
			content: `a.initialSeed("short",window.utimezone.berlin)`,
			wantErr: "no secrets decoded",
		},
		{
			name: "a short timezone is skipped",
			content: fmt.Sprintf(`a.initialSeed("%s",window.utimezone.berlin),b.initialSeed("short",window.utimezone.london);`+
				`{name:"Europe/Berlin",info:"%s",extras:"%s"}`, bSeed, bInfo, bExtras),
			want: map[string]string{"berlin": berlin},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := (&bundle{content: tt.content}).secrets()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("secrets() error = %v, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("secrets() error = %v", err)
			}
			if !maps.Equal(got, tt.want) {
				t.Errorf("secrets() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestScrapeCredentials serves a web player login page and its bundle from
// an httptest server and checks what scrapeCredentials reads from them.
func TestScrapeCredentials(t *testing.T) {
	const (
		bundlePath = "/resources/7.1.2-b012/bundle.js"
		secret     = "0123456789abcdef0123456789abcdef"
	)
	seed, info, extras := seedParts(t, secret)
	loginPage := `<html><script src="` + bundlePath + `"></script></html>`
	validBundle := fmt.Sprintf(`production:{api:{appId:"798273057",appSecret:"05a4851e74ee47fda346f50cfdfc4f09"}}`+
		`a.initialSeed("%s",window.utimezone.berlin);{name:"Europe/Berlin",info:"%s",extras:"%s"};privateKey: "scrapedKey123"`,
		seed, info, extras)

	tests := []struct {
		name       string
		loginPage  string
		loginCode  int
		bundle     string
		bundleCode int
		wantAppID  string
		wantKey    string
		wantErr    string
	}{
		{name: "login page and bundle", loginPage: loginPage, bundle: validBundle, wantAppID: "798273057", wantKey: "scrapedKey123"},
		{name: "login page error", loginCode: http.StatusInternalServerError, wantErr: "get login page: HTTP 500"},
		{name: "no bundle URL", loginPage: `<html></html>`, wantErr: "bundle URL not found"},
		{name: "bundle error", loginPage: loginPage, bundleCode: http.StatusNotFound, wantErr: "get bundle.js: HTTP 404"},
		{name: "no app id", loginPage: loginPage, bundle: `privateKey: "scrapedKey123"`, wantErr: "app_id not found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/login":
					if tt.loginCode != 0 {
						w.WriteHeader(tt.loginCode)
					}
					_, _ = io.WriteString(w, tt.loginPage)
				case bundlePath:
					if tt.bundleCode != 0 {
						w.WriteHeader(tt.bundleCode)
					}
					_, _ = io.WriteString(w, tt.bundle)
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()

			appID, secrets, key, err := scrapeCredentials(context.Background(), srv.URL)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("scrapeCredentials() error = %v, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("scrapeCredentials() error = %v", err)
			}
			if appID != tt.wantAppID || key != tt.wantKey || !slices.Equal(secrets, []string{secret}) {
				t.Errorf("scrapeCredentials() = %q, %q, %q, want %q, [%q], %q", appID, secrets, key, tt.wantAppID, secret, tt.wantKey)
			}
		})
	}
}
