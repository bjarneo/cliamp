package tidal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bjarneo/cliamp/provider"
)

func TestBrowseEntriesExposeFavoriteArtistsAndAlbums(t *testing.T) {
	entries := New("lossless", "", "").BrowseEntries()
	if len(entries) != 2 {
		t.Fatalf("browse entries = %d, want 2", len(entries))
	}
	if entries[0].ID != browseArtistsID || entries[0].Name != "Favorite Artists" || entries[0].Mode != provider.BrowseArtistAlbums {
		t.Fatalf("artists entry = %+v", entries[0])
	}
	if entries[1].ID != browseAlbumsID || entries[1].Name != "Favorite Albums" || entries[1].Mode != provider.BrowseAlbums {
		t.Fatalf("albums entry = %+v", entries[1])
	}
	for _, entry := range entries {
		if entry.Section != "Library" || entry.AfterID != favoriteTracksID || entry.AfterSection != "Library" {
			t.Fatalf("browse entry placement = %+v, want Library section after Favorite Tracks", entry)
		}
		if entry.OpenInPlaylist {
			t.Fatalf("browse entry %q must open the browser track screen, not the playlist", entry.ID)
		}
	}
}

func TestNewNormalizesQualityAndCredentials(t *testing.T) {
	tests := []struct {
		name         string
		quality      string
		clientID     string
		clientSecret string
		wantQuality  string
		wantID       string
		wantSecret   string
	}{
		{
			name:        "defaults",
			wantQuality: qualityLossless,
			wantID:      fallbackClientID,
			wantSecret:  fallbackClientSecret,
		},
		{
			name:        "invalid quality falls back to lossless",
			quality:     "ultra",
			wantQuality: qualityLossless,
			wantID:      fallbackClientID,
			wantSecret:  fallbackClientSecret,
		},
		{
			name:         "custom credentials",
			quality:      "hires",
			clientID:     "my-id",
			clientSecret: "my-secret",
			wantQuality:  qualityHiRes,
			wantID:       "my-id",
			wantSecret:   "my-secret",
		},
		{
			name:        "custom id without secret uses id as secret",
			quality:     "high",
			clientID:    "solo-id",
			wantQuality: qualityHigh,
			wantID:      "solo-id",
			wantSecret:  "solo-id",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := New(tt.quality, tt.clientID, tt.clientSecret)
			if p.quality != tt.wantQuality {
				t.Errorf("quality = %q, want %q", p.quality, tt.wantQuality)
			}
			if p.clientID != tt.wantID {
				t.Errorf("clientID = %q, want %q", p.clientID, tt.wantID)
			}
			if p.clientSecret != tt.wantSecret {
				t.Errorf("clientSecret = %q, want %q", p.clientSecret, tt.wantSecret)
			}
		})
	}
}

