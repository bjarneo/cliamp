package model

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui"
)

// loadingSpinnerCases puts the model into each load that shows a spinner.
var loadingSpinnerCases = []struct {
	name  string
	setup func(*Model)
	body  func(*Model) string
	want  string
}{
	{
		name:  "lyrics",
		setup: func(m *Model) { m.lyrics.visible, m.lyrics.loading = true, true },
		body:  (*Model).renderLyricsBody,
		want:  "Searching for lyrics...",
	},
	{
		name: "YouTube search",
		setup: func(m *Model) {
			m.openProviderSearchWith(commandsTestProvider{name: "Radio"})
			m.netSearch.loading = true
		},
		body: (*Model).renderNetSearchBody,
		want: "Searching YouTube...",
	},
	{
		name: "provider search",
		setup: func(m *Model) {
			m.openProviderSearchWith(&catalogTestProvider{commandsTestProvider: commandsTestProvider{name: "Spotify"}})
			m.spotSearch.loading = true
		},
		body: (*Model).renderSpotSearchBody,
		want: "Searching Spotify...",
	},
	{
		name: "radio search",
		setup: func(m *Model) {
			m.provider = &catalogTestProvider{commandsTestProvider: commandsTestProvider{name: "Radio"}}
			m.focus = focusProvider
			m.providerLists = []playlist.PlaylistInfo{{ID: "c:1", Name: "Old station"}}
			m.provLoading, m.provSearch.loading = true, true
		},
		body: (*Model).renderProviderList,
		want: "Searching Radio…",
	},
}

func TestLoadingTextsShowTheSpinner(t *testing.T) {
	old := ui.PanelWidth
	ui.PanelWidth = 80
	t.Cleanup(func() { ui.PanelWidth = old })

	for _, tt := range loadingSpinnerCases {
		t.Run(tt.name, func(t *testing.T) {
			m := keybindingTestModel()
			m.plVisible = 6
			tt.setup(&m)
			body := ansi.Strip(tt.body(&m))
			for line := range strings.Lines(body) {
				if !strings.Contains(line, tt.want) {
					continue
				}
				if fields := strings.Fields(line); !slices.Contains(spinnerFrames, fields[0]) {
					t.Fatalf("loading line = %q, want a spinner frame first", line)
				}
				return
			}
			t.Fatalf("body = %q, want %q", body, tt.want)
		})
	}
}

func TestSpinnerKeepsTheTickAtSpinnerRate(t *testing.T) {
	for _, tt := range loadingSpinnerCases {
		t.Run(tt.name, func(t *testing.T) {
			m := keybindingTestModel()
			if !m.isFullyIdle() {
				t.Fatal("a stopped model without a load is not idle")
			}
			tt.setup(&m)
			if m.isFullyIdle() {
				t.Fatal("isFullyIdle() = true while a spinner shows")
			}
			if got := m.tickInterval(); got > spinnerInterval {
				t.Fatalf("tickInterval() = %v, want at most %v", got, spinnerInterval)
			}
		})
	}
}

func TestUpdateRedrawsSpinnerUntilTheLoadEnds(t *testing.T) {
	m := keybindingTestModel()
	m.openProviderSearchWith(commandsTestProvider{name: "Radio"})
	m.netSearch.query = "jazz"

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if !m.netSearch.loading || !m.spinnerTicking {
		t.Fatalf("search start: loading=%t spinnerTicking=%t, want both true", m.netSearch.loading, m.spinnerTicking)
	}

	updated, cmd := m.Update(spinnerTickMsg{})
	m = updated.(Model)
	if cmd == nil || !m.spinnerTicking {
		t.Fatal("spinner tick stopped while the search still runs")
	}

	m.netSearch.loading = false
	updated, cmd = m.Update(spinnerTickMsg{})
	m = updated.(Model)
	if cmd != nil || m.spinnerTicking {
		t.Fatal("spinner tick continued after the search ended")
	}
}
