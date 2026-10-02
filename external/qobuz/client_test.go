package qobuz

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestMD5Hex(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", "d41d8cd98f00b204e9800998ecf8427e"},
		{"abc", "900150983cd24fb0d6963f7d28e17f72"},
	}
	for _, tt := range tests {
		if got := md5hex(tt.in); got != tt.want {
			t.Errorf("md5hex(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestTrackFileURLSig pins the request_sig layout for track/getFileUrl against
// a precomputed md5. If the raw string format in trackFileURLSig changes,
// streaming breaks and this test fails.
func TestTrackFileURLSig(t *testing.T) {
	// md5("trackgetFileUrlformat_id6intentstreamtrack_id59667831700000000deadbeefsecret")
	const want = "bc7a09d686b3e5c1cd32f5268eff1030"
	got := trackFileURLSig("5966783", 6, "1700000000", "deadbeefsecret")
	if got != want {
		t.Fatalf("trackFileURLSig = %q, want %q", got, want)
	}
}

func TestValidQuality(t *testing.T) {
	for _, q := range []int{5, 6, 7, 27} {
		if !validQuality(q) {
			t.Errorf("expected quality %d to be valid", q)
		}
	}
	for _, q := range []int{0, 1, 4, 8, 100} {
		if validQuality(q) {
			t.Errorf("expected quality %d to be invalid", q)
		}
	}
}

// testClient returns a signed-in client pointed at srv.
func testClient(srv *httptest.Server) *client {
	c := newClient("app", []string{"deadbeefsecret"})
	c.baseURL = srv.URL + "/"
	c.http = srv.Client()
	c.secret = "deadbeefsecret"
	c.uat = "token"
	c.userID = "42"
	return c
}

// decodeLogin parses a user/login or oauth/callback body for a test.
func decodeLogin(t *testing.T, body string) loginResponse {
	t.Helper()
	var info loginResponse
	if err := json.Unmarshal([]byte(body), &info); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return info
}

func TestApplyUserInfo(t *testing.T) {
	const eligible = `{"user_auth_token":"new-uat","user":{"id":7,"credential":{"parameters":{"short_label":"Studio"}}}}`
	tests := []struct {
		name       string
		body       string
		uat        string
		userID     string
		wantErr    bool
		wantUAT    string
		wantUserID string
		wantLabel  string
	}{
		{
			name:       "fills empty token and user",
			body:       eligible,
			wantUAT:    "new-uat",
			wantUserID: "7",
			wantLabel:  "Studio",
		},
		{
			name:       "keeps existing token and user",
			body:       eligible,
			uat:        "old-uat",
			userID:     "3",
			wantUAT:    "old-uat",
			wantUserID: "3",
			wantLabel:  "Studio",
		},
		{
			name:      "missing user id stays empty",
			body:      `{"user":{"credential":{"parameters":{"short_label":"Sublime"}}}}`,
			wantLabel: "Sublime",
		},
		{
			name:    "free account is rejected",
			body:    `{"user_auth_token":"new-uat","user":{"id":7,"credential":{}}}`,
			wantErr: true,
		},
		{
			name:    "null parameters are rejected",
			body:    `{"user":{"id":7,"credential":{"parameters":null}}}`,
			uat:     "old-uat",
			wantErr: true,
			wantUAT: "old-uat",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newClient("app", nil)
			c.uat = tt.uat
			c.userID = tt.userID
			err := c.applyUserInfo(decodeLogin(t, tt.body))
			if (err != nil) != tt.wantErr {
				t.Fatalf("applyUserInfo() error = %v, wantErr %v", err, tt.wantErr)
			}
			if c.uat != tt.wantUAT || c.userID != tt.wantUserID || c.label != tt.wantLabel {
				t.Errorf("client = {uat %q, userID %q, label %q}, want {%q, %q, %q}",
					c.uat, c.userID, c.label, tt.wantUAT, tt.wantUserID, tt.wantLabel)
			}
		})
	}
}

func TestExchangeOAuthCode(t *testing.T) {
	const (
		eligibleLogin = `{"user":{"id":7,"credential":{"parameters":{"short_label":"Studio"}}}}`
		freeLogin     = `{"user":{"id":7,"credential":{}}}`
	)
	allAttempts := []string{"GET code", "POST code", "GET code_autorisation", "POST code_autorisation"}
	tests := []struct {
		name string
		// callback answers each oauth/callback attempt. An empty body means 404.
		callback     map[string]string
		login        string
		wantErr      string
		wantAttempts []string
		wantLogins   int
		wantUAT      string
		wantUserID   string
		wantLabel    string
	}{
		{
			name:         "first attempt returns a token",
			callback:     map[string]string{"GET code": `{"token":"tok"}`},
			login:        eligibleLogin,
			wantAttempts: allAttempts[:1],
			wantLogins:   1,
			wantUAT:      "tok",
			wantUserID:   "7",
			wantLabel:    "Studio",
		},
		{
			name:         "falls through to POST code_autorisation",
			callback:     map[string]string{"POST code_autorisation": `{"token":"tok"}`},
			login:        eligibleLogin,
			wantAttempts: allAttempts,
			wantLogins:   1,
			wantUAT:      "tok",
			wantUserID:   "7",
			wantLabel:    "Studio",
		},
		{
			name:         "skips a body that does not decode",
			callback:     map[string]string{"GET code": `not json`, "POST code": `{"token":"tok"}`},
			login:        eligibleLogin,
			wantAttempts: allAttempts[:2],
			wantLogins:   1,
			wantUAT:      "tok",
			wantUserID:   "7",
			wantLabel:    "Studio",
		},
		{
			name: "credentials without a token skip user/login",
			callback: map[string]string{
				"GET code": `{"user_auth_token":"uat","user":{"id":9,"credential":{"parameters":{"short_label":"Sublime"}}}}`,
			},
			wantAttempts: allAttempts[:1],
			wantUAT:      "uat",
			wantUserID:   "9",
			wantLabel:    "Sublime",
		},
		{
			name:         "no token in any response",
			callback:     map[string]string{"GET code": `{}`, "POST code": `{}`, "GET code_autorisation": `{}`, "POST code_autorisation": `{}`},
			wantErr:      "no token in oauth/callback response",
			wantAttempts: allAttempts,
		},
		{
			name:         "every attempt fails",
			callback:     map[string]string{},
			wantErr:      "HTTP 404",
			wantAttempts: allAttempts,
		},
		{
			name:         "free account after the exchange",
			callback:     map[string]string{"GET code": `{"token":"tok"}`},
			login:        freeLogin,
			wantErr:      "not eligible for streaming",
			wantAttempts: allAttempts[:1],
			wantLogins:   1,
			wantUAT:      "tok",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var attempts []string
			logins := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := r.ParseForm(); err != nil {
					t.Errorf("parse form: %v", err)
				}
				if got := r.Header.Get("X-App-Id"); got != "app" {
					t.Errorf("X-App-Id = %q, want app", got)
				}
				switch r.URL.Path {
				case "/oauth/callback":
					name := "code"
					if r.Form.Has("code_autorisation") {
						name = "code_autorisation"
					}
					if got := r.Form.Get(name); got != "the-code" {
						t.Errorf("%s = %q, want the-code", name, got)
					}
					if got := r.Form.Get("private_key"); got != "pk" {
						t.Errorf("private_key = %q, want pk", got)
					}
					if r.Method == http.MethodPost && r.URL.RawQuery != "" {
						t.Errorf("POST attempt sent query %q, want form body only", r.URL.RawQuery)
					}
					key := r.Method + " " + name
					attempts = append(attempts, key)
					body, ok := tt.callback[key]
					if !ok {
						http.NotFound(w, r)
						return
					}
					_, _ = w.Write([]byte(body))
				case "/user/login":
					logins++
					if got := r.Header.Get("X-User-Auth-Token"); got != "tok" {
						t.Errorf("user/login token = %q, want tok", got)
					}
					_, _ = w.Write([]byte(tt.login))
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()

			c := testClient(srv)
			c.uat, c.userID = "", ""
			err := c.exchangeOAuthCode(context.Background(), "the-code", "pk")
			if tt.wantErr == "" && err != nil {
				t.Fatalf("exchangeOAuthCode() error = %v", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("exchangeOAuthCode() error = %v, want it to contain %q", err, tt.wantErr)
			}
			if !slices.Equal(attempts, tt.wantAttempts) {
				t.Errorf("attempts = %q, want %q", attempts, tt.wantAttempts)
			}
			if logins != tt.wantLogins {
				t.Errorf("user/login calls = %d, want %d", logins, tt.wantLogins)
			}
			if c.uat != tt.wantUAT || c.userID != tt.wantUserID || c.label != tt.wantLabel {
				t.Errorf("client = {uat %q, userID %q, label %q}, want {%q, %q, %q}",
					c.uat, c.userID, c.label, tt.wantUAT, tt.wantUserID, tt.wantLabel)
			}
		})
	}
}

// TestFavoriteParamsSignature pins the favorite/getUserFavorites signature:
// it signs only the object, the method, the timestamp and the secret. The
// type, limit and offset params must stay out of the signature.
func TestFavoriteParamsSignature(t *testing.T) {
	c := newClient("app", nil)
	c.secret = "deadbeefsecret"
	c.uat = "token"
	v := c.favoriteParams("albums", 200, 100)

	ts := v.Get("request_ts")
	if ts == "" {
		t.Fatal("request_ts is empty")
	}
	if got, want := v.Get("request_sig"), md5hex("favoritegetUserFavorites"+ts+"deadbeefsecret"); got != want {
		t.Errorf("request_sig = %q, want %q", got, want)
	}
	for key, want := range map[string]string{
		"app_id": "app", "user_auth_token": "token", "type": "albums", "offset": "200", "limit": "100",
	} {
		if got := v.Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestTrackFileURLRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/track/getFileUrl" {
			t.Errorf("path = %q, want /track/getFileUrl", r.URL.Path)
		}
		q := r.URL.Query()
		if want := trackFileURLSig("5966783", 27, q.Get("request_ts"), "deadbeefsecret"); q.Get("request_sig") != want {
			t.Errorf("request_sig = %q, want %q", q.Get("request_sig"), want)
		}
		if q.Get("track_id") != "5966783" || q.Get("format_id") != "27" || q.Get("intent") != "stream" {
			t.Errorf("query = %v", q)
		}
		if got := r.Header.Get("X-User-Auth-Token"); got != "token" {
			t.Errorf("X-User-Auth-Token = %q, want token", got)
		}
		if got := r.Header.Get("User-Agent"); got != apiUA {
			t.Errorf("User-Agent = %q", got)
		}
		_, _ = w.Write([]byte(`{"url":"https://cdn.example/file.flac","format_id":27,"bit_depth":24}`))
	}))
	defer srv.Close()

	got, err := testClient(srv).trackFileURL(context.Background(), "5966783", 27, "")
	if err != nil {
		t.Fatalf("trackFileURL() error = %v", err)
	}
	if got.URL != "https://cdn.example/file.flac" || got.FormatID != 27 || got.BitDepth != 24 {
		t.Errorf("trackFileURL() = %+v", got)
	}

	if _, err := testClient(srv).trackFileURL(context.Background(), "1", 4, ""); err == nil {
		t.Error("trackFileURL() with quality 4 must fail before any request")
	}
}

