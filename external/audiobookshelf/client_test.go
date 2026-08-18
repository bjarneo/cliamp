package audiobookshelf

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func jsonResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewBufferString(body)),
	}
}

func statusResponse(code int, status string) *http.Response {
	return &http.Response{
		StatusCode: code,
		Status:     status,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewBufferString("")),
	}
}

func mockClient(token, user, password string, libraries []string, fn roundTripFunc) *Client {
	c := NewClient("https://abs.example.com", token, user, password, libraries)
	c.SetHTTPClient(&http.Client{Transport: fn})
	return c
}

const librariesJSON = `{"libraries":[
	{"id":"lib-b","name":"Audiobooks","mediaType":"book"},
	{"id":"lib-p","name":"Podcasts","mediaType":"podcast"},
	{"id":"lib-x","name":"Kids","mediaType":"book"}
]}`

func TestLibrariesSendsBearerToken(t *testing.T) {
	var gotAuth string
	c := mockClient("tok", "", "", nil, func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/api/libraries" {
			t.Fatalf("unexpected path %s", req.URL.Path)
		}
		gotAuth = req.Header.Get("Authorization")
		return jsonResponse(librariesJSON), nil
	})

	libs, err := c.Libraries()
	if err != nil {
		t.Fatalf("Libraries() error: %v", err)
	}
	if len(libs) != 3 {
		t.Fatalf("got %d libraries, want 3", len(libs))
	}
	if gotAuth != "Bearer tok" {
		t.Fatalf("Authorization = %q, want Bearer tok", gotAuth)
	}
}

func TestLibrariesFilteredByName(t *testing.T) {
	c := mockClient("tok", "", "", []string{"podcasts"}, func(req *http.Request) (*http.Response, error) {
		return jsonResponse(librariesJSON), nil
	})

	libs, err := c.Libraries()
	if err != nil {
		t.Fatalf("Libraries() error: %v", err)
	}
	if len(libs) != 1 || libs[0].ID != "lib-p" {
		t.Fatalf("libs = %+v, want only lib-p", libs)
	}
}

func TestLibrariesCached(t *testing.T) {
	calls := 0
	c := mockClient("tok", "", "", nil, func(req *http.Request) (*http.Response, error) {
		calls++
		return jsonResponse(librariesJSON), nil
	})

	if _, err := c.Libraries(); err != nil {
		t.Fatalf("first Libraries() error: %v", err)
	}
	if _, err := c.Libraries(); err != nil {
		t.Fatalf("second Libraries() error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("http calls = %d, want 1", calls)
	}

	c.ClearCache()
	if _, err := c.Libraries(); err != nil {
		t.Fatalf("third Libraries() error: %v", err)
	}
	if calls != 2 {
		t.Fatalf("http calls after ClearCache = %d, want 2", calls)
	}
}

func TestLazyLogin(t *testing.T) {
	var loginBody string
	c := mockClient("", "listener", "secret", nil, func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/login":
			data, _ := io.ReadAll(req.Body)
			loginBody = string(data)
			return jsonResponse(`{"user":{"id":"u-1","token":"logged-in"}}`), nil
		case "/api/libraries":
			if got := req.Header.Get("Authorization"); got != "Bearer logged-in" {
				t.Fatalf("Authorization = %q, want Bearer logged-in", got)
			}
			return jsonResponse(librariesJSON), nil
		default:
			t.Fatalf("unexpected path %s", req.URL.Path)
			return nil, nil
		}
	})

	if err := c.Ping(); err != nil {
		t.Fatalf("Ping() error: %v", err)
	}
	if !strings.Contains(loginBody, `"username":"listener"`) || !strings.Contains(loginBody, `"password":"secret"`) {
		t.Fatalf("login body = %s", loginBody)
	}
}

func TestLoginAcceptsAccessToken(t *testing.T) {
	c := mockClient("", "listener", "secret", nil, func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/login":
			return jsonResponse(`{"user":{"id":"u-1"},"accessToken":"newer"}`), nil
		case "/api/libraries":
			if got := req.Header.Get("Authorization"); got != "Bearer newer" {
				t.Fatalf("Authorization = %q, want Bearer newer", got)
			}
			return jsonResponse(librariesJSON), nil
		default:
			t.Fatalf("unexpected path %s", req.URL.Path)
			return nil, nil
		}
	})

	if err := c.Ping(); err != nil {
		t.Fatalf("Ping() error: %v", err)
	}
}

