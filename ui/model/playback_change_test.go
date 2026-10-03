package model

import (
	"errors"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/internal/playback"
	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// countingReporter counts the scrobbles of each track.
type countingReporter struct {
	plainProv
	mu        sync.Mutex
	scrobbles []string
}

func (r *countingReporter) CanReportPlayback(playlist.Track) bool { return true }

func (r *countingReporter) ReportNowPlaying(playlist.Track, time.Duration, bool) error { return nil }

func (r *countingReporter) ReportScrobble(track playlist.Track, _, _ time.Duration, _ bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.scrobbles = append(r.scrobbles, track.Path)
	return nil
}

func (r *countingReporter) scrobbled() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.scrobbles...)
}

// changeEngine moves its position as a player does: a start plays from its
// offset through startsAtOffset, a stop goes to 0 and a seek that works moves
// the position.
type changeEngine struct {
	playbackFakeEngine
}

func (e *changeEngine) Stop() {
	e.position = 0
	e.playbackFakeEngine.Stop()
}

func (e *changeEngine) Seek(d time.Duration) error {
	if err := e.playbackFakeEngine.Seek(d); err != nil {
		return err
	}
	e.position += d
	return nil
}

// playbackChange is the Model and fakes that one playback-changing message
// runs against.
type playbackChange struct {
	m        Model
	engine   *changeEngine
	notifier *fakeNotifier
	reporter *countingReporter
}

// newPlaybackChange returns a Model that plays a.mp3 at 150 of 180 seconds,
// past the scrobble threshold, with b.mp3 and c.mp3 after it.
func newPlaybackChange() playbackChange {
	engine := &changeEngine{playbackFakeEngine: playbackFakeEngine{playing: true, position: 150 * time.Second, duration: 180 * time.Second, startsAtOffset: true}}
	reporter := &countingReporter{}
	m := newColumnTestModel(100, 30)
	m.playlist.Replace([]playlist.Track{
		{Title: "A", Path: "a.mp3", DurationSecs: 180},
		{Title: "B", Path: "b.mp3", DurationSecs: 180},
		{Title: "C", Path: "c.mp3", DurationSecs: 180},
	})
	m.playlist.SetIndex(0)
	m.player = engine
	m.providers = []provider.Entry{{Key: "p", Name: "P", Provider: reporter}}
	m.playingTrack, m.playingTrackActive, m.playingTrackStarted = playlist.Track{Title: "A", Path: "a.mp3", DurationSecs: 180}, true, true
	notifier := &fakeNotifier{}
	m.notifier = notifier
	// The media controls already show this state.
	m.notifyPlaybackChange()
	notifier.updates = nil
	return playbackChange{m: m, engine: engine, notifier: notifier, reporter: reporter}
}

// playbackChangeNav returns a browser that shows the track d.mp3. With
// confirm, it asks to replace the queue.
func playbackChangeNav(confirm bool) navBrowserState {
	return navBrowserState{
		prov:           commandsTestProvider{name: "Browse"},
		visible:        true,
		mode:           navBrowseModeByAlbum,
		screen:         navBrowseScreenTracks,
		tracks:         []playlist.Track{{Title: "D", Path: "d.mp3"}},
		confirmReplace: confirm,
	}
}

