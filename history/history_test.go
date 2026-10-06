package history

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/playlist"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	return NewAt(filepath.Join(dir, "history.toml"))
}

func mustRecord(t *testing.T, s *Store, track playlist.Track, at time.Time) {
	t.Helper()
	if err := s.Record(track, at); err != nil {
		t.Fatalf("Record: %v", err)
	}
}

func TestRecentEmpty(t *testing.T) {
	s := newTestStore(t)
	got, err := s.Recent(0)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Recent on empty store = %d entries, want 0", len(got))
	}
}

func TestRecordOrdering(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC().Truncate(time.Second)

	mustRecord(t, s, playlist.Track{Path: "/a.mp3", Title: "A"}, now.Add(-3*time.Hour))
	mustRecord(t, s, playlist.Track{Path: "/b.mp3", Title: "B"}, now.Add(-2*time.Hour))
	mustRecord(t, s, playlist.Track{Path: "/c.mp3", Title: "C"}, now.Add(-1*time.Hour))

	got, err := s.Recent(0)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d entries, want 3", len(got))
	}
	wantOrder := []string{"C", "B", "A"}
	for i, e := range got {
		if e.Track.Title != wantOrder[i] {
			t.Errorf("entry %d title = %q, want %q", i, e.Track.Title, wantOrder[i])
		}
	}
}

// A replay moves the entry to the top with the new time, however long ago
// the earlier play was. There is no dedup window.
func TestReplayMovesEntryToTop(t *testing.T) {
	a := playlist.Track{Path: "/a.mp3", Title: "A"}
	b := playlist.Track{Path: "/b.mp3", Title: "B"}
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	for _, gap := range []time.Duration{2 * time.Minute, time.Hour, 30 * 24 * time.Hour} {
		t.Run(gap.String(), func(t *testing.T) {
			s := newTestStore(t)
			mustRecord(t, s, a, base)
			mustRecord(t, s, b, base.Add(time.Minute))

			replay := base.Add(gap)
			mustRecord(t, s, a, replay)

			got, err := s.Recent(0)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 2 {
				t.Fatalf("got %d entries, want 2 (no duplicate for the replay)", len(got))
			}
			if got[0].Track.Path != "/a.mp3" || !got[0].PlayedAt.Equal(replay) {
				t.Fatalf("top = %q at %v, want /a.mp3 at %v", got[0].Track.Path, got[0].PlayedAt, replay)
			}
			if got[1].Track.Path != "/b.mp3" {
				t.Fatalf("second = %q, want /b.mp3", got[1].Track.Path)
			}
		})
	}
}

// Record applies no duration or live-stream check, so a radio station and a
// track with no known duration enter the list as docs/history.md says.
func TestRecordKeepsLiveAndUnknownDurationTracks(t *testing.T) {
	tests := []struct {
		name  string
		track playlist.Track
	}{
		{name: "radio station", track: playlist.Track{Path: "https://radio.example/live", Title: "Radio", Stream: true, Realtime: true}},
		{name: "unknown duration", track: playlist.Track{Path: "/a.mp3", Title: "A"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestStore(t)
			mustRecord(t, s, tt.track, time.Now())
			got, err := s.Tracks(0)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || got[0].Path != tt.track.Path || got[0].Realtime != tt.track.Realtime {
				t.Fatalf("Tracks = %+v, want one entry for %+v", got, tt.track)
			}
		})
	}
}

func TestCapTruncates(t *testing.T) {
	tests := []struct {
		name    string
		cap     int // 0 keeps the cap that NewAt sets
		records int
		want    int
	}{
		{name: "small cap", cap: 3, records: 5, want: 3},
		{name: "default cap", records: DefaultCap + 2, want: DefaultCap},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestStore(t)
			if tt.cap > 0 {
				s.cap = tt.cap
			}

			base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
			for i := range tt.records {
				mustRecord(t, s, playlist.Track{
					Path:  filepath.FromSlash(fmt.Sprintf("/track%d.mp3", i)),
					Title: fmt.Sprint(i),
				}, base.Add(time.Duration(i)*time.Hour))
			}

			got, _ := s.Recent(0)
			if len(got) != tt.want {
				t.Fatalf("got %d entries, want %d (cap)", len(got), tt.want)
			}
			for i, e := range got {
				// The newest entries survive, newest first.
				if want := fmt.Sprint(tt.records - 1 - i); e.Track.Title != want {
					t.Fatalf("entry %d = %q, want %q", i, e.Track.Title, want)
				}
			}
		})
	}
}

