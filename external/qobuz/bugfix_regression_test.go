package qobuz

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bjarneo/cliamp/playlist"
)

func TestQobuzClosePreventsStaleAuthenticateInstall(t *testing.T) {
	orig := signIn
	t.Cleanup(func() { signIn = orig })

	entered := make(chan struct{})
	release := make(chan struct{})
	signIn = func(context.Context) (*client, error) {
		close(entered)
		<-release
		return &client{}, nil
	}

	p := New(6)
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

func TestQobuzSilentAuthPreservesServerErrors(t *testing.T) {
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
	if err := credsFile.Save(&storedCreds{AppID: "123", UserAuthToken: "tok", UserID: "1"}); err != nil {
		t.Fatal(err)
	}
	var status int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"message":"error"}`))
	}))
	defer srv.Close()

	status = http.StatusInternalServerError
	_, err := newClientSilent(context.Background(), srv.URL+"/")
	if err == nil || errors.Is(err, playlist.ErrNeedsAuth) || errors.Is(err, errUnauthorized) {
		t.Fatalf("newClientSilent() on HTTP 500 = %v, want non-auth server error", err)
	}

	status = http.StatusUnauthorized
	_, err = newClientSilent(context.Background(), srv.URL+"/")
	if !errors.Is(err, errUnauthorized) {
		t.Fatalf("newClientSilent() on HTTP 401 = %v, want errUnauthorized", err)
	}
}
