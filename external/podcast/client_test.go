package podcast

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/internal/appmeta"
)

func newDirectoryTestClient(t *testing.T, handler http.HandlerFunc) *client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := newClient()
	c.directoryURL = srv.URL
	c.chartsURL = srv.URL + "/api/v2"
	return c
}

func TestNewClient(t *testing.T) {
	c := newClient()
	if c.http == nil || c.http.Timeout != 30*time.Second {
		t.Fatalf("HTTP client = %#v, want 30s timeout", c.http)
	}
	if c.directoryURL != "https://itunes.apple.com" {
		t.Errorf("directoryURL = %q", c.directoryURL)
	}
	if c.chartsURL != "https://rss.marketingtools.apple.com/api/v2" {
		t.Errorf("chartsURL = %q", c.chartsURL)
	}
}

func TestCategories(t *testing.T) {
	want := []category{
		{"1489", "News"},
		{"1318", "Technology"},
		{"1488", "True Crime"},
		{"1303", "Comedy"},
		{"1324", "Society & Culture"},
		{"1487", "History"},
		{"1533", "Science"},
		{"1512", "Health & Fitness"},
		{"1321", "Business"},
		{"1310", "Music"},
		{"1545", "Sports"},
		{"1301", "Arts"},
		{"1304", "Education"},
		{"1483", "Fiction"},
		{"1309", "TV & Film"},
		{"1314", "Religion & Spirituality"},
		{"1305", "Kids & Family"},
		{"1502", "Leisure"},
		{"1511", "Government"},
	}
	if !slices.Equal(categories, want) {
		t.Errorf("categories = %v, want %v", categories, want)
	}
}

