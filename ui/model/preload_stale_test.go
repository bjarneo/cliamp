package model

import (
	"cmp"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/internal/playback"
	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
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
		configSaver: &recordingSaver{},
		preloadFor:  "b.mp3",
	}
	return m, player
}

// Every way of changing the next track drops the armed b.mp3 and arms the
// new next track at once, with no tick.
func TestUpdateDropsStalePreload(t *testing.T) {
	// queueC puts c.mp3 in the play-next list, so c.mp3 is next and armed.
	queueC := func(m *Model, _ *playbackFakeEngine) {
		m.playlist.Queue(2)
		m.preloadFor = "c.mp3"
	}
	for _, tc := range []struct {
		name     string
		cursor   int
		setup    func(m *Model, player *playbackFakeEngine)
		msg      tea.Msg
		wantNext string
	}{
		{name: "IPC playnext.remove", setup: queueC, msg: v2Request(t, "playnext.remove", ipc.Request{Index: 0}), wantNext: "b.mp3"},
		{name: "IPC playnext.clear", setup: queueC, msg: v2Request(t, "playnext.clear", ipc.Request{}), wantNext: "b.mp3"},
		{name: "IPC playnext.move", setup: func(m *Model, player *playbackFakeEngine) {
			queueC(m, player)
			m.playlist.Queue(1)
		}, msg: v2Request(t, "playnext.move", ipc.Request{Index: 0, To: 1}), wantNext: "b.mp3"},
		{name: "IPC playnext.remove while paused", setup: func(m *Model, player *playbackFakeEngine) {
			queueC(m, player)
			player.paused = true
		}, msg: v2Request(t, "playnext.remove", ipc.Request{Index: 0}), wantNext: "b.mp3"},
		{name: "queue overlay remove", setup: func(m *Model, player *playbackFakeEngine) {
			queueC(m, player)
			m.queue.visible = true
		}, msg: tea.KeyPressMsg{Text: "d", Code: 'd'}, wantNext: "b.mp3"},
		{name: "IPC repeat one", msg: v2Request(t, "repeat", ipc.Request{Name: "one"}), wantNext: "a.mp3"},
		{name: "IPC enqueue a later track", msg: v2Request(t, "queue.enqueue", ipc.Request{Index: 2}), wantNext: "c.mp3"},
		{name: "plugin swap next away", msg: PluginQueueMsg{Op: "move", Index: 1, To: 2}, wantNext: "c.mp3"},
		{name: "plugin remove next", msg: PluginQueueMsg{Op: "remove", Index: 1}, wantNext: "c.mp3"},
		{name: "TUI move next down", cursor: 1, msg: tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModShift}, wantNext: "c.mp3"},
		{name: "TUI delete next", cursor: 1, msg: tea.KeyPressMsg{Text: "x", Code: 'x'}, wantNext: "c.mp3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, player := armedModel()
			m.plCursor = tc.cursor
			if tc.setup != nil {
				tc.setup(&m, player)
			}
			armed := m.preloadFor

			next, _ := m.Update(tc.msg)
			m = next.(Model)
			if player.clearPreloadCalls == 0 || (m.preloadFor == armed && (m.preloading || player.hasPreload)) {
				t.Fatalf("%s still armed after the next track changed (ClearPreload %d)", armed, player.clearPreloadCalls)
			}
			if !m.preloading || m.preloadFor != tc.wantNext {
				t.Fatalf("preloading %v for %q, want %s in flight at once", m.preloading, m.preloadFor, tc.wantNext)
			}
		})
	}
}

