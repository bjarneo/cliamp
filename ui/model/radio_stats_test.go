package model

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

type radioStatsTestProvider struct {
	commandsTestProvider
	stats provider.RadioStats
	err   error
}

func (p radioStatsTestProvider) RadioStats() (provider.RadioStats, error) {
	return p.stats, p.err
}

func TestBuiltinRadioStatsRoute(t *testing.T) {
	prov := radioStatsTestProvider{
		commandsTestProvider: commandsTestProvider{name: "Radio"},
		stats: provider.RadioStats{
			TotalSessions:    42,
			TotalListenHours: 12.5,
			PeakListeners:    7,
			Stations: map[string]provider.RadioStationStats{
				"lofi": {TotalSessions: 40, ActiveListeners: 3},
			},
		},
	}
	m := Model{
		playlist:      playlist.New(),
		provider:      prov,
		providerLists: []playlist.PlaylistInfo{{ID: builtinRadioPlaylistID, Name: "cliamp radio"}},
		focus:         focusProvider,
		plVisible:     8,
	}

	cmd := m.handleKey(tea.KeyPressMsg{Text: "i"})
	if !m.radioStats.visible || !m.radioStats.loading {
		t.Fatalf("radio stats state = %+v, want visible loading screen", m.radioStats)
	}
	if cmd == nil {
		t.Fatal("stats command is nil")
	}

	updated, _ := m.Update(cmd())
	m = updated.(Model)
	if m.radioStats.loading {
		t.Fatal("radio stats still loading after response")
	}
	if body := stripAnsi(m.renderRadioStatsBody()); !strings.Contains(body, "Live listeners: 3") || !strings.Contains(body, "LOFI") {
		t.Fatalf("stats body = %q, want aggregate and station data", body)
	}

	m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.radioStats.visible {
		t.Fatal("radio stats remains visible after Esc")
	}
}

func TestRadioStatsRouteRequiresBuiltinStation(t *testing.T) {
	prov := radioStatsTestProvider{commandsTestProvider: commandsTestProvider{name: "Radio"}}
	m := Model{
		provider:      prov,
		providerLists: []playlist.PlaylistInfo{{ID: "c:0", Name: "Catalog station"}},
		focus:         focusProvider,
	}

	if cmd := m.handleKey(tea.KeyPressMsg{Text: "i"}); cmd != nil {
		t.Fatal("stats command is not nil for a catalog station")
	}
	if m.radioStats.visible {
		t.Fatal("radio stats opened for a catalog station")
	}
}
