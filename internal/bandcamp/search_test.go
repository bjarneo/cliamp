package bandcamp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const searchResponseBody = `{"auto":{"results":[
	{"type":"t","name":"Turbo Killer","band_name":"Carpenter Brut","album_name":"Trilogy","item_url_path":"https://carpenterbrut.bandcamp.com/track/turbo-killer"},
	{"type":"t","name":"Own Domain","band_name":"John","album_name":"Tales","item_url_path":"https://ilistentojohn.com/track/own-domain"},
	{"type":"a","name":"Trilogy","band_name":"Carpenter Brut","item_url_path":"https://carpenterbrut.bandcamp.com/album/trilogy"},
	{"type":"t","name":"Plain HTTP","band_name":"Old","item_url_path":"http://old.bandcamp.com/track/plain"},
	{"type":"t","name":"Roller Mobster","band_name":"Carpenter Brut","album_name":"Trilogy","item_url_path":"https://carpenterbrut.bandcamp.com/track/roller-mobster"},
	{"type":"t","name":"Le Perv","band_name":"Carpenter Brut","album_name":"Trilogy","item_url_path":"https://carpenterbrut.bandcamp.com/track/le-perv"}
]}}`

// serveSearch points searchURL at a test server that runs handler.
func serveSearch(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	orig := searchURL
	searchURL = server.URL
	t.Cleanup(func() { searchURL = orig })
}

func TestSearchTracksSendsTrackSearch(t *testing.T) {
	var got searchRequest
	serveSearch(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("request = %s with Content-Type %q, want a JSON POST", r.Method, r.Header.Get("Content-Type"))
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode request: %v", err)
		}
		_, _ = w.Write([]byte(`{"auto":{"results":[]}}`))
	})

	if _, err := SearchTracks(t.Context(), "  carpenter brut  ", 10); err != nil {
		t.Fatalf("SearchTracks: %v", err)
	}
	if got.SearchText != "carpenter brut" || got.SearchFilter != "t" {
		t.Fatalf("request = %+v, want a trimmed track search", got)
	}
}

func TestSearchTracksKeepsPlayableTrackPages(t *testing.T) {
	serveSearch(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(searchResponseBody))
	})

	tests := []struct {
		limit int
		want  []string
	}{
		{limit: 0, want: []string{"Turbo Killer", "Roller Mobster", "Le Perv"}},
		{limit: 2, want: []string{"Turbo Killer", "Roller Mobster"}},
	}
	for _, tt := range tests {
		tracks, err := SearchTracks(t.Context(), "carpenter", tt.limit)
		if err != nil {
			t.Fatalf("limit %d: %v", tt.limit, err)
		}
		var titles []string
		for _, tr := range tracks {
			titles = append(titles, tr.Title)
		}
		if strings.Join(titles, ", ") != strings.Join(tt.want, ", ") {
			t.Fatalf("limit %d: titles = %q, want %q", tt.limit, titles, tt.want)
		}
	}

	tracks, _ := SearchTracks(t.Context(), "carpenter", 1)
	want := "https://carpenterbrut.bandcamp.com/track/turbo-killer"
	if tr := tracks[0]; tr.Path != want || tr.Artist != "Carpenter Brut" || tr.Album != "Trilogy" || !tr.Stream {
		t.Fatalf("track = %+v, want the page URL %s with artist and album as a stream", tr, want)
	}
}

func TestSearchTracksErrors(t *testing.T) {
	serveSearch(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "blocked", http.StatusForbidden)
	})
	if _, err := SearchTracks(t.Context(), "carpenter", 10); err == nil || !strings.Contains(err.Error(), "HTTP 403") {
		t.Fatalf("err = %v, want the HTTP status", err)
	}

	called := false
	serveSearch(t, func(http.ResponseWriter, *http.Request) { called = true })
	if tracks, err := SearchTracks(t.Context(), "   ", 10); err != nil || tracks != nil || called {
		t.Fatalf("blank query = %v, %v, request sent %v; want nothing", tracks, err, called)
	}
}
