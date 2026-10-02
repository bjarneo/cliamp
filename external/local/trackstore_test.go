package local

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/favorites"
	"github.com/bjarneo/cliamp/history"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// A track must come back with the same shared fields from every store that
// saves it, so it scrobbles, stars and resumes the same way from Favorites,
// Recently Played and a saved playlist.
func TestTrackSurvivesEveryStore(t *testing.T) {
	full := playlist.Track{
		Path:           "https://nd.example.com/rest/stream?id=42",
		Title:          `Say "Hi"`,
		Artist:         "Artist",
		Album:          "Album",
		Genre:          "Rock",
		Year:           1979,
		TrackNumber:    3,
		Stream:         true,
		Realtime:       true,
		Feed:           true,
		Restricted:     true,
		DurationSecs:   208,
		Bookmark:       true,
		Unplayable:     true,
		DirSourced:     true,
		EmbeddedLyrics: "[00:01.00]Line",
		AlbumArtURL:    "file:///tmp/cover.jpg",
		ProviderMeta: map[string]string{
			provider.MetaNavidromeID:      "42",
			provider.MetaPodcastFeed:      "https://rss.example.com/show",
			provider.MetaPodcastGUID:      "guid-1",
			provider.MetaPodcastPublished: "2026-09-10",
			"radio.name":                  "Station",
		},
	}
	v := reflect.ValueOf(full)
	for i := range v.NumField() {
		if f := v.Type().Field(i); f.IsExported() && v.Field(i).IsZero() {
			t.Fatalf("the test track does not set %s", f.Name)
		}
	}

	shared := playlist.Track{
		Path:         full.Path,
		Title:        full.Title,
		Artist:       full.Artist,
		Album:        full.Album,
		Genre:        full.Genre,
		Year:         full.Year,
		TrackNumber:  full.TrackNumber,
		Stream:       true,
		Realtime:     true,
		Feed:         true,
		Restricted:   true,
		DurationSecs: full.DurationSecs,
		AlbumArtURL:  full.AlbumArtURL,
		ProviderMeta: full.ProviderMeta,
	}
	saved := shared
	saved.EmbeddedLyrics = full.EmbeddedLyrics
	saved.Bookmark = true

	tests := []struct {
		name string
		save func(p *Provider) (string, error)
		want playlist.Track
	}{
		{
			name: "favorites",
			save: func(p *Provider) (string, error) {
				_, err := p.favorites.Toggle(full)
				return favorites.PlaylistName, err
			},
			want: shared,
		},
		{
			name: "history",
			save: func(p *Provider) (string, error) {
				return history.PlaylistName, p.history.Record(full, time.Now())
			},
			want: shared,
		},
		{
			name: "saved playlist",
			save: func(p *Provider) (string, error) {
				_, _, err := p.AddTracks("saved", []playlist.Track{full})
				return "saved", err
			},
			want: saved,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			p := &Provider{
				dir:       filepath.Join(dir, "playlists"),
				history:   history.NewAt(filepath.Join(dir, "history.toml")),
				favorites: favorites.NewAt(filepath.Join(dir, "favorites.toml")),
			}
			name, err := tt.save(p)
			if err != nil {
				t.Fatalf("save: %v", err)
			}
			got, err := p.Tracks(name)
			if err != nil {
				t.Fatalf("Tracks(%q): %v", name, err)
			}
			if len(got) != 1 || !reflect.DeepEqual(got[0], tt.want) {
				t.Fatalf("Tracks(%q):\n got %+v\nwant %+v", name, got, tt.want)
			}
		})
	}
}
