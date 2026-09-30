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
