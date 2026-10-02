package qobuz

import (
	"context"
	"io"
	"net/http"
	"slices"
	"testing"

	"github.com/bjarneo/cliamp/internal/httpclient/httpclienttest"
)

// TestClientUsesEnvironmentProxy checks that API calls and the bundle fetch
// follow ALL_PROXY.
func TestClientUsesEnvironmentProxy(t *testing.T) {
	tests := []struct {
		name      string
		call      func() error
		wantHosts []string
	}{
		{
			name: "api",
			call: func() error {
				c := newClient("app", nil)
				c.baseURL = "http://www.qobuz.invalid/api.json/0.2/"
				return c.doGet(context.Background(), "track/get", nil, nil)
			},
			wantHosts: []string{"www.qobuz.invalid"},
		},
		{
			name: "bundle",
			call: func() error {
				_, err := fetchBundle(context.Background(), "http://play.qobuz.invalid")
				return err
			},
			wantHosts: []string{"play.qobuz.invalid", "play.qobuz.invalid"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proxy := httpclienttest.UseAllProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/login" {
					_, _ = io.WriteString(w, `<script src="/resources/7.1.2-b012/bundle.js"></script>`)
					return
				}
				_, _ = io.WriteString(w, `{}`)
			}))
			if err := tt.call(); err != nil {
				t.Fatal(err)
			}
			if got := proxy.Hosts(); !slices.Equal(got, tt.wantHosts) {
				t.Errorf("proxy hosts = %q, want %q", got, tt.wantHosts)
			}
		})
	}
}
