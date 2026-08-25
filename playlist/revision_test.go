package playlist

import "testing"

func TestRevisionIncrementsForStateChanges(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*Playlist) func()
	}{
		{
			name: "add",
			setup: func(p *Playlist) func() {
				return func() { p.Add(Track{Title: "A"}) }
			},
		},
		{
			name: "replace",
			setup: func(p *Playlist) func() {
				p.Add(Track{Title: "A"})
				return func() { p.Replace([]Track{{Title: "B"}}) }
			},
		},
		{
			name: "activate selected queued track",
			setup: func(p *Playlist) func() {
				p.Add(Track{Title: "A"}, Track{Title: "B"})
				p.Queue(1)
				p.Next()
				return func() { p.ActivateSelected() }
			},
		},
		{
			name: "next",
			setup: func(p *Playlist) func() {
				p.Add(Track{Title: "A"}, Track{Title: "B"})
				return func() { p.Next() }
			},
		},
		{
			name: "next clears unavailable queue",
			setup: func(p *Playlist) func() {
				p.Add(Track{Title: "A"}, Track{Title: "B", Unplayable: true})
				p.Queue(1)
				return func() { p.Next() }
			},
		},
		{
			name: "prev",
			setup: func(p *Playlist) func() {
				p.Add(Track{Title: "A"}, Track{Title: "B"})
				p.SetIndex(1)
				return func() { p.Prev() }
			},
		},
		{
			name: "set index",
			setup: func(p *Playlist) func() {
				p.Add(Track{Title: "A"}, Track{Title: "B"})
				return func() { p.SetIndex(1) }
			},
		},
		{
			name: "queue",
			setup: func(p *Playlist) func() {
				p.Add(Track{Title: "A"})
				return func() { p.Queue(0) }
			},
		},
		{
			name: "dequeue",
			setup: func(p *Playlist) func() {
				p.Add(Track{Title: "A"})
				p.Queue(0)
				return func() { p.Dequeue(0) }
			},
		},
		{
			name: "remove queue entry",
			setup: func(p *Playlist) func() {
				p.Add(Track{Title: "A"})
				p.Queue(0)
				return func() { p.RemoveQueueAt(0) }
			},
		},
		{
			name: "move queue entry",
			setup: func(p *Playlist) func() {
				p.Add(Track{Title: "A"}, Track{Title: "B"})
				p.Queue(0)
				p.Queue(1)
				return func() { p.MoveQueue(0, 1) }
			},
		},
		{
			name: "clear queue",
			setup: func(p *Playlist) func() {
				p.Add(Track{Title: "A"})
				p.Queue(0)
				return p.ClearQueue
			},
		},
		{
			name: "move track",
			setup: func(p *Playlist) func() {
				p.Add(Track{Title: "A"}, Track{Title: "B"})
				return func() { p.Move(0, 1) }
			},
		},
		{
			name: "remove track",
			setup: func(p *Playlist) func() {
				p.Add(Track{Title: "A"})
				return func() { p.Remove(0) }
			},
		},
		{
			name: "set track",
			setup: func(p *Playlist) func() {
				p.Add(Track{Title: "A"})
				return func() { p.SetTrack(0, Track{Title: "B"}) }
			},
		},
		{
			name: "toggle bookmark",
			setup: func(p *Playlist) func() {
				p.Add(Track{Title: "A"})
				return func() { p.ToggleBookmark(0) }
			},
		},
		{
			name: "toggle shuffle",
			setup: func(p *Playlist) func() {
				p.Add(Track{Title: "A"})
				return p.ToggleShuffle
			},
		},
		{
			name: "cycle repeat",
			setup: func(p *Playlist) func() {
				return p.CycleRepeat
			},
		},
		{
			name: "set repeat",
			setup: func(p *Playlist) func() {
				return func() { p.SetRepeat(RepeatAll) }
			},
		},
		{
			name: "restore",
			setup: func(p *Playlist) func() {
				p.Add(Track{Title: "A"})
				snapshot := p.Snapshot()
				p.Add(Track{Title: "B"})
				return func() { p.Restore(snapshot) }
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := New()
			mutate := tt.setup(p)
			before := p.Revision()
			mutate()
			if got, want := p.Revision(), before+1; got != want {
				t.Errorf("Revision() = %d, want %d", got, want)
			}
		})
	}
}