// While a track buffers, the engine still holds the old pipeline. A change
// of the next track then arms nothing, and the start of the track arms the
// new next track.
func TestRearmWaitsWhileBuffering(t *testing.T) {
	for _, tc := range []struct {
		name string
		msg  tea.Msg
	}{
		{name: "shuffle key", msg: tea.KeyPressMsg{Text: "z", Code: 'z'}},
		{name: "repeat key", msg: tea.KeyPressMsg{Text: "r", Code: 'r'}},
		{name: "queue key", msg: tea.KeyPressMsg{Text: "a", Code: 'a'}},
		{name: "IPC shuffle", msg: v2Request(t, "shuffle", ipc.Request{Name: "on"})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, player := armedModel()
			m.plCursor = 2
			player.hasPreload, m.preloadFor = false, ""
			m.buffering = true

			next, _ := m.Update(tc.msg)
			m = next.(Model)
			if m.preloading || len(player.preloadCalls) != 0 {
				t.Fatalf("preloading %v for %q, calls %v, want no preload while the track buffers", m.preloading, m.preloadFor, player.preloadCalls)
			}
		})
	}
}

// A preload that still loads for the next track is not started again.
func TestPreloadNextKeepsAnInFlightPreload(t *testing.T) {
	m, player := armedModel()
	player.hasPreload, m.preloading = false, true
	gen := m.requests.preload
	if cmd := m.preloadNext(); cmd != nil || m.requests.preload != gen {
		t.Fatal("preloadNext started a second preload of b.mp3")
	}
}

// Appending over IPC while the last track plays with repeat-all puts the new
// track next instead of the first one.
func TestUpdateDropsStalePreloadOnRepeatAllAppend(t *testing.T) {
	m, player := armedModel()
	m.playlist.SetIndex(2)
	m.playlist.SetRepeat(playlist.RepeatAll)
	m.preloadFor = "a.mp3"

	if response := runV2(t, &m, "queue", ipc.Request{Path: "d.mp3"}); !response.OK {
		t.Fatalf("queue response = %+v", response)
	}
	if player.clearPreloadCalls != 1 || !m.preloading || m.preloadFor != "d.mp3" {
		t.Fatalf("ClearPreload %d, preloading %v for %q; want a.mp3 dropped and d.mp3 in flight at once", player.clearPreloadCalls, m.preloading, m.preloadFor)
	}
	next, _ := m.Update(tickMsg(time.Now()))
	if m = next.(Model); player.clearPreloadCalls != 1 || m.preloadFor != "d.mp3" {
		t.Fatalf("ClearPreload %d, preloading %q; want a.mp3 dropped and d.mp3 armed", player.clearPreloadCalls, m.preloadFor)
	}
}

// A message that leaves b.mp3 next keeps it armed. An append of d.mp3 after
// c.mp3 leaves b.mp3 next from each entry point.
func TestUpdateKeepsValidPreload(t *testing.T) {
	d := playlist.Track{Title: "D", Path: "d.mp3", DurationSecs: 180}
	for _, tc := range []struct {
		name  string
		setup func(m *Model)
		msg   tea.Msg
		// wantLen is the queue length after msg. It is 3 when 0.
		wantLen int
	}{
		{name: "status", msg: ShowStatusMsg{}},
		{name: "plugin remove below next", msg: PluginQueueMsg{Op: "remove", Index: 2}, wantLen: 2},
		{name: "tick", msg: tickMsg(time.Now())},
		{name: "plugin add_track", msg: PluginQueueMsg{Op: "add_track", Track: d}, wantLen: 4},
		{name: "plugin add resolved", msg: pluginQueueAddedMsg{tracks: []playlist.Track{d}}, wantLen: 4},
		{name: "IPC queue", msg: v2Request(t, "queue", ipc.Request{Path: d.Path}), wantLen: 4},
		{
			name: "playlist manager append",
			setup: func(m *Model) {
				m.plManager = plManagerState{visible: true, screen: plMgrScreenTracks, selPlaylist: "Other", tracks: []playlist.Track{d}}
			},
			msg:     tea.KeyPressMsg{Text: "A"},
			wantLen: 4,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, player := armedModel()
			if tc.setup != nil {
				tc.setup(&m)
			}
			next, _ := m.Update(tc.msg)
			m = next.(Model)
			if got, want := m.playlist.Len(), cmp.Or(tc.wantLen, 3); got != want {
				t.Fatalf("queue length = %d, want %d", got, want)
			}
			if player.clearPreloadCalls != 0 || !player.hasPreload || m.preloadFor != "b.mp3" {
				t.Fatalf("still-valid b.mp3 was dropped (ClearPreload %d)", player.clearPreloadCalls)
			}
		})
	}
}

