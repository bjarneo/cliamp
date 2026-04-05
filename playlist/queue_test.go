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
