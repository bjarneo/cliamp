package playlist

import "testing"

func TestQueueAndDequeue(t *testing.T) {
	p := makePlaylist(5, false)

	p.Queue(2)
	p.Queue(4)

	if p.QueueLen() != 2 {
		t.Fatalf("QueueLen() = %d, want 2", p.QueueLen())
	}

	// Dequeue first item
	if !p.Dequeue(2) {
		t.Fatal("Dequeue(2) = false, want true")
	}
	if p.QueueLen() != 1 {
		t.Fatalf("QueueLen() after dequeue = %d, want 1", p.QueueLen())
	}

	// Dequeue non-existent
	if p.Dequeue(2) {
		t.Fatal("Dequeue(2) again = true, want false")
	}
}

func TestQueuePosition(t *testing.T) {
	p := makePlaylist(5, false)

	p.Queue(1)
	p.Queue(3)
	p.Queue(4)

	if pos := p.QueuePosition(1); pos != 1 {
		t.Errorf("QueuePosition(1) = %d, want 1", pos)
	}
	if pos := p.QueuePosition(3); pos != 2 {
		t.Errorf("QueuePosition(3) = %d, want 2", pos)
	}
	if pos := p.QueuePosition(4); pos != 3 {
		t.Errorf("QueuePosition(4) = %d, want 3", pos)
	}
	if pos := p.QueuePosition(0); pos != 0 {
		t.Errorf("QueuePosition(0) = %d, want 0 (not queued)", pos)
	}
}

func TestQueueTracks(t *testing.T) {
	p := makePlaylist(5, false)

	p.Queue(0) // A
	p.Queue(2) // C
	p.Queue(4) // E

	qt := p.QueueTracks()
	if len(qt) != 3 {
		t.Fatalf("QueueTracks len = %d, want 3", len(qt))
	}
	if qt[0].Title != "A" || qt[1].Title != "C" || qt[2].Title != "E" {
		t.Errorf("QueueTracks = [%s, %s, %s], want [A, C, E]",
			qt[0].Title, qt[1].Title, qt[2].Title)
	}
}

func TestQueueWindow(t *testing.T) {
	p := makePlaylist(5, false)
	for i := range 5 {
		p.Queue(i)
	}

	tests := []struct {
		name       string
		start      int
		limit      int
		wantTitles string
	}{
		{name: "middle", start: 1, limit: 2, wantTitles: "BC"},
		{name: "negative start", start: -2, limit: 2, wantTitles: "AB"},
		{name: "limit past end", start: 3, limit: 10, wantTitles: "DE"},
		{name: "start at end", start: 5, limit: 1},
		{name: "zero limit", start: 0, limit: 0},
		{name: "negative limit", start: 0, limit: -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tracks := p.QueueWindow(tt.start, tt.limit)
			got := ""
			for _, track := range tracks {
				got += track.Title
			}
			if got != tt.wantTitles {
				t.Fatalf("QueueWindow(%d, %d) titles = %q, want %q", tt.start, tt.limit, got, tt.wantTitles)
			}
		})
	}

	window := p.QueueWindow(1, 1)
	window[0].Title = "changed"
	if got := p.QueueWindow(1, 1)[0].Title; got != "B" {
		t.Fatalf("mutating QueueWindow result changed playlist title to %q", got)
	}
}

func TestQueueOutputsOwnProviderMeta(t *testing.T) {
	p := New()
	p.Replace([]Track{{ProviderMeta: map[string]string{"provider.id": "original"}}})
	p.Queue(0)

	window := p.QueueWindow(0, 1)
	window[0].ProviderMeta["provider.id"] = "window"
	queued := p.QueueTracks()
	queued[0].ProviderMeta["provider.id"] = "queue"

	if got := p.Tracks()[0].ProviderMeta["provider.id"]; got != "original" {
		t.Fatalf("metadata after queue output mutation = %q, want original", got)
	}
}

