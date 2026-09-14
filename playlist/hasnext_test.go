package playlist

import "testing"

func TestHasNext(t *testing.T) {
	a := Track{Path: "/a.mp3"}
	b := Track{Path: "/b.mp3"}
	dead := Track{Path: "/dead.mp3", Unplayable: true}

	tests := []struct {
		name  string
		setup func(pl *Playlist)
		want  bool
	}{
		{name: "empty", setup: func(*Playlist) {}, want: false},
		{name: "middle track", setup: func(pl *Playlist) { pl.Add(a, b); pl.SetIndex(0) }, want: true},
		{name: "last track repeat off", setup: func(pl *Playlist) { pl.Add(a, b); pl.SetIndex(1) }, want: false},
		{name: "last track repeat all", setup: func(pl *Playlist) {
			pl.Add(a, b)
			pl.SetIndex(1)
			pl.SetRepeat(RepeatAll)
		}, want: true},
		{name: "repeat one", setup: func(pl *Playlist) { pl.Add(a); pl.SetRepeat(RepeatOne) }, want: true},
		{name: "repeat one on an unplayable track", setup: func(pl *Playlist) { pl.Add(dead); pl.SetRepeat(RepeatOne) }, want: false},
		{name: "play-next queued after the last track", setup: func(pl *Playlist) {
			pl.Add(a, b)
			pl.SetIndex(1)
			pl.Queue(0)
		}, want: true},
		{name: "only unplayable tracks follow", setup: func(pl *Playlist) { pl.Add(a, dead); pl.SetIndex(0) }, want: false},
		{name: "repeat all over unplayable tracks only", setup: func(pl *Playlist) {
			pl.Add(dead, Track{Path: "/dead2.mp3", Unplayable: true})
			pl.SetRepeat(RepeatAll)
		}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pl := New()
			tt.setup(pl)
			if got := pl.HasNext(); got != tt.want {
				t.Errorf("HasNext = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHasNextAtShuffleWrap(t *testing.T) {
	pl := New()
	pl.Add(Track{Path: "/a.mp3"}, Track{Path: "/b.mp3"}, Track{Path: "/c.mp3"})
	pl.SetRepeat(RepeatAll)
	pl.ToggleShuffle()
	pl.SetIndex(pl.order[len(pl.order)-1])

	if _, ok := pl.PeekNext(); ok {
		t.Error("PeekNext = true at a shuffle wrap with repeat all, want false")
	}
	if !pl.HasNext() {
		t.Error("HasNext = false at a shuffle wrap with repeat all, want true")
	}
}
