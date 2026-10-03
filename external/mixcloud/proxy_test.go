package mixcloud

import (
	"context"
	"io"
	"net/http"
	"slices"
	"testing"

	"github.com/bjarneo/cliamp/internal/httpclient/httpclienttest"
)

// TestClientUsesEnvironmentProxy checks that API calls follow ALL_PROXY.
func TestClientUsesEnvironmentProxy(t *testing.T) {
	proxy := httpclienttest.UseAllProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{}`)
	}))
	c := newClient("")
	c.baseURL = "http://api.mixcloud.invalid"
	var out map[string]any
	if err := c.getJSON(context.Background(), "/discover/", nil, &out); err != nil {
		t.Fatalf("getJSON: %v", err)
	}
	if got := proxy.Hosts(); !slices.Equal(got, []string{"api.mixcloud.invalid"}) {
		t.Errorf("proxy hosts = %q, want [api.mixcloud.invalid]", got)
	}
}
