package spotify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/oauth2"
)

type searchRequest struct {
	limit  int
	offset int
}

// devModeSpotify fakes an app in Development Mode: /v1/search rejects any limit
// above devModeSearchLimit with the same 400 "Invalid limit" the real API
// returns, and serves offset paging normally. It records each page request.
func devModeSpotify(t *testing.T, total, failOffset int) (*SpotifyProvider, *[]searchRequest) {
	t.Helper()
	var requests []searchRequest

	originalTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/v1/search" {
			return nil, fmt.Errorf("unexpected Spotify API path %q", req.URL.Path)
		}
		q := req.URL.Query()
		limit, _ := strconv.Atoi(q.Get("limit"))
		offset, _ := strconv.Atoi(q.Get("offset"))
		requests = append(requests, searchRequest{limit: limit, offset: offset})

		if offset == failOffset {
			return nil, context.Canceled
		}

		if limit > devModeSearchLimit {
			return &http.Response{
				StatusCode: http.StatusBadRequest,
				Status:     "400 Bad Request",
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"error":{"status":400,"message":"Invalid limit"}}`)),
				Request:    req,
			}, nil
		}

		items := []map[string]any{}
		for i := offset; i < offset+limit && i < total; i++ {
			items = append(items, map[string]any{
				"id":   fmt.Sprintf("t%d", i),
				"name": fmt.Sprintf("Track %d", i),
				"type": "track",
				"uri":  fmt.Sprintf("spotify:track:t%d", i),
			})
		}
		body, _ := json.Marshal(map[string]any{
			"tracks":   map[string]any{"items": items},
			"episodes": map[string]any{"items": []any{}},
		})
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(string(body))),
			Request:    req,
		}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	sess := &Session{tokenSource: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "token"})}
	return New(sess, "client", 320), &requests
}

func TestSearchTracksDevModePagination(t *testing.T) {
	tests := []struct {
		name         string
		total        int
		limit        int
		wantResults  int
		wantRequests []searchRequest
	}{
		{
			name:        "pages around Development Mode limit",
			total:       100,
			limit:       25,
			wantResults: 25,
			wantRequests: []searchRequest{
				{limit: 25, offset: 0},
				{limit: 10, offset: 0},
				{limit: 10, offset: 10},
				{limit: 5, offset: 20},
			},
		},
		{
			name:         "keeps single request when limit fits",
			total:        100,
			limit:        10,
			wantResults:  10,
			wantRequests: []searchRequest{{limit: 10, offset: 0}},
		},
		{
			name:        "stops when results run out",
			total:       12,
			limit:       50,
			wantResults: 12,
			wantRequests: []searchRequest{
				{limit: 50, offset: 0},
				{limit: 10, offset: 0},
				{limit: 10, offset: 10},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, requests := devModeSpotify(t, tt.total, -1)

			got, err := p.SearchTracks(context.Background(), "radiohead", tt.limit)
			if err != nil {
				t.Fatalf("SearchTracks() error = %v", err)
			}
			if len(got) != tt.wantResults {
				t.Errorf("got %d tracks, want %d", len(got), tt.wantResults)
			}
			if !slices.Equal(*requests, tt.wantRequests) {
				t.Errorf("requests = %v, want %v", *requests, tt.wantRequests)
			}
		})
	}
}

func TestSearchTracksReturnsLaterPageError(t *testing.T) {
	p, requests := devModeSpotify(t, 100, 10)

	got, err := p.SearchTracks(context.Background(), "radiohead", 25)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("SearchTracks() error = %v, want context.Canceled", err)
	}
	if got != nil {
		t.Errorf("SearchTracks() returned %d partial results, want none", len(got))
	}
	wantRequests := []searchRequest{
		{limit: 25, offset: 0},
		{limit: 10, offset: 0},
		{limit: 10, offset: 10},
	}
	if !slices.Equal(*requests, wantRequests) {
		t.Errorf("requests = %v, want %v", *requests, wantRequests)
	}
}
