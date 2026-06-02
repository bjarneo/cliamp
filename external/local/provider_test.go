package local

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cliamp/history"
	"cliamp/playlist"
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
	bad := []string{"..", ".", "", "foo/bar", "foo\\bar"}
	for _, name := range bad {
		if _, err := p.safePath(name); err == nil {
			t.Errorf("safePath(%q) should have returned error", name)
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
		Path:         "/music/song.flac",
		Title:        "Title",
		Artist:       "Artist",
		Album:        "Album",
		Genre:        "Rock",
		Year:         2024,
		TrackNumber:  3,
		DurationSecs: 240,
		Bookmark:     true,
		Feed:         true,
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
		"bookmark = true",
		"feed = true",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in output:\n%s", want, got)
		}
	}
}

// --- loadTOML round-trip ---

func TestLoadTOMLRoundTrip(t *testing.T) {
	p := newTestProvider(t)
	os.MkdirAll(p.dir, 0o755)

	tracks := []playlist.Track{
		{Path: "/a.mp3", Title: "A", Artist: "Art1", Album: "Alb", Year: 2020, TrackNumber: 1, DurationSecs: 180, Bookmark: true},
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

	tracks, err := p.loadTOML(path)
	if err != nil {
		t.Fatalf("loadTOML: %v", err)
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

// --- SetBookmark ---

func TestSetBookmark(t *testing.T) {
	p := newTestProvider(t)
	p.AddTrack("marks", playlist.Track{Path: "/a.mp3", Title: "A"})

	if err := p.SetBookmark("marks", 0); err != nil {
		t.Fatalf("SetBookmark: %v", err)
	}

	tracks, _ := p.Tracks("marks")
	if !tracks[0].Bookmark {
		t.Fatal("track should be bookmarked after toggle")
	}

	// Toggle off.
	p.SetBookmark("marks", 0)
	tracks, _ = p.Tracks("marks")
	if tracks[0].Bookmark {
		t.Fatal("track should not be bookmarked after second toggle")
	}
}

func TestSetBookmarkOutOfRange(t *testing.T) {
	p := newTestProvider(t)
	p.AddTrack("one", playlist.Track{Path: "/a.mp3", Title: "A"})

	if err := p.SetBookmark("one", 5); err == nil {
		t.Fatal("expected error for out-of-range index")
	}
	if err := p.SetBookmark("one", -1); err == nil {
		t.Fatal("expected error for negative index")
	}
}

// --- RemoveTrack ---

func TestRemoveTrack(t *testing.T) {
	p := newTestProvider(t)
	p.AddTracks("rem", []playlist.Track{
		{Path: "/a.mp3", Title: "A"},
		{Path: "/b.mp3", Title: "B"},
		{Path: "/c.mp3", Title: "C"},
	})

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

func TestRemoveTrackDeletesEmptyPlaylist(t *testing.T) {
	p := newTestProvider(t)
	p.AddTrack("solo", playlist.Track{Path: "/a.mp3", Title: "A"})

	if err := p.RemoveTrack("solo", 0); err != nil {
		t.Fatalf("RemoveTrack: %v", err)
	}

	if p.Exists("solo") {
		t.Fatal("playlist should be deleted when last track is removed")
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
	p.AddTracks("over", []playlist.Track{
		{Path: "/a.mp3", Title: "A"},
		{Path: "/b.mp3", Title: "B"},
	})

	// Overwrite with single track.
	if err := p.SavePlaylist("over", []playlist.Track{{Path: "/c.mp3", Title: "C"}}); err != nil {
		t.Fatalf("SavePlaylist: %v", err)
	}

	tracks, _ := p.Tracks("over")
	if len(tracks) != 1 || tracks[0].Title != "C" {
		t.Fatalf("expected single track C, got: %+v", tracks)
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

	tracks, err := p.loadTOML(path)
	if err != nil {
		t.Fatalf("loadTOML: %v", err)
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

func TestWritesRejectedForHistoryName(t *testing.T) {
	p := newTestProviderWithHistory(t)
	track := playlist.Track{Path: "/a.mp3", Title: "A"}

	tests := []struct {
		name string
		call func() error
	}{
		{"AddTrack", func() error { return p.AddTrack(history.PlaylistName, track) }},
		{"AddTracks", func() error { return p.AddTracks(history.PlaylistName, []playlist.Track{track}) }},
		{"SavePlaylist", func() error { return p.SavePlaylist(history.PlaylistName, []playlist.Track{track}) }},
		{"DeletePlaylist", func() error { return p.DeletePlaylist(history.PlaylistName) }},
		{"RemoveTrack", func() error { return p.RemoveTrack(history.PlaylistName, 0) }},
		{"SetBookmark", func() error { return p.SetBookmark(history.PlaylistName, 0) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); err == nil {
				t.Errorf("%s should reject history name", tt.name)
			}
		})
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

func TestClearHistoryRemovesEntries(t *testing.T) {
	p := newTestProviderWithHistory(t)
	p.history.Record(playlist.Track{Path: "/a.mp3"}, time.Now())
	if err := p.ClearHistory(); err != nil {
		t.Fatalf("ClearHistory: %v", err)
	}
	if got, _ := p.history.Recent(0); len(got) != 0 {
		t.Errorf("history not cleared: %d entries remain", len(got))
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
