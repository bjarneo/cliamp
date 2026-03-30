//go:build !windows

package spotify

import (
	"context"
	"errors"
	"strings"
	"testing"

	librespotPlayer "github.com/devgianlu/go-librespot/player"
	librespotsession "github.com/devgianlu/go-librespot/session"
	"golang.org/x/oauth2"
)

func stubReconnectSession(devID, accessToken string) *Session {
	return &Session{
		sess:        &librespotsession.Session{},
		player:      &librespotPlayer.Player{},
		devID:       devID,
		tokenSource: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: accessToken}),
	}
}

func TestReconnectPrefersSilentSession(t *testing.T) {
	oldSilent := newSilentSession
	oldInteractive := newInteractiveSessionFn
	t.Cleanup(func() {
		newSilentSession = oldSilent
		newInteractiveSessionFn = oldInteractive
	})

	silentCalls := 0
	interactiveCalls := 0
	newSilentSession = func(context.Context, string) (*Session, error) {
		silentCalls++
		return stubReconnectSession("silent-device", "silent-token"), nil
	}
	newInteractiveSessionFn = func(context.Context, string) (*Session, error) {
		interactiveCalls++
		return nil, errors.New("interactive auth should not run")
	}

	sess := &Session{clientID: "client-id", devID: "old-device"}
	if err := sess.Reconnect(context.Background()); err != nil {
		t.Fatalf("Reconnect() error = %v", err)
	}
	if silentCalls != 1 {
		t.Fatalf("silent reconnect calls = %d, want 1", silentCalls)
	}
	if interactiveCalls != 0 {
		t.Fatalf("interactive reconnect calls = %d, want 0", interactiveCalls)
	}
	if sess.devID != "silent-device" {
		t.Fatalf("devID = %q, want %q", sess.devID, "silent-device")
	}
	tok, err := sess.tokenSource.Token()
	if err != nil {
		t.Fatalf("tokenSource.Token() error = %v", err)
	}
	if tok.AccessToken != "silent-token" {
		t.Fatalf("access token = %q, want %q", tok.AccessToken, "silent-token")
	}
}

func TestReconnectReturnsErrorOnSilentFailure(t *testing.T) {
	oldSilent := newSilentSession
	oldInteractive := newInteractiveSessionFn
	t.Cleanup(func() {
		newSilentSession = oldSilent
		newInteractiveSessionFn = oldInteractive
	})

	silentCalls := 0
	interactiveCalls := 0
	newSilentSession = func(context.Context, string) (*Session, error) {
		silentCalls++
		return nil, errors.New("stored credentials expired")
	}
	newInteractiveSessionFn = func(context.Context, string) (*Session, error) {
		interactiveCalls++
		return stubReconnectSession("interactive-device", "interactive-token"), nil
	}

	sess := &Session{clientID: "client-id", devID: "old-device"}
	err := sess.Reconnect(context.Background())
	if err == nil {
		t.Fatal("Reconnect() error = nil, want non-nil")
	}
	if silentCalls != 1 {
		t.Fatalf("silent reconnect calls = %d, want 1", silentCalls)
	}
	if interactiveCalls != 0 {
		t.Fatalf("interactive reconnect calls = %d, want 0", interactiveCalls)
	}
	if !strings.Contains(err.Error(), "stored credentials expired") {
		t.Fatalf("Reconnect() error = %q, want substring %q", err.Error(), "stored credentials expired")
	}
}

func TestReconnectInteractiveClearsStoredCredentials(t *testing.T) {
	oldDelete := deleteStoredCreds
	oldInteractive := newInteractiveSessionFn
	t.Cleanup(func() {
		deleteStoredCreds = oldDelete
		newInteractiveSessionFn = oldInteractive
	})

	var events []string
	deleteStoredCreds = func() error {
		events = append(events, "delete")
		return nil
	}
	newInteractiveSessionFn = func(context.Context, string) (*Session, error) {
		events = append(events, "interactive")
		return stubReconnectSession("interactive-device", "interactive-token"), nil
	}

	sess := &Session{clientID: "client-id", devID: "old-device"}
	if err := sess.ReconnectInteractive(context.Background()); err != nil {
		t.Fatalf("ReconnectInteractive() error = %v", err)
	}
	if len(events) != 2 || events[0] != "delete" || events[1] != "interactive" {
		t.Fatalf("events = %v, want %v", events, []string{"delete", "interactive"})
	}
	if sess.devID != "interactive-device" {
		t.Fatalf("devID = %q, want %q", sess.devID, "interactive-device")
	}
}
