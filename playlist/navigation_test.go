package playlist

import "testing"

func TestNextRepeatOff(t *testing.T) {
	p := makePlaylist(3, false) // A B C

	// Advance through all tracks
	track, ok := p.Next()
	if !ok || track.Title != "B" {
		t.Fatalf("Next() = (%q, %v), want (B, true)", track.Title, ok)
	}

	track, ok = p.Next()
	if !ok || track.Title != "C" {
		t.Fatalf("Next() = (%q, %v), want (C, true)", track.Title, ok)
	}

	// Past the end with RepeatOff
	_, ok = p.Next()
	if ok {
		t.Fatal("Next() past end should return false with RepeatOff")
	}
}

func TestNextRepeatAll(t *testing.T) {
	p := makePlaylist(3, false) // A B C
	p.SetRepeat(RepeatAll)

	// Advance to end
	p.Next() // B
	p.Next() // C

	// Should wrap around
	track, ok := p.Next()
	if !ok || track.Title != "A" {
		t.Fatalf("Next() wrap = (%q, %v), want (A, true)", track.Title, ok)
	}
}

func TestNextRepeatOne(t *testing.T) {
	p := makePlaylist(3, false) // A B C
	p.SetRepeat(RepeatOne)

	// Should repeat the same track
	track, ok := p.Next()
	if !ok || track.Title != "A" {
		t.Fatalf("Next() RepeatOne = (%q, %v), want (A, true)", track.Title, ok)
	}

	track, ok = p.Next()
	if !ok || track.Title != "A" {
		t.Fatalf("Next() RepeatOne again = (%q, %v), want (A, true)", track.Title, ok)
	}
}

func TestNextWithQueue(t *testing.T) {
	p := makePlaylist(4, false) // A B C D
	p.Queue(2)                  // Queue C
	p.Queue(3)                  // Queue D

	// Queue should be played first
	track, ok := p.Next()
	if !ok || track.Title != "C" {
		t.Fatalf("Next() from queue = (%q, %v), want (C, true)", track.Title, ok)
	}

	track, ok = p.Next()
	if !ok || track.Title != "D" {
		t.Fatalf("Next() from queue = (%q, %v), want (D, true)", track.Title, ok)
	}

	// Queue exhausted, resume normal order
	track, ok = p.Next()
	if !ok || track.Title != "B" {
		t.Fatalf("Next() after queue = (%q, %v), want (B, true)", track.Title, ok)
	}
}

func TestPrevBasic(t *testing.T) {
	p := makePlaylist(3, false)
	p.SetIndex(2) // C

	track, ok := p.Prev()
	if !ok || track.Title != "B" {
		t.Fatalf("Prev() = (%q, %v), want (B, true)", track.Title, ok)
	}

	track, ok = p.Prev()
	if !ok || track.Title != "A" {
		t.Fatalf("Prev() = (%q, %v), want (A, true)", track.Title, ok)
	}

	// At the beginning with RepeatOff
	_, ok = p.Prev()
	if ok {
		t.Fatal("Prev() at beginning should return false with RepeatOff")
	}
}

func TestPrevRepeatAll(t *testing.T) {
	p := makePlaylist(3, false)
	p.SetRepeat(RepeatAll)

	// At position 0, should wrap to end
	track, ok := p.Prev()
	if !ok || track.Title != "C" {
		t.Fatalf("Prev() wrap = (%q, %v), want (C, true)", track.Title, ok)
	}
}

func TestPeekNextBasic(t *testing.T) {
	p := makePlaylist(3, false)

	track, ok := p.PeekNext()
	if !ok || track.Title != "B" {
		t.Fatalf("PeekNext() = (%q, %v), want (B, true)", track.Title, ok)
	}

	// PeekNext should NOT advance the position
	cur, _ := p.Current()
	if cur.Title != "A" {
		t.Fatalf("Current after PeekNext = %q, want A", cur.Title)
	}
}

func TestPeekNextAtEnd(t *testing.T) {
	p := makePlaylist(3, false)
	p.SetIndex(2) // C

	// RepeatOff at end
	_, ok := p.PeekNext()
	if ok {
		t.Fatal("PeekNext() at end with RepeatOff should return false")
	}
}

func TestPeekNextRepeatAll(t *testing.T) {
	p := makePlaylist(3, false)
	p.SetRepeat(RepeatAll)
	p.SetIndex(2) // C

	track, ok := p.PeekNext()
	if !ok || track.Title != "A" {
		t.Fatalf("PeekNext() RepeatAll at end = (%q, %v), want (A, true)", track.Title, ok)
	}
}

func TestPeekNextRepeatOne(t *testing.T) {
	p := makePlaylist(3, false)
	p.SetRepeat(RepeatOne)
	p.SetIndex(1) // B

	track, ok := p.PeekNext()
	if !ok || track.Title != "B" {
		t.Fatalf("PeekNext() RepeatOne = (%q, %v), want (B, true)", track.Title, ok)
	}
}

func TestPeekNextWithQueue(t *testing.T) {
	p := makePlaylist(3, false)
	p.Queue(2) // Queue C

	track, ok := p.PeekNext()
	if !ok || track.Title != "C" {
		t.Fatalf("PeekNext() with queue = (%q, %v), want (C, true)", track.Title, ok)
	}
}

func TestCurrentEmpty(t *testing.T) {
	p := New()

	_, idx := p.Current()
	if idx != -1 {
		t.Fatalf("Current() on empty = %d, want -1", idx)
	}
}

func TestIndexEmpty(t *testing.T) {
	p := New()

	if idx := p.Index(); idx != -1 {
		t.Fatalf("Index() on empty = %d, want -1", idx)
	}
}

func TestNextEmpty(t *testing.T) {
	p := New()

	_, ok := p.Next()
	if ok {
		t.Fatal("Next() on empty should return false")
	}
}

func TestPrevEmpty(t *testing.T) {
	p := New()

	_, ok := p.Prev()
	if ok {
		t.Fatal("Prev() on empty should return false")
	}
}

func TestSetIndex(t *testing.T) {
	p := makePlaylist(5, false)
	p.SetIndex(3) // D

	cur, idx := p.Current()
	if idx != 3 || cur.Title != "D" {
		t.Fatalf("Current() = (%q, %d), want (D, 3)", cur.Title, idx)
	}
}

func TestReplace(t *testing.T) {
	p := makePlaylist(3, false)
	p.SetIndex(2)

	// Replace with new tracks
	newTracks := []Track{
		{Title: "X"},
		{Title: "Y"},
	}
	p.Replace(newTracks)

	if p.Len() != 2 {
		t.Fatalf("Len() = %d, want 2", p.Len())
	}
	cur, idx := p.Current()
	if idx != 0 || cur.Title != "X" {
		t.Fatalf("Current() = (%q, %d), want (X, 0)", cur.Title, idx)
	}
}
