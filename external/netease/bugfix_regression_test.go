package netease

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestRefreshClearsAutoDiscoveredUserID(t *testing.T) {
	var accountCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/nuser/account/get":
			if accountCalls.Add(1) == 1 {
				_, _ = w.Write([]byte(`{"code":200,"account":{"id":101},"profile":{"userId":101,"nickname":"UserA"}}`))
			} else {
				_, _ = w.Write([]byte(`{"code":200,"account":{"id":202},"profile":{"userId":202,"nickname":"UserB"}}`))
			}
		case "/api/user/playlist":
			_, _ = w.Write([]byte(`{"code":200,"playlist":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	p := newWithBase(Config{Enabled: true, CookiesFile: "/unused"}, srv.URL)
	p.cookieHeader = "MUSIC_U=one"
	if _, err := p.Playlists(); err != nil {
		t.Fatalf("first Playlists(): %v", err)
	}
	if p.userID != "101" {
		t.Fatalf("discovered userID = %q, want 101", p.userID)
	}

	p.Refresh()
	if p.userID != "" {
		t.Fatalf("userID after Refresh = %q, want empty", p.userID)
	}
}
