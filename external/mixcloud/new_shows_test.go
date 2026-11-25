package mixcloud

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func feedNode(slug, name, date string) map[string]any {
	return map[string]any{"slug": slug, "name": name, "audio_length": 3600, "is_exclusive": false, "created_time": date,
		"user": map[string]any{"username": "Sam_Maclaren", "name": "Broadcasting"}}
}

func feedPage(username string, nodes []any, cursor string, next bool) map[string]any {
	edges := make([]any, len(nodes))
	for i, node := range nodes {
		edges[i] = map[string]any{"node": node}
	}
	return map[string]any{"data": map[string]any{"viewer": map[string]any{
		"me":                  map[string]any{"username": username},
		"uploadNotifications": map[string]any{"edges": edges, "pageInfo": map[string]any{"endCursor": cursor, "hasNextPage": next}},
	}}}
}

func providerWithFeed(t *testing.T, handler http.HandlerFunc) (*Provider, *httptest.Server) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	p := NewFromConfig(Config{Enabled: true, Username: "configured-public-user", AccessToken: "rest-secret", CookiesFrom: "firefox", MaxItems: 3})
	p.client.httpClient = server.Client()
	p.client.feedURL = server.URL + "/graphql"
	p.client.baseURL = server.URL + "/rest"
	p.client.loadCookies = func(_ context.Context, browser string) (http.CookieJar, error) {
		if browser != "firefox" {
			t.Errorf("cookie source = %q", browser)
		}
		u, _ := url.Parse(server.URL)
		jar, _ := cookiejar.New(nil)
		jar.SetCookies(u, []*http.Cookie{
			{Name: "sessionid", Value: "browser-session", Path: "/", Secure: true, HttpOnly: true},
			{Name: "unrelated", Value: "other-secret", Domain: ".other.example", Path: "/"},
		})
		return jar, nil
	}
	return p, server
}

func TestNewShowsPreservesWebsiteOrderAndCursorPagination(t *testing.T) {
	var cursors []string
	var counts []int
	p, server := providerWithFeed(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/graphql" {
			t.Errorf("unexpected request: %s %s (no aggregation or mutations allowed)", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if c, err := r.Cookie("sessionid"); err != nil || c.Value != "browser-session" {
			t.Error("website session cookie missing")
		}
		if strings.Contains(r.Header.Get("Cookie"), "other-secret") || r.URL.Query().Get("access_token") != "" || r.Header.Get("Authorization") != "" {
			t.Error("unrelated cookies or OAuth credentials leaked to website feed")
		}
		if q := r.URL.Query().Get("query"); !strings.Contains(q, "uploadNotifications(first: $count, after: $cursor)") || strings.Contains(q, "mutation") {
			t.Errorf("query does not read the website connection: %s", q)
		}
		var variables struct {
			Count  int     `json:"count"`
			Cursor *string `json:"cursor"`
		}
		if err := json.Unmarshal([]byte(r.URL.Query().Get("variables")), &variables); err != nil {
			t.Error(err)
		}
		counts = append(counts, variables.Count)
		cursor := ""
		if variables.Cursor != nil {
			cursor = *variables.Cursor
		}
		cursors = append(cursors, cursor)
		if cursor == "" {
			// The first item is intentionally older: a date-sort shortcut fails.
			nodes := []any{
				feedNode("electric-drift-6", "Electric Drift 6", "2026-09-01T00:00:00Z"),
				feedNode("newer", "Newer but second", "2026-10-01T00:00:00Z"),
			}
			for i := len(nodes); i < newShowsPageSize; i++ {
				nodes = append(nodes, feedNode(strconv.Itoa(i), strconv.Itoa(i), "2026-10-01T00:00:00Z"))
			}
			writeJSON(t, w, feedPage("browser-user", nodes, "opaque+/cursor=", true))
		} else {
			writeJSON(t, w, feedPage("browser-user", []any{
				feedNode("third", "Third", "2026-10-02T00:00:00Z"),
				feedNode("extra", "Beyond limit", "2026-10-03T00:00:00Z"),
			}, "end", true))
		}
	})
	defer server.Close()
	p.maxItems = newShowsPageSize + 1

	tracks, err := p.Tracks(streamID)
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, track := range tracks {
		titles = append(titles, track.Title)
	}
	wantTitles := []string{"Electric Drift 6", "Newer but second"}
	for i := 2; i < newShowsPageSize; i++ {
		wantTitles = append(wantTitles, strconv.Itoa(i))
	}
	wantTitles = append(wantTitles, "Third")
	if !slices.Equal(titles, wantTitles) {
		t.Fatalf("titles = %v, want website edge order", titles)
	}
	if tracks[0].Artist != "Broadcasting" || tracks[0].Path != "https://www.mixcloud.com/Sam_Maclaren/electric-drift-6/" || tracks[0].DurationSecs != 3600 || tracks[0].Year != 2026 {
		t.Fatalf("first track metadata = %+v", tracks[0])
	}
	if !slices.Equal(cursors, []string{"", "opaque+/cursor="}) || !slices.Equal(counts, []int{20, 1}) {
		t.Fatalf("cursors = %v, counts = %v", cursors, counts)
	}
	if p.client.httpClient.Jar != nil {
		t.Error("REST HTTP client was changed to carry website cookies")
	}
}

