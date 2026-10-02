package favorites

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/internal/fileutil"
	"github.com/bjarneo/cliamp/playlist"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	return NewAt(filepath.Join(t.TempDir(), "favorites.toml"))
}

func TestToggleAdd(t *testing.T) {
	s := newTestStore(t)
	track := playlist.Track{Path: "/a.mp3", Title: "A", Artist: "Art"}

	added, err := s.Toggle(track)
	if err != nil {
		t.Fatalf("Toggle: %v", err)
	}
	if !added {
		t.Fatal("Toggle should return true when adding")
	}
	if !s.IsFavorited("/a.mp3") {
		t.Fatal("track should be favorited after Toggle")
	}
	if s.Count() != 1 {
		t.Fatalf("count = %d, want 1", s.Count())
	}
}

func TestToggleRemove(t *testing.T) {
	s := newTestStore(t)
	track := playlist.Track{Path: "/a.mp3", Title: "A"}

	s.Toggle(track)
	added, err := s.Toggle(track)
	if err != nil {
		t.Fatalf("Toggle: %v", err)
	}
	if added {
		t.Fatal("Toggle should return false when removing")
	}
	if s.IsFavorited("/a.mp3") {
		t.Fatal("track should not be favorited after second Toggle")
	}
	if s.Count() != 0 {
		t.Fatalf("count = %d, want 0", s.Count())
	}
}

func TestTracksOrdering(t *testing.T) {
	s := newTestStore(t)

	s.Toggle(playlist.Track{Path: "/a.mp3", Title: "A"})
	// Toggle is time.Now()-based, so ordering rests on call sequence:
	// add two tracks in order and expect newest first.
	s.Toggle(playlist.Track{Path: "/b.mp3", Title: "B"})

	tracks, err := s.Tracks()
	if err != nil {
		t.Fatalf("Tracks: %v", err)
	}
	if len(tracks) != 2 {
		t.Fatalf("len = %d, want 2", len(tracks))
	}
	// Newest first: B was added after A.
	if tracks[0].Title != "B" || tracks[1].Title != "A" {
		t.Fatalf("order wrong: %+v", tracks)
	}
}

func TestPersistAcrossInstances(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "favorites.toml")

	s1 := NewAt(path)
	s1.Toggle(playlist.Track{Path: "/a.mp3", Title: "A", Artist: "Art", Album: "Alb", Year: 2026, DurationSecs: 180})

	s2 := NewAt(path)
	tracks, err := s2.Tracks()
	if err != nil {
		t.Fatalf("Tracks: %v", err)
	}
	if len(tracks) != 1 {
		t.Fatalf("reloaded %d tracks, want 1", len(tracks))
	}
	tr := tracks[0]
	if tr.Title != "A" || tr.Artist != "Art" || tr.Album != "Alb" {
		t.Errorf("track meta lost: %+v", tr)
	}
	if tr.Year != 2026 || tr.DurationSecs != 180 {
		t.Errorf("numeric meta lost: year=%d dur=%d", tr.Year, tr.DurationSecs)
	}
}

func TestStreamFlagInferredOnReload(t *testing.T) {
	s := newTestStore(t)
	s.Toggle(playlist.Track{Path: "https://example.com/stream", Title: "Live"})

	s2 := NewAt(s.Path())
	tracks, _ := s2.Tracks()
	if len(tracks) != 1 || !tracks[0].Stream {
		t.Fatalf("Stream flag not inferred: %+v", tracks)
	}
}

// A favorited feed URL without a feed-like extension must survive a restart
// as a feed, or it would reload as an ordinary stream.
func TestFeedFlagPersistsAcrossReload(t *testing.T) {
	s := newTestStore(t)
	added, err := s.Toggle(playlist.Track{
		Path:  "https://example.com/podcast?feed_id=7",
		Title: "Show",
		Feed:  true,
	})
	if err != nil || !added {
		t.Fatalf("toggle = %v, %v", added, err)
	}

	s2 := NewAt(s.Path())
	tracks, _ := s2.Tracks()
	if len(tracks) != 1 || !tracks[0].Feed {
		t.Fatalf("Feed flag lost on reload: %+v", tracks)
	}
}

