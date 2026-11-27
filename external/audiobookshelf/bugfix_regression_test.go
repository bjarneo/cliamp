package audiobookshelf

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestTracksInvalidatesCacheAfterTokenRotation(t *testing.T) {
	var logins atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/login" {
			n := logins.Add(1)
			tok := "tok-1"
			if n > 1 {
				tok = "tok-2"
			}
			_, _ = w.Write([]byte(`{"user":{"token":"` + tok + `"}}`))
			return
		}
		if r.URL.Path == "/api/items/book1" {
			_, _ = w.Write([]byte(`{"id":"book1","mediaType":"book","media":{"metadata":{"title":"Book"},"duration":60,"tracks":[{"index":1,"ino":"f1","title":"Ch1","duration":60}]}}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "", "alice", "secret", nil)
	c.SetHTTPClient(srv.Client())
	p := newProvider(c)

	tracks1, err := p.Tracks(prefixBook + "book1")
	if err != nil {
		t.Fatalf("first Tracks(): %v", err)
	}
	if len(tracks1) != 1 || !strings.Contains(tracks1[0].Path, "token=tok-1") {
		t.Fatalf("first track path = %q, want token=tok-1", tracks1[0].Path)
	}

	// Simulate password token expiry and rotation.
	c.clearTokenIf("tok-1")

	tracks2, err := p.Tracks(prefixBook + "book1")
	if err != nil {
		t.Fatalf("second Tracks(): %v", err)
	}
	if len(tracks2) != 1 || !strings.Contains(tracks2[0].Path, "token=tok-2") {
		t.Fatalf("second track path = %q, want rotated token=tok-2", tracks2[0].Path)
	}
}
