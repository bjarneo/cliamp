package playlist

import "testing"

// helper builds a playlist with n tracks named "A", "B", "C", ...
func makePlaylist(n int, shuffle bool) *Playlist {
	tracks := make([]Track, n)
	for i := range tracks {
		tracks[i] = Track{Title: string(rune('A' + i))}
	}
	p := New()
	if shuffle {
		p.shuffle = true
	}
	p.Replace(tracks)
	return p
}

func titles(p *Playlist) []string {
	out := make([]string, p.Len())
	for i, t := range p.Tracks() {
		out[i] = t.Title
	}
	return out
}

func sliceEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestMoveDown(t *testing.T) {
	p := makePlaylist(5, false) // A B C D E
	p.SetIndex(0)               // playing A

	if !p.Move(1, 2) {
		t.Fatal("Move returned false")
	}

	// Visual order: A C B D E
	got := titles(p)
	want := []string{"A", "C", "B", "D", "E"}
	if !sliceEq(got, want) {
		t.Errorf("tracks = %v, want %v", got, want)
	}

	// Still playing A
	if _, idx := p.Current(); idx != 0 {
		t.Errorf("current index = %d, want 0", idx)
	}
}

func TestMoveUp(t *testing.T) {
	p := makePlaylist(5, false) // A B C D E
	p.SetIndex(3)               // playing D

	if !p.Move(3, 2) {
		t.Fatal("Move returned false")
	}

	// Visual order: A B D C E
	got := titles(p)
	want := []string{"A", "B", "D", "C", "E"}
	if !sliceEq(got, want) {
		t.Errorf("tracks = %v, want %v", got, want)
	}

	// Still playing D, now at index 2
	if _, idx := p.Current(); idx != 2 {
		t.Errorf("current index = %d, want 2", idx)
	}
}

func TestMoveCurrentTrack(t *testing.T) {
	p := makePlaylist(4, false) // A B C D
	p.SetIndex(1)               // playing B

	// Move B down (from 1 to 2)
	if !p.Move(1, 2) {
		t.Fatal("Move returned false")
	}

	// Visual: A C B D
	got := titles(p)
	want := []string{"A", "C", "B", "D"}
	if !sliceEq(got, want) {
		t.Errorf("tracks = %v, want %v", got, want)
	}

	// Still playing B, now at index 2
	if _, idx := p.Current(); idx != 2 {
		t.Errorf("current index = %d, want 2", idx)
	}
}

func TestMoveBoundary(t *testing.T) {
	p := makePlaylist(3, false)

	// Can't move first track up
	if p.Move(0, -1) {
		t.Error("Move(0, -1) should return false")
	}

	// Can't move last track down
	if p.Move(2, 3) {
		t.Error("Move(2, 3) should return false")
	}

	// Same position is a no-op
	if p.Move(1, 1) {
		t.Error("Move(1, 1) should return false")
	}
}

func TestMovePreservesPlaybackOrder_NoShuffle(t *testing.T) {
	p := makePlaylist(5, false) // A B C D E
	p.SetIndex(0)

	// Move C (2) up to (1): A C B D E
	p.Move(2, 1)

	// Playback should follow new visual order: A C B D E
	var playback []string
	track, _ := p.Current()
	playback = append(playback, track.Title) // A

	for i := range 4 {
		track, ok := p.Next()
		if !ok {
			t.Fatalf("Next() returned false at step %d", i)
		}
		playback = append(playback, track.Title)
	}

	want := []string{"A", "C", "B", "D", "E"}
	if !sliceEq(playback, want) {
		t.Errorf("playback = %v, want %v", playback, want)
	}
}

func TestMoveWithQueue(t *testing.T) {
	p := makePlaylist(4, false) // A B C D
	p.Queue(2)                  // queue C (index 2)

	// Move C (2) up to (1): A C B D, queue should now reference index 1
	p.Move(2, 1)

	if pos := p.QueuePosition(1); pos != 1 {
		t.Errorf("QueuePosition(1) = %d, want 1", pos)
	}
	if pos := p.QueuePosition(2); pos != 0 {
		t.Errorf("QueuePosition(2) = %d, want 0 (not queued)", pos)
	}
}

func TestMoveShuffle(t *testing.T) {
	p := makePlaylist(5, true) // shuffled

	// Record the current shuffle playback order
	p.SetIndex(p.order[0])
	// Snapshot the order
	orderBefore := make([]int, len(p.order))
	copy(orderBefore, p.order)

	// Move tracks[1] to tracks[0]
	t0 := p.tracks[0].Title
	t1 := p.tracks[1].Title
	p.Move(1, 0)

	// Visual order should be swapped at 0,1
	if p.tracks[0].Title != t1 || p.tracks[1].Title != t0 {
		t.Errorf("tracks[0]=%s tracks[1]=%s, want %s %s",
			p.tracks[0].Title, p.tracks[1].Title, t1, t0)
	}

	// The shuffle order should still reference the same tracks
	for i, idx := range p.order {
		got := p.tracks[idx].Title
		want := ""
		oldIdx := orderBefore[i]
		// The old index pointed to a track; after swap, find where it went
		if oldIdx == 0 {
			want = t0
		} else if oldIdx == 1 {
			want = t1
		} else {
			want = string(rune('A' + oldIdx))
		}
		if got != want {
			t.Errorf("order[%d]: track=%s, want=%s", i, got, want)
		}
	}
}