func TestLockFileSerializesAndReleases(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "favorites.toml.lock")

	unlock, err := fileutil.LockFile(lockPath)
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}
	if err := unlock(); err != nil {
		t.Fatalf("unlock: %v", err)
	}
	// A released lock must be acquirable again.
	unlock2, err := fileutil.LockFile(lockPath)
	if err != nil {
		t.Fatalf("relock after release: %v", err)
	}
	if err := unlock2(); err != nil {
		t.Fatalf("second unlock: %v", err)
	}
}

func TestNilStoreSafe(t *testing.T) {
	var s *Store
	added, err := s.Toggle(playlist.Track{Path: "/a.mp3"})
	if err != nil || added {
		t.Errorf("nil Toggle: added=%v err=%v", added, err)
	}
	if _, err := s.Tracks(); err != nil {
		t.Errorf("nil Tracks: %v", err)
	}
	if s.IsFavorited("/a.mp3") {
		t.Error("nil IsFavorited should return false")
	}
	if s.Count() != 0 {
		t.Errorf("nil Count = %d, want 0", s.Count())
	}
}

func TestToggleIgnoresEmptyPath(t *testing.T) {
	s := newTestStore(t)
	added, err := s.Toggle(playlist.Track{Title: "no path"})
	if err != nil || added {
		t.Errorf("empty path Toggle: added=%v err=%v", added, err)
	}
	if s.Count() != 0 {
		t.Fatalf("count = %d, want 0", s.Count())
	}
}

func TestTracksEmpty(t *testing.T) {
	s := newTestStore(t)
	tracks, err := s.Tracks()
	if err != nil {
		t.Fatalf("Tracks: %v", err)
	}
	if len(tracks) != 0 {
		t.Fatalf("empty Tracks = %d, want 0", len(tracks))
	}
}

func TestRealtimeAndProviderMetaPersistAcrossReload(t *testing.T) {
	s := newTestStore(t)
	_, err := s.Toggle(playlist.Track{
		Path:         "https://radio.example.com/stream",
		Title:        "Live Radio",
		Realtime:     true,
		ProviderMeta: map[string]string{"navidrome.id": "42", "provider": "navidrome"},
	})
	if err != nil {
		t.Fatalf("Toggle: %v", err)
	}

	s2 := NewAt(s.Path())
	tracks, err := s2.Tracks()
	if err != nil {
		t.Fatalf("Tracks: %v", err)
	}
	if len(tracks) != 1 {
		t.Fatalf("count = %d, want 1", len(tracks))
	}
	if !tracks[0].Realtime {
		t.Fatal("Realtime flag lost on reload")
	}
	if tracks[0].ProviderMeta["navidrome.id"] != "42" {
		t.Fatalf("ProviderMeta lost on reload: %+v", tracks[0].ProviderMeta)
	}
	if tracks[0].ProviderMeta["provider"] != "navidrome" {
		t.Fatalf("ProviderMeta provider lost on reload: %+v", tracks[0].ProviderMeta)
	}
}

func TestImport(t *testing.T) {
	tests := []struct {
		name      string
		existing  []playlist.Track
		imported  []playlist.Track
		wantAdded int
		wantPaths []string
	}{
		{
			name:      "empty store",
			imported:  []playlist.Track{{Path: "/a.mp3"}, {Path: "/b.mp3"}},
			wantAdded: 2,
			wantPaths: []string{"/a.mp3", "/b.mp3"},
		},
		{
			name:      "keeps existing favorites first",
			existing:  []playlist.Track{{Path: "/old.mp3"}},
			imported:  []playlist.Track{{Path: "/a.mp3"}},
			wantAdded: 1,
			wantPaths: []string{"/old.mp3", "/a.mp3"},
		},
		{
			name:      "skips favorites that exist",
			existing:  []playlist.Track{{Path: "/a.mp3", Title: "Kept"}},
			imported:  []playlist.Track{{Path: "/a.mp3", Title: "New"}},
			wantPaths: []string{"/a.mp3"},
		},
		{
			name:      "skips repeated and empty paths",
			imported:  []playlist.Track{{Path: "/a.mp3"}, {Path: " "}, {Path: "/a.mp3"}},
			wantAdded: 1,
			wantPaths: []string{"/a.mp3"},
		},
		{
			name: "no tracks",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestStore(t)
			for _, track := range tt.existing {
				if _, err := s.Toggle(track); err != nil {
					t.Fatal(err)
				}
			}
			added, err := s.Import(tt.imported)
			if err != nil {
				t.Fatalf("Import: %v", err)
			}
			if added != tt.wantAdded {
				t.Fatalf("added = %d, want %d", added, tt.wantAdded)
			}
			tracks, err := s.Tracks()
			if err != nil {
				t.Fatal(err)
			}
			var paths []string
			for _, track := range tracks {
				paths = append(paths, track.Path)
			}
			if !slices.Equal(paths, tt.wantPaths) {
				t.Fatalf("paths = %v, want %v", paths, tt.wantPaths)
			}
			if len(tt.existing) > 0 && tracks[0].Title != tt.existing[0].Title {
				t.Fatalf("existing entry changed: %+v", tracks[0])
			}
		})
	}
}