func TestShowJSON(t *testing.T) {
	s := show{
		ID: "123", Title: "Show", FeedURL: "https://example.com/feed.xml",
		Author: "Author", Artwork: "https://example.com/art.jpg", Genre: "News", EpisodeCount: 12,
	}
	body, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"id":"123","title":"Show","feed_url":"https://example.com/feed.xml","author":"Author","artwork":"https://example.com/art.jpg","genre":"News","episode_count":12}`
	if string(body) != want {
		t.Errorf("JSON = %s, want %s", body, want)
	}
	var restored show
	if err := json.Unmarshal(body, &restored); err != nil {
		t.Fatal(err)
	}
	if restored != s {
		t.Errorf("restored show = %+v, want %+v", restored, s)
	}
}

func TestValidHTTPURL(t *testing.T) {
	tests := []struct {
		raw  string
		want bool
	}{
		{"https://example.com/feed.xml", true},
		{"http://example.com:8080/feed?x=1&y=2", true},
		{"HTTPS://example.com/feed", true},
		{"http://[::1]:8080/feed", true},
		{"https://example.com/a%20b", true},
		{"", false},
		{" ", false},
		{"/tmp/feed.xml", false},
		{"feed.xml", false},
		{"//example.com/feed.xml", false},
		{"file:///tmp/feed.xml", false},
		{"ftp://example.com/feed.xml", false},
		{"javascript:alert(1)", false},
		{"data:text/xml,feed", false},
		{"https:feed.xml", false},
		{"https:///feed.xml", false},
		{"https://:443/feed.xml", false},
		{"https://user:pass@example.com/feed.xml", false},
		{"https://user@example.com/feed.xml", false},
		{"https://@example.com/feed.xml", false},
		{"https://example.com/%zz", false},
		{"https://bad host/feed.xml", false},
		{"https://[::1/feed.xml", false},
		{"https://example.com/feed\n.xml", false},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			if got := validHTTPURL(tt.raw); got != tt.want {
				t.Errorf("validHTTPURL(%q) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestClientSearchQuery(t *testing.T) {
	for _, genreID := range []string{"", "1324", " \t"} {
		t.Run(genreID, func(t *testing.T) {
			var calls atomic.Int32
			term := "science & culture + 50% #news? caf\u00e9"
			c := newDirectoryTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/search" {
					t.Errorf("request = %s %s", r.Method, r.URL.Path)
				}
				want := url.Values{
					"term": {term}, "media": {"podcast"}, "entity": {"podcast"}, "limit": {"100"},
				}
				if strings.TrimSpace(genreID) != "" {
					want.Set("genreId", genreID)
				}
				if got := r.URL.Query(); !reflect.DeepEqual(got, want) {
					t.Errorf("query = %v, want %v", got, want)
				}
				if got, want := r.UserAgent(), appmeta.ClientName()+"/"+appmeta.Version(); got != want {
					t.Errorf("User-Agent = %q, want %q", got, want)
				}
				if got := r.Header.Get("Accept"); got != "application/json" {
					t.Errorf("Accept = %q", got)
				}
				io.WriteString(w, `{"resultCount":1,"results":[{"collectionId":1,"collectionName":"Show","feedUrl":"https://example.com/feed.xml"}]}`)
			})
			got, err := c.search(t.Context(), "  "+term+"\t", genreID)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || got[0].ID != "1" {
				t.Errorf("search = %+v, want one show with ID 1", got)
			}
			if got := calls.Load(); got != 1 {
				t.Errorf("requests = %d, want 1", got)
			}
		})
	}
}

func TestClientSearchBlank(t *testing.T) {
	c := newDirectoryTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("blank search made an HTTP request")
		io.WriteString(w, `{"results":[]}`)
	})
	for _, term := range []string{"", " \t\n", "\u2003"} {
		got, err := c.search(t.Context(), term, "1318")
		if err != nil || len(got) != 0 {
			t.Errorf("search(%q) = %+v, %v; want empty, nil", term, got, err)
		}
	}
}

func TestClientSearchDecode(t *testing.T) {
	c := newDirectoryTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"results":[
			{"collectionId":9007199254740993,"collectionName":"  Science Weekly \t","artistName":" Author ",
			 "feedUrl":" https://example.com/science.xml \n","artworkUrl600":" https://example.com/600.jpg ",
			 "artworkUrl100":"https://example.com/100.jpg","primaryGenreName":" Science ","trackCount":52},
			{"collectionId":2,"collectionName":" \t","feedUrl":"http://example.com/untitled.xml",
			 "artworkUrl600":" \t","artworkUrl100":" https://example.com/small.jpg "},
			{"collectionId":3,"collectionName":"Missing large artwork","feedUrl":"https://example.com/missing-art.xml",
			 "artworkUrl100":"https://example.com/fallback.jpg"},
			{"collectionId":4,"collectionName":"Unsafe artwork","feedUrl":"https://example.com/unsafe-art.xml",
			 "artworkUrl600":"file:///tmp/art.jpg","artworkUrl100":"https://user:pass@example.com/art.jpg"},
			{"collectionId":5,"collectionName":"Safe fallback","feedUrl":"https://example.com/fallback.xml",
			 "artworkUrl600":"//example.com/art.jpg","artworkUrl100":" https://example.com/safe.jpg "},
			{"collectionId":6,"feedUrl":"https://example.com/no-art.xml"},
			{"collectionId":7,"collectionName":"Duplicate","feedUrl":"https://example.com/science.xml"},
			{"collectionId":8},
			{"collectionId":9,"feedUrl":" \t"},
			{"collectionId":10,"feedUrl":"/tmp/feed.xml"},
			{"collectionId":11,"feedUrl":"file:///tmp/feed.xml"},
			{"collectionId":12,"feedUrl":"//example.com/feed.xml"},
			{"collectionId":13,"feedUrl":"https:feed.xml"},
			{"collectionId":14,"feedUrl":"https://user:pass@example.com/feed.xml"},
			{"collectionId":15,"feedUrl":"https://:443/feed.xml"},
			{"collectionId":16,"feedUrl":"https://example.com/%zz"}
		]}`)
	})
	got, err := c.search(t.Context(), "science", "")
	if err != nil {
		t.Fatal(err)
	}
	want := []show{
		{ID: "9007199254740993", Title: "Science Weekly", FeedURL: "https://example.com/science.xml",
			Author: "Author", Artwork: "https://example.com/600.jpg", Genre: "Science", EpisodeCount: 52},
		{ID: "2", Title: "Untitled show", FeedURL: "http://example.com/untitled.xml", Artwork: "https://example.com/small.jpg"},
		{ID: "3", Title: "Missing large artwork", FeedURL: "https://example.com/missing-art.xml", Artwork: "https://example.com/fallback.jpg"},
		{ID: "4", Title: "Unsafe artwork", FeedURL: "https://example.com/unsafe-art.xml"},
		{ID: "5", Title: "Safe fallback", FeedURL: "https://example.com/fallback.xml", Artwork: "https://example.com/safe.jpg"},
		{ID: "6", Title: "Untitled show", FeedURL: "https://example.com/no-art.xml"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("search = %+v, want %+v", got, want)
	}
}

