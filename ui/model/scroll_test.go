package model

import (
	"fmt"
	"testing"

	"github.com/bjarneo/cliamp/playlist"
)

func TestPlaylistScrollPreservesCursorAndAlbumHeaders(t *testing.T) {
	tracks := []playlist.Track{
		{Album: "A"},
		{Album: "A"},
		{Album: "A"},
		{Album: "B"},
		{Album: "B"},
		{},
		{},
		{Album: "C"},
		{Album: "C"},
		{Album: "C"},
	}

	tests := []struct {
		name        string
		scroll      int
		cursor      int
		visible     int
		showHeaders bool
		want        int
	}{
		{name: "beginning", cursor: 0, visible: 4, showHeaders: true, want: 0},
		{name: "jump without headers", cursor: 9, visible: 4, want: 6},
		{name: "jump across blank album", cursor: 9, visible: 4, showHeaders: true, want: 7},
		{name: "jump across album boundary", cursor: 4, visible: 4, showHeaders: true, want: 3},
		{name: "cursor remains visible", scroll: 7, cursor: 9, visible: 4, showHeaders: true, want: 7},
		{name: "wrap to beginning", scroll: 9, cursor: 0, visible: 4, showHeaders: true, want: 0},
		{name: "small viewport includes header", cursor: 9, visible: 2, showHeaders: true, want: 9},
		{name: "end with stale scroll", scroll: 20, cursor: 9, visible: 4, showHeaders: true, want: 9},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := playlistScrollTestModel(tracks, tt.scroll, tt.cursor, tt.showHeaders)
			got := m.playlistScroll(tt.visible)
			if got != tt.want {
				t.Fatalf("playlistScroll(%d) = %d, want %d", tt.visible, got, tt.want)
			}

			cursor := min(max(0, tt.cursor), len(tracks)-1)
			rows := m.albumSeparatorRows(tracks, got, cursor, tt.showHeaders)
			if rows > tt.visible {
				t.Fatalf("rows from scroll %d to cursor %d = %d, want <= %d", got, cursor, rows, tt.visible)
			}
		})
	}
}

func TestPlaylistScrollMatchesFullSpanReference(t *testing.T) {
	patterns := map[string][]playlist.Track{
		"no albums":       make([]playlist.Track, 12),
		"one album":       albumTracks(12, 12),
		"album runs":      albumTracks(12, 3),
		"alternating":     albumTracks(12, 1),
		"albums and gaps": {{Album: "A"}, {Album: "A"}, {}, {}, {Album: "B"}, {Album: "B"}, {}, {Album: "C"}},
	}

	for name, tracks := range patterns {
		for _, showHeaders := range []bool{false, true} {
			for _, scroll := range []int{-2, 0, 1, len(tracks) / 2, len(tracks) - 1, len(tracks) + 3} {
				for _, cursor := range []int{-2, 0, 1, len(tracks) / 2, len(tracks) - 1, len(tracks) + 3} {
					for _, visible := range []int{0, 1, 2, 3, 5, len(tracks)} {
						m := playlistScrollTestModel(tracks, scroll, cursor, showHeaders)
						want := fullSpanPlaylistScroll(m, tracks, visible)
						if got := m.playlistScroll(visible); got != want {
							t.Fatalf("%s: headers=%t scroll=%d cursor=%d visible=%d: got %d, want %d", name, showHeaders, scroll, cursor, visible, got, want)
						}
					}
				}
			}
		}
	}
}

func TestPlaylistScrollLargeJump(t *testing.T) {
	const (
		trackCount = 100_000
		visible    = 9
	)

	tests := []struct {
		name        string
		showHeaders bool
		albumSize   int
		want        int
	}{
		{name: "headers disabled", albumSize: 1, want: trackCount - visible},
		{name: "single album", showHeaders: true, albumSize: trackCount, want: trackCount - visible + 1},
		{name: "album runs", showHeaders: true, albumSize: 4, want: 99_993},
		{name: "header per track", showHeaders: true, albumSize: 1, want: 99_996},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := playlistScrollTestModel(albumTracks(trackCount, tt.albumSize), 0, trackCount-1, tt.showHeaders)
			if got := m.playlistScroll(visible); got != tt.want {
				t.Fatalf("playlistScroll(%d) = %d, want %d", visible, got, tt.want)
			}
		})
	}
}

func TestPlaylistScrollJumpAllocationsDoNotScaleWithPlaylist(t *testing.T) {
	const visible = 10
	small := playlistScrollAllocationModel(100)
	large := playlistScrollAllocationModel(10_000)

	withoutHeaders := testing.AllocsPerRun(10, func() {
		playlistScrollSink = large.playlistScroll(visible)
	})
	if withoutHeaders != 0 {
		t.Fatalf("header-disabled jump allocations = %.0f, want 0", withoutHeaders)
	}

	small.showAlbumHeaders = true
	large.showAlbumHeaders = true
	smallAllocs := testing.AllocsPerRun(10, func() {
		playlistScrollSink = small.playlistScroll(visible)
	})
	largeAllocs := testing.AllocsPerRun(10, func() {
		playlistScrollSink = large.playlistScroll(visible)
	})
	if largeAllocs > smallAllocs+1 {
		t.Fatalf("header-enabled jump allocations scale with playlist: 100 tracks %.0f, 10,000 tracks %.0f", smallAllocs, largeAllocs)
	}
}

