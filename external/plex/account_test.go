package plex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAccountSignIn(t *testing.T) {
	var polls atomic.Int32
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
			n := polls.Add(1)
			if got := r.URL.Query().Get("code"); got != "long-code" {
				t.Errorf("code = %q, want long-code", got)
			}
			if n == 1 {
				w.Write([]byte(`{"authToken":null}`))
			} else {
				w.Write([]byte(`{"authToken":"account-token"}`))
			}
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
			http.Error(w, "unexpected request", http.StatusBadRequest)
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
			t.Errorf("path = %q", r.URL.Path)
			http.NotFound(w, r)
			return
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

// identityHandler answers /identity as the server machineID.
func identityHandler(machineID string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"MediaContainer":{"machineIdentifier":%q}}`, machineID)
	}
}

func TestReachableConnectionPrefersLocal(t *testing.T) {
	// The remote route answers only after the local one has, so the test
	// does not depend on which loopback server the scheduler runs first.
	localAnswered := make(chan struct{})
	var once sync.Once
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identityHandler("server-1")(w, r)
		once.Do(func() { close(localAnswered) })
	}))
	defer local.Close()

	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-localAnswered:
		case <-r.Context().Done():
			return
		}
		identityHandler("server-1")(w, r)
	}))
	defer secure.Close()

	api := &AccountAPI{baseURL: "unused", client: secure.Client(), pollInterval: time.Millisecond}
	server := AccountServer{
		Name:             "Home",
		ClientIdentifier: "server-1",
		AccessToken:      "server-token",
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

	remote := httptest.NewTLSServer(identityHandler("server-1"))
	defer remote.Close()

	api := &AccountAPI{baseURL: "unused", client: remote.Client(), pollInterval: time.Millisecond}
	started := time.Now()
	connection, _, err := api.ReachableConnection(t.Context(), AccountServer{
		Name:             "Home",
		ClientIdentifier: "server-1",
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
		if got := r.Header.Get("X-Plex-Client-Identifier"); got != "client-1" {
			t.Errorf("client identifier = %q, want client-1", got)
		}
		identityHandler("server-1")(w, r)
	}))
	defer secure.Close()

	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer local.Close()

	api := &AccountAPI{baseURL: "unused", client: secure.Client(), pollInterval: time.Millisecond}
	server := AccountServer{
		Name:             "Home",
		ClientIdentifier: "server-1",
		AccessToken:      "server-token",
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

// A public plain-HTTP route is never probed, even when the server would
// answer as the right one, and even when its metadata claims https.
func TestReachableConnectionRejectsPublicHTTP(t *testing.T) {
	for _, protocol := range []string{"http", "https"} {
		t.Run(protocol, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Errorf("public HTTP route got a request: %s", r.URL.Path)
				identityHandler("server-1")(w, r)
			}))
			defer srv.Close()

			api := &AccountAPI{baseURL: "unused", client: srv.Client(), pollInterval: time.Millisecond}
			_, _, err := api.ReachableConnection(context.Background(), AccountServer{
				Name:             "Unsafe",
				ClientIdentifier: "server-1",
				Connections:      []AccountConnection{{URI: srv.URL, Protocol: protocol}},
			}, "account-token", "client-1")
			if err == nil || !strings.Contains(err.Error(), "no reachable secure connection") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

// A probe does not follow a redirect to another host: the original address
// has not shown that it is the server.
func TestReachableConnectionRejectsRedirectedIdentity(t *testing.T) {
	target := httptest.NewServer(identityHandler("server-1"))
	defer target.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/identity", http.StatusFound)
	}))
	defer redirector.Close()

	api := &AccountAPI{baseURL: "unused", client: redirector.Client(), pollInterval: time.Millisecond}
	_, _, err := api.ReachableConnection(t.Context(), AccountServer{
		Name:             "Home",
		ClientIdentifier: "server-1",
		Connections:      []AccountConnection{{URI: redirector.URL, Protocol: "http", Local: true}},
	}, "account-token", "client-1")
	if err == nil {
		t.Fatal("a redirected identity was accepted")
	}
}

// The account token never follows a redirect off the plex.tv origin.
func TestAccountKeepsTokenOnOrigin(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("redirect target got a request, token %q", r.Header.Get("X-Plex-Token"))
		w.Write([]byte(`[]`))
	}))
	defer other.Close()
	tv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer tv.Close()

	api := &AccountAPI{baseURL: tv.URL, client: tv.Client(), pollInterval: time.Millisecond}
	if _, err := api.Servers(t.Context(), "account-token", "client-1"); err == nil {
		t.Fatal("Servers: a cross-origin redirect was followed")
	}
	if err := api.SignOut(t.Context(), "account-token", "client-1"); err == nil {
		t.Fatal("SignOut: a cross-origin redirect was followed")
	}
}

// A failed poll while the user approves the PIN does not end the sign-in;
// an HTTP client error such as an expired PIN does.
func TestAccountSignInRetriesTransientPolls(t *testing.T) {
	for _, tc := range []struct {
		name    string
		failure int
		wantErr bool
	}{
		{"server error", http.StatusBadGateway, false},
		{"expired PIN", http.StatusNotFound, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var polls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if polls.Add(1) == 1 {
					w.WriteHeader(tc.failure)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"authToken":"account-token"}`))
			}))
			defer srv.Close()

			api := &AccountAPI{baseURL: srv.URL, client: srv.Client(), pollInterval: time.Millisecond}
			session := &SignInSession{api: api, id: 42, code: "code", clientID: "client-1"}
			token, err := session.Wait(t.Context())
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Wait = %q, want an error", token)
				}
				return
			}
			if err != nil || token != "account-token" {
				t.Fatalf("Wait = %q, %v; want account-token", token, err)
			}
		})
	}
}