func TestClientTop(t *testing.T) {
	var chartCalls, lookupCalls atomic.Int32
	c := newDirectoryTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if got, want := r.UserAgent(), appmeta.ClientName()+"/"+appmeta.Version(); got != want {
			t.Errorf("User-Agent = %q, want %q", got, want)
		}
		switch r.URL.Path {
		case "/api/v2/no/podcasts/top/100/podcasts.json":
			chartCalls.Add(1)
			if r.URL.RawQuery != "" {
				t.Errorf("chart query = %q, want empty", r.URL.RawQuery)
			}
			io.WriteString(w, `{"feed":{"results":[
				{"id":" 9007199254740993 "},{"id":"2"},{"id":"3"},{"id":"4"},
				{"id":"5"},{"id":"6"},{"id":"7"},{"id":"2"},{"id":""},{"id":" \t"},{}
			]}}`)
		case "/lookup":
			lookupCalls.Add(1)
			want := url.Values{"id": {"9007199254740993,2,3,4,5,6,7,2"}, "entity": {"podcast"}}
			if got := r.URL.Query(); !reflect.DeepEqual(got, want) {
				t.Errorf("lookup query = %v, want %v", got, want)
			}
			io.WriteString(w, `{"results":[
				{"collectionId":7,"collectionName":"Lower-ranked duplicate","feedUrl":"https://example.com/first.xml"},
				{"collectionId":5,"collectionName":" Last ","feedUrl":" https://example.com/last.xml ",
				 "artworkUrl100":" https://example.com/art.jpg "},
				{"collectionId":4,"feedUrl":"file:///tmp/feed.xml"},
				{"collectionId":3},
				{"collectionId":2,"feedUrl":"https://example.com/second.xml"},
				{"collectionId":9007199254740993,"collectionName":"First","feedUrl":"https://example.com/first.xml"},
				{"collectionId":8,"feedUrl":"https://example.com/unrequested.xml"}
			]}`)
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	got, err := c.top(t.Context(), "NO")
	if err != nil {
		t.Fatal(err)
	}
	want := []show{
		{ID: "9007199254740993", Title: "First", FeedURL: "https://example.com/first.xml"},
		{ID: "2", Title: "Untitled show", FeedURL: "https://example.com/second.xml"},
		{ID: "5", Title: "Last", FeedURL: "https://example.com/last.xml", Artwork: "https://example.com/art.jpg"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("top = %+v, want %+v", got, want)
	}
	if got := chartCalls.Load(); got != 1 {
		t.Errorf("chart requests = %d, want 1", got)
	}
	if got := lookupCalls.Load(); got != 1 {
		t.Errorf("lookup requests = %d, want 1", got)
	}
}

func TestClientTopEmpty(t *testing.T) {
	for _, results := range []string{`[]`, `[{"id":""},{"id":" \t"},{}]`} {
		t.Run(results, func(t *testing.T) {
			var calls atomic.Int32
			c := newDirectoryTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != "/api/v2/us/podcasts/top/100/podcasts.json" {
					t.Errorf("unexpected request %q", r.URL.Path)
				}
				io.WriteString(w, `{"feed":{"results":`+results+`}}`)
			})
			got, err := c.top(t.Context(), "us")
			if err != nil || len(got) != 0 {
				t.Errorf("top = %+v, %v; want empty, nil", got, err)
			}
			if got := calls.Load(); got != 1 {
				t.Errorf("requests = %d, want 1", got)
			}
		})
	}
}