// pagedServer serves total items in pages from endpoint. The items live under
// listKey, as playlist/get nests them under "tracks" and artist/get under
// "albums". stopAt, when positive, makes the server return empty pages from
// that offset on, as a server that reports a wrong total would. It records
// the offset of each request. IDs are JSON strings because album IDs are.
func pagedServer(t *testing.T, endpoint, listKey string, total, stopAt int, offsets *[]int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/"+endpoint {
			t.Errorf("path = %q, want /%s", r.URL.Path, endpoint)
		}
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		*offsets = append(*offsets, offset)
		items := []map[string]any{}
		for i := offset; i < offset+limit && i < total && (stopAt <= 0 || i < stopAt); i++ {
			items = append(items, map[string]any{"id": strconv.Itoa(i), "title": fmt.Sprintf("item-%d", i)})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":    1,
			listKey: map[string]any{"items": items, "total": total},
		})
	}))
}

func TestPagination(t *testing.T) {
	tests := []struct {
		name        string
		total       int
		stopAt      int
		wantItems   int
		wantOffsets []int
	}{
		{name: "one partial page", total: 3, wantItems: 3, wantOffsets: []int{0}},
		{name: "exactly one full page", total: 500, wantItems: 500, wantOffsets: []int{0}},
		{name: "two pages", total: 750, wantItems: 750, wantOffsets: []int{0, 500}},
		{name: "three pages", total: 1001, wantItems: 1001, wantOffsets: []int{0, 500, 1000}},
		{name: "empty list", total: 0, wantItems: 0, wantOffsets: []int{0}},
		{name: "stops at an empty page", total: 5000, stopAt: 500, wantItems: 500, wantOffsets: []int{0, 500}},
	}
	fetchers := []struct {
		endpoint, listKey string
		fetch             func(*client) (int, error)
	}{
		{"playlist/get", "tracks", func(c *client) (int, error) {
			items, err := c.playlistTracks(context.Background(), "1")
			return len(items), err
		}},
		{"artist/get", "albums", func(c *client) (int, error) {
			items, err := c.artistAlbums(context.Background(), "1")
			return len(items), err
		}},
	}
	for _, f := range fetchers {
		for _, tt := range tests {
			t.Run(f.endpoint+"/"+tt.name, func(t *testing.T) {
				var offsets []int
				srv := pagedServer(t, f.endpoint, f.listKey, tt.total, tt.stopAt, &offsets)
				defer srv.Close()

				got, err := f.fetch(testClient(srv))
				if err != nil {
					t.Fatalf("fetch error = %v", err)
				}
				if got != tt.wantItems {
					t.Errorf("items = %d, want %d", got, tt.wantItems)
				}
				if !slices.Equal(offsets, tt.wantOffsets) {
					t.Errorf("offsets = %v, want %v", offsets, tt.wantOffsets)
				}
			})
		}
	}
}
