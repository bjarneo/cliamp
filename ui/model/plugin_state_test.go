package model

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/internal/playback"
	"github.com/bjarneo/cliamp/luaplugin"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui"
)

// pluginStateTestMsg is a message that Update ignores. It runs the deferred
// block of update, which publishes the plugin state.
type pluginStateTestMsg struct{}

func newPluginStateModel(engine *playbackFakeEngine, tracks ...playlist.Track) Model {
	pl := playlist.New()
	pl.Replace(tracks)
	pl.SetIndex(0)
	return Model{
		player:      engine,
		playlist:    pl,
		provider:    commandsTestProvider{name: "Test"},
		vis:         ui.NewVisualizer(float64(engine.SampleRate())),
		pluginState: new(atomic.Pointer[PluginState]),
	}
}

// Lua plugins read the track that plays, as the events and the media
// controls report it, and not the current row of the playlist.
func TestPluginStateReportsThePlayingTrack(t *testing.T) {
	tests := []struct {
		name      string
		run       func(t *testing.T) Model
		wantTrack luaplugin.Track
		wantState func(t *testing.T, s PluginState)
	}{
		{
			name: "a list replaces the queue during playback",
			run: func(t *testing.T) Model {
				m := newPluginStateModel(&playbackFakeEngine{playing: true},
					playlist.Track{Title: "Old", Artist: "Band", Path: "old.mp3", DurationSecs: 180})
				m.requests.tracks = 1
				updated, _ := m.Update(tracksLoadedMsg{
					tracks: []playlist.Track{
						{Title: "New 1", Path: "new1.mp3", DurationSecs: 180},
						{Title: "New 2", Path: "new2.mp3", DurationSecs: 180},
					},
					providerName: "Test",
					gen:          1,
				})
				m = updated.(Model)
				if !m.playbackDetached {
					t.Fatal("playbackDetached = false, want the old track to keep playing")
				}
				return m
			},
			wantTrack: luaplugin.Track{Title: "Old", Artist: "Band", Path: "old.mp3", Duration: 180},
			wantState: func(t *testing.T, s PluginState) {
				if s.Status != "playing" || s.Count != 2 || s.Index != 0 {
					t.Errorf("status, count, index = %q, %d, %d; want playing, 2, 0", s.Status, s.Count, s.Index)
				}
				if queue := s.Queue(); len(queue) != 2 || queue[0].Title != "New 1" || queue[1].Title != "New 2" {
					t.Errorf("queue = %+v, want the new list", queue)
				}
			},
		},
		{
			name: "a radio stream with a stream title",
			run: func(t *testing.T) Model {
				station := playlist.Track{Title: "Station", Path: "https://radio.example.com/live", Stream: true}
				m := newPluginStateModel(&playbackFakeEngine{playing: true, live: true}, station)
				m.setPlaybackTrack(station)
				m.streamTitle = "Artist - Song"
				updated, _ := m.Update(pluginStateTestMsg{})
				return updated.(Model)
			},
			wantTrack: luaplugin.Track{Title: "Song", Artist: "Artist", Path: "https://radio.example.com/live", Stream: true, Live: true},
		},
		{
			name: "stopped",
			run: func(t *testing.T) Model {
				m := newPluginStateModel(&playbackFakeEngine{},
					playlist.Track{Title: "A", Path: "a.mp3", Year: 2001, TrackNumber: 3},
					playlist.Track{Title: "B", Path: "b.mp3"})
				m.playlist.Queue(1)
				updated, _ := m.Update(pluginStateTestMsg{})
				return updated.(Model)
			},
			wantTrack: luaplugin.Track{Title: "A", Path: "a.mp3", Year: 2001, Number: 3},
			wantState: func(t *testing.T, s PluginState) {
				if s.Status != "stopped" || !s.HasNext {
					t.Errorf("status, has next = %q, %v; want stopped, true", s.Status, s.HasNext)
				}
				if queue := s.Queue(); len(queue) != 2 || queue[0].Queued || !queue[1].Queued || queue[1].Index != 1 {
					t.Errorf("queue = %+v, want B queued at index 1", queue)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := tt.run(t)
			got := m.PluginStateLoader()()
			if got.Track != tt.wantTrack {
				t.Errorf("track = %+v, want %+v", got.Track, tt.wantTrack)
			}
			if tt.wantState != nil {
				tt.wantState(t, got)
			}
		})
	}
}

// With no plugin loaded, the Model publishes nothing, and a read returns a
// stopped state at normal speed.
func TestPluginStateLoaderWithoutPlugins(t *testing.T) {
	m := Model{player: &playbackFakeEngine{playing: true}, playlist: playlist.New()}
	updated, _ := m.Update(pluginStateTestMsg{})
	m = updated.(Model)
	got := m.PluginStateLoader()()
	if got.Status != "stopped" || got.Speed != 1 || got.Queue() != nil {
		t.Fatalf("PluginStateLoader()() = %+v, want stopped at speed 1 with no queue", got)
	}
}

// The states share the queue until the playlist changes, so an Update does
// not copy a long playlist again.
func TestPluginStateRebuildsQueueOnPlaylistChange(t *testing.T) {
	m := newPluginStateModel(&playbackFakeEngine{},
		playlist.Track{Title: "A", Path: "a.mp3"},
		playlist.Track{Title: "B", Path: "b.mp3"})
	m.publishPluginState()
	load := m.PluginStateLoader()
	first := load().Queue()
	m.publishPluginState()
	if second := load().Queue(); &second[0] != &first[0] {
		t.Fatal("the queue was built again with no playlist change")
	}
	m.playlist.Add(playlist.Track{Title: "C", Path: "c.mp3"})
	m.publishPluginState()
	if third := load().Queue(); len(third) != 3 || third[2].Title != "C" {
		t.Fatalf("queue = %+v, want the added track", third)
	}
}

// A publish does not copy the playlist. The first read of the queue builds
// the rows from the playlist at that time.
func TestPluginStateBuildsQueueOnFirstRead(t *testing.T) {
	m := newPluginStateModel(&playbackFakeEngine{},
		playlist.Track{Title: "A", Path: "a.mp3"},
		playlist.Track{Title: "B", Path: "b.mp3"})
	m.publishPluginState()
	m.playlist.Add(playlist.Track{Title: "C", Path: "c.mp3"})
	if queue := m.PluginStateLoader()().Queue(); len(queue) != 3 || queue[2].Title != "C" {
		t.Fatalf("queue = %+v, want the playlist at the first read", queue)
	}
}

// Plugins read the state on their own goroutines while Update publishes it.
// Run with -race.
func TestPluginStateConcurrentReads(t *testing.T) {
	m := newPluginStateModel(&playbackFakeEngine{playing: true},
		playlist.Track{Title: "A", Path: "a.mp3"})
	// main.go takes the loader from the Model that New returned. The
	// copies that Update returns publish to its store.
	load := m.PluginStateLoader()
	var stop atomic.Bool
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				s := load()
				for _, e := range s.Queue() {
					_ = e.Title
				}
				_ = s.Track.Title
			}
		}()
	}
	for i := range 200 {
		m.playlist.Add(playlist.Track{Title: "T", Path: "t.mp3"})
		msgs := []any{playback.SetSpeedMsg{Ratio: 1 + float64(i%4)/4}, playback.ToggleMonoMsg{}, pluginStateTestMsg{}}
		updated, _ := m.Update(msgs[i%len(msgs)])
		m = updated.(Model)
	}
	stop.Store(true)
	wg.Wait()
	if got := load().Count; got != 201 {
		t.Fatalf("count = %d, want 201", got)
	}
}