func TestMissingCredentials(t *testing.T) {
	c := mockClient("", "", "", nil, func(req *http.Request) (*http.Response, error) {
		t.Fatal("no request should be made")
		return nil, nil
	})

	err := c.Ping()
	if err == nil || !strings.Contains(err.Error(), "missing token or user/password") {
		t.Fatalf("Ping() error = %v, want a missing-credentials error", err)
	}
	// The client no longer prefixes its own errors: the provider (or the setup
	// wizard) adds the single "audiobookshelf:" tag when it wraps.
	if strings.Contains(err.Error(), "audiobookshelf:") {
		t.Fatalf("Ping() error = %v, want no provider prefix at the client layer", err)
	}
}

func TestGetHTTPError(t *testing.T) {
	c := mockClient("tok", "", "", nil, func(req *http.Request) (*http.Response, error) {
		return statusResponse(http.StatusUnauthorized, "401 Unauthorized"), nil
	})

	err := c.Ping()
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("Ping() error = %v, want 401", err)
	}
}

func TestStreamURL(t *testing.T) {
	c := mockClient("tok", "", "", nil, func(req *http.Request) (*http.Response, error) {
		t.Fatal("no request should be made")
		return nil, nil
	})

	got := c.StreamURL("item-1", "12345")
	want := "https://abs.example.com/api/items/item-1/file/12345?token=tok"
	if got != want {
		t.Fatalf("StreamURL() = %q, want %q", got, want)
	}
}

func TestIsStreamURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want bool
	}{
		{name: "item file", url: "https://abs.example.com/api/items/item-1/file/123?token=t", want: true},
		{name: "cover", url: "https://abs.example.com/api/items/item-1/cover", want: false},
		{name: "other host path", url: "https://music.example.com/rest/stream", want: false},
		{name: "not a url", url: "::::", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsStreamURL(tt.url); got != tt.want {
				t.Fatalf("IsStreamURL(%q) = %v, want %v", tt.url, got, tt.want)
			}
		})
	}
}

func TestItemsFollowsPagination(t *testing.T) {
	pages := 0
	c := mockClient("tok", "", "", nil, func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/api/libraries/lib-b/items" {
			t.Fatalf("unexpected path %s", req.URL.Path)
		}
		if got := req.URL.Query().Get("limit"); got != "500" {
			t.Fatalf("limit = %q, want 500", got)
		}
		page := req.URL.Query().Get("page")
		pages++
		switch page {
		case "0":
			items := make([]string, 0, itemsPageLimit)
			for i := 0; i < itemsPageLimit; i++ {
				items = append(items, `{"id":"item-a"}`)
			}
			return jsonResponse(`{"total":501,"results":[` + strings.Join(items, ",") + `]}`), nil
		case "1":
			return jsonResponse(`{"total":501,"results":[{"id":"item-b"}]}`), nil
		default:
			t.Fatalf("unexpected page %q", page)
			return nil, nil
		}
	})

	items, err := c.Items("lib-b")
	if err != nil {
		t.Fatalf("Items() error: %v", err)
	}
	if len(items) != itemsPageLimit+1 {
		t.Fatalf("got %d items, want %d", len(items), itemsPageLimit+1)
	}
	if pages != 2 {
		t.Fatalf("pages fetched = %d, want 2", pages)
	}
}

func TestItemsStopsAtPageCap(t *testing.T) {
	items := make([]string, 0, itemsPageLimit)
	for i := 0; i < itemsPageLimit; i++ {
		items = append(items, `{"id":"item-a"}`)
	}
	fullPage := `{"total":0,"results":[` + strings.Join(items, ",") + `]}`

	pages := 0
	c := mockClient("tok", "", "", nil, func(req *http.Request) (*http.Response, error) {
		pages++
		return jsonResponse(fullPage), nil
	})

	got, err := c.Items("lib-b")
	if err != nil {
		t.Fatalf("Items() error: %v", err)
	}
	if pages != itemsMaxPages {
		t.Fatalf("pages fetched = %d, want %d (must stop at the page cap)", pages, itemsMaxPages)
	}
	if len(got) != itemsMaxPages*itemsPageLimit {
		t.Fatalf("got %d items, want %d", len(got), itemsMaxPages*itemsPageLimit)
	}
}

