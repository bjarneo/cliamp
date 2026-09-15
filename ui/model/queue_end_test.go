package model

import (
	"errors"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/internal/playback"
	"github.com/bjarneo/cliamp/luaplugin"
	"github.com/bjarneo/cliamp/player"
	"github.com/bjarneo/cliamp/playlist"
)

// TestQueueEndEvent checks that every way playback can run out of tracks
// reports queue.end once with the finished track, and that advancing when
// nothing is loaded, or after a start failed, reports nothing.
func TestQueueEndEvent(t *testing.T) {
	first := playlist.Track{Path: "/music/first.flac", Artist: "Artist", Title: "First"}
	last := playlist.Track{Path: "https://www.youtube.com/watch?v=abc", Artist: "Artist", Title: "Last"}
	local := playlist.Track{Path: "/music/last.flac", Artist: "Artist", Title: "Last"}
	startErr := errors.New("playback startup failed")
	loadLast := func(_ *testing.T, m *Model) { m.setPlaybackTrack(last) }
	tests := []struct {
		name   string
		player player.Engine
		tracks []playlist.Track
		load   func(t *testing.T, m *Model) // brings the model to the state before advancing
		msgs   []tea.Msg
		want   bool
	}{
		{
			name:   "track drained at the end",
			player: &playbackFakeEngine{playing: true, drained: true},
			tracks: []playlist.Track{first, last},
			load:   loadLast,
			msgs:   []tea.Msg{tickMsg(time.Now())},
			want:   true,
		},
		{
			name:   "next pressed on the last track, then again",
			player: &playbackFakeEngine{playing: true},
			tracks: []playlist.Track{first, last},
			load:   loadLast,
			msgs:   []tea.Msg{playback.NextMsg{}, playback.NextMsg{}},
			want:   true,
		},
		{
			name:   "gapless boundary with nothing left",
			player: &playbackFakeEngine{playing: true, gaplessAdvanced: true},
			tracks: []playlist.Track{last},
			load:   loadLast,
			msgs:   []tea.Msg{tickMsg(time.Now())},
			want:   true,
		},
		{
			name:   "playlist emptied while the track played",
			player: &playbackFakeEngine{playing: true, drained: true},
			tracks: []playlist.Track{last},
			load: func(_ *testing.T, m *Model) {
				m.setPlaybackTrack(last)
				m.detachPlaybackTrack()
				m.replacePlaylist(nil)
			},
			msgs: []tea.Msg{tickMsg(time.Now())},
			want: true,
		},
		{
			name:   "next pressed with nothing loaded",
			player: &playbackFakeEngine{},
			tracks: []playlist.Track{first, last},
			msgs:   []tea.Msg{playback.NextMsg{}},
		},
		{
			name:   "next pressed on an empty playlist",
			player: &playbackFakeEngine{},
			msgs:   []tea.Msg{playback.NextMsg{}},
		},
		{
			name:   "next pressed after a local start failed",
			player: &nowPlayingEngine{startErr: startErr},
			tracks: []playlist.Track{local},
			load: func(t *testing.T, m *Model) {
				m.playTrack(local)
				if !errors.Is(m.err, startErr) {
					t.Fatalf("playback error = %v, want %v", m.err, startErr)
				}
			},
			msgs: []tea.Msg{playback.NextMsg{}},
		},
		{
			name:   "next pressed after a stream start failed",
			player: &nowPlayingEngine{startErr: startErr},
			tracks: []playlist.Track{last},
			load: func(t *testing.T, m *Model) {
				msg, ok := m.playTrack(last)().(streamPlayedMsg)
				if !ok {
					t.Fatal("playback command did not return streamPlayedMsg")
				}
				updated, _ := m.Update(msg)
				*m = updated.(Model)
				if !errors.Is(m.err, startErr) {
					t.Fatalf("playback error = %v, want %v", m.err, startErr)
				}
			},
			msgs: []tea.Msg{playback.NextMsg{}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mgr, messages, closePlugins := newEventTestPlugin(t, luaplugin.EventQueueEnd)
			pl := playlist.New()
			pl.Add(tt.tracks...)
			if len(tt.tracks) > 0 {
				pl.SetIndex(len(tt.tracks) - 1)
			}
			m := Model{player: tt.player, playlist: pl, luaMgr: mgr}
			if tt.load != nil {
				tt.load(t, &m)
			}
			for _, msg := range tt.msgs {
				updated, _ := m.Update(msg)
				m = updated.(Model)
			}
			// Close waits for every asynchronous Lua callback before assertions.
			closePlugins()
			want := last.Path + "\n" + last.Artist + "\n" + last.Title
			select {
			case got := <-messages:
				if !tt.want {
					t.Fatalf("unexpected queue.end: %q", got)
				}
				if got != want {
					t.Fatalf("queue.end carried %q, want %q", got, want)
				}
			default:
				if tt.want {
					t.Fatal("plugin did not receive queue.end")
				}
			}
			select {
			case got := <-messages:
				t.Fatalf("queue.end delivered again: %q", got)
			default:
			}
		})
	}
}

