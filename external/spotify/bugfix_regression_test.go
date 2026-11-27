package spotify

import (
	"context"
	"errors"
	"testing"

	"github.com/bjarneo/cliamp/playlist"
)

func TestRelatedTracksHandlesNilSessionAfterEnsureSession(t *testing.T) {
	orig := newSessionSilent
	t.Cleanup(func() { newSessionSilent = orig })

	p := New(nil, "client-id", 320)
	// Simulate Close racing with ensureSession so ensureSession succeeds but p.session is cleared.
	newSessionSilent = func(context.Context, string) (*Session, error) {
		return nil, nil
	}

	_, err := p.RelatedTracks(context.Background(), playlist.Track{Path: "spotify:track:abc"}, 5)
	if !errors.Is(err, playlist.ErrNeedsAuth) {
		t.Fatalf("RelatedTracks() error = %v, want ErrNeedsAuth", err)
	}
}

func TestEnsureSessionPropagatesNonAuthErrors(t *testing.T) {
	orig := newSessionSilent
	t.Cleanup(func() { newSessionSilent = orig })

	netErr := errors.New("dial tcp: no such host")
	newSessionSilent = func(context.Context, string) (*Session, error) {
		return nil, netErr
	}

	p := New(nil, "client-id", 320)
	err := p.ensureSession()
	if !errors.Is(err, netErr) {
		t.Fatalf("ensureSession() error = %v, want %v (not ErrNeedsAuth)", err, netErr)
	}
}

func TestSessionReconnectDoesNotResurrectClosedSession(t *testing.T) {
	s := &Session{clientID: "cid"}
	entered := make(chan struct{})
	release := make(chan struct{})

	done := make(chan error, 1)
	go func() {
		done <- s.reconnect(context.Background(), func(context.Context, string) (*Session, error) {
			close(entered)
			<-release
			return &Session{devID: "adopted"}, nil
		})
	}()
	<-entered
	s.Close()
	close(release)

	if err := <-done; err == nil {
		t.Fatal("reconnect() succeeded on closed Session, want error")
	}
	s.mu.RLock()
	devID := s.devID
	s.mu.RUnlock()
	if devID != "" {
		t.Fatalf("closed Session adopted devID %q", devID)
	}
}
