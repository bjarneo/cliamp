package playlist

import "testing"

func TestToggleShuffle(t *testing.T) {
	p := makePlaylist(5, false)
	p.SetIndex(2) // C

	if p.Shuffled() {
		t.Fatal("Shuffled() initially should be false")
	}

	p.ToggleShuffle()
	if !p.Shuffled() {
		t.Fatal("Shuffled() after toggle should be true")
	}

	cur, _ := p.Current()
	if cur.Title != "C" {
		t.Fatalf("Current after shuffle = %q, want C", cur.Title)
	}
}

func TestToggleShuffleOff(t *testing.T) {
	p := makePlaylist(5, true) // start shuffled

	curTrack, _ := p.Current()

	p.ToggleShuffle() // turn off

	if p.Shuffled() {
		t.Fatal("Shuffled() after toggle off should be false")
	}

	// Current track should be preserved
	cur2, _ := p.Current()
	if cur2.Title != curTrack.Title {
		t.Fatalf("Current after unshuffle = %q, want %q", cur2.Title, curTrack.Title)
	}

	// Playback should follow sequential order from current track onward
	for i := range 4 {
		next, ok := p.Next()
		if !ok {
			t.Fatalf("Next() returned false at step %d", i)
		}
		_ = next // just verify it advances without error
	}
}

func TestToggleShuffleEmpty(t *testing.T) {
	p := New()
	p.ToggleShuffle() // should not panic
	if !p.Shuffled() {
		t.Fatal("Shuffled() should be true even when empty")
	}
}

func TestCycleRepeat(t *testing.T) {
	p := New()

	if p.Repeat() != RepeatOff {
		t.Fatalf("initial repeat = %v, want Off", p.Repeat())
	}

	p.CycleRepeat()
	if p.Repeat() != RepeatAll {
		t.Fatalf("after 1 cycle = %v, want All", p.Repeat())
	}

	p.CycleRepeat()
	if p.Repeat() != RepeatOne {
		t.Fatalf("after 2 cycles = %v, want One", p.Repeat())
	}

	p.CycleRepeat()
	if p.Repeat() != RepeatOff {
		t.Fatalf("after 3 cycles = %v, want Off", p.Repeat())
	}
}

func TestSetRepeat(t *testing.T) {
	p := New()

	p.SetRepeat(RepeatOne)
	if p.Repeat() != RepeatOne {
		t.Fatalf("Repeat() = %v, want One", p.Repeat())
	}

	p.SetRepeat(RepeatAll)
	if p.Repeat() != RepeatAll {
		t.Fatalf("Repeat() = %v, want All", p.Repeat())
	}
}

func TestShufflePreservesAllTracks(t *testing.T) {
	p := makePlaylist(10, false)
	p.SetRepeat(RepeatAll)
	p.ToggleShuffle()

	// Walk through all tracks via Next() and verify each title appears exactly once
	seen := make(map[string]bool)
	cur, _ := p.Current()
	seen[cur.Title] = true

	for i := range 9 {
		next, ok := p.Next()
		if !ok {
			t.Fatalf("Next() returned false at step %d", i)
		}
		if seen[next.Title] {
			t.Fatalf("duplicate track %q at step %d", next.Title, i)
		}
		seen[next.Title] = true
	}
	if len(seen) != 10 {
		t.Fatalf("saw %d unique tracks, want 10", len(seen))
	}
}

func TestSetTrack(t *testing.T) {
	p := makePlaylist(3, false)

	p.SetTrack(1, Track{Title: "NEW"})

	tracks := p.Tracks()
	if tracks[1].Title != "NEW" {
		t.Fatalf("tracks[1].Title = %q, want NEW", tracks[1].Title)
	}
}

func TestSetTrackOutOfBounds(t *testing.T) {
	p := makePlaylist(3, false)

	// Should be no-op, not panic
	p.SetTrack(-1, Track{Title: "X"})
	p.SetTrack(5, Track{Title: "X"})

	if p.Tracks()[0].Title != "A" {
		t.Fatal("tracks were modified by out-of-bounds SetTrack")
	}
}

func TestToggleBookmark(t *testing.T) {
	p := makePlaylist(3, false)

	p.ToggleBookmark(0)
	if !p.Tracks()[0].Bookmark {
		t.Fatal("track 0 should be bookmarked")
	}
	if p.BookmarkCount() != 1 {
		t.Fatalf("BookmarkCount() = %d, want 1", p.BookmarkCount())
	}

	p.ToggleBookmark(0) // toggle off
	if p.Tracks()[0].Bookmark {
		t.Fatal("track 0 should be unbookmarked")
	}
	if p.BookmarkCount() != 0 {
		t.Fatalf("BookmarkCount() = %d, want 0", p.BookmarkCount())
	}
}

func TestToggleBookmarkOutOfBounds(t *testing.T) {
	p := makePlaylist(3, false)

	// Should be no-op, not panic
	p.ToggleBookmark(-1)
	p.ToggleBookmark(5)

	if p.BookmarkCount() != 0 {
		t.Fatal("bookmarks were modified by out-of-bounds ToggleBookmark")
	}
}

func TestBookmarkCount(t *testing.T) {
	p := makePlaylist(5, false)

	p.ToggleBookmark(0)
	p.ToggleBookmark(2)
	p.ToggleBookmark(4)

	if got := p.BookmarkCount(); got != 3 {
		t.Fatalf("BookmarkCount() = %d, want 3", got)
	}
}

func TestBookmarkCountStaysCoherentAcrossMutations(t *testing.T) {
	p := New()
	p.Replace([]Track{
		{Title: "A", Bookmark: true},
		{Title: "B"},
		{Title: "C", Bookmark: true},
	})
	if got := p.BookmarkCount(); got != 2 {
		t.Fatalf("BookmarkCount() after Replace = %d, want 2", got)
	}

	p.Add(Track{Title: "D", Bookmark: true}, Track{Title: "E"})
	if got := p.BookmarkCount(); got != 3 {
		t.Fatalf("BookmarkCount() after Add = %d, want 3", got)
	}

	p.SetTrack(0, Track{Title: "A"})
	p.SetTrack(1, Track{Title: "B", Bookmark: true})
	if got := p.BookmarkCount(); got != 3 {
		t.Fatalf("BookmarkCount() after SetTrack = %d, want 3", got)
	}

	if !p.Move(1, 3) {
		t.Fatal("Move(1, 3) = false, want true")
	}
	if got := p.BookmarkCount(); got != 3 {
		t.Fatalf("BookmarkCount() after Move = %d, want 3", got)
	}
	snapshot := p.Snapshot()

	if !p.Remove(2) {
		t.Fatal("Remove(2) = false, want true")
	}
	if got := p.BookmarkCount(); got != 2 {
		t.Fatalf("BookmarkCount() after removing bookmark = %d, want 2", got)
	}

	p.Replace(nil)
	if got := p.BookmarkCount(); got != 0 {
		t.Fatalf("BookmarkCount() after clearing Replace = %d, want 0", got)
	}
	p.Restore(snapshot)
	if got := p.BookmarkCount(); got != 3 {
		t.Fatalf("BookmarkCount() after Restore = %d, want 3", got)
	}
}