// A dropped connection during a poll is retried too.
func TestAccountSignInRetriesNetworkFailure(t *testing.T) {
	var polls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if polls.Add(1) == 1 {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				conn.Close()
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"authToken":"account-token"}`))
	}))
	defer srv.Close()

	api := &AccountAPI{baseURL: srv.URL, client: srv.Client(), pollInterval: time.Millisecond}
	session := &SignInSession{api: api, id: 42, code: "code", clientID: "client-1"}
	if token, err := session.Wait(t.Context()); err != nil || token != "account-token" {
		t.Fatalf("Wait = %q, %v; want account-token", token, err)
	}
}

// TestReachableConnectionChecksServerIdentity keeps the token from a host
// that holds the local address on another network: the probe sends no token,
// and a host that answers as another server is not a route.
func TestReachableConnectionChecksServerIdentity(t *testing.T) {
	probe := func(w http.ResponseWriter, r *http.Request, machineID string) {
		if got := r.Header.Get("X-Plex-Token"); got != "" {
			t.Errorf("probe of %s sent token %q", machineID, got)
		}
		identityHandler(machineID)(w, r)
	}
	stranger := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probe(w, r, "other-server")
	}))
	defer stranger.Close()
	remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probe(w, r, "server-1")
	}))
	defer remote.Close()

	tests := []struct {
		name        string
		connections []AccountConnection
		want        string
	}{
		{"skips a stranger", []AccountConnection{
			{URI: stranger.URL, Protocol: "http", Local: true},
			{URI: remote.URL, Protocol: "https"},
		}, remote.URL},
		{"only a stranger", []AccountConnection{
			{URI: stranger.URL, Protocol: "http", Local: true},
		}, ""},
	}
	api := &AccountAPI{baseURL: "unused", client: remote.Client(), pollInterval: time.Millisecond}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			connection, _, err := api.ReachableConnection(t.Context(), AccountServer{
				Name:             "Home",
				ClientIdentifier: "server-1",
				AccessToken:      "server-token",
				Connections:      tt.connections,
			}, "account-token", "client-1")
			if tt.want == "" {
				if err == nil {
					t.Fatalf("connection = %+v, want an error", connection)
				}
				return
			}
			if err != nil || connection.URI != tt.want {
				t.Fatalf("connection = %+v, err = %v, want %s", connection, err, tt.want)
			}
		})
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
			var checks atomic.Int32
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
					checks.Add(1)
					w.WriteHeader(tt.user)
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
					http.Error(w, "unexpected request", http.StatusBadRequest)
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
			if got := int(checks.Load()); got != tt.wantChecks {
				t.Fatalf("token checks = %d, want %d", got, tt.wantChecks)
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
