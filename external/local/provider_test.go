package local

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/favorites"
	"github.com/bjarneo/cliamp/history"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

func newTestProvider(t *testing.T) *Provider {
	t.Helper()
	dir := t.TempDir()
	return &Provider{dir: dir}
}

// --- safePath ---

func TestSafePathValid(t *testing.T) {
	p := newTestProvider(t)
	got, err := p.safePath("rock")
	if err != nil {
		t.Fatalf("safePath(%q): %v", "rock", err)
	}
	want := filepath.Join(p.dir, "rock.toml")
	if got != want {
		t.Fatalf("safePath(%q) = %q, want %q", "rock", got, want)
	}
}

func TestSafePathRejectsTraversal(t *testing.T) {
	p := newTestProvider(t)
	bad := []string{"", "   ", "foo/bar", "foo\\bar"}
	for _, name := range bad {
		if _, err := p.safePath(name); err == nil {
			t.Errorf("safePath(%q) should have returned error", name)
		}
	}
}

func TestValidateNewNameRejectsNonPortableNames(t *testing.T) {
	bad := []string{"..", ".", "", "   ", "foo/bar", "foo\\bar", "bad:name", "bad?name"}
	for _, name := range bad {
		if err := validateNewName(name); err == nil {
			t.Errorf("validateNewName(%q) should have returned error", name)
		}
	}
}

func TestSafePathRejectsSlash(t *testing.T) {
	p := newTestProvider(t)
	if _, err := p.safePath("../escape"); err == nil {
		t.Fatal("safePath should reject paths with /")
	}
}

// --- Name ---

func TestProviderName(t *testing.T) {
	p := newTestProvider(t)
	if got := p.Name(); got != "Local" {
		t.Fatalf("Name() = %q, want %q", got, "Local")
	}
}

// --- writeTrack ---

func TestWriteTrackMinimal(t *testing.T) {
	var buf bytes.Buffer
	writeTrack(&buf, playlist.Track{
		Path:  "/music/song.mp3",
		Title: "Song",
	})
	got := buf.String()

	if !strings.Contains(got, "[[track]]") {
		t.Fatal("missing [[track]] header")
	}
	if !strings.Contains(got, `path = "/music/song.mp3"`) {
		t.Fatal("missing path")
	}
	if !strings.Contains(got, `title = "Song"`) {
		t.Fatal("missing title")
	}
	// Optional fields should be absent.
	if strings.Contains(got, "artist") {
		t.Fatal("empty artist should not be written")
	}
	if strings.Contains(got, "bookmark") {
		t.Fatal("false bookmark should not be written")
	}
}

func TestWriteTrackAllFields(t *testing.T) {
	var buf bytes.Buffer
	writeTrack(&buf, playlist.Track{
		Path:           "/music/song.flac",
		Title:          "Title",
		Artist:         "Artist",
		Album:          "Album",
		Genre:          "Rock",
		Year:           2024,
		TrackNumber:    3,
		DurationSecs:   240,
		Bookmark:       true,
		Feed:           true,
		Realtime:       true,
		EmbeddedLyrics: "[00:01.00]Line",
		AlbumArtURL:    "file:///tmp/cover.jpg",
	})
	got := buf.String()

	for _, want := range []string{
		`path = "/music/song.flac"`,
		`title = "Title"`,
		`artist = "Artist"`,
		`album = "Album"`,
		`genre = "Rock"`,
		"year = 2024",
		"track_number = 3",
		"duration_secs = 240",
		`embedded_lyrics = "[00:01.00]Line"`,
		`album_art_url = "file:///tmp/cover.jpg"`,
		"bookmark = true",
		"feed = true",
		"realtime = true",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in output:\n%s", want, got)
		}
	}
}

// The shared track keys come first and the playlist-only keys follow.
func TestWriteTrackGolden(t *testing.T) {
	const want = `[[track]]
path = "https://cdn.example.com/ep1.mp3"
title = "Episode"
album = "Show"
duration_secs = 3768
album_art_url = "https://cdn.example.com/cover.jpg"
provider_meta.podcast.feed = "https://rss.example.com/show"
provider_meta.podcast.guid = "guid-1"
bookmark = true
`
	var buf bytes.Buffer
	writeTrack(&buf, playlist.Track{
		Path:         "https://cdn.example.com/ep1.mp3",
		Title:        "Episode",
		Album:        "Show",
		DurationSecs: 3768,
		AlbumArtURL:  "https://cdn.example.com/cover.jpg",
		Bookmark:     true,
		ProviderMeta: map[string]string{
			provider.MetaPodcastGUID: "guid-1",
			provider.MetaPodcastFeed: "https://rss.example.com/show",
		},
	})
	if got := buf.String(); got != want {
		t.Fatalf("writeTrack:\n got:\n%s\nwant:\n%s", got, want)
	}
}

