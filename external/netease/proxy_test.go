package netease

import (
	"context"
	"io"
	"net/http"
	"slices"
	"testing"

	"github.com/bjarneo/cliamp/internal/httpclient/httpclienttest"
)

// TestAPIUsesEnvironmentProxy checks that API calls follow ALL_PROXY.
func TestAPIUsesEnvironmentProxy(t *testing.T) {
	proxy := httpclienttest.UseAllProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"code":200}`)
	}))
	p := newWithBase(Config{Enabled: true}, "http://music.netease.invalid")
	var out map[string]any
	if err := p.apiGet(context.Background(), "/api/x", nil, &out); err != nil {
		t.Fatalf("apiGet: %v", err)
	}
	if got := proxy.Hosts(); !slices.Equal(got, []string{"music.netease.invalid"}) {
		t.Errorf("proxy hosts = %q, want [music.netease.invalid]", got)
	}
}