func TestQueueEndAfterReplacementStartFails(t *testing.T) {
	for _, path := range []string{"/music/b.flac", "https://example.com/b.mp3", "https://www.youtube.com/watch?v=b"} {
		for _, ending := range []string{"next", "paused next", "drain"} {
			t.Run(path+"/"+ending, func(t *testing.T) {
				mgr, messages, closePlugins := newEventTestPlugin(t, luaplugin.EventQueueEnd)
				a := playlist.Track{Path: "/music/a.flac", Artist: "A artist", Title: "A"}
				b := playlist.Track{Path: path, Title: "B", Stream: playlist.IsURL(path)}
				pl := playlist.New()
				pl.Add(a, b)
				engine := &nowPlayingEngine{}
				m := Model{player: engine, playlist: pl, luaMgr: mgr}
				m.playTrack(a)
				engine.paused = ending == "paused next"
				engine.startErr = errors.New("B failed")
				pl.SetIndex(1)
				cmd := m.playTrack(b)
				if b.Stream {
					completeQueueEndStream(t, &m, cmd)
				}
				if !errors.Is(m.err, engine.startErr) {
					t.Fatalf("start error = %v, want %v", m.err, engine.startErr)
				}
				if !m.playingTrackActive || m.playingTrack.Path != a.Path || !engine.IsPlaying() {
					t.Fatalf("failed B lost A: active=%t track=%q playing=%t", m.playingTrackActive, m.playingTrack.Path, engine.IsPlaying())
				}
				if ending == "paused next" && !engine.IsPaused() {
					t.Fatal("failed B unpaused A")
				}
				var msg tea.Msg = playback.NextMsg{}
				if ending == "drain" {
					engine.drained = true
					msg = tickMsg(time.Now())
				}
				updated, _ := m.Update(msg)
				m = updated.(Model)
				updated, _ = m.Update(playback.NextMsg{})
				m = updated.(Model)
				closePlugins()
				assertQueueEndTrack(t, messages, a)
			})
		}
	}
}

func TestQueueEndPendingInitialStreamDoesNotFinishTrack(t *testing.T) {
	mgr, messages, closePlugins := newEventTestPlugin(t, luaplugin.EventQueueEnd)
	track := playlist.Track{Path: "https://example.com/pending.mp3", Stream: true}
	pl := playlist.New()
	pl.Add(track)
	m := Model{player: &nowPlayingEngine{}, playlist: pl, luaMgr: mgr}
	cmd := m.playTrack(track)
	updated, _ := m.Update(playback.NextMsg{})
	m = updated.(Model)
	completeQueueEndStream(t, &m, cmd)
	m.Update(playback.NextMsg{})
	closePlugins()
	select {
	case got := <-messages:
		t.Fatalf("unstarted stream emitted queue.end: %q", got)
	default:
	}
}