// --- loadTOML round-trip ---

func TestLoadTOMLRoundTrip(t *testing.T) {
	p := newTestProvider(t)
	os.MkdirAll(p.dir, 0o755)

	tracks := []playlist.Track{
		{Path: "/a.mp3", Title: "A", Artist: "Art1", Album: "Alb", Year: 2020, TrackNumber: 1, DurationSecs: 180, Bookmark: true, EmbeddedLyrics: "Line 1\nLine 2", AlbumArtURL: "file:///tmp/a.jpg"},
		{Path: "/b.flac", Title: "B", Genre: "Jazz", Feed: true},
	}

	if err := p.savePlaylist("test", tracks); err != nil {
		t.Fatalf("savePlaylist: %v", err)
	}

	loaded, err := p.Tracks("test")
	if err != nil {
		t.Fatalf("Tracks: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("got %d tracks, want 2", len(loaded))
	}

	if loaded[0].Path != "/a.mp3" || loaded[0].Title != "A" || loaded[0].Artist != "Art1" {
		t.Fatalf("track 0 mismatch: %+v", loaded[0])
	}
	if !loaded[0].Bookmark {
		t.Fatal("track 0 should be bookmarked")
	}
	if loaded[0].Year != 2020 || loaded[0].TrackNumber != 1 || loaded[0].DurationSecs != 180 {
		t.Fatalf("track 0 numeric fields mismatch: %+v", loaded[0])
	}
	if loaded[0].EmbeddedLyrics != "Line 1\nLine 2" || loaded[0].AlbumArtURL != "file:///tmp/a.jpg" {
		t.Fatalf("track 0 embedded fields mismatch: %+v", loaded[0])
	}

	if loaded[1].Path != "/b.flac" || loaded[1].Title != "B" || loaded[1].Genre != "Jazz" {
		t.Fatalf("track 1 mismatch: %+v", loaded[1])
	}
	if !loaded[1].Feed {
		t.Fatal("track 1 should have feed=true")
	}
}

func TestLoadTOMLComments(t *testing.T) {
	p := newTestProvider(t)
	os.MkdirAll(p.dir, 0o755)

	content := `# This is a comment
[[track]]
path = "/a.mp3"
title = "A"
# inline comment
`
	path := filepath.Join(p.dir, "commented.toml")
	os.WriteFile(path, []byte(content), 0o644)

	tracks, err := p.Tracks("commented")
	if err != nil {
		t.Fatalf("Tracks: %v", err)
	}
	if len(tracks) != 1 {
		t.Fatalf("got %d tracks, want 1", len(tracks))
	}
	if tracks[0].Title != "A" {
		t.Fatalf("Title = %q, want %q", tracks[0].Title, "A")
	}
}

// --- Playlists ---

func TestPlaylistsEmpty(t *testing.T) {
	p := newTestProvider(t)
	lists, err := p.Playlists()
	if err != nil {
		t.Fatalf("Playlists: %v", err)
	}
	if len(lists) != 0 {
		t.Fatalf("got %d playlists, want 0", len(lists))
	}
}

func TestPlaylistsLists(t *testing.T) {
	p := newTestProvider(t)
	os.MkdirAll(p.dir, 0o755)

	p.savePlaylist("rock", []playlist.Track{{Path: "/a.mp3", Title: "A"}})
	p.savePlaylist("jazz", []playlist.Track{{Path: "/b.mp3", Title: "B"}, {Path: "/c.mp3", Title: "C"}})

	lists, err := p.Playlists()
	if err != nil {
		t.Fatalf("Playlists: %v", err)
	}
	if len(lists) != 2 {
		t.Fatalf("got %d playlists, want 2", len(lists))
	}

	counts := map[string]int{}
	for _, l := range lists {
		counts[l.Name] = l.TrackCount
	}
	if counts["rock"] != 1 {
		t.Fatalf("rock has %d tracks, want 1", counts["rock"])
	}
	if counts["jazz"] != 2 {
		t.Fatalf("jazz has %d tracks, want 2", counts["jazz"])
	}
}

// --- AddTrack ---

func TestAddTrack(t *testing.T) {
	p := newTestProvider(t)

	if err := p.AddTrack("new", playlist.Track{Path: "/x.mp3", Title: "X"}); err != nil {
		t.Fatalf("AddTrack: %v", err)
	}

	tracks, err := p.Tracks("new")
	if err != nil {
		t.Fatalf("Tracks: %v", err)
	}
	if len(tracks) != 1 || tracks[0].Title != "X" {
		t.Fatalf("unexpected tracks: %+v", tracks)
	}

	// Append another.
	if err := p.AddTrack("new", playlist.Track{Path: "/y.mp3", Title: "Y"}); err != nil {
		t.Fatalf("AddTrack: %v", err)
	}

	tracks, _ = p.Tracks("new")
	if len(tracks) != 2 {
		t.Fatalf("got %d tracks, want 2", len(tracks))
	}
}

func TestPlaylistWritesPreserveSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks may require elevated privileges on Windows")
	}

	root := t.TempDir()
	p := &Provider{dir: filepath.Join(root, "playlists")}
	managedDir := filepath.Join(root, "managed")
	dirs := []string{p.dir, managedDir}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	checkDirModes := func() {
		t.Helper()
		for _, dir := range dirs {
			info, err := os.Stat(dir)
			if err != nil {
				t.Fatal(err)
			}
			if got := info.Mode().Perm(); got != 0o755 {
				t.Errorf("directory %q mode = %o, want unchanged mode 755", dir, got)
			}
		}
	}
	target := filepath.Join(managedDir, "radio.toml")
	initial := []byte("[[track]]\npath = \"https://example.com/one\"\ntitle = \"One\"\n")
	if err := os.WriteFile(target, initial, 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(p.dir, "radio.toml")
	if err := os.Symlink(filepath.Join("..", "managed", "radio.toml"), link); err != nil {
		t.Fatal(err)
	}

	if err := p.AddTrack("radio", playlist.Track{Path: "https://example.com/two", Title: "Two"}); err != nil {
		t.Fatal(err)
	}
	checkDirModes()
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("playlist symlink was replaced")
	}
	tracks, err := p.Tracks("radio")
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 2 || tracks[1].Title != "Two" {
		t.Fatalf("Tracks = %+v, want appended track", tracks)
	}

	restored := []byte("[[track]]\npath = \"https://example.com/restored\"\ntitle = \"Restored\"\n")
	if err := p.RestorePlaylistDocument("radio", restored); err != nil {
		t.Fatal(err)
	}
	info, err = os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("playlist symlink was replaced during restore")
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, restored) {
		t.Fatalf("restored target contents = %q, want %q", data, restored)
	}
	checkDirModes()
}