func TestRecentLimit(t *testing.T) {
	s := newTestStore(t)
	for i := 0; i < 10; i++ {
		mustRecord(t, s, playlist.Track{Path: "/x" + string(rune('0'+i))}, time.Now().Add(time.Duration(i)*time.Minute))
	}
	got, _ := s.Recent(4)
	if len(got) != 4 {
		t.Fatalf("Recent(4) returned %d, want 4", len(got))
	}
}

func TestRecordIgnoresEmptyPath(t *testing.T) {
	s := newTestStore(t)
	if err := s.Record(playlist.Track{Title: "no path"}, time.Now()); err != nil {
		t.Fatalf("Record: %v", err)
	}
	got, _ := s.Recent(0)
	if len(got) != 0 {
		t.Fatalf("got %d entries, want 0 (empty path skipped)", len(got))
	}
}

func TestPersistAcrossInstances(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.toml")

	s1 := NewAt(path)
	mustRecord(t, s1, playlist.Track{Path: "/a.mp3", Title: "A", Artist: "Artist", Album: "Album", Year: 2026, DurationSecs: 180}, time.Date(2026, 5, 6, 22, 0, 0, 0, time.UTC))

	s2 := NewAt(path)
	got, err := s2.Recent(0)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("reloaded %d entries, want 1", len(got))
	}
	e := got[0]
	if e.Track.Title != "A" || e.Track.Artist != "Artist" || e.Track.Album != "Album" {
		t.Errorf("track meta lost: %+v", e.Track)
	}
	if e.Track.Year != 2026 || e.Track.DurationSecs != 180 {
		t.Errorf("numeric meta lost: year=%d dur=%d", e.Track.Year, e.Track.DurationSecs)
	}
	if !e.PlayedAt.Equal(time.Date(2026, 5, 6, 22, 0, 0, 0, time.UTC)) {
		t.Errorf("PlayedAt round-trip wrong: %v", e.PlayedAt)
	}
}

func TestStreamFlagInferredOnReload(t *testing.T) {
	s := newTestStore(t)
	mustRecord(t, s, playlist.Track{Path: "https://example.com/stream", Title: "Live"}, time.Now())

	// Force a reload by creating a fresh store at the same path.
	s2 := NewAt(s.Path())
	got, _ := s2.Recent(0)
	if len(got) != 1 || !got[0].Track.Stream {
		t.Fatalf("Stream flag not inferred from URL on reload: %+v", got)
	}
}

func TestClearRemovesFile(t *testing.T) {
	s := newTestStore(t)
	mustRecord(t, s, playlist.Track{Path: "/a.mp3"}, time.Now())
	if err := s.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if _, err := os.Stat(s.Path()); !os.IsNotExist(err) {
		t.Fatalf("file should be gone after Clear, stat err = %v", err)
	}
	got, _ := s.Recent(0)
	if len(got) != 0 {
		t.Fatalf("post-Clear Recent = %d, want 0", len(got))
	}
}

func TestClearMissingFileNoError(t *testing.T) {
	s := newTestStore(t)
	if err := s.Clear(); err != nil {
		t.Fatalf("Clear on missing file: %v", err)
	}
}

func TestTracksOrdered(t *testing.T) {
	s := newTestStore(t)
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	mustRecord(t, s, playlist.Track{Path: "/a.mp3", Title: "A"}, base)
	mustRecord(t, s, playlist.Track{Path: "/b.mp3", Title: "B"}, base.Add(1*time.Hour))

	tracks, err := s.Tracks(0)
	if err != nil {
		t.Fatalf("Tracks: %v", err)
	}
	if len(tracks) != 2 || tracks[0].Title != "B" || tracks[1].Title != "A" {
		t.Fatalf("Tracks order wrong: %+v", tracks)
	}
}

