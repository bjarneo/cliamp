package resolve

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/playlist"
)

func TestEnrichSoundCloudTracks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/tracks" {
			q := r.URL.Query()
			ids := q.Get("ids")
			if ids == "101,102" {
				tracks := []scTrackInfo{
					{
						ID:           101,
						Title:        "First Song",
						PermalinkURL: "https://soundcloud.com/artist1/first-song",
						Duration:     120000,
						User: struct {
							Username string `json:"username"`
						}{Username: "Artist One"},
					},
					{
						ID:           102,
						Title:        "Second Song",
						PermalinkURL: "https://soundcloud.com/artist2/second-song",
						Duration:     180000,
						User: struct {
							Username string `json:"username"`
						}{Username: "Artist Two"},
					},
				}
				_ = json.NewEncoder(w).Encode(tracks)
				return
			}
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	// Override client ID
	scClientIDMu.Lock()
	oldID := scClientID
	oldRejectedIDs := scRejectedClientIDs
	scClientID = "test_client_id_1234567890123456"
	scRejectedClientIDs = make(map[string]struct{})
	scClientIDMu.Unlock()
	t.Cleanup(func() {
		scClientIDMu.Lock()
		scClientID = oldID
		scRejectedClientIDs = oldRejectedIDs
		scClientIDMu.Unlock()
	})

	// Override scAPIBase
	oldAPIBase := scAPIBase
	scAPIBase = server.URL
	t.Cleanup(func() {
		scAPIBase = oldAPIBase
	})

	// Override httpClient
	oldClient := httpClient
	httpClient = server.Client()
	t.Cleanup(func() {
		httpClient = oldClient
	})

	tracks := []playlist.Track{
		{
			Path:   "https://api-v2.soundcloud.com/tracks/101",
			Title:  "101",
			Artist: "PlaylistCreator",
		},
		{
			Path:   "https://api-v2.soundcloud.com/tracks/102",
			Title:  "102",
			Artist: "PlaylistCreator",
		},
		{
			Path:   "https://soundcloud.com/regular/track",
			Title:  "Regular Track",
			Artist: "Regular Artist",
		},
	}

	enrichSoundCloudTracks(context.Background(), tracks)

	if got, want := tracks[0].Title, "First Song"; got != want {
		t.Errorf("tracks[0].Title = %q, want %q", got, want)
	}
	if got, want := tracks[0].Artist, "Artist One"; got != want {
		t.Errorf("tracks[0].Artist = %q, want %q", got, want)
	}
	if got, want := tracks[0].Path, "https://soundcloud.com/artist1/first-song"; got != want {
		t.Errorf("tracks[0].Path = %q, want %q", got, want)
	}
	if got, want := tracks[0].DurationSecs, 120; got != want {
		t.Errorf("tracks[0].DurationSecs = %d, want %d", got, want)
	}

	if got, want := tracks[1].Title, "Second Song"; got != want {
		t.Errorf("tracks[1].Title = %q, want %q", got, want)
	}
	if got, want := tracks[1].Artist, "Artist Two"; got != want {
		t.Errorf("tracks[1].Artist = %q, want %q", got, want)
	}

	// Verify regular track unchanged
	if got, want := tracks[2].Title, "Regular Track"; got != want {
		t.Errorf("tracks[2].Title = %q, want %q", got, want)
	}
}

func TestEnrichSoundCloudTracksRefreshesRejectedClientID(t *testing.T) {
	const staleID = "stale_client_id_1234567890123456"
	const freshID = "freshclientid1234567890123456789"
	var apiClientIDs []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			fmt.Fprintf(w, `<script src=%q></script>`, serverURL(r)+"/bundle.js")
		case "/bundle.js":
			fmt.Fprintf(w, `client_id: %q`, freshID)
		case "/tracks":
			apiClientIDs = append(apiClientIDs, r.URL.Query().Get("client_id"))
			if r.URL.Query().Get("client_id") == staleID {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`[{"id":201,"title":"Rejected title"}]`))
				return
			}
			_ = json.NewEncoder(w).Encode([]scTrackInfo{{
				ID:    201,
				Title: "Fresh title",
				User: struct {
					Username string `json:"username"`
				}{Username: "Fresh artist"},
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	oldAPIBase, oldHomepageURL := scAPIBase, scHomepageURL
	scAPIBase, scHomepageURL = server.URL, server.URL+"/"
	t.Cleanup(func() { scAPIBase, scHomepageURL = oldAPIBase, oldHomepageURL })
	oldClient := httpClient
	httpClient = server.Client()
	t.Cleanup(func() { httpClient = oldClient })
	t.Setenv("HOME", t.TempDir())
	cachePath := filepath.Join(os.Getenv("HOME"), ".cache", "yt-dlp", "soundcloud", "client_id.json")
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachePath, []byte(`{"data":"`+staleID+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	scClientIDMu.Lock()
	oldID, oldRejectedIDs := scClientID, scRejectedClientIDs
	scClientID, scRejectedClientIDs = staleID, make(map[string]struct{})
	scClientIDMu.Unlock()
	t.Cleanup(func() {
		scClientIDMu.Lock()
		scClientID, scRejectedClientIDs = oldID, oldRejectedIDs
		scClientIDMu.Unlock()
	})

	tracks := []playlist.Track{{Path: "https://api-v2.soundcloud.com/tracks/201", Title: "201"}}
	enrichSoundCloudTracks(context.Background(), tracks)
	if got, want := tracks[0].Title, "Fresh title"; got != want {
		t.Errorf("track title = %q, want %q", got, want)
	}
	if got, want := tracks[0].Artist, "Fresh artist"; got != want {
		t.Errorf("track artist = %q, want %q", got, want)
	}
	if len(apiClientIDs) != 2 || apiClientIDs[0] != staleID || apiClientIDs[1] != freshID {
		t.Errorf("API client IDs = %v, want [%q %q]", apiClientIDs, staleID, freshID)
	}
}

func serverURL(r *http.Request) string {
	return "http://" + r.Host
}

func TestParseYTDLSoundCloudAPIURL(t *testing.T) {
	input := `{"url":"https://api-v2.soundcloud.com/tracks/101","webpage_url":"https://soundcloud.com/artist/song","title":"101"}`
	tracks, entries, err := parseYTDLTracks(strings.NewReader(input))
	if err != nil {
		t.Fatalf("parseYTDLTracks() error: %v", err)
	}
	if entries != 1 || len(tracks) != 1 {
		t.Fatalf("parseYTDLTracks() = %d entries, %d tracks; want 1 each", entries, len(tracks))
	}
	if got, want := tracks[0].Path, "https://api-v2.soundcloud.com/tracks/101"; got != want {
		t.Errorf("track path = %q, want %q", got, want)
	}
}