func TestCreatePlaylistCreatesEmptyFile(t *testing.T) {
	p := newTestProvider(t)

	id, err := p.CreatePlaylist(context.Background(), "empty")
	if err != nil {
		t.Fatalf("CreatePlaylist: %v", err)
	}
	if id != "empty" {
		t.Fatalf("CreatePlaylist id = %q, want empty", id)
	}
	if !p.Exists("empty") {
		t.Fatal("created playlist should exist")
	}
	tracks, err := p.Tracks("empty")
	if err != nil {
		t.Fatalf("Tracks: %v", err)
	}
	if len(tracks) != 0 {
		t.Fatalf("got %d tracks, want 0", len(tracks))
	}
	if _, err := p.CreatePlaylist(context.Background(), "empty"); err == nil {
		t.Fatal("creating an existing playlist should fail")
	}
}

func TestExistingPlaylistWithLegacyNameRemainsWritable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("colon is an illegal filename character on Windows")
	}
	p := newTestProvider(t)
	if err := os.MkdirAll(p.dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	path := filepath.Join(p.dir, "bad:name.toml")
	data := []byte("[[track]]\npath = \"/a.mp3\"\ntitle = \"A\"\n")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	tracks, err := p.Tracks("bad:name")
	if err != nil {
		t.Fatalf("Tracks: %v", err)
	}
	if len(tracks) != 1 || tracks[0].Path != "/a.mp3" {
		t.Fatalf("Tracks = %+v, want legacy playlist contents", tracks)
	}
	if err := p.AddTrack("bad:name", playlist.Track{Path: "/b.mp3", Title: "B"}); err != nil {
		t.Fatalf("AddTrack legacy name: %v", err)
	}
	tracks, err = p.Tracks("bad:name")
	if err != nil {
		t.Fatalf("Tracks after AddTrack: %v", err)
	}
	if len(tracks) != 2 || tracks[1].Path != "/b.mp3" {
		t.Fatalf("Tracks after AddTrack = %+v, want appended track", tracks)
	}
	if _, err := p.CreatePlaylist(context.Background(), "new:name"); err == nil {
		t.Fatal("CreatePlaylist should reject new non-portable names")
	}
}

