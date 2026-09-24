package tidal

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestResolveSourceCancellation(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	p := &TidalProvider{client: testClient(server)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, _, err := p.ResolveSource(ctx, TrackURIPrefix+"1"); done <- err }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("playback request did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("playback resolution did not cancel")
	}
}
