package plex

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAccountSignIn(t *testing.T) {
	polls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Plex-Client-Identifier"); got != "client-1" {
			t.Errorf("client identifier = %q, want client-1", got)
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v2/pins":
			if got := r.URL.Query().Get("strong"); got != "true" {
				t.Errorf("strong = %q, want true", got)
			}
			w.Write([]byte(`{"id":42,"code":"long-code"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/pins/42":
			polls++
			if got := r.URL.Query().Get("code"); got != "long-code" {
				t.Errorf("code = %q, want long-code", got)
			}
			if polls == 1 {
				w.Write([]byte(`{"authToken":null}`))
			} else {
				w.Write([]byte(`{"authToken":"account-token"}`))
			}
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer srv.Close()

	api := &AccountAPI{baseURL: srv.URL, client: srv.Client(), pollInterval: time.Millisecond}
	session, err := api.BeginSignIn(t.Context(), "client-1")
	if err != nil {
		t.Fatalf("BeginSignIn: %v", err)
	}
	if got := session.URL(); !strings.Contains(got, "clientID=client-1") || !strings.Contains(got, "code=long-code") {
		t.Fatalf("sign-in URL = %q", got)
	}
	token, err := session.Wait(t.Context())
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if token != "account-token" {
		t.Fatalf("token = %q, want account-token", token)
	}
}

func TestAccountServersFiltersAndSorts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/resources" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("X-Plex-Token"); got != "account-token" {
			t.Errorf("token = %q, want account-token", got)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]AccountServer{
			{Name: "Shared", ClientIdentifier: "shared", Provides: "server", Owned: false},
			{Name: "Player", ClientIdentifier: "player", Provides: "player", Owned: true},
			{Name: "Home", ClientIdentifier: "home", Provides: "server,player", Owned: true},
		})
	}))
	defer srv.Close()

	api := &AccountAPI{baseURL: srv.URL, client: srv.Client(), pollInterval: time.Millisecond}
	servers, err := api.Servers(t.Context(), "account-token", "client-1")
	if err != nil {
		t.Fatalf("Servers: %v", err)
	}
	if len(servers) != 2 || servers[0].Name != "Home" || servers[1].Name != "Shared" {
		t.Fatalf("servers = %+v", servers)
	}
}

func TestReachableConnectionPrefersLocal(t *testing.T) {
	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer secure.Close()

	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer local.Close()

	api := &AccountAPI{baseURL: "unused", client: secure.Client(), pollInterval: time.Millisecond}
	server := AccountServer{
		Name:        "Home",
		AccessToken: "server-token",
		Connections: []AccountConnection{
			{URI: local.URL, Protocol: "http", Local: true},
			{URI: secure.URL, Protocol: "https"},
		},
	}
	connection, token, err := api.ReachableConnection(t.Context(), server, "account-token", "client-1")
	if err != nil {
		t.Fatalf("ReachableConnection: %v", err)
	}
	if connection.URI != local.URL || token != "server-token" {
		t.Fatalf("connection = %+v, token = %q", connection, token)
	}
}

func TestReachableConnectionDoesNotWaitForSlowLocalAddress(t *testing.T) {
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer local.Close()

	remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer remote.Close()

	api := &AccountAPI{baseURL: "unused", client: remote.Client(), pollInterval: time.Millisecond}
	started := time.Now()
	connection, _, err := api.ReachableConnection(t.Context(), AccountServer{
		Name: "Home",
		Connections: []AccountConnection{
			{URI: local.URL, Protocol: "http", Local: true},
			{URI: remote.URL, Protocol: "https"},
		},
	}, "account-token", "client-1")
	if err != nil {
		t.Fatalf("ReachableConnection: %v", err)
	}
	if connection.URI != remote.URL {
		t.Fatalf("connection URI = %q, want %q", connection.URI, remote.URL)
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("probe took %s; slow local address blocked remote fallback", elapsed)
	}
}

func TestReachableConnectionFallsBackToRemoteHTTPS(t *testing.T) {
	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Plex-Token"); got != "server-token" {
			t.Errorf("token = %q, want server-token", got)
		}
		if got := r.Header.Get("X-Plex-Client-Identifier"); got != "client-1" {
			t.Errorf("client identifier = %q, want client-1", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer secure.Close()

	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer local.Close()

	api := &AccountAPI{baseURL: "unused", client: secure.Client(), pollInterval: time.Millisecond}
	server := AccountServer{
		Name:        "Home",
		AccessToken: "server-token",
		Connections: []AccountConnection{
			{URI: local.URL, Protocol: "http", Local: true},
			{URI: secure.URL, Protocol: "https"},
		},
	}
	connection, _, err := api.ReachableConnection(t.Context(), server, "account-token", "client-1")
	if err != nil {
		t.Fatalf("ReachableConnection: %v", err)
	}
	if connection.URI != secure.URL {
		t.Fatalf("connection URI = %q, want %q", connection.URI, secure.URL)
	}
}

func TestReachableConnectionRejectsPublicHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	api := &AccountAPI{baseURL: "unused", client: srv.Client(), pollInterval: time.Millisecond}
	_, _, err := api.ReachableConnection(context.Background(), AccountServer{
		Name:        "Unsafe",
		Connections: []AccountConnection{{URI: srv.URL, Protocol: "http"}},
	}, "account-token", "client-1")
	if err == nil || !strings.Contains(err.Error(), "no reachable secure connection") {
		t.Fatalf("error = %v", err)
	}
}

func TestNewClientIdentifier(t *testing.T) {
	a, err := NewClientIdentifier()
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewClientIdentifier()
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 32 || a == b {
		t.Fatalf("identifiers = %q, %q", a, b)
	}
}

func TestAccountSignOut(t *testing.T) {
	tests := []struct {
		name       string
		signout    int
		user       int
		wantErr    bool
		wantStill  bool
		wantChecks int
	}{
		{name: "revoked", signout: http.StatusNoContent, user: http.StatusUnauthorized, wantChecks: 1},
		{name: "already invalid", signout: http.StatusUnauthorized, user: http.StatusUnauthorized, wantChecks: 1},
		{name: "still accepted", signout: http.StatusNoContent, user: http.StatusOK, wantErr: true, wantStill: true, wantChecks: 1},
		{name: "sign-out rejected", signout: http.StatusInternalServerError, wantErr: true},
		{name: "check fails", signout: http.StatusNoContent, user: http.StatusBadGateway, wantErr: true, wantChecks: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checks := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get("X-Plex-Token"); got != "account-token" {
					t.Errorf("token = %q", got)
				}
				if got := r.Header.Get("X-Plex-Client-Identifier"); got != "client-1" {
					t.Errorf("client identifier = %q", got)
				}
				switch {
				case r.Method == http.MethodDelete && r.URL.Path == "/api/v2/users/signout":
					w.WriteHeader(tt.signout)
				case r.Method == http.MethodGet && r.URL.Path == "/api/v2/user":
					checks++
					w.WriteHeader(tt.user)
				default:
					t.Fatalf("unexpected request: %s %s", r.Method, r.URL)
				}
			}))
			defer srv.Close()
			api := &AccountAPI{baseURL: srv.URL, client: srv.Client(), pollInterval: time.Millisecond}

			err := api.SignOut(context.Background(), "account-token", "client-1")
			if (err != nil) != tt.wantErr {
				t.Fatalf("SignOut error = %v, want error %v", err, tt.wantErr)
			}
			if got := errors.Is(err, ErrTokenStillAccepted); got != tt.wantStill {
				t.Fatalf("still accepted = %v, want %v (%v)", got, tt.wantStill, err)
			}
			if checks != tt.wantChecks {
				t.Fatalf("token checks = %d, want %d", checks, tt.wantChecks)
			}
		})
	}
}

func TestAccountSignOutNeedsToken(t *testing.T) {
	api := &AccountAPI{baseURL: "unused", client: http.DefaultClient}
	if err := api.SignOut(context.Background(), " ", "client-1"); err == nil {
		t.Fatal("SignOut accepted an empty token")
	}
}