func TestClientResponseFailures(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		status int
		short  bool
		want   string
	}{
		{name: "rate limited", status: http.StatusTooManyRequests, body: `{"results":[]}`, want: "429"},
		{name: "server error", status: http.StatusInternalServerError, want: "500"},
		{name: "no content", status: http.StatusNoContent, want: "204"},
		{name: "other success status", status: http.StatusCreated, body: `{"results":[]}`, want: "201"},
		{name: "empty body", want: "decode response"},
		{name: "whitespace body", body: " \t\n", want: "decode response"},
		{name: "invalid JSON", body: `<html>unavailable</html>`, want: "decode response"},
		{name: "truncated JSON", body: `{"results":[`, want: "decode response"},
		{name: "trailing JSON", body: `{"results":[]} {}`, want: "decode response"},
		{name: "trailing junk", body: `{"results":[]} invalid`, want: "decode response"},
		{name: "null", body: `null`, want: "missing results"},
		{name: "empty object", body: `{}`, want: "missing results"},
		{name: "error object", body: `{"error":"unavailable"}`, want: "missing results"},
		{name: "null results", body: `{"results":null}`, want: "missing results"},
		{name: "wrong root type", body: `[]`, want: "decode response"},
		{name: "wrong results type", body: `{"results":{}}`, want: "decode response"},
		{name: "wrong entry type", body: `{"results":[true]}`, want: "decode response"},
		{name: "null entry", body: `{"results":[null]}`, want: "null result"},
		{name: "short body", body: `{"results":[]}`, short: true, want: "read response"},
		{name: "oversized body", body: `{"results":[]}` + strings.Repeat(" ", 4<<20), want: "response exceeds"},
	}
	for _, endpoint := range []string{"search", "charts", "lookup"} {
		t.Run(endpoint, func(t *testing.T) {
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					c := newDirectoryTestClient(t, func(w http.ResponseWriter, r *http.Request) {
						if endpoint == "lookup" && r.URL.Path != "/lookup" {
							io.WriteString(w, `{"feed":{"results":[{"id":"1"}]}}`)
							return
						}
						body := tt.body
						if endpoint == "charts" {
							body = `{"feed":` + body + `}`
						}
						if tt.short {
							w.Header().Set("Content-Length", "1000")
						}
						if tt.status != 0 {
							w.WriteHeader(tt.status)
						}
						io.WriteString(w, body)
					})
					var err error
					if endpoint == "search" {
						_, err = c.search(t.Context(), "test", "")
					} else {
						_, err = c.top(t.Context(), "us")
					}
					if err == nil || !strings.Contains(err.Error(), tt.want) {
						t.Fatalf("error = %v, want containing %q", err, tt.want)
					}
					if tt.short && !errors.Is(err, io.ErrUnexpectedEOF) {
						t.Errorf("error = %v, want wrapped unexpected EOF", err)
					}
				})
			}
		})
	}
}

func TestClientSearchMalformedFields(t *testing.T) {
	for _, entry := range []string{
		`{"collectionId":"not a number","feedUrl":"https://example.com/feed.xml"}`,
		`{"feedUrl":42}`,
		`{"collectionName":true}`,
		`{"artworkUrl600":[]}`,
		`{"trackCount":"many"}`,
	} {
		t.Run(entry, func(t *testing.T) {
			c := newDirectoryTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				io.WriteString(w, `{"results":[`+entry+`]}`)
			})
			if _, err := c.search(t.Context(), "test", ""); err == nil {
				t.Error("malformed show returned no error")
			}
		})
	}
}

func TestClientBodyLimit(t *testing.T) {
	for _, size := range []int{(4 << 20) - 1, 4 << 20, (4 << 20) + 1} {
		body := `{"results":[]}`
		body += strings.Repeat(" ", size-len(body))
		c := newDirectoryTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, body)
		})
		got, err := c.search(t.Context(), "test", "")
		if (err != nil) != (size > 4<<20) {
			t.Errorf("body size %d: error = %v", size, err)
		}
		if len(got) != 0 {
			t.Errorf("body size %d: shows = %+v, want empty", size, got)
		}
	}
}

func TestClientRequestFailures(t *testing.T) {
	for _, method := range []string{"search", "top"} {
		t.Run(method, func(t *testing.T) {
			for _, failure := range []string{"canceled", "invalid URL", "closed server"} {
				t.Run(failure, func(t *testing.T) {
					srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						t.Error("unexpected HTTP request")
					}))
					t.Cleanup(srv.Close)
					c := newClient()
					c.directoryURL = srv.URL
					c.chartsURL = srv.URL
					ctx, cancel := context.WithCancel(t.Context())
					defer cancel()
					switch failure {
					case "canceled":
						cancel()
					case "invalid URL":
						c.directoryURL = "%"
						c.chartsURL = "%"
					case "closed server":
						srv.Close()
					}
					var err error
					if method == "search" {
						_, err = c.search(ctx, "test", "")
					} else {
						_, err = c.top(ctx, "us")
					}
					if err == nil {
						t.Fatal("request failure returned no error")
					}
					if failure == "canceled" && !errors.Is(err, context.Canceled) {
						t.Errorf("error = %v, want wrapped context.Canceled", err)
					}
				})
			}
		})
	}
}