// Each message that changes the playback state tells the media controls
// once, and a message that changes nothing does not. Each message that
// leaves the track that plays scrobbles it once.
func TestPlaybackChangesNotifyAndScrobbleOnce(t *testing.T) {
	key := func(text string) tea.Msg { return tea.KeyPressMsg{Text: text} }
	tests := []struct {
		name  string
		setup func(c *playbackChange)
		msg   func(t *testing.T) tea.Msg
		// notify is true when the message changes the state that the media
		// controls show.
		notify bool
		// scrobble is true when the message leaves a.mp3.
		scrobble bool
	}{
		{name: "next key", msg: func(*testing.T) tea.Msg { return key(">") }, notify: true, scrobble: true},
		{name: "next message", msg: func(*testing.T) tea.Msg { return playback.NextMsg{} }, notify: true, scrobble: true},
		{name: "V2 next", msg: func(t *testing.T) tea.Msg { return v2Request(t, "next", ipc.Request{}) }, notify: true, scrobble: true},
		{name: "prev key restarts a stream", msg: func(*testing.T) tea.Msg { return key("<") }, notify: true, scrobble: true},
		{name: "prev key rewinds a file", setup: func(c *playbackChange) { c.engine.seekable = true },
			msg: func(*testing.T) tea.Msg { return key("<") }, notify: true, scrobble: true},
		{name: "prev message", msg: func(*testing.T) tea.Msg { return playback.PrevMsg{} }, notify: true, scrobble: true},
		{name: "stop key", msg: func(*testing.T) tea.Msg { return key("s") }, notify: true, scrobble: true},
		{name: "stop message", msg: func(*testing.T) tea.Msg { return playback.StopMsg{} }, notify: true, scrobble: true},
		{name: "V2 stop", msg: func(t *testing.T) tea.Msg { return v2Request(t, "stop", ipc.Request{}) }, notify: true, scrobble: true},
		{name: "quit key", msg: func(*testing.T) tea.Msg { return key("q") }, scrobble: true},
		{name: "quit message", msg: func(*testing.T) tea.Msg { return playback.QuitMsg{} }, scrobble: true},
		{name: "V2 queue.play", msg: func(t *testing.T) tea.Msg { return v2Request(t, "queue.play", ipc.Request{Index: 2}) }, notify: true, scrobble: true},
		{name: "V2 queue.clear", msg: func(t *testing.T) tea.Msg { return v2Request(t, "queue.clear", ipc.Request{}) }, notify: true, scrobble: true},
		{name: "plugin jump", msg: func(*testing.T) tea.Msg { return PluginQueueMsg{Op: "jump", Index: 2} }, notify: true, scrobble: true},
		{name: "enter on a row", setup: func(c *playbackChange) { c.m.plCursor = 2 },
			msg: func(*testing.T) tea.Msg { return tea.KeyPressMsg{Code: tea.KeyEnter} }, notify: true, scrobble: true},
		{name: "url.load with play", msg: func(*testing.T) tea.Msg {
			return ipcURLLoadResult{
				request: ipcURLRequest{Play: true, Reply: make(chan ipc.Response, 1)},
				tracks:  []playlist.Track{{Title: "D", Path: "d.mp3"}},
			}
		}, notify: true, scrobble: true},
		{name: "provider.load", msg: func(*testing.T) tea.Msg {
			return ipcProviderLoadResult{
				request: ipcLibraryRequest{Reply: make(chan ipc.Response, 1)},
				tracks:  []playlist.Track{{Title: "D", Path: "d.mp3"}},
			}
		}, notify: true, scrobble: true},
		{name: "a file browser replace", msg: func(*testing.T) tea.Msg {
			return fbTracksResolvedMsg{tracks: []playlist.Track{{Title: "D", Path: "d.mp3"}}, replace: true}
		}, notify: true, scrobble: true},
		{name: "search play now", setup: func(c *playbackChange) {
			c.m.netSearch = netSearchState{active: true, screen: netSearchResults, results: []playlist.Track{{Title: "D", Path: "d.mp3"}}}
		}, msg: func(*testing.T) tea.Msg { return tea.KeyPressMsg{Code: tea.KeyEnter} }, notify: true, scrobble: true},
		{name: "V2 track.play", msg: func(t *testing.T) tea.Msg {
			return v2Request(t, "track.play", ipc.Request{Track: &ipc.TrackInfo{Path: "d.mp3"}})
		}, notify: true, scrobble: true},
		{name: "album play now", setup: func(c *playbackChange) { c.m.requests.searchOverlayAlbum = 1 },
			msg: func(*testing.T) tea.Msg {
				return searchOverlayAlbumTracksMsg{gen: 1, action: searchOverlayAlbumPlay, album: playlist.Track{Title: "Album"},
					tracks: []playlist.Track{{Title: "D", Path: "d.mp3"}}}
			}, notify: true, scrobble: true},
		{name: "enter on a browser track", setup: func(c *playbackChange) { c.m.navBrowser = playbackChangeNav(false) },
			msg: func(*testing.T) tea.Msg { return tea.KeyPressMsg{Code: tea.KeyEnter} }, notify: true, scrobble: true},
		{name: "a browser replace", setup: func(c *playbackChange) { c.m.navBrowser = playbackChangeNav(true) },
			msg: func(*testing.T) tea.Msg { return tea.KeyPressMsg{Code: tea.KeyEnter} }, notify: true, scrobble: true},
		{name: "a playlist manager load", setup: func(c *playbackChange) {
			c.m.plManager.visible = true
			c.m.plManager.screen = plMgrScreenTracks
			c.m.plManager.selPlaylist = "Mix"
			c.m.plManager.tracks = []playlist.Track{{Title: "D", Path: "d.mp3"}}
		}, msg: func(*testing.T) tea.Msg { return tea.KeyPressMsg{Code: tea.KeyEnter} }, notify: true, scrobble: true},
		{name: "remove key on the playing row", setup: func(c *playbackChange) { c.m.focus = focusPlaylist },
			msg: func(*testing.T) tea.Msg { return key("x") }, notify: true, scrobble: true},
		{name: "V2 queue.remove of the playing row", msg: func(t *testing.T) tea.Msg {
			return v2Request(t, "queue.remove", ipc.Request{Index: 0})
		}, notify: true, scrobble: true},
		{name: "plugin remove of the playing row", msg: func(*testing.T) tea.Msg {
			return PluginQueueMsg{Op: "remove", Index: 0}
		}, notify: true, scrobble: true},
		{name: "a gapless advance", setup: func(c *playbackChange) {
			c.engine.gaplessAdvanced = true
			c.engine.lastPlayedDuration = 180 * time.Second
		}, msg: func(*testing.T) tea.Msg { return tickMsg(time.Now()) }, notify: true, scrobble: true},
		{name: "a drained track", setup: func(c *playbackChange) { c.engine.drained = true },
			msg: func(*testing.T) tea.Msg { return tickMsg(time.Now()) }, notify: true, scrobble: true},
		{name: "a drained last track", setup: func(c *playbackChange) {
			c.engine.drained = true
			c.m.playlist.SetIndex(2)
		}, msg: func(*testing.T) tea.Msg { return tickMsg(time.Now()) }, notify: true, scrobble: true},
		{name: "pause message", msg: func(*testing.T) tea.Msg { return playback.PauseMsg{} }, notify: true},
		{name: "pause key", msg: func(*testing.T) tea.Msg { return key("space") }, notify: true},
		{name: "V2 toggle", msg: func(t *testing.T) tea.Msg { return v2Request(t, "toggle", ipc.Request{}) }, notify: true},
		{name: "seek", setup: func(c *playbackChange) { c.engine.seekable = true },
			msg: func(*testing.T) tea.Msg { return playback.SetPositionMsg{Position: 10 * time.Second} }, notify: true},
		{name: "a seek within the second", setup: func(c *playbackChange) { c.engine.seekable = true },
			msg: func(*testing.T) tea.Msg {
				return playback.SetPositionMsg{Position: 150*time.Second + 500*time.Millisecond}
			}, notify: true},
		{name: "volume message", msg: func(*testing.T) tea.Msg { return playback.SetVolumeMsg{VolumeDB: -6} }, notify: true},
		{name: "volume key", msg: func(*testing.T) tea.Msg { return key("+") }, notify: true},
		{name: "a tick a second later", setup: func(c *playbackChange) { c.engine.position += time.Second },
			msg: func(*testing.T) tea.Msg { return tickMsg(time.Now()) }, notify: true},
		{name: "a new stream title", setup: func(c *playbackChange) {
			c.m.playingTrack.Stream = true
			c.m.notifyPlaybackChange()
			c.notifier.updates = nil
			c.engine.streamTitle = "Tycho - Awake"
		}, msg: func(*testing.T) tea.Msg { return tickMsg(time.Now()) }, notify: true},
		{name: "a tick within the second", msg: func(*testing.T) tea.Msg { return tickMsg(time.Now()) }},
		{name: "the same volume", msg: func(*testing.T) tea.Msg { return playback.SetVolumeMsg{VolumeDB: 0} }},
		{name: "a cursor key", msg: func(*testing.T) tea.Msg { return key("j") }},
		{name: "a track that never started", setup: func(c *playbackChange) { c.m.playingTrackStarted = false },
			msg: func(*testing.T) tea.Msg { return key(">") }, notify: true},
		{name: "a track under half played", setup: func(c *playbackChange) { c.engine.position = 60 * time.Second },
			msg: func(*testing.T) tea.Msg { return key(">") }, notify: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newPlaybackChange()
			if tt.setup != nil {
				tt.setup(&c)
			}
			next, _ := c.m.Update(tt.msg(t))
			m := next.(Model)
			if m.reports != nil {
				m.reports.waitIdle(t)
			}
			wantUpdates := 0
			if tt.notify {
				wantUpdates = 1
			}
			if got := len(c.notifier.updates); got != wantUpdates {
				t.Fatalf("notifier updates = %d, want %d: %+v", got, wantUpdates, c.notifier.updates)
			}
			if tt.notify {
				if _, now := m.playbackState(); c.notifier.updates[0] != now {
					t.Fatalf("notified state = %+v, want the current %+v", c.notifier.updates[0], now)
				}
			}
			want := 0
			if tt.scrobble {
				want = 1
			}
			got := c.reporter.scrobbled()
			if len(got) != want {
				t.Fatalf("scrobbles = %v, want %d of a.mp3", got, want)
			}
			if want == 1 && got[0] != "a.mp3" {
				t.Fatalf("scrobbles = %v, want a.mp3", got)
			}
		})
	}
}