// When nothing plays after the current track any more, the preload is
// dropped and nothing replaces it. The first remove drops b.mp3 and arms
// c.mp3 at once. The second remove drops c.mp3.
func TestUpdateDropsPreloadWhenNothingPlaysNext(t *testing.T) {
	m, player := armedModel()
	for range 2 {
		next, _ := m.Update(PluginQueueMsg{Op: "remove", Index: 1})
		m = next.(Model)
	}
	next, _ := m.Update(tickMsg(time.Now()))
	m = next.(Model)
	if player.clearPreloadCalls != 2 || player.hasPreload || m.preloading {
		t.Fatalf("ClearPreload %d, armed %v, preloading %v; want b.mp3 and c.mp3 dropped and nothing armed", player.clearPreloadCalls, player.hasPreload, m.preloading)
	}
}

// A preload still loading is dropped as well, and its late completion does not
// clear the in-flight flag of the one that replaces it. The remove arms the
// replacement c.mp3 at once.
func TestUpdateDropsStaleInFlightPreload(t *testing.T) {
	m, player := armedModel()
	player.hasPreload, m.preloading = false, true
	stale := m.requests.preload

	next, _ := m.Update(PluginQueueMsg{Op: "remove", Index: 1})
	m = next.(Model)
	if player.clearPreloadCalls != 1 || !m.preloading || m.preloadFor != "c.mp3" {
		t.Fatalf("ClearPreload %d, preloading %v for %q; want b.mp3 dropped and c.mp3 in flight", player.clearPreloadCalls, m.preloading, m.preloadFor)
	}
	next, _ = m.Update(tickMsg(time.Now()))
	next, _ = next.(Model).Update(streamPreloadedMsg{path: "b.mp3", gen: stale})
	if m = next.(Model); !m.preloading || m.preloadFor != "c.mp3" {
		t.Fatalf("preloading %v for %q, want c.mp3 still in flight", m.preloading, m.preloadFor)
	}
}

// A shuffle or repeat change over IPC re-arms the preload for the new next
// track at once, and a failed config save shows in the TUI, as the keys do.
func TestV2ModeChangeRearmsPreloadAndReportsSaveError(t *testing.T) {
	for _, tc := range []struct {
		op, name string
	}{
		{op: "shuffle", name: "on"},
		{op: "repeat", name: "one"},
	} {
		t.Run(tc.op, func(t *testing.T) {
			m, player := armedModel()
			m.configSaver = &recordingSaver{err: errors.New("disk full")}

			if response := runV2(t, &m, tc.op, ipc.Request{Name: tc.name}); !response.OK {
				t.Fatalf("response = %+v", response)
			}
			// Shuffle can keep b.mp3 next, and then its preload stays.
			next, ok := m.playlist.PeekNext()
			if !ok || m.preloadFor != next.Path || !m.preloading && !player.hasPreload {
				t.Fatalf("preloading %v for %q after ClearPreload %d, want %q armed at once", m.preloading, m.preloadFor, player.clearPreloadCalls, next.Path)
			}
			if !strings.Contains(m.status.text, "Config save failed: disk full") {
				t.Fatalf("status = %q, want the config save error", m.status.text)
			}
		})
	}
}

// verbReporter records the tracks that a verb scrobbles.
type verbReporter struct {
	plainProv
	scrobbles chan string
}

func (r *verbReporter) CanReportPlayback(playlist.Track) bool { return true }

func (r *verbReporter) ReportNowPlaying(playlist.Track, time.Duration, bool) error { return nil }

func (r *verbReporter) ReportScrobble(track playlist.Track, _, _ time.Duration, _ bool) error {
	r.scrobbles <- track.Path
	return nil
}

