package tidal

import (
	"context"
	"testing"
)

func TestTidalClosePreventsStaleAuthenticateInstall(t *testing.T) {
	orig := signIn
	t.Cleanup(func() { signIn = orig })

	entered := make(chan struct{})
	release := make(chan struct{})
	signIn = func(context.Context, string, string) (*client, error) {
		close(entered)
		<-release
		return &client{}, nil
	}

	p := New("lossless", "cid", "csec")
	done := make(chan error, 1)
	go func() {
		done <- p.Authenticate()
	}()
	<-entered
	p.Close()
	close(release)
	<-done

	p.mu.Lock()
	got := p.client
	p.mu.Unlock()
	if got != nil {
		t.Fatal("stale Authenticate installed client after Close")
	}
}
