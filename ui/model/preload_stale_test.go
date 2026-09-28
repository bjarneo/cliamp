package model

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/playlist"
)

// armedModel plays a.mp3 one second before its end, with b.mp3 next and
// already armed, then c.mp3.
func armedModel() (Model, *playbackFakeEngine) {
	player := &playbackFakeEngine{playing: true, duration: 180 * time.Second, position: 179 * time.Second, hasPreload: true}
	p := playlist.New()
	p.Replace([]playlist.Track{
		{Title: "A", Path: "a.mp3", DurationSecs: 180},
		{Title: "B", Path: "b.mp3", DurationSecs: 180},
		{Title: "C", Path: "c.mp3", DurationSecs: 180},
	})
	p.SetIndex(0)
	m := Model{
		player:      player,
		playlist:    p,
		focus:       focusPlaylist,
		configSaver: &recordingConfigSaver{},
		preloadFor:  "b.mp3",
	}
	return m, player
}

func reply() chan ipc.Response { return make(chan ipc.Response, 1) }

// Every way of changing the next track drops the armed b.mp3, and the next
// tick arms the new next track instead.
func TestUpdateDropsStalePreload(t *testing.T) {
	for _, tc := range []struct {
		name     string
		cursor   int
		msg      tea.Msg
		wantNext string
	}{
		{name: "IPC repeat one", msg: ipc.RepeatMsg{Name: "one", Reply: reply()}, wantNext: "a.mp3"},
		{name: "plugin swap next away", msg: PluginQueueMsg{Op: "move", Index: 1, To: 2}, wantNext: "c.mp3"},
		{name: "plugin remove next", msg: PluginQueueMsg{Op: "remove", Index: 1}, wantNext: "c.mp3"},
		{name: "TUI move next down", cursor: 1, msg: tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModShift}, wantNext: "c.mp3"},
		{name: "TUI delete next", cursor: 1, msg: tea.KeyPressMsg{Text: "x", Code: 'x'}, wantNext: "c.mp3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, player := armedModel()
			m.plCursor = tc.cursor

			next, _ := m.Update(tc.msg)
			m = next.(Model)
			if player.clearPreloadCalls == 0 || (m.preloadFor == "b.mp3" && (m.preloading || player.hasPreload)) {
				t.Fatalf("b.mp3 still armed after the next track changed (ClearPreload %d)", player.clearPreloadCalls)
			}

			next, _ = m.Update(tickMsg(time.Now()))
			m = next.(Model)
			if !m.preloading || m.preloadFor != tc.wantNext {
				t.Fatalf("after a tick: preloading %v for %q, want %s", m.preloading, m.preloadFor, tc.wantNext)
			}
		})
	}
}

// Appending over IPC while the last track plays with repeat-all puts the new
// track next instead of the first one.
func TestUpdateDropsStalePreloadOnRepeatAllAppend(t *testing.T) {
	m, player := armedModel()
	m.playlist.SetIndex(2)
	m.playlist.SetRepeat(playlist.RepeatAll)
	m.preloadFor = "a.mp3"

	next, _ := m.Update(ipc.QueueMsg{Path: "d.mp3"})
	next, _ = next.(Model).Update(tickMsg(time.Now()))
	if m = next.(Model); player.clearPreloadCalls != 1 || m.preloadFor != "d.mp3" {
		t.Fatalf("ClearPreload %d, preloading %q; want a.mp3 dropped and d.mp3 armed", player.clearPreloadCalls, m.preloadFor)
	}
}

// A message that leaves b.mp3 next keeps it armed.
func TestUpdateKeepsValidPreload(t *testing.T) {
	for _, tc := range []struct {
		name string
		msg  tea.Msg
	}{
		{name: "status", msg: ShowStatusMsg{}},
		{name: "plugin remove below next", msg: PluginQueueMsg{Op: "remove", Index: 2}},
		{name: "tick", msg: tickMsg(time.Now())},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, player := armedModel()
			next, _ := m.Update(tc.msg)
			if player.clearPreloadCalls != 0 || !player.hasPreload || next.(Model).preloadFor != "b.mp3" {
				t.Fatalf("still-valid b.mp3 was dropped (ClearPreload %d)", player.clearPreloadCalls)
			}
		})
	}
}

// When nothing plays after the current track any more, the preload is
// dropped and nothing replaces it.
func TestUpdateDropsPreloadWhenNothingPlaysNext(t *testing.T) {
	m, player := armedModel()
	for range 2 {
		next, _ := m.Update(PluginQueueMsg{Op: "remove", Index: 1})
		m = next.(Model)
	}
	next, _ := m.Update(tickMsg(time.Now()))
	m = next.(Model)
	if player.clearPreloadCalls != 1 || player.hasPreload || m.preloading {
		t.Fatalf("ClearPreload %d, armed %v, preloading %v; want b.mp3 dropped and nothing armed", player.clearPreloadCalls, player.hasPreload, m.preloading)
	}
}

// A preload still loading is dropped as well, and its late completion does not
// clear the in-flight flag of the one that replaces it.
func TestUpdateDropsStaleInFlightPreload(t *testing.T) {
	m, player := armedModel()
	player.hasPreload, m.preloading = false, true
	stale := m.requests.preload

	next, _ := m.Update(PluginQueueMsg{Op: "remove", Index: 1})
	m = next.(Model)
	if player.clearPreloadCalls != 1 || m.preloading {
		t.Fatalf("in-flight b.mp3 kept (ClearPreload %d, preloading %v)", player.clearPreloadCalls, m.preloading)
	}
	next, _ = m.Update(tickMsg(time.Now()))
	next, _ = next.(Model).Update(streamPreloadedMsg{path: "b.mp3", gen: stale})
	if m = next.(Model); !m.preloading || m.preloadFor != "c.mp3" {
		t.Fatalf("preloading %v for %q, want c.mp3 still in flight", m.preloading, m.preloadFor)
	}
}
