package local

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/bjarneo/cliamp/favorites"
	"github.com/bjarneo/cliamp/history"
	"github.com/bjarneo/cliamp/playlist"
)

func TestUpdatePlaylist(t *testing.T) {
	errFn := errors.New("fn failed")
	tests := []struct {
		name      string
		list      string
		fn        func([]playlist.Track) ([]playlist.Track, error)
		wantErr   error    // nil when UpdatePlaylist must succeed
		anyErr    bool     // UpdatePlaylist must fail with any error
		initial   []string // track paths of Mix before the update. nil means /a.mp3 and /b.mp3
		wantPaths []string
		wantSame  bool // the file keeps its bytes
	}{
		{
			name: "fn changes the tracks",
			list: "Mix",
			fn: func(tracks []playlist.Track) ([]playlist.Track, error) {
				tracks[0].DurationSecs = 99
				return slices.Delete(tracks, 1, 2), nil
			},
			wantPaths: []string{"/a.mp3"},
		},
		{
			name: "fn inserts a track between two",
			list: "Mix",
			fn: func(tracks []playlist.Track) ([]playlist.Track, error) {
				return slices.Insert(tracks, 1, playlist.Track{Path: "/c.mp3"}), nil
			},
			wantPaths: []string{"/a.mp3", "/c.mp3", "/b.mp3"},
		},
		{
			name: "fn reports no change",
			list: "Mix",
			fn: func([]playlist.Track) ([]playlist.Track, error) {
				return nil, playlist.ErrPlaylistUnchanged
			},
			wantPaths: []string{"/a.mp3", "/b.mp3"},
			wantSame:  true,
		},
		{
			name: "fn fails",
			list: "Mix",
			fn: func([]playlist.Track) ([]playlist.Track, error) {
				return nil, errFn
			},
			wantErr:   errFn,
			wantPaths: []string{"/a.mp3", "/b.mp3"},
			wantSame:  true,
		},
		{
			name:    "duplicate path, add a track",
			list:    "Mix",
			initial: []string{"/a.mp3", "/b.mp3", "/a.mp3"},
			fn: func(tracks []playlist.Track) ([]playlist.Track, error) {
				return append(tracks, playlist.Track{Path: "/c.mp3"}), nil
			},
			wantPaths: []string{"/a.mp3", "/b.mp3", "/a.mp3", "/c.mp3"},
		},
		{
			name:    "duplicate path, remove the other track",
			list:    "Mix",
			initial: []string{"/a.mp3", "/b.mp3", "/a.mp3"},
			fn: func(tracks []playlist.Track) ([]playlist.Track, error) {
				return slices.Delete(tracks, 1, 2), nil
			},
			wantPaths: []string{"/a.mp3", "/a.mp3"},
		},
		{
			name:    "duplicate path, remove the first copy",
			list:    "Mix",
			initial: []string{"/a.mp3", "/b.mp3", "/a.mp3"},
			fn: func(tracks []playlist.Track) ([]playlist.Track, error) {
				return slices.Delete(tracks, 0, 1), nil
			},
			wantPaths: []string{"/b.mp3", "/a.mp3"},
		},
		{
			name:      "duplicate path, no-op update",
			list:      "Mix",
			initial:   []string{"/a.mp3", "/b.mp3", "/a.mp3"},
			wantPaths: []string{"/a.mp3", "/b.mp3", "/a.mp3"},
			wantSame:  true,
		},
		{name: "missing playlist", list: "Nope", anyErr: true, wantPaths: []string{"/a.mp3", "/b.mp3"}, wantSame: true},
		{name: "favorites", list: favorites.PlaylistName, wantErr: errReservedFavoritesName, wantPaths: []string{"/a.mp3", "/b.mp3"}, wantSame: true},
		{name: "history", list: history.PlaylistName, wantErr: errReservedHistoryName, wantPaths: []string{"/a.mp3", "/b.mp3"}, wantSame: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newTestProvider(t)
			initial := []playlist.Track{{Path: "/a.mp3", Title: "A"}, {Path: "/b.mp3", Title: "B"}}
			if tt.initial != nil {
				initial = nil
				for _, path := range tt.initial {
					initial = append(initial, playlist.Track{Path: path})
				}
			}
			if err := p.SavePlaylist("Mix", initial); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(filepath.Join(p.dir, "Mix.toml"))
			if err != nil {
				t.Fatal(err)
			}
			called := false
			fn := func(tracks []playlist.Track) ([]playlist.Track, error) {
				called = true
				if tt.fn == nil {
					return tracks, nil
				}
				return tt.fn(tracks)
			}

			err = p.UpdatePlaylist(tt.list, fn)
			switch {
			case tt.anyErr && err == nil:
				t.Fatal("UpdatePlaylist succeeded, want an error")
			case !tt.anyErr && !errors.Is(err, tt.wantErr):
				t.Fatalf("UpdatePlaylist error = %v, want %v", err, tt.wantErr)
			}
			if (tt.wantErr == errReservedFavoritesName || tt.wantErr == errReservedHistoryName) && called {
				t.Fatal("fn ran for a virtual playlist")
			}
			after, err := os.ReadFile(filepath.Join(p.dir, "Mix.toml"))
			if err != nil {
				t.Fatal(err)
			}
			if same := string(before) == string(after); same != tt.wantSame {
				t.Fatalf("file unchanged = %v, want %v:\n%s", same, tt.wantSame, after)
			}
			tracks, err := p.Tracks("Mix")
			if err != nil {
				t.Fatal(err)
			}
			if got := paths(tracks); !slices.Equal(got, tt.wantPaths) {
				t.Fatalf("tracks = %v, want %v", got, tt.wantPaths)
			}
		})
	}
}

