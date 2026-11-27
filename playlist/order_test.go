package playlist

import (
	"slices"
	"testing"
)

func TestOrderWindow(t *testing.T) {
	tests := []struct {
		name    string
		shuffle bool
		start   int
		limit   int
		want    []int
	}{
		{name: "track order without shuffle", limit: 4, want: []int{0, 1, 2, 3}},
		{name: "shuffle order", shuffle: true, limit: 4, want: []int{2, 0, 3, 1}},
		{name: "window inside shuffle order", shuffle: true, start: 1, limit: 2, want: []int{0, 3}},
		{name: "window past the end", shuffle: true, start: 3, limit: 5, want: []int{1}},
		{name: "start past the end", start: 4, limit: 2},
		{name: "zero limit", shuffle: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := makePlaylist(4, tt.shuffle)
			if tt.shuffle {
				p.order = []int{2, 0, 3, 1}
			}

			indices, tracks := p.OrderWindow(tt.start, tt.limit)
			if !slices.Equal(indices, tt.want) {
				t.Fatalf("OrderWindow(%d, %d) indices = %v, want %v", tt.start, tt.limit, indices, tt.want)
			}
			if len(tracks) != len(indices) {
				t.Fatalf("OrderWindow(%d, %d) returned %d tracks for %d indices", tt.start, tt.limit, len(tracks), len(indices))
			}
			for i, idx := range indices {
				if want := string(rune('A' + idx)); tracks[i].Title != want {
					t.Fatalf("OrderWindow(%d, %d) track %d = %q, want %q", tt.start, tt.limit, i, tracks[i].Title, want)
				}
			}
			if len(indices) > 0 {
				indices[0] = -1
				if p.order[tt.start] == -1 {
					t.Fatal("OrderWindow returned indices that share memory with the play order")
				}
			}
		})
	}
}

func TestOrderPosition(t *testing.T) {
	p := makePlaylist(4, true)
	p.order = []int{2, 0, 3, 1}
	for idx, want := range []int{1, 3, 0, 2} {
		if got := p.OrderPosition(idx); got != want {
			t.Fatalf("OrderPosition(%d) = %d, want %d", idx, got, want)
		}
	}
	for _, idx := range []int{-1, 4} {
		if got := p.OrderPosition(idx); got != -1 {
			t.Fatalf("OrderPosition(%d) = %d, want -1", idx, got)
		}
	}
}

func TestOrderFollowsShuffleToggle(t *testing.T) {
	p := makePlaylist(8, false)
	p.SetIndex(5)

	p.ToggleShuffle()
	order, _ := p.OrderWindow(0, p.Len())
	if len(order) != p.Len() || order[0] != 5 {
		t.Fatalf("OrderWindow after shuffle = %v, want all 8 tracks with track 5 first", order)
	}
	for pos, idx := range order {
		if got := p.OrderPosition(idx); got != pos {
			t.Fatalf("OrderPosition(%d) = %d, want %d", idx, got, pos)
		}
	}

	p.ToggleShuffle()
	if got, _ := p.OrderWindow(0, p.Len()); !slices.Equal(got, []int{0, 1, 2, 3, 4, 5, 6, 7}) {
		t.Fatalf("OrderWindow after shuffle off = %v, want the track order", got)
	}
	for idx := range p.Len() {
		if got := p.OrderPosition(idx); got != idx {
			t.Fatalf("OrderPosition(%d) after shuffle off = %d, want %d", idx, got, idx)
		}
	}
}

func assertOrderPosInvariant(t *testing.T, p *Playlist) {
	t.Helper()
	if len(p.orderPos) != len(p.order) {
		t.Fatalf("len(orderPos) = %d, want %d", len(p.orderPos), len(p.order))
	}
	for pos, idx := range p.order {
		if idx < 0 || idx >= len(p.tracks) {
			t.Fatalf("order[%d] = %d is out of range for %d tracks", pos, idx, len(p.tracks))
		}
		if p.orderPos[idx] != pos {
			t.Fatalf("orderPos[%d] = %d, want %d (order=%v)", idx, p.orderPos[idx], pos, p.order)
		}
		if got := p.OrderPosition(idx); got != pos {
			t.Fatalf("OrderPosition(%d) = %d, want %d", idx, got, pos)
		}
	}
	if got := p.OrderPosition(-1); got != -1 {
		t.Fatalf("OrderPosition(-1) = %d, want -1", got)
	}
	if got := p.OrderPosition(len(p.tracks)); got != -1 {
		t.Fatalf("OrderPosition(%d) = %d, want -1", len(p.tracks), got)
	}
}

