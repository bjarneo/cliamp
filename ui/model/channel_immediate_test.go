package model

import (
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/external/radio"
	"github.com/bjarneo/cliamp/playlist"
)

func TestOpenProviderListImmediateNavigation(t *testing.T) {
	for _, tt := range []struct {
		name      string
		provider  playlist.Provider
		wantFocus focusArea
	}{
		{name: "cliamp radio jumps to playlist", provider: radio.NewChannels(), wantFocus: focusPlaylist},
		{name: "other provider stays in pane", provider: commandsTestProvider{name: "Radio"}, wantFocus: focusProvider},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := keybindingTestModel()
			m.provider = tt.provider
			m.focus = focusProvider
			m.provPane.lists = []playlist.PlaylistInfo{{ID: "lofi", Name: "Lofi · live"}}
			m.provPane.cursor = 0

			if cmd := m.openProviderList(0); cmd == nil {
				t.Fatal("openProviderList(0) returned no load command")
			}
			if m.focus != tt.wantFocus {
				t.Fatalf("focus = %v, want %v", m.focus, tt.wantFocus)
			}
			if !m.provPane.loading {
				t.Fatal("provPane.loading = false after open, want true")
			}
			if m.activeProviderPlaylistID != "lofi" {
				t.Fatalf("activeProviderPlaylistID = %q, want lofi", m.activeProviderPlaylistID)
			}
		})
	}
}

func TestRenderPlaylistShowsFeedLoadingDuringProviderTrackLoad(t *testing.T) {
	m := keybindingTestModel()
	m.provider = radio.NewChannels()
	m.focus = focusPlaylist
	m.provPane.loading = true
	m.activeProviderPlaylistID = "lofi"
	m.width = 80
	m.height = 20
	m.plVisible = 10
	m.recomputeLayout()

	if got := stripAnsi(m.renderPlaylist()); !strings.Contains(got, "Loading feed") {
		t.Fatalf("renderPlaylist() = %q, want Loading feed indicator", got)
	}
}
