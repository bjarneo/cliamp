package model

import (
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/playlist"
)

func TestPlaylistPickerHeaderShowsTrackInTitle(t *testing.T) {
	m := keybindingTestModel()
	m.layout.panelWidth = 80
	m.plPicker = playlistPickerState{
		visible: true,
		screen:  plPickerChoose,
		tracks:  []playlist.Track{{Title: "Song", Artist: "Artist"}},
		playlists: []playlist.PlaylistInfo{
			{Name: "mix"},
		},
	}

	header := stripAnsi(m.plPickerHeaderLine())
	if strings.Contains(header, "1/") || strings.Contains(header, "Write to") {
		t.Fatalf("header should not show counter or old title: %q", header)
	}
	if !strings.Contains(header, "Add to playlist") || !strings.Contains(header, "Song") {
		t.Fatalf("header = %q, want track in Add to playlist title", header)
	}
}

func TestPlaylistPickerBodyOmitsTrackLineAndBlankBeforeNew(t *testing.T) {
	m := keybindingTestModel()
	m.layout.panelWidth = 80
	m.plVisible = 8
	m.plPicker = playlistPickerState{
		visible: true,
		screen:  plPickerChoose,
		tracks:  []playlist.Track{{Title: "Song", Artist: "Artist"}},
		playlists: []playlist.PlaylistInfo{
			{Name: "playlist1"},
			{Name: "playlist2"},
		},
	}

	rows := strings.Split(stripAnsi(m.renderPlaylistPickerBody()), "\n")
	if strings.Contains(rows[0], "Track:") || strings.Contains(rows[0], "Song") {
		t.Fatalf("body should not repeat track detail: %q", rows)
	}
	for i, row := range rows {
		if strings.Contains(row, "New Playlist") {
			if i == 0 || strings.TrimSpace(rows[i-1]) != "" {
				t.Fatalf("row before New Playlist = %q, want blank: %q", rows[i-1], rows)
			}
			break
		}
	}
}

func TestPlaylistPickerHelpShowsAddNotSelect(t *testing.T) {
	m := keybindingTestModel()
	m.width, m.height = 120, 40
	m.plPicker = playlistPickerState{visible: true, screen: plPickerChoose}
	m.recomputeLayout()
	help := stripAnsi(m.renderHelpBar())
	if strings.Contains(help, "Enter") && strings.Contains(help, "Select") {
		t.Fatalf("help should not say Select for Enter: %q", help)
	}
	if !strings.Contains(help, "Enter") || !strings.Contains(help, "Add") {
		t.Fatalf("help should advertise Add on Enter: %q", help)
	}
}
