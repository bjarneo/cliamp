package model

import (
	"testing"

	"github.com/bjarneo/cliamp/playlist"
)

func albumResult(name string) playlist.Track {
	return playlist.Track{
		Title: name, Album: name, Artist: "NOFX",
		ProviderMeta: map[string]string{
			playlist.MetaKind:    playlist.MetaKindAlbum,
			playlist.MetaAlbumID: name,
		},
	}
}

func trackResult(name string) playlist.Track {
	return playlist.Track{Title: name, Artist: "NOFX"}
}

func TestSpotSearchRowsSections(t *testing.T) {
	results := []playlist.Track{
		albumResult("Punk In Drublic"),
		albumResult("The Decline"),
		trackResult("Linoleum"),
		trackResult("Bob"),
	}

	tests := []struct {
		name   string
		scroll int
		want   []string // "=Albums" for a separator, otherwise the title
	}{
		{
			name:   "separates albums from tracks",
			scroll: 0,
			want:   []string{"=Albums", "Punk In Drublic", "The Decline", "=Tracks", "Linoleum", "Bob"},
		},
		{
			// Scrolled past the album header, the section must still be named.
			name:   "repeats the header when the viewport opens mid-section",
			scroll: 1,
			want:   []string{"=Albums", "The Decline", "=Tracks", "Linoleum", "Bob"},
		},
		{
			name:   "names the tracks section when albums are scrolled away",
			scroll: 3,
			want:   []string{"=Tracks", "Bob"},
		},
		{
			name:   "yields nothing past the end",
			scroll: 4,
			want:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			for row := range spotSearchRows(results, tt.scroll) {
				if row.Index < 0 {
					got = append(got, "="+row.Section)
					continue
				}
				got = append(got, row.Track.Title)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("rows = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("rows = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestSpotSearchRowsToCursorCountsSeparators(t *testing.T) {
	results := []playlist.Track{
		albumResult("Punk In Drublic"),
		albumResult("The Decline"),
		trackResult("Linoleum"),
	}

	tests := []struct {
		name           string
		scroll, cursor int
		want           int
	}{
		{name: "first result sits below its header", scroll: 0, cursor: 0, want: 2},
		{name: "second album adds one row", scroll: 0, cursor: 1, want: 3},
		{name: "crossing into tracks costs a second header", scroll: 0, cursor: 2, want: 5},
		{name: "cursor above scroll counts nothing", scroll: 2, cursor: 1, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := spotSearchRowsToCursor(results, tt.scroll, tt.cursor); got != tt.want {
				t.Errorf("spotSearchRowsToCursor(%d, %d) = %d, want %d", tt.scroll, tt.cursor, got, tt.want)
			}
		})
	}
}

// The separators must not push the selected result out of the window.
func TestSpotSearchResultsScrollKeepsCursorVisible(t *testing.T) {
	m := &Model{}
	for i := range 6 {
		m.spotSearch.results = append(m.spotSearch.results, albumResult("Album "+string(rune('A'+i))))
	}
	m.spotSearch.results = append(m.spotSearch.results, trackResult("Linoleum"))
	m.spotSearch.cursor = 6

	const visible = 5
	m.spotSearchResultsMaybeAdjustScroll(visible)

	if rows := spotSearchRowsToCursor(m.spotSearch.results, m.spotSearch.scroll, m.spotSearch.cursor); rows > visible {
		t.Errorf("cursor sits %d rows below scroll %d, window is %d", rows, m.spotSearch.scroll, visible)
	}
}
