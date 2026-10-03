package cmd

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/favorites"
	"github.com/bjarneo/cliamp/history"
	"github.com/bjarneo/cliamp/playlist"
)

// fakeFFprobe puts an ffprobe script first on PATH. The script appends its
// last argument, the probed path, to the returned file and prints 123.4.
// When copyFrom is not empty, the script first copies that file to copyTo,
// as another writer that changes the playlist during the probes.
func fakeFFprobe(t *testing.T, copyFrom, copyTo string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake ffprobe binary is a shell script")
	}
	dir := t.TempDir()
	logFile := filepath.Join(dir, "probed")
	script := "#!/bin/sh\n"
	if copyFrom != "" {
		script += "cp " + shellQuote(copyFrom) + " " + shellQuote(copyTo) + "\n"
	}
	script += "for last; do :; done\nprintf '%s\\n' \"$last\" >> " + shellQuote(logFile) + "\nprintf '123.4\\n'\n"
	if err := os.WriteFile(filepath.Join(dir, "ffprobe"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake ffprobe: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logFile
}

// TestPlaylistEnrich checks that enrich fills only the fields that are still
// empty after the probes, matched by path. A change that another writer made
// during the probes stays, and a directory-sourced track is not probed or
// written.
func TestPlaylistEnrich(t *testing.T) {
	tests := []struct {
		name       string
		tracks     []playlist.Track // explicit tracks in "mix"
		dirSource  bool             // "mix" also lists a directory with b.mp3
		other      []playlist.Track // tracks that another writer saves during the probes
		want       []playlist.Track // explicit tracks after enrich
		wantProbed []string         // base names of the probed files
	}{
		{
			name:       "fills duration and album from the path",
			tracks:     []playlist.Track{{Path: "/music/Blue Album/a.mp3", Title: "A"}},
			want:       []playlist.Track{{Path: "/music/Blue Album/a.mp3", Title: "A", Album: "Blue Album", DurationSecs: 123}},
			wantProbed: []string{"a.mp3"},
		},
		{
			name:   "keeps a duration that is set",
			tracks: []playlist.Track{{Path: "/music/Blue Album/a.mp3", Title: "A", DurationSecs: 200}},
			want:   []playlist.Track{{Path: "/music/Blue Album/a.mp3", Title: "A", Album: "Blue Album", DurationSecs: 200}},
		},
		{
			name:       "keeps an album that another writer set",
			tracks:     []playlist.Track{{Path: "/music/Blue Album/a.mp3", Title: "A"}},
			other:      []playlist.Track{{Path: "/music/Blue Album/a.mp3", Title: "A", Album: "Other"}},
			want:       []playlist.Track{{Path: "/music/Blue Album/a.mp3", Title: "A", Album: "Other", DurationSecs: 123}},
			wantProbed: []string{"a.mp3"},
		},
		{
			name:   "matches tracks by path after another writer",
			tracks: []playlist.Track{{Path: "/music/X/a.mp3", Title: "A"}, {Path: "/music/Y/c.mp3", Title: "C"}},
			other:  []playlist.Track{{Path: "/music/Z/new.mp3", Title: "New", DurationSecs: 7}, {Path: "/music/Y/c.mp3", Title: "C"}},
			want: []playlist.Track{
				{Path: "/music/Z/new.mp3", Title: "New", DurationSecs: 7},
				{Path: "/music/Y/c.mp3", Title: "C", Album: "Y", DurationSecs: 123},
			},
			wantProbed: []string{"a.mp3", "c.mp3"},
		},
		{
			name:       "skips a directory-sourced track",
			tracks:     []playlist.Track{{Path: "/music/Blue Album/a.mp3", Title: "A"}},
			dirSource:  true,
			want:       []playlist.Track{{Path: "/music/Blue Album/a.mp3", Title: "A", Album: "Blue Album", DurationSecs: 123}},
			wantProbed: []string{"a.mp3"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := setupTestEnv(t)
			p, err := newProvider()
			if err != nil {
				t.Fatal(err)
			}
			if tt.dirSource {
				dir := filepath.Join(home, "dir")
				writeAudioFile(t, filepath.Join(dir, "b.mp3"))
				if err := p.CreateDirPlaylist("mix", []string{dir}); err != nil {
					t.Fatalf("CreateDirPlaylist: %v", err)
				}
				if _, _, err := p.AddTracks("mix", tt.tracks); err != nil {
					t.Fatalf("AddTracks: %v", err)
				}
			} else if err := p.SavePlaylist("mix", tt.tracks); err != nil {
				t.Fatalf("SavePlaylist: %v", err)
			}
			var copyFrom, copyTo string
			if tt.other != nil {
				if err := p.SavePlaylist("other", tt.other); err != nil {
					t.Fatalf("SavePlaylist(other): %v", err)
				}
				dir := filepath.Join(home, ".config", "cliamp", "playlists")
				copyFrom, copyTo = filepath.Join(dir, "other.toml"), filepath.Join(dir, "mix.toml")
			}
			logFile := fakeFFprobe(t, copyFrom, copyTo)

			if _, err := captureStdout(t, func() error { return PlaylistEnrich("mix", "path") }); err != nil {
				t.Fatalf("PlaylistEnrich: %v", err)
			}

			var got []playlist.Track
			if err := p.UpdatePlaylist("mix", func(tracks []playlist.Track) ([]playlist.Track, error) {
				got = slices.Clone(tracks)
				return nil, playlist.ErrPlaylistUnchanged
			}); err != nil {
				t.Fatalf("read mix: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("tracks = %+v, want %+v", got, tt.want)
			}
			for i, w := range tt.want {
				g := got[i]
				if g.Path != w.Path || g.Album != w.Album || g.DurationSecs != w.DurationSecs {
					t.Errorf("track %d = {%s %q %d}, want {%s %q %d}", i, g.Path, g.Album, g.DurationSecs, w.Path, w.Album, w.DurationSecs)
				}
			}
			var probed []string
			if data, err := os.ReadFile(logFile); err == nil {
				for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
					probed = append(probed, filepath.Base(line))
				}
			}
			if !slices.Equal(probed, tt.wantProbed) {
				t.Errorf("probed %q, want %q", probed, tt.wantProbed)
			}
			if tt.dirSource {
				doc, err := p.PlaylistDocument("mix")
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(doc), "b.mp3") {
					t.Errorf("playlist file holds the directory-sourced track:\n%s", doc)
				}
			}
		})
	}
}

// TestPlaylistEnrichVirtualPlaylist checks that enrich of a virtual playlist
// fails before it probes any track.
func TestPlaylistEnrichVirtualPlaylist(t *testing.T) {
	setupTestEnv(t)
	if _, err := favorites.New().Toggle(playlist.Track{Path: "/music/Blue Album/a.mp3", Title: "A"}); err != nil {
		t.Fatalf("Toggle: %v", err)
	}
	logFile := fakeFFprobe(t, "", "")
	for _, name := range []string{favorites.PlaylistName, history.PlaylistName} {
		t.Run(name, func(t *testing.T) {
			if err := PlaylistEnrich(name, "path"); err == nil {
				t.Fatal("PlaylistEnrich() error = nil, want an error")
			}
			if _, err := os.Stat(logFile); err == nil {
				t.Error("PlaylistEnrich probed a track of a virtual playlist")
			}
		})
	}
}
