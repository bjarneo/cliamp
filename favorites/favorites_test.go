package favorites

import (
	"os"
	"path/filepath"
	"testing"
	"time"

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

func TestFavoriteIdempotent(t *testing.T) {
	s := newTestStore(t)
	track := playlist.Track{Path: "/a.mp3", Title: "A"}

	added, _ := s.Favorite(track)
	if !added {
		t.Fatal("first Favorite should return true")
	}
	added, _ = s.Favorite(track)
	if added {
		t.Fatal("second Favorite should return false (already present)")
	}
	if s.Count() != 1 {
		t.Fatalf("count = %d, want 1", s.Count())
	}
}

func TestRemoveNonexistent(t *testing.T) {
	s := newTestStore(t)
	removed, err := s.Remove("/nope.mp3")
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if removed {
		t.Fatal("Remove of nonexistent track should return false")
	}
}

func TestTracksOrdering(t *testing.T) {
	s := newTestStore(t)
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	s.Toggle(playlist.Track{Path: "/a.mp3", Title: "A"})
	// Simulate earlier favorited time by toggling and re-adding with a known time.
	// Since Toggle is time.Now()-based, we just add two tracks in sequence.
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
	_ = base // used above for documentation
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

func TestClearRemovesFile(t *testing.T) {
	s := newTestStore(t)
	s.Toggle(playlist.Track{Path: "/a.mp3", Title: "A"})
	if err := s.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if _, err := os.Stat(s.Path()); !os.IsNotExist(err) {
		t.Fatalf("file should be gone after Clear, err = %v", err)
	}
	if s.Count() != 0 {
		t.Fatalf("count after Clear = %d, want 0", s.Count())
	}
}

func TestClearMissingFileNoError(t *testing.T) {
	s := newTestStore(t)
	if err := s.Clear(); err != nil {
		t.Fatalf("Clear on missing file: %v", err)
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
	if err := s.Clear(); err != nil {
		t.Errorf("nil Clear: %v", err)
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