// verbState is the part of the Model that an action verb changes.
type verbState struct {
	index, cursor int
	volume        float64
	shuffle       bool
	repeat        playlist.RepeatMode
	vis           string
	saved         string
	// preload is true when an armed or loading preload holds the track
	// that plays next. Shuffle picks that track at random.
	preload     bool
	notified    bool
	scrobbled   string
	pluginState bool
}

// Each action verb reaches the same end state from each entry point: the
// main keys, the full-screen visualizer keys, a playback message from media
// controls or Lua, and a V2 request.
func TestVerbEntryPointsReachTheSameState(t *testing.T) {
	key := func(text string) tea.Msg { return tea.KeyPressMsg{Text: text} }
	fullVis := func(text string) tea.Msg {
		return withSetup{func(m *Model) { m.fullVis = true; m.recomputeLayout() }, tea.KeyPressMsg{Text: text}}
	}
	v2 := func(op string, params ipc.Request) tea.Msg { return v2Request(t, op, params) }
	for _, verb := range []struct {
		name string
		// ref names the entry that the others must match. It is "key" when
		// empty.
		ref string
		// skips is true for a verb that leaves the track, so it scrobbles.
		// notifies is true when the verb tells the media controls and the
		// playback.state plugin hook.
		skips, notifies bool
		entries         map[string]tea.Msg
		// done reports whether the ref state shows the verb.
		done func(verbState) bool
	}{
		{name: "skipNext", skips: true, notifies: true, entries: map[string]tea.Msg{
			"key": key(">"), "full-screen key": fullVis(">"),
			"playback message": playback.NextMsg{}, "V2": v2("next", ipc.Request{}),
		}, done: func(s verbState) bool { return s.index == 1 && s.cursor == 1 && s.scrobbled == "a.mp3" }},
		{name: "skipPrev", skips: true, notifies: true, entries: map[string]tea.Msg{
			"key": key("<"), "full-screen key": fullVis("<"),
			"playback message": playback.PrevMsg{}, "V2": v2("prev", ipc.Request{}),
		}, done: func(s verbState) bool { return s.index == 0 && s.scrobbled == "a.mp3" }},
		{name: "playIndex", skips: true, notifies: true, entries: map[string]tea.Msg{
			"key":        withSetup{func(m *Model) { m.plCursor = 2 }, tea.KeyPressMsg{Code: tea.KeyEnter}},
			"search key": withSetup{func(m *Model) { m.search = searchState{active: true, results: []int{2}} }, tea.KeyPressMsg{Code: tea.KeyEnter}},
			"plugin":     PluginQueueMsg{Op: "jump", Index: 2},
			"V2":         v2("queue.play", ipc.Request{Index: 2}),
		}, done: func(s verbState) bool { return s.index == 2 && s.cursor == 2 && s.scrobbled == "a.mp3" }},
		{name: "setShuffle", entries: map[string]tea.Msg{
			"key": key("z"), "V2": v2("shuffle", ipc.Request{Name: "on"}), "V2 toggle": v2("shuffle", ipc.Request{}),
		}, done: func(s verbState) bool {
			return s.shuffle && s.saved == "map[shuffle:true]" && s.preload
		}},
		{name: "setRepeat", entries: map[string]tea.Msg{
			"key": key("r"), "V2": v2("repeat", ipc.Request{Name: "all"}), "V2 cycle": v2("repeat", ipc.Request{}),
		}, done: func(s verbState) bool {
			return s.repeat == playlist.RepeatAll && s.saved == `map[repeat:"All"]` && s.preload
		}},
		{name: "cycleVisualizer", entries: map[string]tea.Msg{
			"key": key("v"), "full-screen key": fullVis("v"), "V2": v2("vis", ipc.Request{Name: "next"}),
		}, done: func(s verbState) bool { return s.vis == "BarsDot" && s.saved == `map[visualizer:"BarsDot"]` }},
		{name: "adjustVolume", notifies: true, entries: map[string]tea.Msg{
			"key": key("+"), "full-screen key": fullVis("+"), "V2": v2("volume.adjust", ipc.Request{Value: 1}),
		}, done: func(s verbState) bool { return s.volume == 1 }},
		{name: "setVolume", ref: "V2", notifies: true, entries: map[string]tea.Msg{
			"V2": v2("volume", ipc.Request{Value: -6}), "playback message": playback.SetVolumeMsg{VolumeDB: -6},
		}, done: func(s verbState) bool { return s.volume == -6 }},
	} {
		t.Run(verb.name, func(t *testing.T) {
			states := map[string]verbState{}
			for entry, msg := range verb.entries {
				states[entry] = runVerbEntry(t, msg, verb.skips, verb.notifies)
			}
			ref := cmp.Or(verb.ref, "key")
			want := states[ref]
			if !verb.done(want) {
				t.Fatalf("%s state = %+v, want the verb done", ref, want)
			}
			if verb.notifies && (!want.notified || !want.pluginState) {
				t.Fatalf("%s state = %+v, want the media controls and the playback.state hook told", ref, want)
			}
			for entry, got := range states {
				if got != want {
					t.Errorf("%s: state = %+v, want the %s state %+v", entry, got, ref, want)
				}
			}
		})
	}
}

