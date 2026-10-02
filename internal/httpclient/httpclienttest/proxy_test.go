package httpclienttest

import (
	"io"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/internal/httpclient"
)

func TestUseAllProxy(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		wantBody string
		wantHost string
	}{
		{name: "plain http reaches the handler", url: "http://api.example.invalid/x", wantBody: "ok", wantHost: "api.example.invalid"},
		{name: "https fails at the tunnel", url: "https://api.example.invalid/x", wantHost: "api.example.invalid:443"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := UseAllProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, "ok")
			}))
			resp, err := httpclient.NewAPI(5 * time.Second).Get(tt.url)
			if tt.wantBody == "" {
				if err == nil {
					_ = resp.Body.Close()
					t.Fatal("Get succeeded, want a tunnel error")
				}
			} else {
				if err != nil {
					t.Fatalf("Get: %v", err)
				}
				body, _ := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				if string(body) != tt.wantBody {
					t.Errorf("body = %q, want %q", body, tt.wantBody)
				}
			}
			if got := p.Hosts(); !slices.Equal(got, []string{tt.wantHost}) {
				t.Errorf("Hosts() = %q, want [%q]", got, tt.wantHost)
			}
		})
	}
}