// fn gets only the explicit tracks, so no directory is scanned while the
// lock is held, and the save keeps the [[dir]] section.
func TestUpdatePlaylistKeepsDirSources(t *testing.T) {
	p := newTestProvider(t)
	music := t.TempDir()
	if err := os.WriteFile(filepath.Join(music, "dir.mp3"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := p.SavePlaylist("Mix", []playlist.Track{{Path: "/a.mp3", Title: "A"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.AddDirSources("Mix", []string{music}); err != nil {
		t.Fatal(err)
	}

	var seen []playlist.Track
	err := p.UpdatePlaylist("Mix", func(tracks []playlist.Track) ([]playlist.Track, error) {
		seen = slices.Clone(tracks)
		return append(tracks, playlist.Track{Path: "/b.mp3", Title: "B"}), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := paths(seen); !slices.Equal(got, []string{"/a.mp3"}) {
		t.Fatalf("fn got %v, want only the explicit track /a.mp3", got)
	}
	dirs, err := p.DirSources("Mix")
	if err != nil || len(dirs) != 1 {
		t.Fatalf("dir sources = %+v, %v, want the one source", dirs, err)
	}
	doc, err := p.loadDocByName("Mix")
	if err != nil {
		t.Fatal(err)
	}
	if got := paths(doc.tracks); !slices.Equal(got, []string{"/a.mp3", "/b.mp3"}) {
		t.Fatalf("explicit tracks = %v, want /a.mp3 and /b.mp3", got)
	}
	tracks, err := p.Tracks("Mix")
	if err != nil {
		t.Fatal(err)
	}
	if got := paths(tracks); !slices.Equal(got, []string{"/a.mp3", filepath.Join(music, "dir.mp3"), "/b.mp3"}) {
		t.Fatalf("tracks = %v, want the directory track between /a.mp3 and /b.mp3", got)
	}
}

// TestDuplicateTrackWrites verifies that every write to an existing playlist
// keeps a track path that the playlist lists more than once.
func TestDuplicateTrackWrites(t *testing.T) {
	a, b, c := "/a.mp3", "/b.mp3", "/c.mp3"
	tests := []struct {
		name      string
		write     func(p *Provider) error
		wantPaths []string
	}{
		{
			name: "AddTracks",
			write: func(p *Provider) error {
				_, _, err := p.AddTracks("Mix", []playlist.Track{{Path: c}})
				return err
			},
			wantPaths: []string{a, b, a, c},
		},
		{
			name:      "RemoveTrack of the other track",
			write:     func(p *Provider) error { return p.RemoveTrack("Mix", 1) },
			wantPaths: []string{a, a},
		},
		{
			name:      "RemoveTrack of the first copy",
			write:     func(p *Provider) error { return p.RemoveTrack("Mix", 0) },
			wantPaths: []string{b, a},
		},
		{
			name:      "RemoveTrack of the second copy",
			write:     func(p *Provider) error { return p.RemoveTrack("Mix", 2) },
			wantPaths: []string{a, b},
		},
		{
			name: "SavePlaylist with the same tracks",
			write: func(p *Provider) error {
				return p.SavePlaylist("Mix", []playlist.Track{{Path: a}, {Path: b}, {Path: a}})
			},
			wantPaths: []string{a, b, a},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newTestProvider(t)
			if err := p.SavePlaylist("Mix", []playlist.Track{{Path: a}, {Path: b}, {Path: a}}); err != nil {
				t.Fatal(err)
			}
			if err := tt.write(p); err != nil {
				t.Fatal(err)
			}
			tracks, err := p.Tracks("Mix")
			if err != nil {
				t.Fatal(err)
			}
			if got := paths(tracks); !slices.Equal(got, tt.wantPaths) {
				t.Fatalf("tracks = %v, want %v", got, tt.wantPaths)
			}
		})
	}
}
