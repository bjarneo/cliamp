package yandex

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// blockingServer serves every request by waiting until the client goes away
// or the test ends. reached receives one value per request.
func blockingServer(t *testing.T) (*httptest.Server, <-chan struct{}) {
	t.Helper()
	reached := make(chan struct{}, 8)
	release := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached <- struct{}{}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(ts.Close)
	t.Cleanup(func() { close(release) })
	return ts, reached
}

func TestSearchTracksHonorsCancellation(t *testing.T) {
	ts, reached := blockingServer(t)
	p := New("test-token")
	p.api.apiBase = ts.URL

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := p.SearchTracks(ctx, "query", 10)
		done <- err
	}()

	<-reached
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("SearchTracks() error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SearchTracks() did not return after cancel")
	}
}

// TestAccountLookupDoesNotHoldLock checks that a slow first account status
// call does not stall stream resolution, which also takes p.mu.
func TestAccountLookupDoesNotHoldLock(t *testing.T) {
	ts, reached := blockingServer(t)
	p := New("test-token")
	p.api.apiBase = ts.URL
	p.urlCache["77"] = urlEntry{url: "https://s1.music.yandex.ru/get-mp3/cached", at: time.Now()}

	go func() { _, _ = p.Playlists() }()
	<-reached

	done := make(chan error, 1)
	go func() {
		_, err := p.ResolveSource(TrackURIPrefix + "77")
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ResolveSource() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ResolveSource() waited for the account status call")
	}
}