// The playback.state plugin event fires once for a change, not again for a
// message that changes nothing.
func TestPlaybackStateEventFiresOncePerChange(t *testing.T) {
	c := newPlaybackChange()
	mgr, messages, _ := newReportTestPlugin(t, "playback.state", `ev.status`)
	c.m.luaMgr = mgr
	c.m.pluginEmit = &pluginEmitState{}

	next, _ := c.m.Update(playback.PauseMsg{})
	next, _ = next.(Model).Update(tickMsg(time.Now()))
	_, _ = next.(Model).Update(tea.KeyPressMsg{Text: "j"})

	select {
	case got := <-messages:
		if got != "paused" {
			t.Fatalf("playback.state status = %q, want paused", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no playback.state event for the pause")
	}
	select {
	case got := <-messages:
		t.Fatalf("second playback.state event %q, want none", got)
	case <-time.After(100 * time.Millisecond):
	}
}

// A track that is left once scrobbles once, also when a stop or a start of
// the next track follows.
func TestLeaveTrackReportsEachStartOnce(t *testing.T) {
	c := newPlaybackChange()
	m := c.m
	m.leaveTrack(180*time.Second, 180*time.Second)
	m.stopPlayback()
	m.leaveTrack(180*time.Second, 180*time.Second)
	if m.reports != nil {
		m.reports.waitIdle(t)
	}
	if got := c.reporter.scrobbled(); len(got) != 1 {
		t.Fatalf("scrobbles = %v, want one", got)
	}
}

// A rewind with previous scrobbles the play so far when it lands, and the
// replay can scrobble again. A rewind that fails plays on as the same play,
// which scrobbles once when it is left.
func TestRewindStartsAReplayWhenItLands(t *testing.T) {
	seekErr := errors.New("seek failed")
	tests := []struct {
		name  string
		setup func(e *changeEngine)
		// drain ends the play at the end of the track instead of with a skip
		// at 170 seconds.
		drain bool
		want  int
	}{
		{name: "an in-place rewind", want: 2},
		{name: "a failed in-place rewind", setup: func(e *changeEngine) { e.seekErr = seekErr }, want: 1},
		{name: "a decoder rewind", setup: func(e *changeEngine) { e.ytdlSeek = true }, want: 2},
		{name: "a failed decoder rewind", setup: func(e *changeEngine) {
			e.ytdlSeek = true
			e.seekYTDLErr = seekErr
		}, want: 1},
		{name: "a failed early rewind", setup: func(e *changeEngine) {
			e.position = 20 * time.Second
			e.seekErr = seekErr
		}, drain: true, want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newPlaybackChange()
			c.engine.seekable = true
			if tt.setup != nil {
				tt.setup(c.engine)
			}
			m := c.m
			if cmd := m.prevTrack(); cmd != nil {
				next, _ := m.Update(cmd())
				m = next.(Model)
			}
			if tt.drain {
				c.engine.position = 180 * time.Second
				c.engine.drained = true
				next, _ := m.Update(tickMsg(time.Now()))
				m = next.(Model)
			} else {
				c.engine.position = 170 * time.Second
				m.nextTrack()
			}
			if m.reports != nil {
				m.reports.waitIdle(t)
			}
			if got := c.reporter.scrobbled(); len(got) != tt.want {
				t.Fatalf("scrobbles = %v, want %d", got, tt.want)
			}
		})
	}
}
