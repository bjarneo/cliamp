package model

import (
	"strings"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
	"github.com/bjarneo/cliamp/ui"
)

func TestClampedScroll(t *testing.T) {
	tests := []struct {
		name                          string
		scroll, cursor, count, budget int
		want                          int
	}{
		{"everything fits", 0, 3, 4, 10, 0},
		{"cursor above the window", 5, 2, 20, 5, 2},
		{"cursor below the window", 0, 9, 20, 5, 5},
		{"cursor inside the window", 3, 4, 20, 5, 3},
		{"scroll past the end is pulled back", 99, 19, 20, 5, 15},
		{"negative scroll", -4, 0, 20, 5, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := clampedScroll(tt.scroll, tt.cursor, tt.count, tt.budget); got != tt.want {
				t.Errorf("clampedScroll(%d, %d, %d, %d) = %d, want %d",
					tt.scroll, tt.cursor, tt.count, tt.budget, got, tt.want)
			}
		})
	}
}

func queueViewModel(t *testing.T) *Model {
	t.Helper()
	old := ui.PanelWidth
	ui.PanelWidth = 80
	t.Cleanup(func() { ui.PanelWidth = old })

	prov := &stateProv{states: map[string]provider.PlaybackState{
		"https://cdn/done.mp3": {Played: true},
		"https://cdn/half.mp3": {Position: 5 * time.Minute},
	}}
	m := &Model{provider: prov, playlist: playlist.New(), plVisible: 12, showAlbumHeaders: true}
	m.playlist.Replace([]playlist.Track{
		{Path: "https://cdn/done.mp3", Title: "Finished One", Album: "Part Of The Problem", DurationSecs: 3768},
		{Path: "https://cdn/half.mp3", Title: "Half Heard", Album: "Wading Through AI", DurationSecs: 6751},
		{Path: "https://cdn/fresh.mp3", Title: "Untouched", Album: "Wading Through AI", DurationSecs: 60},
	})
	for i := range 3 {
		m.playlist.Queue(i)
	}
	return m
}

func TestRenderQueueBodyMatchesPlaylistPresentation(t *testing.T) {
	m := queueViewModel(t)

	body := stripAnsi(m.renderQueueBody())

	for _, want := range []string{
		"Part Of The Problem", // show header
		"Wading Through AI",   // second show header
		"1:02:48",             // duration, right-aligned
		"1:52:31",
		"Finished One",
		playedMarker,  // the finished episode
		partialMarker, // the part-heard one
	} {
		if !strings.Contains(body, want) {
			t.Errorf("queue body is missing %q\ngot:\n%s", want, body)
		}
	}
}

func TestRenderQueueBodyNumbersByQueuePosition(t *testing.T) {
	m := queueViewModel(t)

	body := stripAnsi(m.renderQueueBody())

	for _, want := range []string{"1. Finished One", "2. Half Heard", "3. Untouched"} {
		if !strings.Contains(body, want) {
			t.Errorf("queue body is missing %q\ngot:\n%s", want, body)
		}
	}
}

func TestRenderQueueBodyEmpty(t *testing.T) {
	old := ui.PanelWidth
	ui.PanelWidth = 80
	t.Cleanup(func() { ui.PanelWidth = old })
	m := &Model{playlist: playlist.New(), plVisible: 12}

	if got := stripAnsi(m.renderQueueBody()); !strings.Contains(got, "(empty)") {
		t.Errorf("empty queue body = %q, want an (empty) message", got)
	}
}