func TestAddTracksSkipsDuplicatePaths(t *testing.T) {
	p := newTestProvider(t)
	if err := p.AddTrack("dupes", playlist.Track{Path: "/a.mp3", Title: "A"}); err != nil {
		t.Fatalf("AddTrack: %v", err)
	}

	added, skipped, err := p.AddTracks("dupes", []playlist.Track{
		{Path: "/a.mp3", Title: "A again"},
		{Path: "/b.mp3", Title: "B"},
		{Path: "/b.mp3", Title: "B again"},
	})
	if err != nil {
		t.Fatalf("AddTracks: %v", err)
	}
	if added != 1 || skipped != 2 {
		t.Fatalf("AddTracks added=%d skipped=%d, want 1/2", added, skipped)
	}
	tracks, err := p.Tracks("dupes")
	if err != nil {
		t.Fatalf("Tracks: %v", err)
	}
	if len(tracks) != 2 || tracks[0].Path != "/a.mp3" || tracks[1].Path != "/b.mp3" {
		t.Fatalf("unexpected tracks: %+v", tracks)
	}
}

// An IPC client can send any provider_meta key. A key must not add a
// [[dir]] section to the saved playlist.
func TestAddTracksDropsMetaKeyThatWritesADirSource(t *testing.T) {
	p := newTestProvider(t)
	dir := t.TempDir()
	_, _, err := p.AddTracks("meta", []playlist.Track{{
		Path:         "/a.mp3",
		Title:        "A",
		ProviderMeta: map[string]string{"x\n[[dir]]\npath": dir, "navidrome.id": "7"},
	}})
	if err != nil {
		t.Fatalf("AddTracks: %v", err)
	}
	dirs, err := p.DirSources("meta")
	if err != nil {
		t.Fatalf("DirSources: %v", err)
	}
	if len(dirs) != 0 {
		t.Fatalf("DirSources = %+v, want none", dirs)
	}
	tracks, err := p.Tracks("meta")
	if err != nil {
		t.Fatalf("Tracks: %v", err)
	}
	want := map[string]string{"navidrome.id": "7"}
	if len(tracks) != 1 || !reflect.DeepEqual(tracks[0].ProviderMeta, want) {
		t.Fatalf("Tracks = %+v, want one track with ProviderMeta %v", tracks, want)
	}
}

// --- Exists ---

func TestExists(t *testing.T) {
	p := newTestProvider(t)

	if p.Exists("nope") {
		t.Fatal("should not exist")
	}

	p.AddTrack("yes", playlist.Track{Path: "/a.mp3", Title: "A"})
	if !p.Exists("yes") {
		t.Fatal("should exist after AddTrack")
	}
}

// --- RemoveTrack ---

func TestRemoveTrack(t *testing.T) {
	p := newTestProvider(t)
	if _, _, err := p.AddTracks("rem", []playlist.Track{
		{Path: "/a.mp3", Title: "A"},
		{Path: "/b.mp3", Title: "B"},
		{Path: "/c.mp3", Title: "C"},
	}); err != nil {
		t.Fatalf("AddTracks: %v", err)
	}

	if err := p.RemoveTrack("rem", 1); err != nil {
		t.Fatalf("RemoveTrack: %v", err)
	}

	tracks, _ := p.Tracks("rem")
	if len(tracks) != 2 {
		t.Fatalf("got %d tracks, want 2", len(tracks))
	}
	if tracks[0].Title != "A" || tracks[1].Title != "C" {
		t.Fatalf("wrong tracks after remove: %+v", tracks)
	}
}

func TestRemoveTrackKeepsEmptyPlaylist(t *testing.T) {
	p := newTestProvider(t)
	p.AddTrack("solo", playlist.Track{Path: "/a.mp3", Title: "A"})

	if err := p.RemoveTrack("solo", 0); err != nil {
		t.Fatalf("RemoveTrack: %v", err)
	}

	if !p.Exists("solo") {
		t.Fatal("playlist should remain after last track is removed")
	}
	tracks, err := p.Tracks("solo")
	if err != nil {
		t.Fatalf("Tracks: %v", err)
	}
	if len(tracks) != 0 {
		t.Fatalf("got %d tracks, want 0", len(tracks))
	}
}

// --- DeletePlaylist ---

func TestDeletePlaylist(t *testing.T) {
	p := newTestProvider(t)
	p.AddTrack("del", playlist.Track{Path: "/a.mp3", Title: "A"})

	if err := p.DeletePlaylist("del"); err != nil {
		t.Fatalf("DeletePlaylist: %v", err)
	}

	if p.Exists("del") {
		t.Fatal("playlist should be deleted")
	}
}

