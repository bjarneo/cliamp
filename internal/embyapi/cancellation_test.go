package embyapi

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestResolveSourceCancelsAuthenticationAndWaiters(t *testing.T) {
	started := make(chan struct{})
	c := NewJellyfinClient("https://jf.example", "", "", "user", "password")
	c.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		close(started)
		<-req.Context().Done()
		return nil, req.Context().Err()
	})})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := c.ResolveSource(ctx, "https://jf.example/Items/1/Download"); done <- err }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("authentication did not start")
	}
	waiterCtx, waiterCancel := context.WithCancel(context.Background())
	waiterCancel()
	waiter := make(chan error, 1)
	go func() { _, err := c.ResolveSource(waiterCtx, "https://jf.example/Items/2/Download"); waiter <- err }()
	select {
	case err := <-waiter:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("waiter error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled resolution waited for another login")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("authentication error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("authentication did not cancel")
	}
}