func TestQueueEndStaleStreamAfterFailedReplacementPreservesTrack(t *testing.T) {
	mgr, messages, closePlugins := newEventTestPlugin(t, luaplugin.EventQueueEnd)
	a := playlist.Track{Path: "/music/a.flac", Title: "A"}
	b := playlist.Track{Path: "https://example.com/b.mp3", Stream: true}
	pl := playlist.New()
	pl.Add(a, b)
	engine := &nowPlayingEngine{}
	m := Model{player: engine, playlist: pl, luaMgr: mgr}
	m.playTrack(a)
	pl.SetIndex(1)
	stale := m.playTrack(b)
	engine.startErr = errors.New("newer B failed")
	completeQueueEndStream(t, &m, m.playTrack(b))
	completeQueueEndStream(t, &m, stale)
	if !m.playingTrackActive || m.playingTrack.Path != a.Path || !errors.Is(m.err, engine.startErr) {
		t.Fatalf("stale result changed retained playback: active=%t track=%q err=%v", m.playingTrackActive, m.playingTrack.Path, m.err)
	}
	updated, _ := m.Update(playback.NextMsg{})
	m = updated.(Model)
	m.Update(playback.NextMsg{})
	closePlugins()
	assertQueueEndTrack(t, messages, a)
}

func TestQueueEndAcknowledgesStreamBeforeCompletionMessage(t *testing.T) {
	for _, replacement := range []string{"", "/music/c.flac", "https://example.com/c.mp3"} {
		t.Run("replacement="+replacement, func(t *testing.T) {
			mgr, messages, closePlugins := newEventTestPlugin(t, luaplugin.EventQueueEnd)
			a := playlist.Track{Path: "/music/a.flac", Title: "A"}
			b := playlist.Track{Path: "https://example.com/b.mp3", Stream: true, Title: "B"}
			pl := playlist.New()
			pl.Add(a, b)
			if replacement != "" {
				pl.Add(playlist.Track{Path: replacement, Stream: playlist.IsURL(replacement)})
			}
			engine := &nowPlayingEngine{}
			m := Model{player: engine, playlist: pl, luaMgr: mgr}
			m.playTrack(a)
			// B has replaced A in the engine, but its UI message is delayed.
			msg := m.nextTrack()()
			if replacement != "" {
				engine.startErr = errors.New("C failed")
				cmd := m.nextTrack()
				if playlist.IsURL(replacement) {
					completeQueueEndStream(t, &m, cmd)
				}
				if !m.playingTrackActive || m.playingTrack.Path != b.Path || !errors.Is(m.err, engine.startErr) {
					t.Fatalf("failed C lost started B: active=%t track=%q err=%v", m.playingTrackActive, m.playingTrack.Path, m.err)
				}
			}
			// Advancing must acknowledge B even if its message still hasn't arrived.
			m.nextTrack()
			updated, _ := m.Update(msg)
			m = updated.(Model)
			m.nextTrack()
			closePlugins()
			assertQueueEndTrack(t, messages, b)
		})
	}
}

func completeQueueEndStream(t *testing.T, m *Model, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("missing stream command")
	}
	msg, ok := cmd().(streamPlayedMsg)
	if !ok {
		t.Fatal("stream command did not return streamPlayedMsg")
	}
	updated, _ := m.Update(msg)
	*m = updated.(Model)
}

func assertQueueEndTrack(t *testing.T, messages <-chan string, track playlist.Track) {
	t.Helper()
	select {
	case got := <-messages:
		if want := track.Path + "\n" + track.Artist + "\n" + track.Title; got != want {
			t.Fatalf("queue.end = %q, want %q", got, want)
		}
	default:
		t.Fatal("missing queue.end")
	}
	select {
	case got := <-messages:
		t.Fatalf("duplicate queue.end: %q", got)
	default:
	}
}