// Files written before the shared track codec must load unchanged.
func TestParseLoadsExistingFiles(t *testing.T) {
	at := time.Date(2026, 5, 6, 22, 9, 11, 0, time.UTC)
	tests := []struct {
		name string
		data string
		want []Entry
	}{
		{
			name: "entry with unsorted meta and unknown keys",
			data: `# comment
[[entry]]
favorited_at = "2026-05-06T22:09:11Z"
path = "https://radio.example.com/stream"
title = "Live"
realtime = true
provider_meta.radio.url = "https://radio.example.com/stream"
provider_meta.radio.name = "Station"
future_key = "ignored"
`,
			want: []Entry{{
				FavoritedAt: at,
				Track: playlist.Track{
					Path:     "https://radio.example.com/stream",
					Title:    "Live",
					Stream:   true,
					Realtime: true,
					ProviderMeta: map[string]string{
						"radio.url":  "https://radio.example.com/stream",
						"radio.name": "Station",
					},
				},
			}},
		},
		{
			name: "entry without a path is dropped",
			data: `[[entry]]
title = "No path"

[[entry]]
path = "/a.mp3"
title = "A"
year = 1979
`,
			want: []Entry{{Track: playlist.Track{Path: "/a.mp3", Title: "A", Year: 1979}}},
		},
		{
			name: "bad timestamp keeps the track",
			data: `[[entry]]
favorited_at = "yesterday"
path = "/a.mp3"
title = "A"
`,
			want: []Entry{{Track: playlist.Track{Path: "/a.mp3", Title: "A"}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parse([]byte(tt.data)); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parse:\n got %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

func TestSaveWritesStableBytes(t *testing.T) {
	s := newTestStore(t)
	at := time.Date(2026, 5, 6, 22, 9, 11, 0, time.UTC)
	entries := []Entry{
		{FavoritedAt: at, Track: playlist.Track{
			Path:         "https://nd.example.com/rest/stream?id=42",
			Title:        "Song",
			Artist:       "Artist",
			DurationSecs: 208,
			ProviderMeta: map[string]string{"navidrome.id": "42", "jellyfin.id": "7"},
		}},
		{FavoritedAt: at, Track: playlist.Track{Path: "/a.mp3", Title: "A"}},
	}
	const want = `[[entry]]
favorited_at = "2026-05-06T22:09:11Z"
path = "https://nd.example.com/rest/stream?id=42"
title = "Song"
artist = "Artist"
duration_secs = 208
provider_meta.jellyfin.id = "7"
provider_meta.navidrome.id = "42"

[[entry]]
favorited_at = "2026-05-06T22:09:11Z"
path = "/a.mp3"
title = "A"
`
	if err := s.saveLocked(entries); err != nil {
		t.Fatalf("saveLocked: %v", err)
	}
	data, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Fatalf("favorites.toml:\n got:\n%s\nwant:\n%s", data, want)
	}
}

// A write must work before the config directory exists, as in the
// `cliamp playlist` CLI on a new machine. The file lock is taken first, so
// it must create the directory.
func TestWritesCreateMissingConfigDir(t *testing.T) {
	track := playlist.Track{Path: "/a.mp3"}
	tests := []struct {
		name string
		fn   func(s *Store) error
	}{
		{"Toggle", func(s *Store) error { _, err := s.Toggle(track); return err }},
		{"Import", func(s *Store) error { _, err := s.Import([]playlist.Track{track}); return err }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewAt(filepath.Join(t.TempDir(), "missing", "favorites.toml"))
			if err := tt.fn(s); err != nil {
				t.Fatalf("%s: %v", tt.name, err)
			}
		})
	}
}
