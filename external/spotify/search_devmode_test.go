//go:build !windows

package spotify

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/oauth2"
)

// devModeSpotify fakes an app in Development Mode: /v1/search rejects any limit
// above devModeSearchLimit with the same 400 "Invalid limit" the real API
// returns, and serves offset paging normally. It records every limit it saw.
func devModeSpotify(t *testing.T, total int) (*SpotifyProvider, *[]int) {
	t.Helper()
	var limits []int

	originalTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/v1/search" {
			return nil, fmt.Errorf("unexpected Spotify API path %q", req.URL.Path)
		}
		q := req.URL.Query()
		limit, _ := strconv.Atoi(q.Get("limit"))
		offset, _ := strconv.Atoi(q.Get("offset"))
		limits = append(limits, limit)

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
	return New(sess, "client", 320), &limits
}

func TestSearchTracksPagesAroundDevModeLimit(t *testing.T) {
	p, limits := devModeSpotify(t, 100)

	got, err := p.SearchTracks(context.Background(), "radiohead", 25)
	if err != nil {
		t.Fatalf("SearchTracks() failed instead of paging around the cap: %v", err)
	}
	if len(got) != 25 {
		t.Errorf("got %d tracks, want 25", len(got))
	}
	// One rejected attempt at 25, then pages of 10, 10 and 5.
	want := []int{25, 10, 10, 5}
	if fmt.Sprint(*limits) != fmt.Sprint(want) {
		t.Errorf("requested limits = %v, want %v", *limits, want)
	}
}

func TestSearchTracksKeepsSingleRequestWhenLimitFits(t *testing.T) {
	p, limits := devModeSpotify(t, 100)

	got, err := p.SearchTracks(context.Background(), "radiohead", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 10 {
		t.Errorf("got %d tracks, want 10", len(got))
	}
	if len(*limits) != 1 {
		t.Errorf("made %d requests, want 1: %v", len(*limits), *limits)
	}
}

func TestSearchTracksStopsWhenResultsRunOut(t *testing.T) {
	p, limits := devModeSpotify(t, 12) // fewer results than asked for

	got, err := p.SearchTracks(context.Background(), "radiohead", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 12 {
		t.Errorf("got %d tracks, want 12", len(got))
	}
	// 50 rejected, then 10 + 2; the short second page ends the loop.
	if len(*limits) != 3 {
		t.Errorf("made %d requests, want 3: %v", len(*limits), *limits)
	}
}
