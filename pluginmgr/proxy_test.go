package pluginmgr

import (
	"io"
	"net/http"
	"slices"
	"testing"

	"github.com/bjarneo/cliamp/internal/httpclient/httpclienttest"
)

// TestDownloadUsesEnvironmentProxy checks that plugin downloads follow
// ALL_PROXY.
func TestDownloadUsesEnvironmentProxy(t *testing.T) {
	proxy := httpclienttest.UseAllProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "-- plugin\n")
	}))
	body, err := download("http://plugins.example.invalid/hello.lua")
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	if string(body) != "-- plugin\n" {
		t.Errorf("body = %q, want the plugin text", body)
	}
	if got := proxy.Hosts(); !slices.Equal(got, []string{"plugins.example.invalid"}) {
		t.Errorf("proxy hosts = %q, want [plugins.example.invalid]", got)
	}
}
