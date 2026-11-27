package playlist

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

func TestPlaylistMetaMatchesGetters(t *testing.T) {
	tests := []struct {
		name  string
		setup func() *Playlist
	}{
		{
			name:  "empty playlist",
			setup: func() *Playlist { return New() },
		},
		{
			name: "single playable track",
			setup: func() *Playlist {
				p := New()
				p.Replace([]Track{{Path: "/music/a.flac", Title: "A", ProviderMeta: map[string]string{"id": "1"}}})
				return p
			},
		},
		{
			name: "middle index with queued tracks",
			setup: func() *Playlist {
				p := New()
				p.Replace([]Track{
					{Path: "/music/a.flac", Title: "A"},
					{Path: "/music/b.flac", Title: "B", ProviderMeta: map[string]string{"id": "2"}},
					{Path: "/music/c.flac", Title: "C"},
				})
				p.SetIndex(1)
				p.Queue(2)
				p.Queue(0)
				return p
			},
		},
		{
			name: "active track from play-next queue",
			setup: func() *Playlist {
				p := New()
				p.Replace([]Track{
					{Path: "/music/a.flac", Title: "A"},
					{Path: "/music/b.flac", Title: "B"},
					{Path: "/music/c.flac", Title: "C"},
				})
				p.Queue(2)
				_, _ = p.Next()
				return p
			},
		},
		{
			name: "last track with repeat off",
			setup: func() *Playlist {
				p := New()
				p.Replace([]Track{
					{Path: "/music/a.flac", Title: "A"},
					{Path: "/music/b.flac", Title: "B"},
				})
				p.SetIndex(1)
				return p
			},
		},
		{
			name: "last track with repeat all and shuffle",
			setup: func() *Playlist {
				p := New()
				p.Replace([]Track{
					{Path: "/music/a.flac", Title: "A"},
					{Path: "/music/b.flac", Title: "B"},
				})
				p.ToggleShuffle()
				p.SetRepeat(RepeatAll)
				_, _ = p.Next()
				return p
			},
		},
		{
			name: "repeat one on unplayable track",
			setup: func() *Playlist {
				p := New()
				p.Replace([]Track{
					{Path: "/music/a.flac", Title: "A", Unplayable: true},
				})
				p.SetRepeat(RepeatOne)
				return p
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := tt.setup()
			got := p.Meta()
			cur, curIdx := p.Current()
			want := PlaylistMeta{
				Revision:    p.Revision(),
				Index:       curIdx,
				Len:         p.Len(),
				QueueLen:    p.QueueLen(),
				Shuffled:    p.Shuffled(),
				Repeat:      p.Repeat(),
				HasNext:     p.HasNext(),
				CurrentPath: cur.Path,
			}
			if got != want {
				t.Fatalf("Meta() = %+v, want %+v", got, want)
			}
		})
	}
}

func TestPlaylistMetaConcurrentAccess(t *testing.T) {
	p := New()
	p.Replace([]Track{
		{Path: "/music/0.flac", Title: "0", ProviderMeta: map[string]string{"k": "v"}},
		{Path: "/music/1.flac", Title: "1"},
	})

	var stop atomic.Bool
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				meta := p.Meta()
				if meta.Len > 0 && meta.Index >= 0 && meta.CurrentPath == "" {
					t.Errorf("Meta() returned empty CurrentPath for index %d", meta.Index)
					return
				}
			}
		}()
	}

	for i := range 200 {
		p.Add(Track{Path: fmt.Sprintf("/music/add-%d.flac", i), Title: "Add"})
		if i%3 == 0 {
			p.ToggleShuffle()
		}
		if i%5 == 0 {
			p.CycleRepeat()
		}
		p.Queue(i % p.Len())
		if i%7 == 0 {
			p.ClearQueue()
		}
	}
	stop.Store(true)
	wg.Wait()
}