func TestNewShowsRejectsPartialOrInvalidFeed(t *testing.T) {
	for _, mode := range []string{"graphql-error", "logged-out", "null-feed", "cursor-cycle", "missing-cursor", "account-changed", "bad-identity", "missing-edges", "missing-page-info", "malformed-json", "rate-limit", "redirect"} {
		t.Run(mode, func(t *testing.T) {
			requests := 0
			p, server := providerWithFeed(t, func(w http.ResponseWriter, r *http.Request) {
				requests++
				page := feedPage("browser-user", []any{feedNode("one", "One", "2026-10-01T00:00:00Z")}, "same", true)
				viewer := page["data"].(map[string]any)["viewer"].(map[string]any)
				switch mode {
				case "graphql-error":
					if requests > 1 {
						page["errors"] = []any{map[string]any{"message": "session-secret"}}
					}
				case "logged-out":
					viewer["me"] = nil
				case "null-feed":
					viewer["uploadNotifications"] = nil
				case "missing-cursor":
					viewer["uploadNotifications"].(map[string]any)["pageInfo"] = map[string]any{"hasNextPage": true}
				case "account-changed":
					if requests > 1 {
						viewer["me"] = map[string]any{"username": "other"}
					}
				case "bad-identity":
					page = feedPage("browser-user", []any{feedNode("../hidden", "Invalid", "2026-10-01T00:00:00Z")}, "", false)
				case "missing-edges":
					delete(viewer["uploadNotifications"].(map[string]any), "edges")
				case "missing-page-info":
					delete(viewer["uploadNotifications"].(map[string]any), "pageInfo")
				case "malformed-json":
					w.Write([]byte(`{"data":`))
					return
				case "rate-limit":
					w.Header().Set("Retry-After", "12")
					w.WriteHeader(http.StatusTooManyRequests)
					return
				case "redirect":
					w.Header().Set("Location", "/unexpected")
					w.WriteHeader(http.StatusFound)
					return
				}
				writeJSON(t, w, page)
			})
			defer server.Close()
			tracks, err := p.Tracks(streamID)
			if err == nil || len(tracks) != 0 {
				t.Fatalf("tracks=%v error=%v, want no partial feed", tracks, err)
			}
			if strings.Contains(err.Error(), "session-secret") {
				t.Error("server error text exposed session data")
			}
			if mode == "rate-limit" {
				var apiErr *APIError
				if !errors.As(err, &apiErr) || apiErr.RetryAfter != 12*time.Second {
					t.Fatalf("rate-limit error = %v", err)
				}
			}
			if mode == "redirect" && requests != 1 {
				t.Fatalf("followed redirect: %d requests", requests)
			}
		})
	}
}

func TestNewShowsRequiresSessionWithoutPublicFallback(t *testing.T) {
	p := NewFromConfig(Config{Enabled: true, Username: "alice", AccessToken: "token"})
	p.client.loadCookies = func(context.Context, string) (http.CookieJar, error) {
		t.Fatal("unexpected cookie extraction")
		return nil, nil
	}
	_, err := p.Tracks(streamID)
	if !errors.Is(err, errNewShowsSession) {
		t.Fatalf("error = %v", err)
	}

	p.cookiesFrom = "firefox"
	p.client.loadCookies = func(context.Context, string) (http.CookieJar, error) { return nil, context.Canceled }
	_, err = p.Tracks(streamID)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation = %v", err)
	}
}

func TestNewShowsEmptyAndNullNodes(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(strconv.FormatBool(empty), func(t *testing.T) {
			requests := 0
			p, server := providerWithFeed(t, func(w http.ResponseWriter, r *http.Request) {
				requests++
				if empty {
					writeJSON(t, w, feedPage("browser-user", []any{}, "", false))
				} else if requests == 1 {
					writeJSON(t, w, feedPage("browser-user", []any{nil}, "null-node-page", true))
				} else {
					node := feedNode("same", "Same", "2026-10-01T00:00:00Z")
					writeJSON(t, w, feedPage("browser-user", []any{node, node}, "", false))
				}
			})
			defer server.Close()
			tracks, err := p.Tracks(streamID)
			want := 2
			if empty {
				want = 0
			}
			if err != nil || len(tracks) != want {
				t.Fatalf("tracks=%v error=%v", tracks, err)
			}
			if !empty && requests != 2 {
				t.Fatalf("requests=%d, stopped on null-node page", requests)
			}
		})
	}
}
