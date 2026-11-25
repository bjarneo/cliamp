package lyrics

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/internal/httpclient"
)

// These bodies were produced from music.163.com on 2026-11-22 with the exact
// POST that fetchNetEase sends:
//
//	curl -X POST 'https://music.163.com/api/search/get' \
//	  -H 'Content-Type: application/x-www-form-urlencoded' \
//	  -H 'Referer: https://music.163.com' \
//	  -H 'User-Agent: cliamp/1.0 (https://github.com/bjarneo/cliamp)' \
//	  --data-urlencode 'limit=1' --data-urlencode 's=Boyz II Men A Song for Mama' \
//	  --data-urlencode 'type=1'
//
// The /web gateway answers plain form params with an opaque string;
// /api/search/get returns result.songs.
const (
	netEaseOpaqueReply = `{"result":"35b1748964af8a7c1d883e3a6f3c773bc15667bf74e3dde5ba8c890d6f668c64b4ab3bc02486e0d4f0951af242c9d1c9c7809220c400b434441c200ab387b440ca21851885ed3a229deb65ec51c2629156bcf1df2fb4c981891c55c2da5649dee68a2f10d6b7208650a5d9d75bcb9813c1d6be00529dda04ddc4252dafdee67c8c95f2f5bba4570f604004783192b2a6d"}`
	netEaseSongReply   = `{"result":{"songs":[{"id":3954676}]}}`
	netEaseLyricReply  = `{"lrc":{"lyric":"[00:25.100]You taught me everything\n[00:27.100]And everything you've given me"}}`
)

type netEaseRequest struct {
	method, path, rawQuery, body, referer, userAgent string
}

// TestNetEaseSearchEndpoint asserts the wire contract of the search request
// and covers both reply shapes without using the network.
func TestNetEaseSearchEndpoint(t *testing.T) {
	tests := []struct {
		name         string
		servePath    string
		reply        string
		wantLines    int
		wantErr      error
		wantAnyErr   bool
		wantRequests int
	}{
		{
			name:         "canonical path with the live opaque reply finds nothing",
			servePath:    "/api/search/get",
			reply:        netEaseOpaqueReply,
			wantErr:      ErrNotFound,
			wantRequests: 1,
		},
		{
			name:         "the old /web path is never requested",
			servePath:    "/api/search/get/web",
			reply:        netEaseSongReply,
			wantAnyErr:   true,
			wantRequests: 1,
		},
		{
			name:         "a song list at the canonical path still parses",
			servePath:    "/api/search/get",
			reply:        netEaseSongReply,
			wantLines:    2,
			wantRequests: 2,
		},
	}

	const query = "Boyz II Men A Song for Mama"
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var mu sync.Mutex
			var requests []netEaseRequest
			record := func(r *http.Request) netEaseRequest {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("read request body: %v", err)
				}
				got := netEaseRequest{
					method: r.Method, path: r.URL.Path, rawQuery: r.URL.RawQuery,
					body: string(body), referer: r.Header.Get("Referer"),
					userAgent: r.Header.Get("User-Agent"),
				}
				mu.Lock()
				requests = append(requests, got)
				mu.Unlock()
				return got
			}

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				record(r)
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case tt.servePath:
					_, _ = io.WriteString(w, tt.reply)
				case "/api/song/lyric":
					_, _ = io.WriteString(w, netEaseLyricReply)
				default:
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(srv.Close)
			old := neteaseBaseURL
			t.Cleanup(func() { neteaseBaseURL = old })
			neteaseBaseURL = srv.URL

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			lines, err := fetchNetEase(ctx, query)

			mu.Lock()
			got := append([]netEaseRequest(nil), requests...)
			mu.Unlock()
			if len(got) == 0 {
				t.Fatal("no request reached the fake NetEase API")
			}
			search := got[0]
			if search.method != http.MethodPost {
				t.Errorf("search method = %q, want POST", search.method)
			}
			if search.path != "/api/search/get" {
				t.Errorf("search path = %q, want /api/search/get", search.path)
			}
			if want := "limit=1&s=Boyz+II+Men+A+Song+for+Mama&type=1"; search.body != want {
				t.Errorf("search body = %q, want %q", search.body, want)
			}
			if search.referer != srv.URL {
				t.Errorf("search Referer = %q, want %q", search.referer, srv.URL)
			}
			if search.userAgent != httpclient.UserAgent {
				t.Errorf("search User-Agent = %q, want %q", search.userAgent, httpclient.UserAgent)
			}
			if len(got) != tt.wantRequests {
				t.Fatalf("got %d requests, want %d", len(got), tt.wantRequests)
			}
			if len(got) > 1 {
				lyric := got[1]
				if lyric.method != http.MethodGet ||
					lyric.path != "/api/song/lyric" || lyric.rawQuery != "id=3954676&lv=1&kv=1&tv=-1" {
					t.Errorf("unexpected lyric request: %+v", lyric)
				}
			}

			switch {
			case tt.wantErr != nil && !errors.Is(err, tt.wantErr):
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			case tt.wantAnyErr && err == nil:
				t.Fatal("err = nil, want an error")
			case !tt.wantAnyErr && tt.wantErr == nil && err != nil:
				t.Fatalf("err = %v, want nil", err)
			}
			if len(lines) != tt.wantLines {
				t.Errorf("lines = %d, want %d", len(lines), tt.wantLines)
			}

			if tt.wantErr == ErrNotFound {
				var decoded ncmSearchResponse
				decodeErr := httpclient.ReadJSON(strings.NewReader(netEaseOpaqueReply), maxResponseBody, &decoded)
				var typeErr *json.UnmarshalTypeError
				if !errors.As(decodeErr, &typeErr) {
					t.Fatalf("decode error = %v, want *json.UnmarshalTypeError", decodeErr)
				}
				if len(decoded.Result.Songs) != 0 {
					t.Fatalf("decoded songs = %d, want 0", len(decoded.Result.Songs))
				}
			}
		})
	}
}

// TestNetEaseLiveLookup asserts the intended behavior against the real API.
// It is skipped by default so the test suite stays deterministic offline.
// Run with CLIAMP_LIVE_NCM_TEST=1 to exercise music.163.com.
func TestNetEaseLiveLookup(t *testing.T) {
	if os.Getenv("CLIAMP_LIVE_NCM_TEST") == "" {
		t.Skip("set CLIAMP_LIVE_NCM_TEST=1 to run the live NetEase check")
	}
	for _, query := range []string{"Boyz II Men A Song for Mama", "Taylor Swift Blank Space"} {
		t.Run(query, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			lines, err := fetchNetEase(ctx, query)
			if err != nil {
				t.Fatalf("fetchNetEase(%q): %v", query, err)
			}
			if len(lines) == 0 {
				t.Fatalf("fetchNetEase(%q): no lines", query)
			}
			synced := false
			for _, line := range lines {
				if line.Start > 0 {
					synced = true
					break
				}
			}
			if !synced {
				t.Errorf("no line has a timestamp: %+v", lines[0])
			}
			t.Logf("got %d lines, first: %q at %s", len(lines), lines[0].Text, lines[0].Start)
		})
	}
}