func TestRevisionUnchangedByNoopMutations(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*Playlist) func()
	}{
		{
			name: "add no tracks",
			setup: func(p *Playlist) func() {
				return func() { p.Add() }
			},
		},
		{
			name: "replace identical state",
			setup: func(p *Playlist) func() {
				p.Add(Track{Title: "A"})
				tracks := p.Tracks()
				return func() { p.Replace(tracks) }
			},
		},
		{
			name: "activate current selection",
			setup: func(p *Playlist) func() {
				p.Add(Track{Title: "A"})
				return func() { p.ActivateSelected() }
			},
		},
		{
			name: "next at end",
			setup: func(p *Playlist) func() {
				p.Add(Track{Title: "A"})
				return func() { p.Next() }
			},
		},
		{
			name: "next repeat one",
			setup: func(p *Playlist) func() {
				p.Add(Track{Title: "A"})
				p.SetRepeat(RepeatOne)
				return func() { p.Next() }
			},
		},
		{
			name: "prev at beginning",
			setup: func(p *Playlist) func() {
				p.Add(Track{Title: "A"})
				return func() { p.Prev() }
			},
		},
		{
			name: "prev repeat all one track",
			setup: func(p *Playlist) func() {
				p.Add(Track{Title: "A"})
				p.SetRepeat(RepeatAll)
				return func() { p.Prev() }
			},
		},
		{
			name: "set current index",
			setup: func(p *Playlist) func() {
				p.Add(Track{Title: "A"})
				return func() { p.SetIndex(0) }
			},
		},
		{
			name: "set invalid index",
			setup: func(p *Playlist) func() {
				p.Add(Track{Title: "A"})
				return func() { p.SetIndex(1) }
			},
		},
		{
			name: "queue invalid index",
			setup: func(p *Playlist) func() {
				return func() { p.Queue(0) }
			},
		},
		{
			name: "dequeue missing track",
			setup: func(p *Playlist) func() {
				p.Add(Track{Title: "A"})
				return func() { p.Dequeue(0) }
			},
		},
		{
			name: "remove invalid queue entry",
			setup: func(p *Playlist) func() {
				return func() { p.RemoveQueueAt(0) }
			},
		},
		{
			name: "move queue entry to itself",
			setup: func(p *Playlist) func() {
				p.Add(Track{Title: "A"})
				p.Queue(0)
				return func() { p.MoveQueue(0, 0) }
			},
		},
		{
			name: "clear empty queue",
			setup: func(p *Playlist) func() {
				return p.ClearQueue
			},
		},
		{
			name: "move track to itself",
			setup: func(p *Playlist) func() {
				p.Add(Track{Title: "A"})
				return func() { p.Move(0, 0) }
			},
		},
		{
			name: "remove invalid track",
			setup: func(p *Playlist) func() {
				return func() { p.Remove(0) }
			},
		},
		{
			name: "set identical track",
			setup: func(p *Playlist) func() {
				p.Add(Track{Title: "A", ProviderMeta: map[string]string{"id": "1"}})
				track, ok := p.Track(0)
				if !ok {
					t.Fatal("Track(0) = false")
				}
				return func() { p.SetTrack(0, track) }
			},
		},
		{
			name: "set invalid track",
			setup: func(p *Playlist) func() {
				return func() { p.SetTrack(0, Track{Title: "A"}) }
			},
		},
		{
			name: "toggle invalid bookmark",
			setup: func(p *Playlist) func() {
				return func() { p.ToggleBookmark(0) }
			},
		},
		{
			name: "set same repeat mode",
			setup: func(p *Playlist) func() {
				return func() { p.SetRepeat(RepeatOff) }
			},
		},
		{
			name: "restore matching snapshot",
			setup: func(p *Playlist) func() {
				snapshot := p.Snapshot()
				return func() { p.Restore(snapshot) }
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := New()
			mutate := tt.setup(p)
			before := p.Revision()
			mutate()
			if got := p.Revision(); got != before {
				t.Errorf("Revision() = %d, want %d", got, before)
			}
		})
	}
}

func TestRevisionUnchangedByReadOnlyCalls(t *testing.T) {
	p := New()
	p.Add(Track{Title: "A"}, Track{Title: "B"})
	p.Queue(1)
	before := p.Revision()

	reads := []func(){
		func() { p.Revision() },
		func() { p.Len() },
		func() { p.Current() },
		func() { p.Index() },
		func() { p.CurrentIsQueued() },
		func() { p.PeekNext() },
		func() { p.QueuePosition(1) },
		func() { p.QueueLen() },
		func() { p.QueueTracks() },
		func() { p.QueueWindow(0, 1) },
		func() { p.Snapshot() },
		func() { p.Tracks() },
		func() { p.Track(0) },
		func() { p.TrackWindow(0, 1) },
		func() { p.BookmarkCount() },
		func() { p.Shuffled() },
		func() { p.Repeat() },
	}
	for _, read := range reads {
		read()
	}

	if got := p.Revision(); got != before {
		t.Errorf("Revision() after reads = %d, want %d", got, before)
	}
}