func TestOrderPositionInvariants(t *testing.T) {
	tests := []struct {
		name string
		run  func(*testing.T) *Playlist
	}{
		{
			name: "add unshuffled and shuffled",
			run: func(t *testing.T) *Playlist {
				p := New()
				p.Add(Track{Title: "A"}, Track{Title: "B"}, Track{Title: "C"})
				assertOrderPosInvariant(t, p)
				p.ToggleShuffle()
				assertOrderPosInvariant(t, p)
				p.Add(Track{Title: "D"}, Track{Title: "E"}, Track{Title: "F"})
				assertOrderPosInvariant(t, p)
				return p
			},
		},
		{
			name: "add into empty shuffled playlist",
			run: func(t *testing.T) *Playlist {
				p := New()
				p.ToggleShuffle()
				p.Add(Track{Title: "A"}, Track{Title: "B"}, Track{Title: "C"}, Track{Title: "D"})
				assertOrderPosInvariant(t, p)
				return p
			},
		},
		{
			name: "remove tracks while shuffled",
			run: func(t *testing.T) *Playlist {
				p := makePlaylist(8, true)
				assertOrderPosInvariant(t, p)
				for _, idx := range []int{0, 3, 5, 2} {
					if !p.Remove(idx) {
						t.Fatalf("Remove(%d) = false, want true", idx)
					}
					assertOrderPosInvariant(t, p)
				}
				return p
			},
		},
		{
			name: "move tracks unshuffled and shuffled",
			run: func(t *testing.T) *Playlist {
				p := makePlaylist(6, false)
				p.Move(1, 4)
				assertOrderPosInvariant(t, p)
				p.ToggleShuffle()
				assertOrderPosInvariant(t, p)
				p.Move(0, 5)
				assertOrderPosInvariant(t, p)
				p.Move(3, 2)
				assertOrderPosInvariant(t, p)
				return p
			},
		},
		{
			name: "reverse unshuffled and shuffled",
			run: func(t *testing.T) *Playlist {
				p := makePlaylist(7, false)
				p.Reverse()
				assertOrderPosInvariant(t, p)
				p.ToggleShuffle()
				assertOrderPosInvariant(t, p)
				p.Reverse()
				assertOrderPosInvariant(t, p)
				return p
			},
		},
		{
			name: "replace at and clear",
			run: func(t *testing.T) *Playlist {
				p := makePlaylist(5, true)
				p.ReplaceAt([]Track{{Title: "X"}, {Title: "Y"}, {Title: "Z"}}, 1)
				assertOrderPosInvariant(t, p)
				p.Replace(nil)
				assertOrderPosInvariant(t, p)
				return p
			},
		},
		{
			name: "restore snapshot preserves orderPos",
			run: func(t *testing.T) *Playlist {
				p := makePlaylist(6, true)
				assertOrderPosInvariant(t, p)
				snap := p.Snapshot()
				p.ToggleShuffle()
				p.Remove(2)
				p.Add(Track{Title: "Extra"})
				assertOrderPosInvariant(t, p)
				p.Restore(snap)
				assertOrderPosInvariant(t, p)
				return p
			},
		},
		{
			name: "set index uses order position",
			run: func(t *testing.T) *Playlist {
				p := makePlaylist(6, true)
				assertOrderPosInvariant(t, p)
				for idx := range p.Len() {
					p.SetIndex(idx)
					if got := p.Index(); got != idx {
						t.Fatalf("Index() after SetIndex(%d) = %d, want %d", idx, got, idx)
					}
					if p.pos != p.OrderPosition(idx) {
						t.Fatalf("pos = %d, want OrderPosition(%d) = %d", p.pos, idx, p.OrderPosition(idx))
					}
				}
				return p
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.run(t)
		})
	}
}

func TestOrderWindowShallow(t *testing.T) {
	tests := []struct {
		name    string
		shuffle bool
		start   int
		count   int
	}{
		{name: "sequential full window", shuffle: false, start: 0, count: 4},
		{name: "sequential partial window", shuffle: false, start: 1, count: 2},
		{name: "shuffled partial window", shuffle: true, start: 1, count: 3},
		{name: "past end returns nil", shuffle: false, start: 5, count: 2},
		{name: "zero count returns nil", shuffle: true, start: 0, count: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := New()
			tracks := make([]Track, 4)
			for i := range tracks {
				tracks[i] = Track{
					Path:         string(rune('a' + i)),
					Title:        string(rune('A' + i)),
					ProviderMeta: map[string]string{"id": string(rune('0' + i))},
				}
			}
			p.Replace(tracks)
			if tt.shuffle {
				p.ToggleShuffle()
			}

			wantIdx, wantTracks := p.OrderWindow(tt.start, tt.count)
			gotIdx, gotTracks := p.OrderWindowShallow(tt.start, tt.count)
			if !slices.Equal(gotIdx, wantIdx) {
				t.Fatalf("OrderWindowShallow indices = %v, want %v", gotIdx, wantIdx)
			}
			if len(gotTracks) != len(wantTracks) {
				t.Fatalf("OrderWindowShallow tracks len = %d, want %d", len(gotTracks), len(wantTracks))
			}
			for i := range gotTracks {
				if gotTracks[i].Path != wantTracks[i].Path || gotTracks[i].Title != wantTracks[i].Title || gotTracks[i].Meta("id") != wantTracks[i].Meta("id") {
					t.Fatalf("track %d = %+v, want %+v", i, gotTracks[i], wantTracks[i])
				}
			}
		})
	}
}

func TestDisplayNames(t *testing.T) {
	tests := []struct {
		name   string
		tracks []Track
		want   []string
	}{
		{name: "empty playlist", tracks: nil, want: nil},
		{
			name: "mixed track metadata",
			tracks: []Track{
				{Path: "/music/a.mp3", Artist: "Artist", Title: "Song", ProviderMeta: map[string]string{"k": "v"}},
				{Path: "/music/b.mp3", Title: "Only Title"},
				{Path: "/music/fallback.flac"},
			},
			want: []string{"Artist - Song", "Only Title", ""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := New()
			p.Replace(tt.tracks)
			got := p.DisplayNames()
			if !slices.Equal(got, tt.want) {
				t.Fatalf("DisplayNames() = %v, want %v", got, tt.want)
			}
		})
	}
}