func TestItemAndAuthors(t *testing.T) {
	c := mockClient("tok", "", "", nil, func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/api/items/item-1":
			return jsonResponse(`{"id":"item-1","mediaType":"book","media":{"metadata":{"title":"Mistborn"}}}`), nil
		case "/api/libraries/lib-b/authors":
			return jsonResponse(`{"authors":[{"id":"au-1","name":"Brandon Sanderson","numBooks":9}]}`), nil
		case "/api/authors/au-1":
			if got := req.URL.Query().Get("include"); got != "items" {
				t.Fatalf("include = %q, want items", got)
			}
			return jsonResponse(`{"id":"au-1","name":"Brandon Sanderson","libraryItems":[{"id":"item-1"}]}`), nil
		default:
			t.Fatalf("unexpected path %s", req.URL.Path)
			return nil, nil
		}
	})

	item, err := c.Item("item-1")
	if err != nil {
		t.Fatalf("Item() error: %v", err)
	}
	if item.Media.Metadata.Title != "Mistborn" {
		t.Fatalf("title = %q, want Mistborn", item.Media.Metadata.Title)
	}

	authors, err := c.Authors("lib-b")
	if err != nil {
		t.Fatalf("Authors() error: %v", err)
	}
	if len(authors) != 1 || authors[0].NumBooks != 9 {
		t.Fatalf("authors = %+v", authors)
	}

	items, err := c.AuthorItems("au-1")
	if err != nil {
		t.Fatalf("AuthorItems() error: %v", err)
	}
	if len(items) != 1 || items[0].ID != "item-1" {
		t.Fatalf("author items = %+v", items)
	}
}

func TestSearchFlattensBooksAndPodcasts(t *testing.T) {
	c := mockClient("tok", "", "", nil, func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/api/libraries/lib-b/search" {
			t.Fatalf("unexpected path %s", req.URL.Path)
		}
		if got := req.URL.Query().Get("q"); got != "mistborn" {
			t.Fatalf("q = %q, want mistborn", got)
		}
		return jsonResponse(`{
			"book":[{"libraryItem":{"id":"item-1","mediaType":"book"}}],
			"podcast":[{"libraryItem":{"id":"item-2","mediaType":"podcast"}}]
		}`), nil
	})

	items, err := c.Search("lib-b", "mistborn", 10)
	if err != nil {
		t.Fatalf("Search() error: %v", err)
	}
	if len(items) != 2 || items[0].ID != "item-1" || items[1].ID != "item-2" {
		t.Fatalf("items = %+v", items)
	}
}

func TestProgressAndUpdateProgress(t *testing.T) {
	var gotPath, gotBody string
	c := mockClient("tok", "", "", nil, func(req *http.Request) (*http.Response, error) {
		switch {
		case req.URL.Path == "/api/me":
			return jsonResponse(`{"id":"u-1","mediaProgress":[
				{"libraryItemId":"item-1","currentTime":1200.5,"duration":36000,"isFinished":false}
			]}`), nil
		case strings.HasPrefix(req.URL.Path, "/api/me/progress/"):
			if req.Method != http.MethodPatch {
				t.Fatalf("progress method = %s, want PATCH (the server 404s on POST)", req.Method)
			}
			data, _ := io.ReadAll(req.Body)
			gotPath, gotBody = req.URL.Path, string(data)
			return statusResponse(http.StatusOK, "200 OK"), nil
		default:
			t.Fatalf("unexpected path %s", req.URL.Path)
			return nil, nil
		}
	})

	list, err := c.Progress()
	if err != nil {
		t.Fatalf("Progress() error: %v", err)
	}
	if len(list) != 1 || list[0].CurrentTime != 1200.5 {
		t.Fatalf("progress = %+v", list)
	}

	if err := c.UpdateProgress("item-1", "ep-9", 30, 600, true); err != nil {
		t.Fatalf("UpdateProgress() error: %v", err)
	}
	if gotPath != "/api/me/progress/item-1/ep-9" {
		t.Fatalf("path = %q, want /api/me/progress/item-1/ep-9", gotPath)
	}
	for _, want := range []string{`"currentTime":30`, `"duration":600`, `"isFinished":true`, `"progress":0.05`} {
		if !strings.Contains(gotBody, want) {
			t.Fatalf("body %s missing %s", gotBody, want)
		}
	}
}

