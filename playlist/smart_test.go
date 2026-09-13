package playlist

import (
	"slices"
	"testing"
)

// orderPosOf returns the order slot holding track index idx, or -1.
func orderPosOf(p *Playlist, idx int) int {
	return slices.Index(p.order, idx)
}

func TestSmartFlagToggle(t *testing.T) {
	p := makePlaylist(3, false)

	if p.Smart() {
		t.Fatal("Smart() initially should be false")
	}
	p.EnableSmart()
	if !p.Smart() {
		t.Fatal("Smart() after EnableSmart should be true")
	}

	// DisableSmart with no Smart rows only clears the flag.
	p.DisableSmart()
	if p.Smart() {
		t.Fatal("Smart() after DisableSmart should be false")
	}
	if got := p.Len(); got != 3 {
		t.Fatalf("Len() = %d, want 3 (DisableSmart must not touch regular rows)", got)
	}
	if got := p.SmartPending(); got != 0 {
		t.Fatalf("SmartPending() = %d, want 0", got)
	}
}

func TestAddSmartNoopWhenNotShuffled(t *testing.T) {
	p := makePlaylist(3, false) // A B C
	p.SetIndex(1)

	p.EnableSmart()
	p.AddSmart(Track{Title: "X"}, Track{Title: "Y"})

	if got := p.Len(); got != 3 {
		t.Fatalf("Len() = %d, want 3 (AddSmart is a full no-op without shuffle)", got)
	}
	for i, tr := range p.Tracks() {
		if tr.Smart {
			t.Errorf("track %d marked Smart, want unmarked", i)
		}
	}
	if got := p.SmartPending(); got != 0 {
		t.Fatalf("SmartPending() = %d, want 0", got)
	}
	if got := len(p.order); got != 3 {
		t.Fatalf("order len = %d, want 3", got)
	}
}

func TestAddSmartInsertsIntoUpcomingTail(t *testing.T) {
	p := makePlaylist(10, true)
	p.SetIndex(p.order[0])
	cur, curIdx := p.Current()

	p.EnableSmart()
	start := p.Len()
	p.AddSmart(Track{Title: "S1"}, Track{Title: "S2"}, Track{Title: "S3"})

	if got := p.Len(); got != start+3 {
		t.Fatalf("Len() = %d, want %d", got, start+3)
	}
	if got := p.SmartPending(); got != 3 {
		t.Fatalf("SmartPending() = %d, want 3", got)
	}

	// Current track unchanged.
	cur2, curIdx2 := p.Current()
	if cur2.Title != cur.Title || curIdx2 != curIdx {
		t.Fatalf("current = (%q,%d), want (%q,%d)", cur2.Title, curIdx2, cur.Title, curIdx)
	}

	// New Smart rows appear only after the current position, never before it
	// and never at it.
	for pos := 0; pos <= p.pos; pos++ {
		if idx := p.order[pos]; idx >= start {
			t.Fatalf("smart track %d at played order slot %d (pos=%d)", idx, pos, p.pos)
		}
	}
	smartSlots := 0
	for _, idx := range p.order[p.pos+1:] {
		if idx >= start {
			smartSlots++
			if !p.tracks[idx].Smart {
				t.Errorf("track %d injected but not marked Smart", idx)
			}
		}
	}
	if smartSlots != 3 {
		t.Fatalf("smart rows in upcoming order = %d, want 3", smartSlots)
	}

	// Old upcoming entries stay ahead of the injection point relative to the
	// played part: nothing before the current position changed.
	played := slices.Clone(p.order[:p.pos+1])
	for pos, idx := range played {
		if idx >= start {
			t.Fatalf("played order mutated at slot %d", pos)
		}
	}
}

func TestSmartPendingCountsOnlyUnplayed(t *testing.T) {
	p := makePlaylist(5, true)
	p.SetIndex(p.order[0])

	p.EnableSmart()
	p.AddSmart(Track{Title: "S1"}, Track{Title: "S2"}, Track{Title: "S3"}, Track{Title: "S4"})
	if got := p.SmartPending(); got != 4 {
		t.Fatalf("SmartPending() = %d, want 4", got)
	}

	// Advance one step: whatever Smart row (if any) sat at the slot we leave
	// behind is no longer pending.
	p.Next()
	want := 0
	for _, idx := range p.order[p.pos+1:] {
		if p.tracks[idx].Smart {
			want++
		}
	}
	if got := p.SmartPending(); got != want {
		t.Fatalf("SmartPending() = %d, want %d after one Next", got, want)
	}
}