// withSetup is a message that runVerbEntry sends after setup prepares the
// Model, for example to open the full-screen visualizer.
type withSetup struct {
	setup func(m *Model)
	msg   tea.Msg
}

// runVerbEntry sends msg to a Model that plays a.mp3 near its end, with
// b.mp3 armed next, and returns the state that msg leaves.
func runVerbEntry(t *testing.T, msg tea.Msg, skips, notifies bool) verbState {
	t.Helper()
	mgr, messages, _ := newReportTestPlugin(t, "playback.state", `ev.status`)
	engine := &playbackFakeEngine{playing: true, duration: 180 * time.Second, position: 179 * time.Second, hasPreload: true}
	reporter := &verbReporter{scrobbles: make(chan string, 4)}
	notifier := &fakeNotifier{}
	saver := &recordingSaver{}
	m := newColumnTestModel(100, 30)
	m.playlist.Replace([]playlist.Track{
		{Title: "A", Path: "a.mp3", DurationSecs: 180},
		{Title: "B", Path: "b.mp3", DurationSecs: 180},
		{Title: "C", Path: "c.mp3", DurationSecs: 180},
	})
	m.playlist.SetIndex(0)
	m.player, m.notifier, m.configSaver, m.luaMgr = engine, notifier, saver, mgr
	m.providers = []provider.Entry{{Key: "p", Name: "P", Provider: reporter}}
	m.playingTrack, m.playingTrackActive, m.playingTrackStarted = playlist.Track{Title: "A", Path: "a.mp3", DurationSecs: 180}, true, true
	m.preloadFor = "b.mp3"

	switch msg := msg.(type) {
	case withSetup:
		msg.setup(&m)
		next, _ := m.Update(msg.msg)
		m = next.(Model)
	case V2RequestMsg:
		next, _ := m.Update(msg)
		m = next.(Model)
		if job, _ := msg.Jobs.Get(msg.JobID); job.State != ipc.JobSucceeded {
			t.Fatalf("V2 job = %+v, want success", job)
		}
	default:
		next, _ := m.Update(msg)
		m = next.(Model)
	}

	state := verbState{
		index:    m.playlist.Index(),
		cursor:   m.plCursor,
		volume:   engine.volume,
		shuffle:  m.playlist.Shuffled(),
		repeat:   m.playlist.Repeat(),
		vis:      m.vis.ModeName(),
		saved:    fmt.Sprint(saver.saved),
		notified: len(notifier.updates) > 0,
	}
	if next, ok := m.preloadTarget(); ok && (m.preloading || engine.hasPreload) {
		state.preload = m.preloadFor == next.Path
	}
	if skips {
		select {
		case state.scrobbled = <-reporter.scrobbles:
		case <-time.After(2 * time.Second):
		}
	}
	if notifies {
		select {
		case <-messages:
			state.pluginState = true
		case <-time.After(2 * time.Second):
		}
	}
	return state
}
