package local

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/bjarneo/cliamp/playlist"
)

func TestMigrateBookmarks(t *testing.T) {
	tests := []struct {
		name      string
		files     map[string]string
		existing  []playlist.Track
		wantAdded int
		wantPaths []string
	}{
		{
			name: "no playlist directory",
		},
		{
			name: "bookmarks from every playlist",
			files: map[string]string{
				"a.toml": "[[track]]\npath = \"/a1.mp3\"\ntitle = \"A1\"\nbookmark = true\n\n[[track]]\npath = \"/a2.mp3\"\ntitle = \"A2\"\n",
				"b.toml": "[[track]]\npath = \"/b1.mp3\"\ntitle = \"B1\"\nartist = \"Band\"\nbookmark = true\n",
			},
			wantAdded: 2,
			wantPaths: []string{"/a1.mp3", "/b1.mp3"},
		},
		{
			name: "legacy favorite key",
			files: map[string]string{
				"old.toml": "[[track]]\npath = \"/old.mp3\"\ntitle = \"Old\"\nfavorite = true\n",
			},
			wantAdded: 1,
			wantPaths: []string{"/old.mp3"},
		},
		{
			name: "same track in two playlists",
			files: map[string]string{
				"a.toml": "[[track]]\npath = \"/same.mp3\"\ntitle = \"Same\"\nbookmark = true\n",
				"b.toml": "[[track]]\npath = \"/same.mp3\"\ntitle = \"Same\"\nbookmark = true\n",
			},
			wantAdded: 1,
			wantPaths: []string{"/same.mp3"},
		},
		{
			name: "keeps existing favorites",
			files: map[string]string{
				"a.toml": "[[track]]\npath = \"/fav.mp3\"\ntitle = \"Bookmark copy\"\nbookmark = true\n\n[[track]]\npath = \"/new.mp3\"\ntitle = \"New\"\nbookmark = true\n",
			},
			existing:  []playlist.Track{{Path: "/fav.mp3", Title: "Favorite"}},
			wantAdded: 1,
			wantPaths: []string{"/fav.mp3", "/new.mp3"},
		},
		{
			name: "no bookmarks",
			files: map[string]string{
				"a.toml": "[[track]]\npath = \"/a.mp3\"\ntitle = \"A\"\n",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newTestProviderWithFavorites(t)
			for name, body := range tt.files {
				if err := os.MkdirAll(p.dir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(p.dir, name), []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			for _, track := range tt.existing {
				if _, err := p.favorites.Toggle(track); err != nil {
					t.Fatal(err)
				}
			}

			added, err := p.MigrateBookmarks()
			if err != nil {
				t.Fatalf("MigrateBookmarks: %v", err)
			}
			if added != tt.wantAdded {
				t.Fatalf("added = %d, want %d", added, tt.wantAdded)
			}
			tracks, err := p.favorites.Tracks()
			if err != nil {
				t.Fatal(err)
			}
			var paths []string
			for _, track := range tracks {
				paths = append(paths, track.Path)
			}
			if !slices.Equal(paths, tt.wantPaths) {
				t.Fatalf("favorites = %v, want %v", paths, tt.wantPaths)
			}
			if len(tt.existing) > 0 && tracks[0].Title != tt.existing[0].Title {
				t.Fatalf("migration replaced an existing favorite: %+v", tracks[0])
			}

			// The playlist files keep the legacy flag byte for byte.
			for name, body := range tt.files {
				got, err := os.ReadFile(filepath.Join(p.dir, name))
				if err != nil {
					t.Fatal(err)
				}
				if string(got) != body {
					t.Fatalf("%s changed:\n%s", name, got)
				}
			}
		})
	}
}

func TestMigrateBookmarksRunsOnce(t *testing.T) {
	p := newTestProviderWithFavorites(t)
	if err := os.MkdirAll(p.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(p.dir, "mix.toml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("[[track]]\npath = \"/a.mp3\"\ntitle = \"A\"\nbookmark = true\n")

	if added, err := p.MigrateBookmarks(); err != nil || added != 1 {
		t.Fatalf("first run = %d, %v; want 1, nil", added, err)
	}
	marker := filepath.Join(filepath.Dir(p.favorites.Path()), bookmarksMigratedFile)
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("marker missing: %v", err)
	}

	// An unfavorite after the migration must stick, and a later bookmark is
	// not copied again.
	if on, err := p.favorites.Toggle(playlist.Track{Path: "/a.mp3"}); err != nil || on {
		t.Fatalf("unfavorite = %v, %v; want false, nil", on, err)
	}
	write("[[track]]\npath = \"/a.mp3\"\ntitle = \"A\"\nbookmark = true\n\n[[track]]\npath = \"/b.mp3\"\ntitle = \"B\"\nbookmark = true\n")
	if added, err := p.MigrateBookmarks(); err != nil || added != 0 {
		t.Fatalf("second run = %d, %v; want 0, nil", added, err)
	}
	if n := p.favorites.Count(); n != 0 {
		t.Fatalf("favorites = %d after second run, want 0", n)
	}

	// A repeated run after a lost marker adds no duplicates.
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	if on, err := p.favorites.Toggle(playlist.Track{Path: "/a.mp3"}); err != nil || !on {
		t.Fatalf("favorite = %v, %v; want true, nil", on, err)
	}
	if added, err := p.MigrateBookmarks(); err != nil || added != 1 {
		t.Fatalf("rerun = %d, %v; want 1, nil", added, err)
	}
	if n := p.favorites.Count(); n != 2 {
		t.Fatalf("favorites = %d after rerun, want 2", n)
	}
}

func TestMigrateBookmarksWithoutStore(t *testing.T) {
	var nilProvider *Provider
	if added, err := nilProvider.MigrateBookmarks(); err != nil || added != 0 {
		t.Fatalf("nil provider = %d, %v", added, err)
	}
	p := &Provider{dir: t.TempDir()}
	if added, err := p.MigrateBookmarks(); err != nil || added != 0 {
		t.Fatalf("provider without store = %d, %v", added, err)
	}
}