func TestQueuePositionsStayCoherentAcrossMutations(t *testing.T) {
	p := makePlaylist(5, false)
	p.Queue(1)
	p.Queue(3)
	p.Queue(1)
	p.Queue(4)
	assertQueuePositions(t, p, map[int]int{1: 1, 3: 2, 4: 4})

	if !p.MoveQueue(3, 0) {
		t.Fatal("MoveQueue(3, 0) = false, want true")
	}
	assertQueuePositions(t, p, map[int]int{1: 3, 3: 2, 4: 1})

	p.RemoveQueueAt(1)
	assertQueuePositions(t, p, map[int]int{1: 2, 4: 1})

	if !p.Dequeue(1) {
		t.Fatal("Dequeue(1) = false, want true")
	}
	assertQueuePositions(t, p, map[int]int{1: 2, 4: 1})

	if !p.Move(1, 2) {
		t.Fatal("Move(1, 2) = false, want true")
	}
	assertQueuePositions(t, p, map[int]int{2: 2, 4: 1})
	snapshot := p.Snapshot()

	if !p.Remove(0) {
		t.Fatal("Remove(0) = false, want true")
	}
	assertQueuePositions(t, p, map[int]int{1: 2, 3: 1})

	p.ClearQueue()
	assertQueuePositions(t, p, nil)
	p.Restore(snapshot)
	assertQueuePositions(t, p, map[int]int{2: 2, 4: 1})

	if track, ok := p.Next(); !ok || track.Title != "E" {
		t.Fatalf("Next() = (%q, %t), want (E, true)", track.Title, ok)
	}
	assertQueuePositions(t, p, map[int]int{2: 1})

	p.Replace([]Track{{Title: "replacement"}})
	assertQueuePositions(t, p, nil)
}

func assertQueuePositions(t *testing.T, p *Playlist, want map[int]int) {
	t.Helper()
	for idx := range p.Len() {
		if got := p.QueuePosition(idx); got != want[idx] {
			t.Fatalf("QueuePosition(%d) = %d, want %d", idx, got, want[idx])
		}
	}
}

func TestRestoreSnapshotRestoresTracksAndQueue(t *testing.T) {
	p := makePlaylist(3, false)
	p.Queue(0)
	p.Queue(2)
	snapshot := p.Snapshot()

	p.Remove(0)
	p.ClearQueue()
	p.Restore(snapshot)

	tracks := p.Tracks()
	if len(tracks) != 3 || tracks[0].Title != "A" {
		t.Fatalf("tracks after restore = %#v, want original tracks", tracks)
	}
	queued := p.QueueTracks()
	if len(queued) != 2 || queued[0].Title != "A" || queued[1].Title != "C" {
		t.Fatalf("queue after restore = %#v, want [A C]", queued)
	}
}

func TestClearQueue(t *testing.T) {
	p := makePlaylist(3, false)
	p.Queue(0)
	p.Queue(1)

	p.ClearQueue()

	if p.QueueLen() != 0 {
		t.Fatalf("QueueLen() after clear = %d, want 0", p.QueueLen())
	}
}

func TestRemoveQueueAt(t *testing.T) {
	p := makePlaylist(5, false)
	p.Queue(0) // A
	p.Queue(2) // C
	p.Queue(4) // E

	// Remove middle entry (C)
	p.RemoveQueueAt(1)

	qt := p.QueueTracks()
	if len(qt) != 2 {
		t.Fatalf("QueueTracks after remove = %d, want 2", len(qt))
	}
	if qt[0].Title != "A" || qt[1].Title != "E" {
		t.Errorf("QueueTracks = [%s, %s], want [A, E]", qt[0].Title, qt[1].Title)
	}
}

func TestRemoveQueueAtBounds(t *testing.T) {
	p := makePlaylist(3, false)
	p.Queue(0)

	// Out of bounds should be no-op
	p.RemoveQueueAt(-1)
	p.RemoveQueueAt(5)

	if p.QueueLen() != 1 {
		t.Fatalf("QueueLen() = %d, want 1", p.QueueLen())
	}
}

func TestQueueBoundsCheck(t *testing.T) {
	p := makePlaylist(3, false)

	// Queue out-of-bounds track indices
	p.Queue(-1)
	p.Queue(5)

	if p.QueueLen() != 0 {
		t.Fatalf("QueueLen() = %d, want 0 (invalid indices ignored)", p.QueueLen())
	}
}