// A hook runs on the plugin goroutine while Update goes on, and it must read
// the state that includes the change its event reports. No Update runs in
// these cases, so the hook would read the old state if the Model published
// the state only at the end of Update.
func TestPluginHookReadsTheChangeOfItsEvent(t *testing.T) {
	b := playlist.Track{Title: "B", Path: "b.mp3"}
	tests := []struct {
		event  string
		report string // a Lua expression over the event data ev
		emit   func(m *Model, engine *playbackFakeEngine)
		want   string
	}{
		{luaplugin.EventTrackChange, `ev.title .. "|" .. cliamp.track.title()`, func(m *Model, _ *playbackFakeEngine) {
			m.playlist.SetIndex(1)
			m.setPlaybackTrack(b)
			m.nowPlaying(b)
		}, "B|B"},
		{luaplugin.EventPlaybackState, `ev.status .. "|" .. cliamp.player.state()`, func(m *Model, engine *playbackFakeEngine) {
			engine.paused = true
			m.notifyPlaybackChange()
		}, "paused|paused"},
		{luaplugin.EventQueueChange, `ev.count .. "|" .. cliamp.queue.count()`, func(m *Model, _ *playbackFakeEngine) {
			m.playlist.Add(playlist.Track{Title: "C", Path: "c.mp3"})
			m.emitPluginEvents()
		}, "3|3"},
		{luaplugin.EventPlaybackStop, `cliamp.player.state()`, func(m *Model, _ *playbackFakeEngine) {
			m.stopByUser()
		}, "stopped"},
	}
	for _, tt := range tests {
		t.Run(tt.event, func(t *testing.T) {
			mgr, reports, _ := newReportTestPlugin(t, tt.event, tt.report)
			a := playlist.Track{Title: "A", Path: "a.mp3"}
			engine := &playbackFakeEngine{playing: true}
			m := newPluginStateModel(engine, a, b)
			m.luaMgr = mgr
			m.pluginEmit = &pluginEmitState{}
			m.setPlaybackTrack(a)
			load := m.PluginStateLoader()
			mgr.SetStateProvider(luaplugin.StateProvider{
				PlayerState:   func() string { return load().Status },
				CurrentTrack:  func() luaplugin.Track { return load().Track },
				PlaylistCount: func() int { return load().Count },
			})
			m.emitPluginEvents()
			m.publishPluginState()

			tt.emit(&m, engine)
			select {
			case got := <-reports:
				if got != tt.want {
					t.Fatalf("hook read %q, want %q", got, tt.want)
				}
			case <-time.After(3 * time.Second):
				t.Fatalf("the %s hook did not run", tt.event)
			}
		})
	}
}

// main.go configures the Model after New, and no Update runs before the
// app.start hook. The hook must read the configured track and EQ.
func TestAppStartHookReadsTheConfiguredModel(t *testing.T) {
	mgr, reports, _ := newReportTestPlugin(t, luaplugin.EventAppStart,
		`cliamp.queue.current() .. "|" .. cliamp.player.eq_bands()[1]`)
	pl := playlist.New()
	pl.Replace([]playlist.Track{{Title: "A", Path: "a.mp3"}, {Title: "B", Path: "b.mp3"}})
	pl.SetIndex(0)
	m := New(&playbackFakeEngine{}, pl, nil, "", nil, nil, nil, nil, mgr, nil)
	load := m.PluginStateLoader()
	mgr.SetStateProvider(luaplugin.StateProvider{
		CurrentIndex: func() int { return load().Index },
		EQBands:      func() [10]float64 { return load().EQBands },
	})

	m.SetInitialTrack(1)
	bands := [10]float64{4}
	m.SetEQPreset("", &bands)
	m.Init()
	select {
	case got := <-reports:
		if got != "1|4" {
			t.Fatalf("hook read %q, want %q", got, "1|4")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the app.start hook did not run")
	}
}