func TestItemRequestsExpanded(t *testing.T) {
	var gotQuery string
	c := mockClient("tok", "", "", nil, func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/api/items/book-1" {
			t.Fatalf("unexpected path %s", req.URL.Path)
		}
		gotQuery = req.URL.RawQuery
		return jsonResponse(`{"id":"book-1","media":{"duration":7200,"tracks":[{"index":1,"ino":"111"}]}}`), nil
	})

	item, err := c.Item("book-1")
	if err != nil {
		t.Fatalf("Item() error: %v", err)
	}
	if gotQuery != "expanded=1" {
		t.Fatalf("query = %q, want expanded=1: without it the response omits media.tracks and media.duration", gotQuery)
	}
	if len(item.Media.Tracks) != 1 {
		t.Fatalf("tracks = %d, want 1", len(item.Media.Tracks))
	}
}

func TestGetRetriesOnceAfterUnauthorized(t *testing.T) {
	logins, gets := 0, 0
	tokens := []string{"stale", "fresh"}
	c := mockClient("", "listener", "secret", nil, func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/login":
			body := `{"user":{"id":"u-1","token":"` + tokens[logins] + `"}}`
			logins++
			return jsonResponse(body), nil
		case "/api/libraries":
			gets++
			if req.Header.Get("Authorization") == "Bearer stale" {
				return statusResponse(http.StatusUnauthorized, "401 Unauthorized"), nil
			}
			if got := req.Header.Get("Authorization"); got != "Bearer fresh" {
				t.Fatalf("retry Authorization = %q, want Bearer fresh", got)
			}
			return jsonResponse(librariesJSON), nil
		default:
			t.Fatalf("unexpected path %s", req.URL.Path)
			return nil, nil
		}
	})

	libs, err := c.Libraries()
	if err != nil {
		t.Fatalf("Libraries() error: %v", err)
	}
	if len(libs) != 3 {
		t.Fatalf("got %d libraries, want 3", len(libs))
	}
	if logins != 2 || gets != 2 {
		t.Fatalf("logins = %d, gets = %d; want 2 and 2 (one re-login, one retry)", logins, gets)
	}
}

func TestGetRetriesOnlyOnce(t *testing.T) {
	logins, gets := 0, 0
	c := mockClient("", "listener", "secret", nil, func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/login":
			logins++
			return jsonResponse(`{"user":{"id":"u-1","token":"tok"}}`), nil
		case "/api/libraries":
			gets++
			return statusResponse(http.StatusUnauthorized, "401 Unauthorized"), nil
		default:
			t.Fatalf("unexpected path %s", req.URL.Path)
			return nil, nil
		}
	})

	err := c.Ping()
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("Ping() error = %v, want a 401", err)
	}
	if logins != 2 || gets != 2 {
		t.Fatalf("logins = %d, gets = %d; want 2 and 2 (no retry loop)", logins, gets)
	}
}

func TestGetDoesNotRetryWithConfiguredToken(t *testing.T) {
	gets := 0
	c := mockClient("tok", "", "", nil, func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/login" {
			t.Fatal("must not attempt login when a token is configured")
		}
		gets++
		return statusResponse(http.StatusUnauthorized, "401 Unauthorized"), nil
	})

	if err := c.Ping(); err == nil {
		t.Fatal("Ping() error = nil, want a 401")
	}
	if gets != 1 {
		t.Fatalf("requests = %d, want 1 (no credentials to retry with)", gets)
	}
}

func TestSendJSONRetriesOnceAfterUnauthorized(t *testing.T) {
	logins, patches := 0, 0
	tokens := []string{"stale", "fresh"}
	c := mockClient("", "listener", "secret", nil, func(req *http.Request) (*http.Response, error) {
		switch {
		case req.URL.Path == "/login":
			body := `{"user":{"id":"u-1","token":"` + tokens[logins] + `"}}`
			logins++
			return jsonResponse(body), nil
		case strings.HasPrefix(req.URL.Path, "/api/me/progress/"):
			patches++
			if req.Header.Get("Authorization") == "Bearer stale" {
				return statusResponse(http.StatusUnauthorized, "401 Unauthorized"), nil
			}
			return statusResponse(http.StatusOK, "200 OK"), nil
		default:
			t.Fatalf("unexpected path %s", req.URL.Path)
			return nil, nil
		}
	})

	if err := c.UpdateProgress("item-1", "", 30, 600, false); err != nil {
		t.Fatalf("UpdateProgress() error: %v", err)
	}
	if logins != 2 || patches != 2 {
		t.Fatalf("logins = %d, patches = %d; want 2 and 2", logins, patches)
	}
}
