package model

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/bjarneo/cliamp/playlist"
)

func TestPlaylistStateMarkersStayVisibleWithoutColor(t *testing.T) {
	p := playlist.New()
	p.Add(
		playlist.Track{Path: "/playing.mp3", Title: "Playing"},
		playlist.Track{Title: "Unavailable", Unplayable: true},
	)
	p.Queue(0)
	fake := &playbackFakeEngine{playing: true}
	m := Model{
		layout:    frameLayout{panelWidth: 80},
		player:    fake,
		playlist:  p,
		focus:     focusPlaylist,
		plCursor:  0,
		plVisible: 2,
		favSet:    map[string]struct{}{"/playing.mp3": {}},
	}

	plain := ansi.Strip(m.renderPlaylist())
	if !strings.Contains(plain, ">▶N"+favHeart) {
		t.Fatalf("playlist markers = %q, want cursor, playback, play next, and favorite columns", plain)
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

func TestPlaylistShowsKnownTrackDuration(t *testing.T) {
	p := playlist.New()
	p.Add(playlist.Track{Title: "Timed", DurationSecs: 222})
	m := Model{
		layout:    frameLayout{panelWidth: 80},
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
