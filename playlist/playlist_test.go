package playlist

import (
	"testing"
)

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

	for i := 0; i < 4; i++ {
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
	for i := 0; i < 30; i++ {
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

func TestNextPreservesCurrentOnUnplayableTail(t *testing.T) {
	p := New()
	p.Replace([]Track{
		{Title: "A"},
		{Title: "B", Unplayable: true},
		{Title: "C", Unplayable: true},
	})
	p.SetIndex(0)

	if _, ok := p.Next(); ok {
		t.Fatal("Next() = true, want false")
	}

	track, idx := p.Current()
	if track.Title != "A" || idx != 0 {
		t.Fatalf("current = (%q,%d), want (\"A\",0)", track.Title, idx)
	}
}

func TestPrevPreservesCurrentOnUnplayableHead(t *testing.T) {
	p := New()
	p.Replace([]Track{
		{Title: "A", Unplayable: true},
		{Title: "B", Unplayable: true},
		{Title: "C"},
	})
	p.SetIndex(2)

	if _, ok := p.Prev(); ok {
		t.Fatal("Prev() = true, want false")
	}

	track, idx := p.Current()
	if track.Title != "C" || idx != 2 {
		t.Fatalf("current = (%q,%d), want (\"C\",2)", track.Title, idx)
	}
}

func TestPeekNextMatchesNext(t *testing.T) {
	p := New()
	p.Replace([]Track{
		{Title: "A"},
		{Title: "B", Unplayable: true},
		{Title: "C"},
	})
	p.SetIndex(0)
	p.Queue(1)
	p.Queue(2)

	peek, ok := p.PeekNext()
	if !ok {
		t.Fatal("PeekNext() = false, want true")
	}
	if peek.Title != "C" {
		t.Fatalf("peek = %q, want %q", peek.Title, "C")
	}
	cur, idx := p.Current()
	if cur.Title != "A" || idx != 0 {
		t.Fatalf("current after peek = (%q,%d), want (\"A\",0)", cur.Title, idx)
	}
	if p.QueueLen() != 2 {
		t.Fatalf("QueueLen() after peek = %d, want 2", p.QueueLen())
	}

	next, ok := p.Next()
	if !ok {
		t.Fatal("Next() = false, want true")
	}
	if next.Title != peek.Title {
		t.Fatalf("next = %q, want %q", next.Title, peek.Title)
	}
}

func TestNextConsumesUnplayableQueuedItemsOnFailure(t *testing.T) {
	p := New()
	p.Replace([]Track{
		{Title: "A"},
		{Title: "B", Unplayable: true},
	})
	p.SetIndex(0)
	p.Queue(1)

	if _, ok := p.Next(); ok {
		t.Fatal("Next() = true, want false")
	}
	cur, idx := p.Current()
	if cur.Title != "A" || idx != 0 {
		t.Fatalf("current = (%q,%d), want (\"A\",0)", cur.Title, idx)
	}
	if p.QueueLen() != 0 {
		t.Fatalf("QueueLen() = %d, want 0", p.QueueLen())
	}
}

func TestNextFailurePreservesQueuedCurrentTrack(t *testing.T) {
	p := New()
	p.Replace([]Track{
		{Title: "A"},
		{Title: "B"},
	})
	p.SetIndex(1)
	p.Queue(0)

	track, ok := p.Next()
	if !ok || track.Title != "A" {
		t.Fatalf("first Next() = (%q,%v), want (A,true)", track.Title, ok)
	}

	if _, ok := p.Next(); ok {
		t.Fatal("second Next() = true, want false")
	}
	cur, idx := p.Current()
	if cur.Title != "A" || idx != 0 {
		t.Fatalf("current after failed Next() = (%q,%d), want (A,0)", cur.Title, idx)
	}
}

func TestNextRepeatAllShuffleWrapSkipsCurrentTrack(t *testing.T) {
	p := New()
	p.shuffle = true
	p.repeat = RepeatAll
	p.Replace([]Track{
		{Title: "A"},
		{Title: "B"},
		{Title: "C", Unplayable: true},
	})
	p.order = []int{1, 2, 0}
	p.pos = 2

	track, ok := p.Next()
	if !ok {
		t.Fatal("Next() = false, want true")
	}
	if track.Title != "B" {
		t.Fatalf("next = %q, want %q", track.Title, "B")
	}
	if _, idx := p.Current(); idx != 1 {
		t.Fatalf("current index = %d, want 1", idx)
	}
}

func TestNextRepeatAllShuffleWrapFailureKeepsCurrentTrack(t *testing.T) {
	p := New()
	p.shuffle = true
	p.repeat = RepeatAll
	p.Replace([]Track{
		{Title: "A", Unplayable: true},
		{Title: "B", Unplayable: true},
		{Title: "C", Unplayable: true},
	})
	p.pos = len(p.order) - 1

	before, beforeIdx := p.Current()

	if _, ok := p.Next(); ok {
		t.Fatal("Next() = true, want false")
	}

	after, afterIdx := p.Current()
	if after.Title != before.Title || afterIdx != beforeIdx {
		t.Fatalf("current = (%q,%d), want (%q,%d)", after.Title, afterIdx, before.Title, beforeIdx)
	}
}

func TestNextRepeatOneUnplayableCurrentReturnsFalse(t *testing.T) {
	p := New()
	p.repeat = RepeatOne
	p.Replace([]Track{
		{Title: "A", Unplayable: true},
		{Title: "B"},
		{Title: "C"},
	})
	p.SetIndex(0)

	if _, ok := p.Next(); ok {
		t.Fatal("Next() = true, want false")
	}
	if track, idx := p.Current(); track.Title != "A" || idx != 0 {
		t.Fatalf("current = (%q,%d), want (\"A\",0)", track.Title, idx)
	}
}

func TestActivateSelectedUsesSelectedOrderEvenWhenQueueChangesCurrent(t *testing.T) {
	tests := []struct {
		name           string
		prepare        func(t *testing.T, p *Playlist)
		wantQueueLen   int
		wantQueueTrack int
	}{
		{
			name: "pending queued track",
			prepare: func(_ *testing.T, p *Playlist) {
				p.Queue(0)
			},
			wantQueueLen:   1,
			wantQueueTrack: 0,
		},
		{
			name: "active queued current",
			prepare: func(t *testing.T, p *Playlist) {
				p.Queue(0)
				if track, ok := p.Next(); !ok || track.Title != "Queued" {
					t.Fatalf("Next() = (%q,%t), want (\"Queued\",true)", track.Title, ok)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := New()
			p.Replace([]Track{
				{Title: "Queued"},
				{Title: "Missing", Unplayable: true},
				{Title: "Replacement"},
			})
			p.SetIndex(1)
			tt.prepare(t, p)

			activation, ok := p.ActivateSelected()
			if !ok {
				t.Fatal("ActivateSelected() = false, want true")
			}
			if activation.Track.Title != "Replacement" || activation.Index != 2 || !activation.Skipped {
				t.Fatalf("activation = (%q,%d,%t), want (\"Replacement\",2,true)", activation.Track.Title, activation.Index, activation.Skipped)
			}
			if current, idx := p.Current(); current.Title != "Replacement" || idx != 2 {
				t.Fatalf("current = (%q,%d), want (\"Replacement\",2)", current.Title, idx)
			}
			if p.QueueLen() != tt.wantQueueLen {
				t.Fatalf("QueueLen() = %d, want %d", p.QueueLen(), tt.wantQueueLen)
			}
			if tt.wantQueueLen > 0 && p.QueuePosition(tt.wantQueueTrack) != 1 {
				t.Fatalf("QueuePosition(%d) = %d, want 1", tt.wantQueueTrack, p.QueuePosition(tt.wantQueueTrack))
			}
		})
	}
}

func TestActivateSelectedWrapsWithRepeatAll(t *testing.T) {
	p := New()
	p.repeat = RepeatAll
	p.Replace([]Track{
		{Title: "A"},
		{Title: "B"},
		{Title: "C", Unplayable: true},
	})
	p.SetIndex(2)

	activation, ok := p.ActivateSelected()
	if !ok {
		t.Fatal("ActivateSelected() = false, want true")
	}
	if activation.Track.Title != "A" || activation.Index != 0 || !activation.Skipped {
		t.Fatalf("activation = (%q,%d,%t), want (\"A\",0,true)", activation.Track.Title, activation.Index, activation.Skipped)
	}
}

func TestActivateSelectedFailureKeepsQueuedCurrentTrack(t *testing.T) {
	p := New()
	p.Replace([]Track{
		{Title: "Queued"},
		{Title: "Missing", Unplayable: true},
		{Title: "Still Missing", Unplayable: true},
	})
	p.SetIndex(1)
	p.Queue(0)
	p.Queue(2)
	if track, ok := p.Next(); !ok || track.Title != "Queued" {
		t.Fatalf("Next() = (%q,%t), want (\"Queued\",true)", track.Title, ok)
	}

	if _, ok := p.ActivateSelected(); ok {
		t.Fatal("ActivateSelected() = true, want false")
	}
	if current, idx := p.Current(); current.Title != "Queued" || idx != 0 {
		t.Fatalf("current = (%q,%d), want (\"Queued\",0)", current.Title, idx)
	}
	if p.QueueLen() != 1 {
		t.Fatalf("QueueLen() = %d, want 1", p.QueueLen())
	}
	if p.QueuePosition(2) != 1 {
		t.Fatalf("QueuePosition(2) = %d, want 1", p.QueuePosition(2))
	}
}