var playlistScrollSink int

func playlistScrollTestModel(tracks []playlist.Track, scroll, cursor int, showHeaders bool) Model {
	p := playlist.New()
	p.Replace(tracks)
	return Model{
		playlist:         p,
		plScroll:         scroll,
		plCursor:         cursor,
		showAlbumHeaders: showHeaders,
	}
}

func playlistScrollAllocationModel(count int) Model {
	tracks := albumTracks(count, 4)
	for i := range tracks {
		tracks[i].ProviderMeta = map[string]string{"id": "track"}
	}
	return playlistScrollTestModel(tracks, 0, count-1, false)
}

func albumTracks(count, albumSize int) []playlist.Track {
	tracks := make([]playlist.Track, count)
	for i := range tracks {
		tracks[i].Album = fmt.Sprintf("Album %d", i/albumSize)
	}
	return tracks
}

func fullSpanPlaylistScroll(m Model, tracks []playlist.Track, visible int) int {
	if len(tracks) == 0 {
		return 0
	}
	scroll := min(max(0, m.plScroll), len(tracks)-1)
	cursor := min(max(0, m.plCursor), len(tracks)-1)
	if cursor < scroll {
		return cursor
	}
	for scroll < cursor && m.albumSeparatorRows(tracks, scroll, cursor, m.showAlbumHeaders) > visible {
		scroll++
	}
	return scroll
}

func TestFitHeaderScroll(t *testing.T) {
	// With headers, the rows are: A header, 0, 1, 2, B header, 3, 4.
	tracks := []playlist.Track{{Album: "A"}, {Album: "A"}, {Album: "A"}, {Album: "B"}, {Album: "B"}}
	tests := []struct {
		name        string
		scroll      int
		visible     int
		showHeaders bool
		want        int
	}{
		{name: "every row fits", visible: 7, showHeaders: true, want: 0},
		{name: "sticky header takes a row", visible: 5, showHeaders: true, want: 2},
		{name: "skips to the album boundary", visible: 4, showHeaders: true, want: 3},
		{name: "stops at the cursor", visible: 1, showHeaders: true, want: 4},
		{name: "no headers counts tracks", visible: 3, want: 2},
		{name: "keeps a scroll that fits", scroll: 3, visible: 4, showHeaders: true, want: 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := (Model{}).fitHeaderScroll(tracks, tt.scroll, 4, tt.visible, tt.showHeaders); got != tt.want {
				t.Fatalf("fitHeaderScroll(scroll %d, visible %d) = %d, want %d", tt.scroll, tt.visible, got, tt.want)
			}
		})
	}
}

func TestStepListCursor(t *testing.T) {
	tests := []struct {
		key         string
		cursor      int
		count, page int
		want        int
		wantHandled bool
	}{
		{key: "up", cursor: 2, count: 5, page: 3, want: 1, wantHandled: true},
		{key: "k", cursor: 0, count: 5, page: 3, want: 4, wantHandled: true},
		{key: "up", cursor: 0, count: 0, page: 3, want: 0, wantHandled: true},
		{key: "down", cursor: 2, count: 5, page: 3, want: 3, wantHandled: true},
		{key: "j", cursor: 4, count: 5, page: 3, want: 0, wantHandled: true},
		{key: "down", cursor: 0, count: 0, page: 3, want: 0, wantHandled: true},
		{key: "pgup", cursor: 4, count: 5, page: 3, want: 1, wantHandled: true},
		{key: "ctrl+u", cursor: 2, count: 5, page: 3, want: 0, wantHandled: true},
		{key: "pgup", cursor: 0, count: 5, page: 3, want: 0, wantHandled: true},
		{key: "pgdown", cursor: 0, count: 5, page: 3, want: 3, wantHandled: true},
		{key: "ctrl+d", cursor: 3, count: 5, page: 3, want: 4, wantHandled: true},
		{key: "pgdown", cursor: 4, count: 5, page: 3, want: 4, wantHandled: true},
		{key: "pgdown", cursor: 0, count: 0, page: 3, want: 0, wantHandled: true},
		{key: "home", cursor: 3, count: 5, page: 3, want: 0, wantHandled: true},
		{key: "g", cursor: 3, count: 5, page: 3, want: 0, wantHandled: true},
		{key: "end", cursor: 1, count: 5, page: 3, want: 4, wantHandled: true},
		{key: "G", cursor: 1, count: 0, page: 3, want: 1, wantHandled: true},
		{key: "enter", cursor: 1, count: 5, page: 3, want: 1},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s from %d of %d", tt.key, tt.cursor, tt.count), func(t *testing.T) {
			cursor := tt.cursor
			handled := stepListCursor(tt.key, &cursor, tt.count, tt.page)
			if handled != tt.wantHandled || cursor != tt.want {
				t.Fatalf("stepListCursor = %t, cursor %d; want %t, cursor %d", handled, cursor, tt.wantHandled, tt.want)
			}
		})
	}
}