func TestAddShufflesNewTracksWhenShuffleEnabled(t *testing.T) {
	p := makePlaylist(10, true)
	p.SetIndex(p.order[0]) // ensure pos is valid and stable
	cur, curIdx := p.Current()

	start := p.Len()
	var added []Track
	for i := range 30 {
		added = append(added, Track{Title: string(rune('K' + i))})
	}
	p.Add(added...)

	// Current track should be unchanged.
	cur2, curIdx2 := p.Current()
	if cur2.Title != cur.Title || curIdx2 != curIdx {
		t.Fatalf("current = (%q,%d), want (%q,%d)", cur2.Title, curIdx2, cur.Title, curIdx)
	}

	// Verify that added tracks are interleaved with existing upcoming tracks,
	// not just shuffled among themselves at the tail.
	upcoming := p.order[p.pos+1:]
	isNew := func(idx int) bool { return idx >= start }
	// Find the last new-track position and check that at least one
	// old track appears after some new track in the upcoming order.
	lastNew := -1
	foundOldAfterNew := false
	for i, idx := range upcoming {
		if isNew(idx) {
			lastNew = i
		} else if lastNew >= 0 {
			foundOldAfterNew = true
			break
		}
	}
	if lastNew < 0 {
		t.Fatal("no added track found in upcoming order")
	}
	if !foundOldAfterNew && lastNew < len(upcoming)-1 {
		t.Fatalf("added tracks are not interleaved with existing tracks in upcoming order: %v", upcoming)
	}
}

func TestAddDoesNotShuffleWhenShuffleDisabled(t *testing.T) {
	p := makePlaylist(5, false)
	p.SetIndex(2)
	cur, curIdx := p.Current()

	p.Add(Track{Title: "F"}, Track{Title: "G"})

	cur2, curIdx2 := p.Current()
	if cur2.Title != cur.Title || curIdx2 != curIdx {
		t.Fatalf("current = (%q,%d), want (%q,%d)", cur2.Title, curIdx2, cur.Title, curIdx)
	}

	wantOrder := []int{0, 1, 2, 3, 4, 5, 6}
	if len(p.order) != len(wantOrder) {
		t.Fatalf("order len = %d, want %d", len(p.order), len(wantOrder))
	}
	for i := range wantOrder {
		if p.order[i] != wantOrder[i] {
			t.Fatalf("order[%d] = %d, want %d (order=%v)", i, p.order[i], wantOrder[i], p.order)
		}
	}
}

func TestMoveQueue(t *testing.T) {
	p := makePlaylist(5, false) // A B C D E
	p.Queue(3)                  // D
	p.Queue(1)                  // B
	p.Queue(4)                  // E
	// Queue order: D, B, E

	// Move B (pos 1) up to pos 0
	if !p.MoveQueue(1, 0) {
		t.Fatal("MoveQueue returned false")
	}
	// Queue order should be: B, D, E
	qt := p.QueueTracks()
	if len(qt) != 3 {
		t.Fatalf("queue len = %d, want 3", len(qt))
	}
	if qt[0].Title != "B" || qt[1].Title != "D" || qt[2].Title != "E" {
		t.Errorf("queue = [%s %s %s], want [B D E]", qt[0].Title, qt[1].Title, qt[2].Title)
	}

	// Move B (pos 0) down to pos 1
	if !p.MoveQueue(0, 1) {
		t.Fatal("MoveQueue returned false")
	}
	// Queue order should be: D, B, E
	qt = p.QueueTracks()
	if qt[0].Title != "D" || qt[1].Title != "B" || qt[2].Title != "E" {
		t.Errorf("queue = [%s %s %s], want [D B E]", qt[0].Title, qt[1].Title, qt[2].Title)
	}
}

func TestMoveQueueBoundary(t *testing.T) {
	p := makePlaylist(3, false)
	p.Queue(0)
	p.Queue(1)

	// Can't move beyond bounds
	if p.MoveQueue(0, -1) {
		t.Error("MoveQueue(0, -1) should return false")
	}
	if p.MoveQueue(1, 2) {
		t.Error("MoveQueue(1, 2) should return false")
	}
	if p.MoveQueue(0, 0) {
		t.Error("MoveQueue(0, 0) should return false")
	}
}