// --- SavePlaylist ---

func TestSavePlaylistOverwrites(t *testing.T) {
	p := newTestProvider(t)
	if _, _, err := p.AddTracks("over", []playlist.Track{
		{Path: "/a.mp3", Title: "A"},
		{Path: "/b.mp3", Title: "B"},
	}); err != nil {
		t.Fatalf("AddTracks: %v", err)
	}

	// Overwrite with single track.
	if err := p.SavePlaylist("over", []playlist.Track{{Path: "/c.mp3", Title: "C"}}); err != nil {
		t.Fatalf("SavePlaylist: %v", err)
	}

	tracks, _ := p.Tracks("over")
	if len(tracks) != 1 || tracks[0].Title != "C" {
		t.Fatalf("expected single track C, got: %+v", tracks)
	}
}

func TestSavePlaylistPreservesRealtime(t *testing.T) {
	p := newTestProvider(t)
	want := playlist.Track{
		Path:     "https://stream.example.com/live",
		Title:    "Live Radio",
		Stream:   true,
		Realtime: true,
	}
	if err := p.SavePlaylist("radio", []playlist.Track{want}); err != nil {
		t.Fatalf("SavePlaylist: %v", err)
	}

	tracks, err := p.Tracks("radio")
	if err != nil {
		t.Fatalf("Tracks: %v", err)
	}
	if len(tracks) != 1 || !tracks[0].Stream || !tracks[0].Realtime {
		t.Fatalf("loaded tracks = %+v, want live stream metadata preserved", tracks)
	}
}

// --- loadTOML edge cases ---

func TestLoadTOMLMissingFile(t *testing.T) {
	p := newTestProvider(t)
	_, err := p.Tracks("nonexistent")
	if err == nil {
		t.Fatal("expected error for missing playlist")
	}
}

func TestLoadTOMLStreamField(t *testing.T) {
	p := newTestProvider(t)
	os.MkdirAll(p.dir, 0o755)

	content := `[[track]]
path = "https://stream.example.com/live"
title = "Live Radio"
`
	path := filepath.Join(p.dir, "radio.toml")
	os.WriteFile(path, []byte(content), 0o644)

	tracks, err := p.Tracks("radio")
	if err != nil {
		t.Fatalf("Tracks: %v", err)
	}
	if !tracks[0].Stream {
		t.Fatal("URL path should set Stream=true")
	}
}

// --- Virtual "Recently Played" history playlist ---

func newTestProviderWithHistory(t *testing.T) *Provider {
	t.Helper()
	dir := t.TempDir()
	historyPath := filepath.Join(dir, "history.toml")
	return &Provider{dir: filepath.Join(dir, "playlists"), history: history.NewAt(historyPath)}
}

func TestPlaylistsIncludesHistoryWhenNonEmpty(t *testing.T) {
	p := newTestProviderWithHistory(t)
	if err := p.history.Record(playlist.Track{Path: "/a.mp3", Title: "A"}, time.Now()); err != nil {
		t.Fatalf("Record: %v", err)
	}
	lists, err := p.Playlists()
	if err != nil {
		t.Fatalf("Playlists: %v", err)
	}
	if len(lists) == 0 || lists[0].Name != history.PlaylistName {
		t.Fatalf("expected first playlist to be %q, got %+v", history.PlaylistName, lists)
	}
	if lists[0].TrackCount != 1 {
		t.Errorf("history TrackCount = %d, want 1", lists[0].TrackCount)
	}
}

func TestPlaylistsOmitsHistoryWhenEmpty(t *testing.T) {
	p := newTestProviderWithHistory(t)
	lists, err := p.Playlists()
	if err != nil {
		t.Fatalf("Playlists: %v", err)
	}
	for _, pl := range lists {
		if pl.Name == history.PlaylistName {
			t.Fatalf("history entry should not appear when empty")
		}
	}
}

func TestTracksReadsFromHistory(t *testing.T) {
	p := newTestProviderWithHistory(t)
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	p.history.Record(playlist.Track{Path: "/a.mp3", Title: "A"}, base)
	p.history.Record(playlist.Track{Path: "/b.mp3", Title: "B"}, base.Add(time.Hour))

	tracks, err := p.Tracks(history.PlaylistName)
	if err != nil {
		t.Fatalf("Tracks: %v", err)
	}
	if len(tracks) != 2 || tracks[0].Title != "B" || tracks[1].Title != "A" {
		t.Fatalf("history tracks order wrong: %+v", tracks)
	}
}