func TestTrackArtist(t *testing.T) {
	album := &apiAlbum{Artist: apiArtist{Name: "Album Artist"}}
	tests := []struct {
		name  string
		track apiTrack
		album *apiAlbum
		want  string
	}{
		{
			name:  "main artist",
			track: apiTrack{Artist: apiArtist{Name: "Main"}, Artists: []apiArtist{{Name: "First"}}},
			want:  "Main",
		},
		{
			name:  "first of artists list",
			track: apiTrack{Artists: []apiArtist{{Name: "First"}, {Name: "Second"}}},
			want:  "First",
		},
		{
			name:  "album artist fallback",
			track: apiTrack{},
			album: album,
			want:  "Album Artist",
		},
		{
			name:  "no artist anywhere",
			track: apiTrack{},
			want:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := trackArtist(tt.track, tt.album); got != tt.want {
				t.Errorf("trackArtist = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAlbumInfo(t *testing.T) {
	a := apiAlbum{
		ID:             json.Number("123"),
		Title:          "Album",
		NumberOfTracks: 12,
		ReleaseDate:    "2021-01-02",
		Artist:         apiArtist{ID: json.Number("7"), Name: "Artist"},
	}
	got := albumInfo(a)
	if got.ID != "123" || got.Name != "Album" || got.Artist != "Artist" ||
		got.ArtistID != "7" || got.Year != 2021 || got.TrackCount != 12 {
		t.Errorf("albumInfo = %+v", got)
	}
}

func TestAlbumPlaceholder(t *testing.T) {
	a := apiAlbum{
		ID:          json.Number("123"),
		Title:       "Album",
		ReleaseDate: "2021-01-02",
		Artist:      apiArtist{ID: json.Number("7"), Name: "Artist"},
	}
	tr := albumPlaceholder(a)
	if tr.Path != "tidal://album/123" {
		t.Errorf("Path = %q", tr.Path)
	}
	if tr.Title != "Album" || tr.Album != "Album" || tr.Artist != "Artist" || tr.Year != 2021 {
		t.Errorf("albumPlaceholder = %+v", tr)
	}
	if !tr.IsAlbum() {
		t.Error("IsAlbum() = false, want true")
	}
	if tr.AlbumID() != "123" {
		t.Errorf("AlbumID() = %q, want 123", tr.AlbumID())
	}
}

func TestSearchTracksAlbumsFirst(t *testing.T) {
	trackItems := `{"items": [{"id": 9, "title": "T", "allowStreaming": true, "streamReady": true, "artist": {"name": "X"}}]}`
	albumItems := `{"items": [{"id": 1, "title": "A", "artist": {"id": 2, "name": "Artist"}}]}`

	t.Run("albums lead the results", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/search/tracks":
				_, _ = w.Write([]byte(trackItems))
			case "/search/albums":
				_, _ = w.Write([]byte(albumItems))
			default:
				t.Errorf("unexpected path %q", r.URL.Path)
				http.NotFound(w, r)
			}
		}))
		defer srv.Close()

		p := New("lossless", "", "")
		p.client = testClient(srv)
		got, err := p.SearchTracks(context.Background(), "q", 10)
		if err != nil {
			t.Fatalf("SearchTracks: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("got %d results, want 2", len(got))
		}
		if !got[0].IsAlbum() || got[0].Title != "A" {
			t.Errorf("result[0] = %+v, want album placeholder A", got[0])
		}
		if got[1].IsAlbum() || got[1].Title != "T" {
			t.Errorf("result[1] = %+v, want track T", got[1])
		}
	})

	t.Run("album search failure degrades to tracks only", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/search/tracks":
				_, _ = w.Write([]byte(trackItems))
			case "/search/albums":
				http.Error(w, "boom", http.StatusInternalServerError)
			default:
				t.Errorf("unexpected path %q", r.URL.Path)
				http.NotFound(w, r)
			}
		}))
		defer srv.Close()

		p := New("lossless", "", "")
		p.client = testClient(srv)
		got, err := p.SearchTracks(context.Background(), "q", 10)
		if err != nil {
			t.Fatalf("SearchTracks: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("got %d results, want 1", len(got))
		}
		if got[0].IsAlbum() || got[0].Title != "T" {
			t.Errorf("result[0] = %+v, want track T", got[0])
		}
	})
}

func TestTrackFromAPI(t *testing.T) {
	tr := trackFromAPI(apiTrack{
		ID:             json.Number("42"),
		Title:          "Song",
		Duration:       200,
		AllowStreaming: true,
		StreamReady:    true,
		Artist:         apiArtist{Name: "Artist"},
	}, nil)
	if tr.Path != "tidal://track/42" {
		t.Errorf("Path = %q", tr.Path)
	}
	if !tr.Stream || tr.Unplayable {
		t.Errorf("Stream=%v Unplayable=%v", tr.Stream, tr.Unplayable)
	}
	if tr.ProviderMeta["tidal.id"] != "42" {
		t.Errorf("meta = %v", tr.ProviderMeta)
	}

	blocked := trackFromAPI(apiTrack{ID: json.Number("7"), StreamReady: true}, nil)
	if !blocked.Unplayable {
		t.Error("allowStreaming=false must be unplayable")
	}
}

func TestNoteDowngradeOncePerSession(t *testing.T) {
	p := New("lossless", "", "")
	p.noteDowngrade(qualityHigh) // should latch
	if !p.downgradeNoticed.Load() {
		t.Fatal("downgrade not latched")
	}

	aac := New("high", "", "")
	aac.noteDowngrade(qualityHigh) // AAC requested: not a downgrade
	if aac.downgradeNoticed.Load() {
		t.Error("AAC quality setting must not warn")
	}

	flac := New("hires", "", "")
	flac.noteDowngrade(qualityHiRes) // FLAC delivered: no warning
	if flac.downgradeNoticed.Load() {
		t.Error("delivered FLAC must not warn")
	}
}
