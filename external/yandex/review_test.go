package yandex

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestFullDownloadInfoGuard(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"https://music-resp.yandex.net/dl?sign=x", true},
		{"https://yandex.net/dl", true},
		{"http://music-resp.yandex.net/dl?sign=x", false}, // not https
		{"https://evil.example.com/dl?sign=x", false},     // wrong host
		{"https://evil.example.com/?u=yandex.net", false}, // host trick in query
		{"https://yandex.net.evil.com/dl", false},         // suffix spoof
		{"://broken", false},
	}
	for _, tc := range cases {
		if err := fullDownloadInfoGuard(tc.url); (err == nil) != tc.want {
			t.Errorf("fullDownloadInfoGuard(%q) error = %v, want valid = %v", tc.url, err, tc.want)
		}
	}
}

// TestPlaylistIDPreservesOwner checks that saved playlists keep their owner
// UID in the local ID and that Tracks() requests the owner's playlist, not
// the signed-in user's.
func TestPlaylistIDPreservesOwner(t *testing.T) {
	mux := http.NewServeMux()
	log := &requestLog{}
	mux.HandleFunc("/account/status", func(w http.ResponseWriter, r *http.Request) {
		log.add("/account/status", nil)
		writeResult(w, map[string]any{"account": map[string]any{"uid": 42}})
	})
	mux.HandleFunc("/users/42/playlists/list", func(w http.ResponseWriter, r *http.Request) {
		log.add("/users/42/playlists/list", nil)
		writeResult(w, []map[string]any{
			{
				"kind":       3,
				"title":      "Mine",
				"owner":      map[string]any{"uid": 42},
				"trackCount": 1,
			},
			{
				"kind":       7,
				"title":      "Saved",
				"owner":      map[string]any{"uid": 999},
				"trackCount": 2,
			},
		})
	})
	mux.HandleFunc("/users/", func(w http.ResponseWriter, r *http.Request) {
		log.add(r.URL.Path, nil)
		writeResult(w, []map[string]any{
			{
				"kind":   7,
				"title":  "Saved",
				"owner":  map[string]any{"uid": 999},
				"tracks": []map[string]any{{"id": "77", "title": "T"}},
			},
		})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	p := New("test-token")
	p.api.apiBase = ts.URL

	infos, err := p.Playlists()
	if err != nil {
		t.Fatalf("Playlists error = %v", err)
	}
	var saved *struct{ id string }
	for _, in := range infos {
		if in.Name == "Saved" {
			saved = &struct{ id string }{in.ID}
		}
		if in.Name == "Mine" && in.ID != "pl:42:3" {
			t.Errorf("own playlist ID = %q, want %q", in.ID, "pl:42:3")
		}
	}
	if saved == nil {
		t.Fatal("saved playlist missing from Playlists()")
	}
	if saved.id != "pl:999:7" {
		t.Fatalf("saved playlist ID = %q, want %q (owner must be encoded)", saved.id, "pl:999:7")
	}

	if _, err := p.Tracks("pl:999:7"); err != nil {
		t.Fatalf("Tracks error = %v", err)
	}
	foundOwnerPath := false
	for _, path := range log.paths {
		if strings.HasPrefix(path, "/users/999/playlists") {
			foundOwnerPath = true
		}
		if strings.HasPrefix(path, "/users/42/playlists?") || path == "/users/42/playlists" {
			t.Errorf("Tracks resolved saved playlist against signed-in user: %s", path)
		}
	}
	if !foundOwnerPath {
		t.Error("Tracks did not request the playlist owner's endpoint /users/999/playlists")
	}
}

// TestConcurrentAccess exercises initialization paths from multiple
// goroutines; run with -race to catch unsynchronized userID access.
func TestConcurrentAccess(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/account/status", func(w http.ResponseWriter, r *http.Request) {
		writeResult(w, map[string]any{"account": map[string]any{"uid": 42}})
	})
	mux.HandleFunc("/users/42/playlists/list", func(w http.ResponseWriter, r *http.Request) {
		writeResult(w, []map[string]any{
			{"kind": 3, "title": "Mine", "owner": map[string]any{"uid": 42}, "trackCount": 0},
		})
	})
	mux.HandleFunc("/rotor/session/new", func(w http.ResponseWriter, r *http.Request) {
		writeResult(w, map[string]any{
			"sequence":       []any{},
			"batchId":        "batch-0",
			"radioSessionId": "session-1",
		})
	})
	mux.HandleFunc("/rotor/session/session-1/tracks", func(w http.ResponseWriter, r *http.Request) {
		writeResult(w, map[string]any{"sequence": []any{}, "batchId": "batch-end"})
	})
	mux.HandleFunc("/rotor/session/session-1/feedback", func(w http.ResponseWriter, r *http.Request) {
		writeResult(w, "ok")
	})
	mux.HandleFunc("/play-audio", func(w http.ResponseWriter, r *http.Request) {
		writeResult(w, "ok")
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	p := New("test-token")
	p.api.apiBase = ts.URL

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = p.Playlists()            // initializes userID + playlist cache
			_, _ = p.Tracks(wavePlaylistID) // initializes wave session
			p.Refresh()                     // resets everything mid-flight
		}()
	}
	wg.Wait()
}