func TestExistsForHistoryName(t *testing.T) {
	p := newTestProviderWithHistory(t)
	if p.Exists(history.PlaylistName) {
		t.Error("Exists should be false when history is empty")
	}
	p.history.Record(playlist.Track{Path: "/a.mp3"}, time.Now())
	if !p.Exists(history.PlaylistName) {
		t.Error("Exists should be true once a play is recorded")
	}
}

// --- SearchTracks (fuzzy) ---

func TestTrackMatchScore(t *testing.T) {
	tests := []struct {
		name  string
		track playlist.Track
		query string
		want  bool
	}{
		{"title subsequence", playlist.Track{Title: "Sakura"}, "skr", true},
		{"artist subsequence", playlist.Track{Artist: "Radiohead"}, "rdhd", true},
		{"album substring", playlist.Track{Album: "In Rainbows"}, "rainbow", true},
		{"no match", playlist.Track{Title: "Sakura"}, "zzz", false},
		{"all fields empty", playlist.Track{}, "x", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, ok := trackMatchScore(tt.track, tt.query); ok != tt.want {
				t.Fatalf("trackMatchScore(%+v, %q) ok = %v, want %v", tt.track, tt.query, ok, tt.want)
			}
		})
	}
}

func TestSearchTracksFuzzyRanksAndMatchesSubsequence(t *testing.T) {
	p := newTestProvider(t)
	if err := p.savePlaylist("lib", []playlist.Track{
		{Path: "/1.mp3", Title: "Cherry Blossom (Sakura Mix)"},
		{Path: "/2.mp3", Title: "Sakura"},
		{Path: "/3.mp3", Title: "Thunderstruck"},
	}); err != nil {
		t.Fatalf("savePlaylist: %v", err)
	}

	// "skr" is a non-contiguous subsequence a substring search would miss.
	got, err := p.SearchTracks(context.Background(), "skr", 0)
	if err != nil {
		t.Fatalf("SearchTracks: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d results, want 2: %+v", len(got), got)
	}
	// "Sakura" (prefix match) outranks "Cherry Blossom (Sakura Mix)".
	if got[0].Title != "Sakura" {
		t.Fatalf("top result = %q, want %q", got[0].Title, "Sakura")
	}
}

func TestSearchTracksEmptyQuery(t *testing.T) {
	p := newTestProvider(t)
	if err := p.savePlaylist("lib", []playlist.Track{{Path: "/1.mp3", Title: "Sakura"}}); err != nil {
		t.Fatalf("savePlaylist: %v", err)
	}
	got, err := p.SearchTracks(context.Background(), "   ", 0)
	if err != nil {
		t.Fatalf("SearchTracks: %v", err)
	}
	if got != nil {
		t.Fatalf("blank query should return nil, got %+v", got)
	}
}

func TestSearchTracksLimit(t *testing.T) {
	p := newTestProvider(t)
	if err := p.savePlaylist("lib", []playlist.Track{
		{Path: "/1.mp3", Title: "Sakura One"},
		{Path: "/2.mp3", Title: "Sakura Two"},
		{Path: "/3.mp3", Title: "Sakura Three"},
	}); err != nil {
		t.Fatalf("savePlaylist: %v", err)
	}
	got, err := p.SearchTracks(context.Background(), "sakura", 2)
	if err != nil {
		t.Fatalf("SearchTracks: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d results, want 2 (limit)", len(got))
	}
}

// --- Virtual "Favorites" playlist ---

func newTestProviderWithFavorites(t *testing.T) *Provider {
	t.Helper()
	dir := t.TempDir()
	favPath := filepath.Join(dir, "favorites.toml")
	return &Provider{dir: filepath.Join(dir, "playlists"), favorites: favorites.NewAt(favPath)}
}

func TestPlaylistsIncludesFavoritesWhenNonEmpty(t *testing.T) {
	p := newTestProviderWithFavorites(t)
	p.favorites.Toggle(playlist.Track{Path: "/a.mp3", Title: "A"})

	lists, err := p.Playlists()
	if err != nil {
		t.Fatalf("Playlists: %v", err)
	}
	if len(lists) != 1 || lists[0].ID != "Favorites" {
		t.Fatalf("Playlists = %+v, want [Favorites]", lists)
	}
	if lists[0].Section != "Favorites" {
		t.Errorf("Section = %q, want %q", lists[0].Section, "Favorites")
	}
}

