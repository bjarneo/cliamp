package model

import (
	"fmt"
	"strings"
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

func TestSearchOverlayResultVisibleWithOneBodyRow(t *testing.T) {
	m := newLayoutTestModel(40, 10)
	m.searchOverlay = searchOverlayState{
		visible: true,
		screen:  searchOverlayResults,
		results: []playlist.Track{albumResult("Selected Album")},
	}
	m.recomputeLayout()
	if got := m.effectivePlaylistVisible(); got != 1 {
		t.Fatalf("body rows = %d, want 1", got)
	}

	body := stripAnsi(m.renderSearchOverlayBody())
	if !strings.Contains(body, "Selected Album") {
		t.Fatalf("body = %q, want selected result", body)
	}
}

func TestSearchOverlayErrorFitsBodyBudget(t *testing.T) {
	m := newLayoutTestModel(80, 24)
	m.searchOverlay = searchOverlayState{
		visible: true,
		screen:  searchOverlayResults,
		results: []playlist.Track{albumResult("Album")},
		err:     "Album cannot be added to a playlist",
	}
	m.recomputeLayout()

	body := stripAnsi(m.renderSearchOverlayBody())
	if !strings.Contains(body, m.searchOverlay.err) {
		t.Fatalf("body = %q, want visible error", body)
	}
	if got, want := len(strings.Split(body, "\n")), m.effectivePlaylistVisible(); got != want {
		t.Fatalf("body rows = %d, want %d", got, want)
	}
}

func TestSearchOverlayErrorKeepsCursorVisible(t *testing.T) {
	m := newLayoutTestModel(80, 18)
	m.searchOverlay.visible = true
	m.searchOverlay.screen = searchOverlayResults
	m.recomputeLayout()
	visible := m.effectivePlaylistVisible()
	if visible < 3 {
		t.Fatalf("body rows = %d, want at least 3", visible)
	}
	for i := range visible - 1 {
		m.searchOverlay.results = append(m.searchOverlay.results, trackResult(fmt.Sprintf("Track %d", i)))
	}
	m.searchOverlay.cursor = len(m.searchOverlay.results) - 1

	m.setSearchOverlayError("Search failed")

	resultRows := m.searchOverlayResultsVisible()
	if resultRows != visible-1 {
		t.Fatalf("result rows = %d, want %d", resultRows, visible-1)
	}
	if rows := searchOverlayRowsToCursor(m.searchOverlay.results, m.searchOverlay.scroll, m.searchOverlay.cursor); rows > resultRows {
		t.Fatalf("cursor sits %d rows below scroll %d, result window is %d", rows, m.searchOverlay.scroll, resultRows)
	}
	selected := m.searchOverlay.results[m.searchOverlay.cursor].Title
	if body := stripAnsi(m.renderSearchOverlayBody()); !strings.Contains(body, selected) {
		t.Fatalf("body = %q, want selected result %q", body, selected)
	}
}

func trackResult(name string) playlist.Track {
	return playlist.Track{Title: name, Artist: "NOFX"}
}

func TestSearchOverlayRowsSections(t *testing.T) {
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
			for row := range searchOverlayRows(results, tt.scroll) {
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

func TestSearchOverlayRowsToCursorCountsSeparators(t *testing.T) {
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
			if got := searchOverlayRowsToCursor(results, tt.scroll, tt.cursor); got != tt.want {
				t.Errorf("searchOverlayRowsToCursor(%d, %d) = %d, want %d", tt.scroll, tt.cursor, got, tt.want)
			}
		})
	}
}

// The separators must not push the selected result out of the window.
func TestSearchOverlayResultsScrollKeepsCursorVisible(t *testing.T) {
	m := &Model{}
	for i := range 6 {
		m.searchOverlay.results = append(m.searchOverlay.results, albumResult("Album "+string(rune('A'+i))))
	}
	m.searchOverlay.results = append(m.searchOverlay.results, trackResult("Linoleum"))
	m.searchOverlay.cursor = 6

	const visible = 5
	m.searchOverlayResultsMaybeAdjustScroll(visible)

	if rows := searchOverlayRowsToCursor(m.searchOverlay.results, m.searchOverlay.scroll, m.searchOverlay.cursor); rows > visible {
		t.Errorf("cursor sits %d rows below scroll %d, window is %d", rows, m.searchOverlay.scroll, visible)
	}
}

func TestSearchOverlayPickerShowsAddError(t *testing.T) {
	m := newLayoutTestModel(80, 24)
	m.searchOverlay = searchOverlayState{
		visible:   true,
		screen:    searchOverlayPlaylist,
		selTrack:  playlist.Track{Artist: "NOFX", Title: "Linoleum"},
		playlists: []playlist.PlaylistInfo{{ID: "mine", Name: "Mine"}},
		err:       "Add failed: http status 403: Forbidden",
	}
	m.recomputeLayout()

	body := stripAnsi(m.renderSearchOverlayBody())
	if !strings.Contains(body, m.searchOverlay.err) {
		t.Fatalf("body = %q, want visible add error", body)
	}
	if got, want := len(strings.Split(body, "\n")), m.effectivePlaylistVisible(); got > want {
		t.Fatalf("body rows = %d, want at most %d", got, want)
	}
}
