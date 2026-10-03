package cmd

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/favorites"
	"github.com/bjarneo/cliamp/playlist"
)

// captureStdout runs fn with os.Stdout redirected to a buffer and returns what
// was written.
func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	old := os.Stdout
	os.Stdout = w

	done := make(chan struct{})
	var buf bytes.Buffer
	go func() {
		_, _ = io.Copy(&buf, r)
		close(done)
	}()

	runErr := fn()
	w.Close()
	<-done
	os.Stdout = old
	return buf.String(), runErr
}

func writeAudioFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func setupTestEnv(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

func TestPlaylistListEmpty(t *testing.T) {
	setupTestEnv(t)

	out, err := captureStdout(t, PlaylistList)
	if err != nil {
		t.Fatalf("PlaylistList: %v", err)
	}
	// Favorites always appears as a virtual playlist even when empty.
	if !strings.Contains(out, "Favorites") {
		t.Errorf("output = %q, want Favorites virtual playlist to appear", out)
	}
}

func TestPlaylistCreateAndList(t *testing.T) {
	home := setupTestEnv(t)
	audioDir := filepath.Join(home, "music")
	writeAudioFile(t, filepath.Join(audioDir, "song1.mp3"))
	writeAudioFile(t, filepath.Join(audioDir, "song2.flac"))

	// Create
	out, err := captureStdout(t, func() error {
		return PlaylistCreate("mymix", []string{audioDir}, "", nil)
	})
	if err != nil {
		t.Fatalf("PlaylistCreate: %v", err)
	}
	if !strings.Contains(out, "Created playlist") {
		t.Errorf("create output = %q, want 'Created playlist'", out)
	}

	// List
	out, err = captureStdout(t, PlaylistList)
	if err != nil {
		t.Fatalf("PlaylistList: %v", err)
	}
	if !strings.Contains(out, "mymix") {
		t.Errorf("list output = %q, want to mention 'mymix'", out)
	}
	if !strings.Contains(out, "2 tracks") {
		t.Errorf("list output = %q, want '2 tracks'", out)
	}
}

func TestPlaylistCreateNoAudio(t *testing.T) {
	home := setupTestEnv(t)
	emptyDir := filepath.Join(home, "empty")
	if err := os.MkdirAll(emptyDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	err := PlaylistCreate("nothing", []string{emptyDir}, "", nil)
	if err == nil {
		t.Fatal("PlaylistCreate with no audio should error")
	}
	if !strings.Contains(err.Error(), "no audio") {
		t.Errorf("error = %q, want to mention 'no audio'", err.Error())
	}
}

func TestPlaylistCreateEmpty(t *testing.T) {
	setupTestEnv(t)

	out, err := captureStdout(t, func() error {
		return PlaylistCreate("empty", nil, "", nil)
	})
	if err != nil {
		t.Fatalf("PlaylistCreate empty: %v", err)
	}
	if !strings.Contains(out, "Created empty playlist") {
		t.Fatalf("output = %q, want empty playlist confirmation", out)
	}
	out, err = captureStdout(t, func() error { return PlaylistShow("empty", false) })
	if err != nil {
		t.Fatalf("PlaylistShow empty: %v", err)
	}
	if !strings.Contains(out, "is empty") {
		t.Fatalf("show output = %q, want empty message", out)
	}
}

func TestPlaylistCreateDuplicate(t *testing.T) {
	home := setupTestEnv(t)
	audio := filepath.Join(home, "a.mp3")
	writeAudioFile(t, audio)

	if err := PlaylistCreate("dup", []string{audio}, "", nil); err != nil {
		t.Fatalf("first PlaylistCreate: %v", err)
	}
	err := PlaylistCreate("dup", []string{audio}, "", nil)
	if err == nil {
		t.Error("duplicate create should error")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error = %q, want to mention 'already exists'", err.Error())
	}
}

func TestPlaylistAddAppends(t *testing.T) {
	home := setupTestEnv(t)
	a := filepath.Join(home, "a.mp3")
	b := filepath.Join(home, "b.mp3")
	writeAudioFile(t, a)
	writeAudioFile(t, b)

	if err := PlaylistCreate("mix", []string{a}, "", nil); err != nil {
		t.Fatalf("PlaylistCreate: %v", err)
	}

	if err := PlaylistAdd("mix", []string{b}, nil); err != nil {
		t.Fatalf("PlaylistAdd: %v", err)
	}

	out, _ := captureStdout(t, func() error { return PlaylistShow("mix", false) })
	if !strings.Contains(out, "2 tracks") {
		t.Errorf("Show output = %q, want '2 tracks' after add", out)
	}
}

func TestPlaylistAddSkipsDuplicates(t *testing.T) {
	home := setupTestEnv(t)
	a := filepath.Join(home, "a.mp3")
	writeAudioFile(t, a)

	if err := PlaylistCreate("mix", []string{a}, "", nil); err != nil {
		t.Fatalf("PlaylistCreate: %v", err)
	}
	out, err := captureStdout(t, func() error { return PlaylistAdd("mix", []string{a}, nil) })
	if err != nil {
		t.Fatalf("PlaylistAdd duplicate: %v", err)
	}
	if !strings.Contains(out, "0 tracks") || !strings.Contains(out, "1 duplicate skipped") {
		t.Fatalf("output = %q, want duplicate skip count", out)
	}
}

func TestPlaylistAddNonExistent(t *testing.T) {
	home := setupTestEnv(t)
	a := filepath.Join(home, "a.mp3")
	writeAudioFile(t, a)

	err := PlaylistAdd("ghost", []string{a}, nil)
	if err == nil {
		t.Error("PlaylistAdd on non-existent playlist should error")
	}
}

func TestPlaylistShowEmpty(t *testing.T) {
	setupTestEnv(t)
	err := PlaylistShow("ghost", false)
	if err == nil {
		t.Error("PlaylistShow of missing playlist should error")
	}
}

func TestPlaylistShowJSON(t *testing.T) {
	home := setupTestEnv(t)
	audio := filepath.Join(home, "a.mp3")
	writeAudioFile(t, audio)
	if err := PlaylistCreate("mix", []string{audio}, "", nil); err != nil {
		t.Fatalf("PlaylistCreate: %v", err)
	}

	out, err := captureStdout(t, func() error { return PlaylistShow("mix", true) })
	if err != nil {
		t.Fatalf("PlaylistShow JSON: %v", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(out), "[") {
		t.Errorf("JSON output should start with '[': %s", out)
	}
	if !strings.Contains(out, "\"path\"") {
		t.Errorf("JSON output should contain 'path' key: %s", out)
	}
}

func TestPlaylistRemove(t *testing.T) {
	home := setupTestEnv(t)
	a := filepath.Join(home, "a.mp3")
	b := filepath.Join(home, "b.mp3")
	writeAudioFile(t, a)
	writeAudioFile(t, b)
	if err := PlaylistCreate("mix", []string{a, b}, "", nil); err != nil {
		t.Fatalf("PlaylistCreate: %v", err)
	}

	if err := PlaylistRemove("mix", 1); err != nil {
		t.Fatalf("PlaylistRemove: %v", err)
	}

	out, _ := captureStdout(t, func() error { return PlaylistShow("mix", false) })
	if !strings.Contains(out, "1 tracks") {
		t.Errorf("output = %q, want '1 tracks' after remove", out)
	}
}

func TestPlaylistRemoveOutOfRange(t *testing.T) {
	home := setupTestEnv(t)
	a := filepath.Join(home, "a.mp3")
	writeAudioFile(t, a)
	if err := PlaylistCreate("mix", []string{a}, "", nil); err != nil {
		t.Fatalf("PlaylistCreate: %v", err)
	}

	err := PlaylistRemove("mix", 999)
	if err == nil {
		t.Error("PlaylistRemove with out-of-range index should error")
	}
}

func TestPlaylistDelete(t *testing.T) {
	home := setupTestEnv(t)
	audio := filepath.Join(home, "a.mp3")
	writeAudioFile(t, audio)
	if err := PlaylistCreate("todelete", []string{audio}, "", nil); err != nil {
		t.Fatalf("PlaylistCreate: %v", err)
	}

	if err := PlaylistDelete("todelete"); err != nil {
		t.Fatalf("PlaylistDelete: %v", err)
	}

	out, _ := captureStdout(t, PlaylistList)
	if strings.Contains(out, "todelete") {
		t.Errorf("after delete, List output still contains 'todelete': %s", out)
	}
}

func TestPlaylistDeleteMissing(t *testing.T) {
	setupTestEnv(t)
	err := PlaylistDelete("ghost")
	if err == nil {
		t.Error("PlaylistDelete on missing playlist should error")
	}
}

func TestPlaylistRename(t *testing.T) {
	home := setupTestEnv(t)
	a := filepath.Join(home, "a.mp3")
	writeAudioFile(t, a)
	if err := PlaylistCreate("old", []string{a}, "", nil); err != nil {
		t.Fatalf("PlaylistCreate: %v", err)
	}
	if err := PlaylistRename("old", "new"); err != nil {
		t.Fatalf("PlaylistRename: %v", err)
	}
	if err := PlaylistShow("new", false); err != nil {
		t.Fatalf("renamed playlist missing: %v", err)
	}
	if err := PlaylistShow("old", false); err == nil {
		t.Fatal("old playlist should no longer exist")
	}
}

func TestPlaylistDedupeAndSort(t *testing.T) {
	home := setupTestEnv(t)
	a := filepath.Join(home, "a.mp3")
	b := filepath.Join(home, "b.mp3")
	writeAudioFile(t, a)
	writeAudioFile(t, b)
	if err := PlaylistCreate("mix", nil, "", nil); err != nil {
		t.Fatalf("PlaylistCreate empty: %v", err)
	}
	p, err := newProvider()
	if err != nil {
		t.Fatal(err)
	}
	if err := p.SavePlaylist("mix", []playlist.Track{{Path: b, Title: "B"}, {Path: a, Title: "A"}, {Path: a, Title: "A dup"}}); err != nil {
		t.Fatalf("SavePlaylist: %v", err)
	}
	if err := PlaylistDedupe("mix"); err != nil {
		t.Fatalf("PlaylistDedupe: %v", err)
	}
	if err := PlaylistSort("mix", "title"); err != nil {
		t.Fatalf("PlaylistSort: %v", err)
	}
	tracks, err := p.Tracks("mix")
	if err != nil {
		t.Fatalf("Tracks: %v", err)
	}
	if len(tracks) != 2 || tracks[0].Title != "A" || tracks[1].Title != "B" {
		t.Fatalf("tracks after dedupe/sort = %+v", tracks)
	}
}

// The write commands change a playlist in one locked update. A command
// that finds nothing to change, or that fails, leaves the file as it is.
func TestPlaylistWriteCommandsLeaveTheFileAlone(t *testing.T) {
	tests := []struct {
		name    string
		run     func() error
		wantErr bool
	}{
		{name: "dedupe without duplicates", run: func() error { return PlaylistDedupe("mix") }},
		{name: "sort by an unknown key", run: func() error { return PlaylistSort("mix", "color") }, wantErr: true},
		{name: "dedupe of Recently Played", run: func() error { return PlaylistDedupe("Recently Played") }, wantErr: true},
		{name: "sort of Favorites", run: func() error { return PlaylistSort("Favorites", "title") }, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := setupTestEnv(t)
			p, err := newProvider()
			if err != nil {
				t.Fatal(err)
			}
			if err := p.SavePlaylist("mix", []playlist.Track{{Path: filepath.Join(home, "b.mp3"), Title: "B"}, {Path: filepath.Join(home, "a.mp3"), Title: "A"}}); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(home, ".config", "cliamp", "playlists")
			before, err := os.ReadFile(filepath.Join(dir, "mix.toml"))
			if err != nil {
				t.Fatal(err)
			}

			if err := tt.run(); (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, want an error %v", err, tt.wantErr)
			}
			after, err := os.ReadFile(filepath.Join(dir, "mix.toml"))
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != string(before) {
				t.Fatalf("mix.toml changed:\n%s", after)
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 {
				t.Fatalf("playlist files = %v, want only mix.toml", entries)
			}
		})
	}
}

// TestPlaylistDoctorFixPrunesMissing checks that --fix prunes missing files
// from playlist files. It reports a missing favorite, but the virtual
// Favorites playlist does not stop the prune of the other playlists.
func TestPlaylistDoctorFixPrunesMissing(t *testing.T) {
	tests := []struct {
		name            string
		doctor          string // playlist name for doctor; "" checks all
		missingFavorite bool
		wantErr         bool
	}{
		{name: "one playlist", doctor: "mix"},
		{name: "all playlists", doctor: ""},
		{name: "all playlists with a missing favorite", doctor: "", missingFavorite: true},
		{name: "favorites by name", doctor: favorites.PlaylistName, missingFavorite: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := setupTestEnv(t)
			a := filepath.Join(home, "a.mp3")
			missing := filepath.Join(home, "missing.mp3")
			writeAudioFile(t, a)
			p, err := newProvider()
			if err != nil {
				t.Fatal(err)
			}
			if err := p.SavePlaylist("mix", []playlist.Track{{Path: a, Title: "A"}, {Path: missing, Title: "Missing"}}); err != nil {
				t.Fatalf("SavePlaylist: %v", err)
			}
			if tt.missingFavorite {
				if _, err := favorites.New().Toggle(playlist.Track{Path: missing, Title: "Missing"}); err != nil {
					t.Fatalf("Toggle: %v", err)
				}
			}
			out, err := captureStdout(t, func() error { return PlaylistDoctor(tt.doctor, true) })
			if err != nil {
				t.Fatalf("PlaylistDoctor: %v", err)
			}
			wantMix := 1
			if tt.doctor == favorites.PlaylistName {
				wantMix = 2
			}
			tracks, err := p.Tracks("mix")
			if err != nil {
				t.Fatalf("Tracks: %v", err)
			}
			if len(tracks) != wantMix || tracks[0].Path != a {
				t.Fatalf("mix after doctor = %+v, want %d tracks", tracks, wantMix)
			}
			if !tt.missingFavorite {
				return
			}
			if !strings.Contains(out, "[Favorites] missing: "+missing) {
				t.Errorf("output = %q, want the missing favorite", out)
			}
			favs, err := p.Tracks(favorites.PlaylistName)
			if err != nil {
				t.Fatalf("Tracks(Favorites): %v", err)
			}
			if len(favs) != 1 {
				t.Errorf("favorites after doctor = %+v, want the missing favorite kept", favs)
			}
		})
	}
}

func TestPlaylistImportExportM3U(t *testing.T) {
	home := setupTestEnv(t)
	a := filepath.Join(home, "a.mp3")
	writeAudioFile(t, a)
	m3u := filepath.Join(home, "mix.m3u")
	if err := os.WriteFile(m3u, []byte("#EXTM3U\n#EXTINF:12,Song\na.mp3\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := PlaylistImport(m3u, "imported"); err != nil {
		t.Fatalf("PlaylistImport: %v", err)
	}
	out, err := captureStdout(t, func() error { return PlaylistExport("imported", "m3u", "") })
	if err != nil {
		t.Fatalf("PlaylistExport: %v", err)
	}
	if !strings.Contains(out, "#EXTM3U") || !strings.Contains(out, a) {
		t.Fatalf("export output = %q, want M3U with resolved path", out)
	}
}

func TestPlaylistExportInvalidFormatDoesNotTruncateOutput(t *testing.T) {
	home := setupTestEnv(t)
	p, err := newProvider()
	if err != nil {
		t.Fatal(err)
	}
	if err := p.SavePlaylist("mix", []playlist.Track{{Path: "/a.mp3", Title: "A"}}); err != nil {
		t.Fatalf("SavePlaylist: %v", err)
	}
	out := filepath.Join(home, "keep.txt")
	if err := os.WriteFile(out, []byte("keep"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := PlaylistExport("mix", "bad", out); err == nil {
		t.Fatal("PlaylistExport should reject unsupported format")
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "keep" {
		t.Fatalf("output file = %q, want keep", string(data))
	}
}

func TestPlaylistFavoriteToggle(t *testing.T) {
	home := setupTestEnv(t)
	audio := filepath.Join(home, "a.mp3")
	writeAudioFile(t, audio)
	if err := PlaylistCreate("mix", []string{audio}, "", nil); err != nil {
		t.Fatalf("PlaylistCreate: %v", err)
	}

	for _, step := range []struct {
		name         string
		wantToggle   string
		wantListing  string
		rejectToggle string
	}{
		{name: "favorite", wantToggle: "♥ ", wantListing: "1 favorites", rejectToggle: "Removed"},
		{name: "unfavorite", wantToggle: "Removed ♥ ", wantListing: "No favorites yet"},
	} {
		t.Run(step.name, func(t *testing.T) {
			out, err := captureStdout(t, func() error { return PlaylistFavorite("mix", 1) })
			if err != nil {
				t.Fatalf("PlaylistFavorite: %v", err)
			}
			if !strings.Contains(out, step.wantToggle) || (step.rejectToggle != "" && strings.Contains(out, step.rejectToggle)) {
				t.Errorf("toggle output = %q, want %q", out, step.wantToggle)
			}
			out, err = captureStdout(t, PlaylistFavorites)
			if err != nil {
				t.Fatalf("PlaylistFavorites: %v", err)
			}
			if !strings.Contains(out, step.wantListing) {
				t.Errorf("listing = %q, want %q", out, step.wantListing)
			}
		})
	}

	// The playlist file keeps no bookmark flag.
	data, err := os.ReadFile(filepath.Join(home, ".config", "cliamp", "playlists", "mix.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "bookmark") {
		t.Errorf("playlist file changed:\n%s", data)
	}
}

func TestPlaylistFavoriteOutOfRange(t *testing.T) {
	home := setupTestEnv(t)
	audio := filepath.Join(home, "a.mp3")
	writeAudioFile(t, audio)
	if err := PlaylistCreate("mix", []string{audio}, "", nil); err != nil {
		t.Fatalf("PlaylistCreate: %v", err)
	}
	for _, index := range []int{0, 2} {
		if err := PlaylistFavorite("mix", index); err == nil {
			t.Errorf("PlaylistFavorite(%d) should fail", index)
		}
	}
}

// Old bookmarks show up as favorites the first time the CLI reads them.
func TestPlaylistFavoritesMigratesBookmarks(t *testing.T) {
	home := setupTestEnv(t)
	dir := filepath.Join(home, ".config", "cliamp", "playlists")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "[[track]]\npath = \"/old.mp3\"\ntitle = \"Old Song\"\nbookmark = true\n"
	if err := os.WriteFile(filepath.Join(dir, "mix.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := captureStdout(t, PlaylistFavorites)
	if err != nil {
		t.Fatalf("PlaylistFavorites: %v", err)
	}
	if !strings.Contains(out, "Old Song") || !strings.Contains(out, "1 favorites") {
		t.Errorf("output = %q, want the migrated bookmark", out)
	}
}

func TestCollectLocalAudioMultiplePaths(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.mp3")
	b := filepath.Join(dir, "sub", "b.flac")
	writeAudioFile(t, a)
	writeAudioFile(t, b)

	got, err := collectLocalAudio([]string{a, b})
	if err != nil {
		t.Fatalf("collectLocalAudio: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("got %d paths, want 2 — %v", len(got), got)
	}
}

func TestCollectLocalAudioMissingPath(t *testing.T) {
	_, err := collectLocalAudio([]string{filepath.Join(t.TempDir(), "nope")})
	if err == nil {
		t.Error("collectLocalAudio with missing path should error")
	}
}

func TestNewProvider(t *testing.T) {
	setupTestEnv(t)
	p, err := newProvider()
	if err != nil {
		t.Fatalf("newProvider: %v", err)
	}
	if p == nil {
		t.Error("newProvider returned nil provider")
	}
}