func TestPlaylistsIncludesFavoritesWhenEmpty(t *testing.T) {
	p := newTestProviderWithFavorites(t)
	lists, err := p.Playlists()
	if err != nil {
		t.Fatalf("Playlists: %v", err)
	}
	if len(lists) != 1 || lists[0].ID != "Favorites" {
		t.Fatalf("Playlists = %+v, want [Favorites] even when empty", lists)
	}
	if lists[0].TrackCount != 0 {
		t.Errorf("TrackCount = %d, want 0", lists[0].TrackCount)
	}
}

func TestTracksReadsFromFavorites(t *testing.T) {
	p := newTestProviderWithFavorites(t)
	p.favorites.Toggle(playlist.Track{Path: "/a.mp3", Title: "A"})
	p.favorites.Toggle(playlist.Track{Path: "/b.mp3", Title: "B"})

	tracks, err := p.Tracks("Favorites")
	if err != nil {
		t.Fatalf("Tracks: %v", err)
	}
	if len(tracks) != 2 {
		t.Fatalf("got %d tracks, want 2", len(tracks))
	}
	// Newest first (B was toggled after A).
	if tracks[0].Title != "B" || tracks[1].Title != "A" {
		t.Errorf("order = [%s, %s], want [B, A]", tracks[0].Title, tracks[1].Title)
	}
}

// Every writer must reject both virtual names and leave no file behind: a
// physical file under a virtual name is hidden behind the virtual playlist.
func TestWritesRejectedForVirtualNames(t *testing.T) {
	track := playlist.Track{Path: "/a.mp3", Title: "A"}
	calls := []struct {
		name string
		fn   func(p *Provider, name string) error
	}{
		{"AddTrack", func(p *Provider, name string) error { return p.AddTrack(name, track) }},
		{"AddTracks", func(p *Provider, name string) error {
			_, _, err := p.AddTracks(name, []playlist.Track{track})
			return err
		}},
		{"PrependTracks", func(p *Provider, name string) error {
			_, _, _, err := p.PrependTracks(name, []playlist.Track{track})
			return err
		}},
		{"SavePlaylist", func(p *Provider, name string) error { return p.SavePlaylist(name, []playlist.Track{track}) }},
		{"DeletePlaylist", func(p *Provider, name string) error { return p.DeletePlaylist(name) }},
		{"RemoveTrack", func(p *Provider, name string) error { return p.RemoveTrack(name, 0) }},
		{"RenamePlaylist from", func(p *Provider, name string) error { return p.RenamePlaylist(name, "NewName") }},
		{"RenamePlaylist to", func(p *Provider, name string) error { return p.RenamePlaylist("Mix", name) }},
		{"CreatePlaylist", func(p *Provider, name string) error {
			_, err := p.CreatePlaylist(context.Background(), name)
			return err
		}},
		{"CreateDirPlaylist", func(p *Provider, name string) error { return p.CreateDirPlaylist(name, []string{t.TempDir()}) }},
		{"AddDirSources", func(p *Provider, name string) error {
			_, err := p.AddDirSources(name, []string{t.TempDir()})
			return err
		}},
		{"DirSources", func(p *Provider, name string) error {
			_, err := p.DirSources(name)
			return err
		}},
		{"RemoveDirSource", func(p *Provider, name string) error { return p.RemoveDirSource(name, "/some/dir") }},
		{"SetDirRecursive", func(p *Provider, name string) error { return p.SetDirRecursive(name, "/some/dir", true) }},
		{"RestorePlaylistDocument", func(p *Provider, name string) error {
			return p.RestorePlaylistDocument(name, []byte("[[track]]\npath = \"/a.mp3\"\n"))
		}},
	}
	names := []struct {
		name string
		want error
	}{
		{history.PlaylistName, errReservedHistoryName},
		{favorites.PlaylistName, errReservedFavoritesName},
	}
	for _, n := range names {
		for _, c := range calls {
			t.Run(n.name+"/"+c.name, func(t *testing.T) {
				dir := t.TempDir()
				p := &Provider{
					dir:       filepath.Join(dir, "playlists"),
					history:   history.NewAt(filepath.Join(dir, "history.toml")),
					favorites: favorites.NewAt(filepath.Join(dir, "favorites.toml")),
				}
				if err := p.SavePlaylist("Mix", []playlist.Track{track}); err != nil {
					t.Fatalf("SavePlaylist: %v", err)
				}
				if err := c.fn(p, n.name); !errors.Is(err, n.want) {
					t.Fatalf("%s(%q) error = %v, want %v", c.name, n.name, err, n.want)
				}
				if _, err := os.Stat(filepath.Join(p.dir, n.name+".toml")); !errors.Is(err, fs.ErrNotExist) {
					t.Fatalf("%s(%q) left a playlist file behind: %v", c.name, n.name, err)
				}
			})
		}
	}
}

