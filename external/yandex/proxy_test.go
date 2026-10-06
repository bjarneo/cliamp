package yandex

import (
	"context"
	"io"
	"net/http"
	"slices"
	"testing"

	"github.com/bjarneo/cliamp/internal/httpclient/httpclienttest"
)

// TestClientUsesEnvironmentProxy checks that the API client and the download
// info client follow ALL_PROXY. The test proxy refuses https tunnels, so the
// download info call fails after it reaches the proxy.
func TestClientUsesEnvironmentProxy(t *testing.T) {
	tests := []struct {
		name     string
		call     func(c *client) error
		wantHost string
	}{
		{
			name: "api",
			call: func(c *client) error {
				var out map[string]any
				return c.apiGet(context.Background(), "/account/status", nil, &out)
			},
			wantHost: "api.yandex.invalid",
		},
		{
			name: "download info",
			call: func(c *client) error {
				_, err := c.fullDownloadInfo(context.Background(), "https://storage.mds.yandex.net/download-info/1?sign=x")
				if err == nil {
					t.Error("fullDownloadInfo succeeded through a proxy that opens no tunnels")
				}
				return nil
			},
			wantHost: "storage.mds.yandex.net:443",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proxy := httpclienttest.UseAllProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, `{"result":{}}`)
			}))
			c := newClient("token")
			c.apiBase = "http://api.yandex.invalid"
			if err := tt.call(c); err != nil {
				t.Fatal(err)
			}
			if got := proxy.Hosts(); !slices.Equal(got, []string{tt.wantHost}) {
				t.Errorf("proxy hosts = %q, want [%s]", got, tt.wantHost)
			}
		})
	}
}
