package model

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui"
)

func TestPlaylistStateMarkersStayVisibleWithoutColor(t *testing.T) {
	oldPanelWidth := ui.PanelWidth
	ui.PanelWidth = 80
	t.Cleanup(func() { ui.PanelWidth = oldPanelWidth })

	p := playlist.New()
	p.Add(
		playlist.Track{Title: "Playing", Bookmark: true},
		playlist.Track{Title: "Unavailable", Unplayable: true},
	)
	p.Queue(0)
	fake := &playbackFakeEngine{playing: true}
	m := Model{
		player:    fake,
		playlist:  p,
		focus:     focusPlaylist,
		plCursor:  0,
		plVisible: 2,
	}

	plain := ansi.Strip(m.renderPlaylist())
	if !strings.Contains(plain, ">▶Q★") {
		t.Fatalf("playlist markers = %q, want cursor, playback, queue, and bookmark columns", plain)
	}
	if !strings.Contains(plain, "!") {
		t.Fatalf("playlist markers = %q, want unavailable marker", plain)
	}

	m.playbackDetached = true
	plain = ansi.Strip(m.renderPlaylist())
	if strings.Contains(plain, "▶") {
		t.Fatalf("detached playlist markers = %q, must not show playback marker", plain)
	}
}

func TestFavoritedRowsKeepMarkerColumnAlignment(t *testing.T) {
	oldPanelWidth := ui.PanelWidth
	ui.PanelWidth = 80
	t.Cleanup(func() { ui.PanelWidth = oldPanelWidth })

	favPath := "fav:1"
	p := playlist.New()
	p.Add(playlist.Track{Title: "Favorited", Path: favPath})
	p.Add(playlist.Track{Title: "Normal"})
	m := Model{
		player:    &playbackFakeEngine{},
		playlist:  p,
		focus:     focusPlaylist,
		plVisible: 2,
		favSet:    map[string]struct{}{favPath: {}},
	}

	plain := ansi.Strip(m.renderPlaylist())
	lines := strings.Split(plain, "\n")
	if len(lines) < 2 {
		t.Fatalf("playlist = %q, want at least two rows", plain)
	}
	if !strings.Contains(lines[0], "★") || strings.Contains(lines[0], "☆") {
		t.Fatalf("favorited row = %q, want filled star marker", lines[0])
	}
	if !strings.Contains(lines[1], "☆") || strings.Contains(lines[1], "★") {
		t.Fatalf("plain row = %q, want dim outline star marker", lines[1])
	}
	favCol := ansi.StringWidth(lines[0][:strings.Index(lines[0], "Favorited")])
	normCol := ansi.StringWidth(lines[1][:strings.Index(lines[1], "Normal")])
	if favCol != normCol {
		t.Fatalf("track names misaligned: favorited starts at cell %d, plain row at %d (%q)", favCol, normCol, plain)
	}
}

func TestPlaylistShowsKnownTrackDuration(t *testing.T) {
	oldPanelWidth := ui.PanelWidth
	ui.PanelWidth = 80
	t.Cleanup(func() { ui.PanelWidth = oldPanelWidth })

	p := playlist.New()
	p.Add(playlist.Track{Title: "Timed", DurationSecs: 222})
	m := Model{
		player:    &playbackFakeEngine{},
		playlist:  p,
		focus:     focusPlaylist,
		plVisible: 1,
	}

	plain := ansi.Strip(m.renderPlaylist())
	if !strings.Contains(plain, "3:42") {
		t.Fatalf("playlist row = %q, want right-aligned duration", plain)
	}
}

func TestNonSeekableStreamUsesLiveTime(t *testing.T) {
	fake := &playbackFakeEngine{}
	p := playlist.New()
	p.Add(playlist.Track{Title: "Station", Stream: true})
	m := Model{
		player:    fake,
		playlist:  p,
		cachedPos: 65 * time.Second,
	}

	if plain := ansi.Strip(m.renderTimeStatus()); !strings.Contains(plain, "01:05 / LIVE") {
		t.Fatalf("stream time = %q, want elapsed LIVE time", plain)
	}
}

func TestProviderIndicatorProgressivelyShowsNeighbors(t *testing.T) {
	oldPanelWidth := ui.PanelWidth
	t.Cleanup(func() { ui.PanelWidth = oldPanelWidth })
	m := Model{
		providers:   []ProviderEntry{{Name: "Radio"}, {Name: "Spotify"}, {Name: "Local"}},
		provPillIdx: 1,
	}

	ui.PanelWidth = 80
	if plain := ansi.Strip(m.renderProviderPill()); !strings.Contains(plain, "SRC [Spotify] 2/3") || strings.Contains(plain, "Radio") {
		t.Fatalf("compact source indicator = %q, want current provider only", plain)
	}

	ui.PanelWidth = 120
	if plain := ansi.Strip(m.renderProviderPill()); !strings.Contains(plain, "[Radio]") || !strings.Contains(plain, "[Local]") {
		t.Fatalf("wide source indicator = %q, want neighboring providers", plain)
	}
}
