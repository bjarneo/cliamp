package yandex

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/playlist"
)

func TestDownloadTrackUsesPlaybackURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("OAuth forwarded to CDN")
		}
		if r.URL.Path != "/get-mp3/signed/audio" {
			t.Error("wrong stream path")
		}
		_, _ = w.Write([]byte("synthetic audio"))
	}))
	defer server.Close()
	p := New("private-token")
	p.urlCache["77"] = urlEntry{url: server.URL + "/get-mp3/signed/audio", at: time.Now()}
	track := playlist.Track{Path: TrackURIPrefix + "77", Title: "Song", Stream: true}
	playbackURL, err := p.ResolveSource(track.Path)
	if err != nil || playbackURL != p.urlCache["77"].url {
		t.Fatal("playback cache mismatch")
	}
	path, err := p.DownloadTrack(context.Background(), track, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "synthetic audio" {
		t.Fatalf("data=%q err=%v", data, err)
	}
}

func TestDownloadTrackRejectsUnavailable(t *testing.T) {
	p := New("private-token")
	for _, track := range []playlist.Track{{Path: "other:track:1"}, {Path: TrackURIPrefix + "../bad"}, {Path: TrackURIPrefix + "77", Unplayable: true}} {
		if _, err := p.DownloadTrack(context.Background(), track, t.TempDir()); err == nil {
			t.Fatal("accepted unavailable track")
		}
	}
}

func TestDownloadTrackResolutionCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { cancel(); <-r.Context().Done() }))
	defer server.Close()
	p := New("private-token")
	p.api.apiBase = server.URL
	_, err := p.DownloadTrack(ctx, playlist.Track{Path: TrackURIPrefix + "77"}, t.TempDir())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}

func TestDownloadTrackRedactsResolutionError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "signed-secret private-token", 403) }))
	defer server.Close()
	p := New("private-token")
	p.api.apiBase = server.URL
	_, err := p.DownloadTrack(context.Background(), playlist.Track{Path: TrackURIPrefix + "77"}, t.TempDir())
	if err == nil || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "private-token") {
		t.Fatalf("unsafe err=%v", err)
	}
}

// Exercise all three requests with synthetic audio: API, signed information,
// then CDN. The TLS test transport trusts only this test server.
func TestDownloadTrackResolvesFreshStream(t *testing.T) {
	var base string
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tracks/77/download-info":
			calls.Add(1)
			if r.Header.Get("Authorization") == "" {
				t.Error("missing API authentication")
			}
			writeResult(w, []downloadInfo{{Codec: "mp3", DownloadInfoURL: base + "/dl?sign=synthetic"}})
		case "/dl":
			if r.Header.Get("Authorization") != "" {
				t.Error("OAuth leaked to download info")
			}
			_ = json.NewEncoder(w).Encode(fullDownloadInfo{Host: r.Host, Path: "/audio", Ts: "1", S: "synthetic"})
		default:
			if !strings.HasPrefix(r.URL.Path, "/get-mp3/") {
				t.Error("unexpected CDN request")
			}
			if r.Header.Get("Authorization") != "" {
				t.Error("OAuth leaked to CDN")
			}
			_, _ = w.Write([]byte("synthetic audio"))
		}
	}))
	defer server.Close()
	base = server.URL
	oldTransport, oldGuard := http.DefaultTransport, fullDownloadInfoGuard
	http.DefaultTransport = server.Client().Transport
	fullDownloadInfoGuard = func(string) error { return nil }
	defer func() { http.DefaultTransport = oldTransport; fullDownloadInfoGuard = oldGuard }()
	p := New("test-token")
	p.api.apiBase = base
	path, err := p.DownloadTrack(context.Background(), playlist.Track{Path: TrackURIPrefix + "77"}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "synthetic audio" {
		t.Fatalf("data=%q err=%v", data, err)
	}
	if _, err := p.ResolveSource(TrackURIPrefix + "77"); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("playback did not reuse resolver cache: %d calls", calls.Load())
	}
}
