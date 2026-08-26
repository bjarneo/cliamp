package mixcloud

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func testClient(server *httptest.Server, token string) *client {
	c := newClient(token)
	c.baseURL = server.URL
	c.httpClient = server.Client()
	return c
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}

func TestClientAddsTokenAndPaginates(t *testing.T) {
	var offsets []int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("access_token"); got != "secret-token" {
			t.Errorf("access_token = %q", got)
		}
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		offsets = append(offsets, offset)
		page := apiPage[apiCloudcast]{Data: []apiCloudcast{{Key: "/user/show-" + strconv.Itoa(offset) + "/"}}}
		if offset == 0 {
			page.Paging.Next = serverURLPlaceholder
		}
		writeJSON(t, w, page)
	}))
	defer server.Close()

	items, err := testClient(server, "secret-token").cloudcasts(context.Background(), "/shows/", 2)
	if err != nil {
		t.Fatalf("cloudcasts: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	if len(offsets) != 2 || offsets[0] != 0 || offsets[1] != 1 {
		t.Fatalf("offsets = %v, want [0 1]", offsets)
	}
}

func TestClientCapsItemsWhenAPIExceedsRequestedLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, apiPage[apiCloudcast]{Data: []apiCloudcast{
			{Key: "/user/one/"},
			{Key: "/user/two/"},
			{Key: "/user/three/"},
		}})
	}))
	defer server.Close()

	items, err := testClient(server, "").cloudcasts(context.Background(), "/shows/", 2)
	if err != nil {
		t.Fatalf("cloudcasts: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("items = %d, want requested limit 2", len(items))
	}
}

func TestClientLoadsCategories(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/categories/" {
			t.Errorf("path = %q", r.URL.Path)
		}
		writeJSON(t, w, map[string]any{"data": []any{
			map[string]any{"slug": "deep-house", "name": "Deep House", "format": "music"},
		}})
	}))
	defer server.Close()

	categories, err := testClient(server, "").categories(context.Background())
	if err != nil {
		t.Fatalf("categories: %v", err)
	}
	if len(categories) != 1 || categories[0].Slug != "deep-house" || categories[0].Format != "music" {
		t.Fatalf("categories = %+v", categories)
	}
}

func TestClientSearchesTags(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search/" || r.URL.Query().Get("type") != "tag" || r.URL.Query().Get("q") != "acid techno" {
			t.Errorf("request = %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		writeJSON(t, w, map[string]any{"data": []any{
			map[string]any{"key": "/genres/acid-techno/", "name": "Acid Techno"},
		}})
	}))
	defer server.Close()

	tags, err := testClient(server, "").searchTags(context.Background(), "acid techno", 20)
	if err != nil {
		t.Fatalf("searchTags: %v", err)
	}
	if len(tags) != 1 || tags[0].Key != "/genres/acid-techno/" {
		t.Fatalf("tags = %+v", tags)
	}
}

const serverURLPlaceholder = "https://api.mixcloud.com/next/"

func TestClientPreservesCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := testClient(server, "").searchCloudcasts(ctx, "ambient", 10)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestClientReturnsStructuredRateLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "17")
		w.WriteHeader(http.StatusForbidden)
		writeJSON(t, w, map[string]any{"error": map[string]any{
			"type": "RateLimitException", "message": "slow down", "retry_after": 9,
		}})
	}))
	defer server.Close()

	_, err := testClient(server, "").searchCloudcasts(context.Background(), "house", 5)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %T %v, want *APIError", err, err)
	}
	if apiErr.StatusCode != http.StatusForbidden || apiErr.RetryAfter != 17*time.Second {
		t.Fatalf("APIError = %+v", apiErr)
	}
}

func TestRequestURLRejectsExternalPath(t *testing.T) {
	c := newClient("")
	if _, err := c.requestURL("https://attacker.example/", nil); err == nil {
		t.Fatal("requestURL accepted absolute external URL")
	}
	if _, err := c.requestURL("/users/../me/", nil); err == nil {
		t.Fatal("requestURL accepted a traversal path")
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestClientRedactsTokenFromTransportError(t *testing.T) {
	sentinel := errors.New("network unavailable")
	c := newClient("top-secret-token")
	c.httpClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, sentinel
	})}

	_, err := c.searchCloudcasts(context.Background(), "house", 5)
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want wrapped transport error", err)
	}
	if strings.Contains(err.Error(), "top-secret-token") || strings.Contains(err.Error(), "access_token") {
		t.Fatalf("error leaked credentials: %v", err)
	}
}
