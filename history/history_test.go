package history

import (
	"os"
	"path/filepath"
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

func TestReplayMovesEntryToTop(t *testing.T) {
	a := playlist.Track{Path: "/a.mp3", Title: "A"}
	b := playlist.Track{Path: "/b.mp3", Title: "B"}
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	s := newTestStore(t)
	mustRecord(t, s, a, base)
	mustRecord(t, s, b, base.Add(time.Minute))

	// Re-listen to the older track: no duplicate, entry moves to top with a
	// fresh timestamp.
	replay := base.Add(2 * time.Minute)
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
}

func TestCapTruncates(t *testing.T) {
	s := newTestStore(t)
	s.SetCap(3)

	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		mustRecord(t, s, playlist.Track{
			Path:  filepath.FromSlash("/track" + string(rune('A'+i)) + ".mp3"),
			Title: string(rune('A' + i)),
		}, base.Add(time.Duration(i)*time.Hour))
	}

	got, _ := s.Recent(0)
	if len(got) != 3 {
		t.Fatalf("got %d entries, want 3 (cap)", len(got))
	}
	wantTitles := []string{"E", "D", "C"} // newest 3
	for i, e := range got {
		if e.Track.Title != wantTitles[i] {
			t.Errorf("entry %d = %q, want %q", i, e.Track.Title, wantTitles[i])
		}
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
