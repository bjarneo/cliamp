package plex

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewClientTrimsTrailingSlashAndEscapesIDs(t *testing.T) {
	var gotPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.URL.EscapedPath())
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"MediaContainer":{"Metadata":[]}}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL+"///", "tok")
	if _, err := c.Albums("sec/1"); err != nil {
		t.Fatalf("Albums(): %v", err)
	}
	if _, err := c.PlaylistTracks("pl/2"); err != nil {
		t.Fatalf("PlaylistTracks(): %v", err)
	}
	if _, err := c.Tracks("alb/3"); err != nil {
		t.Fatalf("Tracks(): %v", err)
	}

	want := []string{
		"/library/sections/sec%2F1/all",
		"/playlists/pl%2F2/items",
		"/library/metadata/alb%2F3/children",
	}
	if len(gotPaths) != len(want) {
		t.Fatalf("paths = %v, want %v", gotPaths, want)
	}
	for i := range want {
		if gotPaths[i] != want[i] {
			t.Errorf("path[%d] = %q, want %q", i, gotPaths[i], want[i])
		}
	}
}
