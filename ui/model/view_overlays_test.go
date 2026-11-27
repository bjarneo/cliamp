package model

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bjarneo/cliamp/playlist"
)

// Search results pad the track number to the widest one in the playlist, so
// the names line up as they do in the playlist.
func TestRenderSearchListAlignsNumbers(t *testing.T) {
	m := newLayoutTestModel(100, 40)
	for i := 1; i <= 120; i++ {
		title := fmt.Sprintf("Song %d", i)
		if i == 2 || i == 105 {
			title = "Opeth " + title
		}
		m.playlist.Add(playlist.Track{Title: title})
	}
	m.search.active = true
	m.search.query = "opeth"
	m.updateSearch()
	m.recomputeLayout()

	var cols []int
	for _, line := range strings.Split(ansi.Strip(m.renderSearchList()), "\n") {
		if i := strings.Index(line, "Opeth"); i >= 0 {
			cols = append(cols, lipgloss.Width(line[:i]))
		}
	}
	if len(cols) != 2 || cols[0] != cols[1] {
		t.Fatalf("name columns = %v, want two equal\n%s", cols, ansi.Strip(m.renderSearchList()))
	}
}