func TestNilStoreSafe(t *testing.T) {
	var s *Store
	if err := s.Record(playlist.Track{Path: "/a.mp3"}, time.Now()); err != nil {
		t.Errorf("nil Record returned err: %v", err)
	}
	if got, err := s.Recent(0); err != nil || got != nil {
		t.Errorf("nil Recent: got=%v err=%v", got, err)
	}
	if err := s.Clear(); err != nil {
		t.Errorf("nil Clear returned err: %v", err)
	}
}

func TestLoadHealsLegacyDuplicates(t *testing.T) {
	s := newTestStore(t)
	// A file written by an older version that appended repeats: /a.mp3 twice.
	raw := `[[entry]]
played_at = "2026-01-01T12:02:00Z"
path = "/a.mp3"
title = "A"

[[entry]]
played_at = "2026-01-01T12:01:00Z"
path = "/b.mp3"
title = "B"

[[entry]]
played_at = "2026-01-01T12:00:00Z"
path = "/a.mp3"
title = "A"
`
	if err := os.WriteFile(s.Path(), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := s.Recent(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("entries = %d, want 2 after healing duplicates", len(got))
	}
	if got[0].Track.Path != "/a.mp3" || !got[0].PlayedAt.Equal(time.Date(2026, 1, 1, 12, 2, 0, 0, time.UTC)) {
		t.Fatalf("top = %q at %v, want the newest /a.mp3 play kept", got[0].Track.Path, got[0].PlayedAt)
	}
	if got[1].Track.Path != "/b.mp3" {
		t.Fatalf("second = %q, want /b.mp3", got[1].Track.Path)
	}

	// A subsequent record rewrites the healed list, cleaning the file too.
	mustRecord(t, s, playlist.Track{Path: "/c.mp3", Title: "C"}, time.Date(2026, 1, 1, 12, 3, 0, 0, time.UTC))
	got, err = s.Recent(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("entries = %d, want 3 after a clean rewrite", len(got))
	}
}

// Recently Played must keep what makes a track work after a restart:
// provider IDs for scrobbling and starring, the station meta for radio, and
// the feed and GUID for a podcast episode.
func TestProviderMetaSurvivesRecordAndTracks(t *testing.T) {
	tests := []struct {
		name  string
		track playlist.Track
	}{
		{
			name: "navidrome track",
			track: playlist.Track{
				Path:         "https://nd.example.com/rest/stream?id=42",
				Title:        "Song",
				Artist:       "Artist",
				DurationSecs: 208,
				ProviderMeta: map[string]string{"navidrome.id": "42"},
			},
		},
		{
			name: "jellyfin track",
			track: playlist.Track{
				Path:         "https://jf.example.com/Audio/7/universal",
				Title:        "Song",
				ProviderMeta: map[string]string{"jellyfin.id": "7"},
			},
		},
		{
			name: "radio station",
			track: playlist.Track{
				Path:     "https://radio.example.com/live",
				Title:    "Station",
				Realtime: true,
				ProviderMeta: map[string]string{
					"radio.name": "Station",
					"radio.url":  "https://radio.example.com/live",
				},
			},
		},
		{
			name: "podcast feed",
			track: playlist.Track{
				Path:  "https://example.com/podcast?feed_id=7",
				Title: "Show",
				Feed:  true,
				ProviderMeta: map[string]string{
					"podcast.feed": "https://example.com/podcast?feed_id=7",
					"podcast.guid": "guid-1",
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestStore(t)
			mustRecord(t, s, tt.track, time.Now())

			tracks, err := NewAt(s.Path()).Tracks(0)
			if err != nil {
				t.Fatalf("Tracks: %v", err)
			}
			want := tt.track
			want.Stream = true
			if len(tracks) != 1 || !reflect.DeepEqual(tracks[0], want) {
				t.Fatalf("Tracks after reload:\n got %+v\nwant %+v", tracks, want)
			}
		})
	}
}

func TestMergeTrackMeta(t *testing.T) {
	meta := map[string]string{"navidrome.id": "42"}
	tests := []struct {
		name string
		prev playlist.Track
		cur  playlist.Track
		want playlist.Track
	}{
		{
			name: "sparse replay keeps the stored meta and flags",
			prev: playlist.Track{Path: "/a", Title: "A", Stream: true, Realtime: true, Feed: true, Restricted: true, AlbumArtURL: "https://art.example/a.jpg", ProviderMeta: meta},
			cur:  playlist.Track{Path: "/a"},
			want: playlist.Track{Path: "/a", Title: "A", Stream: true, Realtime: true, Feed: true, Restricted: true, AlbumArtURL: "https://art.example/a.jpg", ProviderMeta: meta},
		},
		{
			name: "replay cover replaces the stored cover",
			prev: playlist.Track{Path: "/a", AlbumArtURL: "https://art.example/old.jpg"},
			cur:  playlist.Track{Path: "/a", AlbumArtURL: "https://art.example/new.jpg"},
			want: playlist.Track{Path: "/a", AlbumArtURL: "https://art.example/new.jpg"},
		},
		{
			name: "replay meta replaces the stored meta",
			prev: playlist.Track{Path: "/a", ProviderMeta: meta},
			cur:  playlist.Track{Path: "/a", ProviderMeta: map[string]string{"jellyfin.id": "7"}},
			want: playlist.Track{Path: "/a", ProviderMeta: map[string]string{"jellyfin.id": "7"}},
		},
		{
			name: "provider replay clears the stored realtime and restricted flags",
			prev: playlist.Track{Path: "/a", Stream: true, Realtime: true, Restricted: true, ProviderMeta: meta},
			cur:  playlist.Track{Path: "/a", Stream: true, ProviderMeta: meta},
			want: playlist.Track{Path: "/a", Stream: true, ProviderMeta: meta},
		},
		{
			name: "provider replay keeps the stored stream and feed flags",
			prev: playlist.Track{Path: "/a", Stream: true, Feed: true, ProviderMeta: meta},
			cur:  playlist.Track{Path: "/a", ProviderMeta: meta},
			want: playlist.Track{Path: "/a", Stream: true, Feed: true, ProviderMeta: meta},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mergeTrackMeta(tt.prev, tt.cur); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("mergeTrackMeta:\n got %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

// Every cliamp process has its own Store, so the per-Store mutex cannot keep
// two processes from overwriting each other's Record. The file lock must.
func TestRecordFromSeparateStoresKeepsEveryEntry(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fileutil.LockFile takes no lock on Windows")
	}
	tests := []struct {
		name   string
		stores int
	}{
		{"one store", 1},
		{"two stores", 2},
		{"four stores", 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const perStore = 20
			path := filepath.Join(t.TempDir(), "history.toml")
			base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
			errs := make(chan error, tt.stores*perStore)
			var wg sync.WaitGroup
			for i := range tt.stores {
				s := NewAt(path)
				wg.Go(func() {
					for j := range perStore {
						track := playlist.Track{Path: fmt.Sprintf("/%d-%d.mp3", i, j)}
						errs <- s.Record(track, base.Add(time.Duration(j)*time.Second))
					}
				})
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				if err != nil {
					t.Fatalf("Record: %v", err)
				}
			}
			got, err := NewAt(path).Recent(0)
			if err != nil {
				t.Fatalf("Recent: %v", err)
			}
			if len(got) != tt.stores*perStore {
				t.Fatalf("got %d entries, want %d", len(got), tt.stores*perStore)
			}
		})
	}
}

func TestRecordCreatesMissingConfigDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "history.toml")
	s := NewAt(path)
	mustRecord(t, s, playlist.Track{Path: "/a.mp3"}, time.Now())
	if got, err := s.Recent(0); err != nil || len(got) != 1 {
		t.Fatalf("Recent = %d entries, %v; want 1 entry", len(got), err)
	}
}