func TestDisableSmartRemovesUnplayedKeepsPlayed(t *testing.T) {
	p := makePlaylist(6, true)
	p.SetIndex(p.order[0])

	p.EnableSmart()
	p.AddSmart(Track{Title: "S1"}, Track{Title: "S2"}, Track{Title: "S3"})

	// Play until at least one Smart row is behind the current position.
	for range 50 {
		if _, ok := p.Next(); !ok {
			t.Fatal("Next() failed before any smart row played")
		}
		if slices.ContainsFunc(p.order[:p.pos], func(idx int) bool { return p.tracks[idx].Smart }) {
			break
		}
	}
	playedSmart := []string{}
	for _, idx := range p.order[:p.pos] {
		if p.tracks[idx].Smart {
			playedSmart = append(playedSmart, p.tracks[idx].Title)
		}
	}
	if len(playedSmart) == 0 {
		t.Fatal("test setup failed to play any smart row")
	}

	cur, _ := p.Current()
	curTitle := cur.Title
	// Smart rows at or before the current position are the played/current
	// ones DisableSmart must keep.
	keptSmart := slices.Clone(playedSmart)
	if cur.Smart {
		keptSmart = append(keptSmart, curTitle)
	}
	p.DisableSmart()

	if p.Smart() {
		t.Fatal("Smart() after DisableSmart should be false")
	}
	if got := p.SmartPending(); got != 0 {
		t.Fatalf("SmartPending() = %d, want 0 after DisableSmart", got)
	}

	// Played Smart rows survive (still marked); unplayed ones are gone.
	remaining := titles(p)
	for _, title := range keptSmart {
		if !slices.Contains(remaining, title) {
			t.Errorf("played smart row %q was removed, want kept", title)
		}
	}
	for _, tr := range p.Tracks() {
		if tr.Smart && !slices.Contains(keptSmart, tr.Title) {
			t.Errorf("unplayed smart row %q survived DisableSmart", tr.Title)
		}
	}

	// Current track survives the removal fixups.
	cur2, _ := p.Current()
	if cur2.Title != curTitle {
		t.Fatalf("current = %q after DisableSmart, want %q", cur2.Title, curTitle)
	}

	// The order is a valid permutation of the surviving track indices.
	seen := make(map[int]bool, len(p.order))
	for _, idx := range p.order {
		if idx < 0 || idx >= len(p.tracks) || seen[idx] {
			t.Fatalf("order not a valid permutation after removals: %v (tracks=%d)", p.order, len(p.tracks))
		}
		seen[idx] = true
	}
}

func TestSmartSnapshotRestoreRoundtrip(t *testing.T) {
	p := makePlaylist(8, true)
	p.SetIndex(p.order[0])

	p.EnableSmart()
	p.AddSmart(Track{Title: "S1"}, Track{Title: "S2"})

	// Play one smart row so the snapshot mixes played and pending rows.
	for range 50 {
		if _, ok := p.Next(); !ok {
			t.Fatal("Next() failed during setup")
		}
		if slices.ContainsFunc(p.order[:p.pos], func(idx int) bool { return p.tracks[idx].Smart }) {
			break
		}
	}

	snap := p.Snapshot()
	pendingBefore := p.SmartPending()
	orderBefore := slices.Clone(p.order)
	tracksBefore := titles(p)
	marksBefore := make([]bool, len(p.tracks))
	for i, tr := range p.Tracks() {
		marksBefore[i] = tr.Smart
	}

	// Mutate after the snapshot: wipe smart state and inject more.
	p.DisableSmart()
	p.AddSmart(Track{Title: "S3"})

	p.Restore(snap)

	if !p.Smart() {
		t.Fatal("Smart flag lost in snapshot roundtrip")
	}
	if got := p.SmartPending(); got != pendingBefore {
		t.Fatalf("SmartPending() = %d after restore, want %d", got, pendingBefore)
	}
	if got := titles(p); !slices.Equal(got, tracksBefore) {
		t.Fatalf("tracks = %v after restore, want %v", got, tracksBefore)
	}
	for i, tr := range p.Tracks() {
		if tr.Smart != marksBefore[i] {
			t.Errorf("track %d Smart = %v after restore, want %v", i, tr.Smart, marksBefore[i])
		}
	}
	if !slices.Equal(p.order, orderBefore) {
		t.Fatalf("order = %v after restore, want %v", p.order, orderBefore)
	}
}

func TestSmartMethodsOnEmptyPlaylist(t *testing.T) {
	p := New()

	p.EnableSmart()
	p.AddSmart(Track{Title: "S1"}) // no shuffle: no-op anyway
	p.ToggleShuffle()              // empty playlist: shuffle flips, order stays empty
	p.DisableSmart()

	if got := p.Len(); got != 0 {
		t.Fatalf("Len() = %d, want 0", got)
	}
	if got := p.SmartPending(); got != 0 {
		t.Fatalf("SmartPending() = %d, want 0", got)
	}
}

func TestRemainingTracksTail(t *testing.T) {
	p := New()
	p.Add(Track{Path: "a"}, Track{Path: "b"}, Track{Path: "c"}, Track{Path: "d"})
	if got := p.Remaining(); got != 3 {
		t.Fatalf("Remaining() before advancing = %d, want 3", got)
	}
	p.Next()
	p.Next()
	if got := p.Remaining(); got != 1 {
		t.Fatalf("Remaining() mid-playback = %d, want 1", got)
	}
	p.Remove(3)
	if got := p.Remaining(); got != 0 {
		t.Fatalf("Remaining() at the end = %d, want 0", got)
	}
}
