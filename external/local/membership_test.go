package local

import (
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/favorites"
	"github.com/bjarneo/cliamp/playlist"
)

func TestPlaylistMembership(t *testing.T) {
	p := newTestProviderWithFavorites(t)
	music := t.TempDir()
	song := filepath.Join(music, "song.mp3")
	other := filepath.Join(t.TempDir(), "other.mp3")

	if err := p.AddTrack("Explicit", playlist.Track{Path: song}); err != nil {
		t.Fatal(err)
	}
	if err := p.AddTrack("Unrelated", playlist.Track{Path: other}); err != nil {
		t.Fatal(err)
	}
	if err := p.CreateDirPlaylist("Folder", []string{music}); err != nil {
		t.Fatal(err)
	}
	// Explicit entry and directory source together: the explicit entry wins,
	// so the track can be removed.
	if err := p.CreateDirPlaylist("Both", []string{music}); err != nil {
		t.Fatal(err)
	}
	if err := p.AddTrack("Both", playlist.Track{Path: song}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ToggleFavorite(playlist.Track{Path: song}); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		path string
		want map[string]bool
	}{
		{"member everywhere", song, map[string]bool{
			"Explicit": false, "Folder": true, "Both": false, favorites.PlaylistName: false,
		}},
		{"one explicit list", other, map[string]bool{"Unrelated": false}},
		{"not a member", filepath.Join(t.TempDir(), "none.mp3"), map[string]bool{}},
		{"empty path", "", map[string]bool{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := p.PlaylistMembership(tt.path)
			if err != nil {
				t.Fatalf("PlaylistMembership: %v", err)
			}
			if !maps.Equal(got, tt.want) {
				t.Fatalf("PlaylistMembership = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPlaylistMembershipMissingDir(t *testing.T) {
	p := newTestProvider(t)
	p.dir = filepath.Join(p.dir, "missing")
	got, err := p.PlaylistMembership("/a.mp3")
	if err != nil || len(got) != 0 {
		t.Fatalf("PlaylistMembership = %v, %v; want empty, nil", got, err)
	}
}

func TestRemoveTrackByPath(t *testing.T) {
	music := t.TempDir()
	song := filepath.Join(music, "song.mp3")

	tests := []struct {
		name     string
		setup    func(p *Provider)
		playlist string
		wantErr  bool
		wantDoc  []string // substrings the saved file must still contain
		lostPath bool     // the explicit entry must be gone
	}{
		{
			name: "keeps dir sources and other tracks",
			setup: func(p *Provider) {
				p.CreateDirPlaylist("Mix", []string{music})
				p.AddTracks("Mix", []playlist.Track{{Path: "/keep.mp3"}, {Path: song}})
			},
			playlist: "Mix",
			wantDoc:  []string{"[[dir]]", "/keep.mp3"},
			lostPath: true,
		},
		{
			name:     "dir-only member is refused",
			setup:    func(p *Provider) { p.CreateDirPlaylist("Folder", []string{music}) },
			playlist: "Folder",
			wantErr:  true,
		},
		{name: "favorites is reserved", setup: func(*Provider) {}, playlist: favorites.PlaylistName, wantErr: true},
		{name: "missing playlist", setup: func(*Provider) {}, playlist: "Nope", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newTestProvider(t)
			tt.setup(p)
			err := p.RemoveTrackByPath(tt.playlist, song)
			if (err != nil) != tt.wantErr {
				t.Fatalf("RemoveTrackByPath err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			data, err := os.ReadFile(filepath.Join(p.dir, tt.playlist+".toml"))
			if err != nil {
				t.Fatal(err)
			}
			for _, s := range tt.wantDoc {
				if !strings.Contains(string(data), s) {
					t.Errorf("saved playlist lost %q:\n%s", s, data)
				}
			}
			if tt.lostPath && strings.Contains(string(data), `"`+song+`"`) {
				t.Errorf("saved playlist still lists %q:\n%s", song, data)
			}
		})
	}
}