// The add-to-playlist pickers list only the entries that CanAddToPlaylist
// accepts, so its answer must match what AddTracks does with each entry.
func TestCanAddToPlaylistMatchesAddTracks(t *testing.T) {
	dir := t.TempDir()
	p := &Provider{
		dir:       filepath.Join(dir, "playlists"),
		history:   history.NewAt(filepath.Join(dir, "history.toml")),
		favorites: favorites.NewAt(filepath.Join(dir, "favorites.toml")),
	}
	track := playlist.Track{Path: "/a.mp3"}
	if err := p.history.Record(track, time.Now()); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := p.SavePlaylist("Mix", nil); err != nil {
		t.Fatalf("SavePlaylist: %v", err)
	}
	lists, err := p.Playlists()
	if err != nil {
		t.Fatalf("Playlists: %v", err)
	}

	want := map[string]bool{
		favorites.PlaylistName: false,
		history.PlaylistName:   false,
		"Mix":                  true,
	}
	if len(lists) != len(want) {
		t.Fatalf("Playlists = %+v, want %d entries", lists, len(want))
	}
	for _, pl := range lists {
		t.Run(pl.ID, func(t *testing.T) {
			if got := p.CanAddToPlaylist(pl); got != want[pl.ID] {
				t.Errorf("CanAddToPlaylist(%q) = %v, want %v", pl.ID, got, want[pl.ID])
			}
			_, _, err := p.AddTracks(pl.ID, []playlist.Track{{Path: "/b.mp3"}})
			if (err == nil) != want[pl.ID] {
				t.Errorf("AddTracks(%q) error = %v, want error: %v", pl.ID, err, !want[pl.ID])
			}
		})
	}
}

// TestNewListsTheSharedStores checks that New lists the virtual playlists
// from the stores that the caller passes. A write through the store shows at
// once, because the provider keeps no copy of its own.
func TestNewListsTheSharedStores(t *testing.T) {
	track := playlist.Track{Path: "/a.mp3", Title: "A"}
	tests := []struct {
		name       string
		withStores bool
		wantLists  []string
	}{
		{name: "shared stores", withStores: true, wantLists: []string{favorites.PlaylistName, history.PlaylistName}},
		{name: "no stores", withStores: false, wantLists: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("CLIAMP_CONFIG_DIR", dir)
			var favs *favorites.Store
			var hist *history.Store
			if tt.withStores {
				favs = favorites.NewAt(filepath.Join(dir, "favorites.toml"))
				hist = history.NewAt(filepath.Join(dir, "history.toml"))
				if _, err := favs.Toggle(track); err != nil {
					t.Fatal(err)
				}
				if err := hist.Record(track, time.Now()); err != nil {
					t.Fatal(err)
				}
			}
			p := New(favs, hist)

			lists, err := p.Playlists()
			if err != nil {
				t.Fatal(err)
			}
			var names []string
			for _, pl := range lists {
				names = append(names, pl.Name)
			}
			if !slices.Equal(names, tt.wantLists) {
				t.Fatalf("Playlists() = %v, want %v", names, tt.wantLists)
			}
			for _, name := range tt.wantLists {
				tracks, err := p.Tracks(name)
				if err != nil || len(tracks) != 1 || tracks[0].Path != track.Path {
					t.Fatalf("Tracks(%q) = %+v, %v, want %s", name, tracks, err, track.Path)
				}
			}
			if favs == nil {
				return
			}
			if _, err := favs.Toggle(track); err != nil {
				t.Fatal(err)
			}
			if tracks, err := p.Tracks(favorites.PlaylistName); err != nil || len(tracks) != 0 {
				t.Fatalf("Tracks(Favorites) after a store toggle = %+v, %v, want none", tracks, err)
			}
		})
	}
}

func TestFavoritesTrackCount(t *testing.T) {
	p := newTestProviderWithFavorites(t)
	p.favorites.Toggle(playlist.Track{Path: "/a.mp3", Title: "A"})
	p.favorites.Toggle(playlist.Track{Path: "/b.mp3", Title: "B"})

	lists, err := p.Playlists()
	if err != nil {
		t.Fatalf("Playlists: %v", err)
	}
	if len(lists) != 1 || lists[0].TrackCount != 2 {
		t.Fatalf("TrackCount = %d, want 2", lists[0].TrackCount)
	}
}
