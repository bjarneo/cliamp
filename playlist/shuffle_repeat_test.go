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

func TestShuffledAddPreservesPrefixAndContainsAllTracks(t *testing.T) {
	p := makePlaylist(10, true)
	// Advance twice so p.pos == 2 and prefix [0..2] must stay untouched.
	p.Next()
	p.Next()

	p.mu.Lock()
	prefixBefore := append([]int(nil), p.order[:p.pos+1]...)
	p.mu.Unlock()

	for i := range 20 {
		p.Add(Track{Path: "/added", Title: string(rune('a' + i))})
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.order) != 30 {
		t.Fatalf("len(order) = %d, want 30", len(p.order))
	}
	for i, want := range prefixBefore {
		if p.order[i] != want {
			t.Fatalf("prefix changed at %d: got %d, want %d", i, p.order[i], want)
		}
	}
	seen := make([]bool, 30)
	for _, idx := range p.order {
		if idx < 0 || idx >= 30 || seen[idx] {
			t.Fatalf("invalid or duplicate index %d in order %v", idx, p.order)
		}
		seen[idx] = true
	}
}

func TestPeekNextAgreesWithShuffleWrapWhenQueueOnlyHasUnplayableTracks(t *testing.T) {
	p := New()
	p.Add(
		Track{Path: "a.mp3", Title: "A"},
		Track{Path: "b.mp3", Title: "B"},
		Track{Path: "c.mp3", Title: "C", Unplayable: true},
	)
	p.SetRepeat(RepeatAll)
	p.ToggleShuffle()

	// Move to the last slot in shuffle order and queue only an unplayable track.
	p.mu.Lock()
	p.pos = len(p.order) - 1
	p.queuedIdx = -1
	p.mu.Unlock()
	p.Queue(2)

	if _, ok := p.PeekNext(); ok {
		t.Fatal("PeekNext() = true at shuffle wrap with only unplayable queued tracks, want false")
	}
}
