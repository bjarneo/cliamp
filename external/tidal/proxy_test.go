package tidal

import (
	"context"
	"io"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/internal/httpclient/httpclienttest"
)

// TestClientUsesEnvironmentProxy checks that API calls follow ALL_PROXY.
func TestClientUsesEnvironmentProxy(t *testing.T) {
	proxy := httpclienttest.UseAllProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{}`)
	}))
	c := newClient("id", "secret")
	c.baseURL = "http://api.tidal.invalid/"
	c.accessToken = "token"
	c.tokenType = "Bearer"
	c.expiresAt = time.Now().Add(time.Hour)
	if err := c.doGet(context.Background(), "sessions", nil, nil); err != nil {
		t.Fatalf("doGet: %v", err)
	}
	if got := proxy.Hosts(); !slices.Equal(got, []string{"api.tidal.invalid"}) {
		t.Errorf("proxy hosts = %q, want [api.tidal.invalid]", got)
	}
}
