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

// startedPlaybackTrack puts the model in the state it reaches after the engine
// started track and track.change fired.
func startedPlaybackTrack(m *Model, track playlist.Track) {
	m.setPlaybackTrack(track)
	m.playingTrackStarted = true
}

// TestQueueEndEvent checks that every way playback can run out of tracks
// reports queue.end once with the finished track, and that advancing when
// nothing is loaded, while a stream still buffers, or after a start failed
// reports nothing.
func TestQueueEndEvent(t *testing.T) {
	first := playlist.Track{Path: "/music/first.flac", Artist: "Artist", Title: "First"}
	last := playlist.Track{Path: "https://www.youtube.com/watch?v=abc", Artist: "Artist", Title: "Last"}
	local := playlist.Track{Path: "/music/last.flac", Artist: "Artist", Title: "Last"}
	startErr := errors.New("playback startup failed")
	loadLast := func(_ *testing.T, m *Model) { startedPlaybackTrack(m, last) }
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
				startedPlaybackTrack(m, last)
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
			name:   "next pressed while the last track still buffers",
			player: &nowPlayingEngine{},
			tracks: []playlist.Track{last},
			load: func(t *testing.T, m *Model) {
				if m.playTrack(last) == nil {
					t.Fatal("stream playback did not return a command")
				}
				if !m.buffering {
					t.Fatal("stream start did not mark the model as buffering")
				}
			},
			msgs: []tea.Msg{playback.NextMsg{}},
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

// A stream that finishes starting after the queue already ended is refused by
// the stream generation, so it neither plays nor counts as a finished track.
func TestQueueEndStaleStreamAfterEndDoesNotReport(t *testing.T) {
	mgr, messages, closePlugins := newEventTestPlugin(t, luaplugin.EventQueueEnd)
	track := playlist.Track{Path: "https://example.com/pending.mp3", Stream: true, Title: "Pending"}
	pl := playlist.New()
	pl.Add(track)
	m := Model{player: &nowPlayingEngine{}, playlist: pl, luaMgr: mgr}
	cmd := m.playTrack(track)
	if cmd == nil {
		t.Fatal("stream playback did not return a command")
	}
	updated, _ := m.Update(playback.NextMsg{})
	m = updated.(Model)
	msg, ok := cmd().(streamPlayedMsg)
	if !ok {
		t.Fatal("stream command did not return streamPlayedMsg")
	}
	updated, _ = m.Update(msg)
	m = updated.(Model)
	if m.playingTrackActive || m.playingTrackStarted {
		t.Fatalf("stale stream result revived playback: active=%t started=%t", m.playingTrackActive, m.playingTrackStarted)
	}
	m.Update(playback.NextMsg{})
	closePlugins()
	select {
	case got := <-messages:
		t.Fatalf("unstarted stream emitted queue.end: %q", got)
	default:
	}
}

// A manual stop clears the started flag, so the next end of queue after it
// reports nothing until another track starts.
func TestQueueEndNotReportedAfterManualStop(t *testing.T) {
	mgr, messages, closePlugins := newEventTestPlugin(t, luaplugin.EventQueueEnd)
	track := playlist.Track{Path: "/music/only.flac", Title: "Only"}
	pl := playlist.New()
	pl.Add(track)
	m := Model{player: &playbackFakeEngine{playing: true}, playlist: pl, luaMgr: mgr}
	startedPlaybackTrack(&m, track)
	updated, _ := m.Update(playback.StopMsg{})
	m = updated.(Model)
	updated, _ = m.Update(playback.NextMsg{})
	m = updated.(Model)
	closePlugins()
	select {
	case got := <-messages:
		t.Fatalf("queue.end after a manual stop: %q", got)
	default:
	}
}
