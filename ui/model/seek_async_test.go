package model

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/internal/playback"
	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/playlist"
)

// runSeekCmd runs cmd and the commands of any batch it returns, and gives
// back the first seekTickMsg.
func runSeekCmd(t *testing.T, cmd tea.Cmd) seekTickMsg {
	t.Helper()
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		switch msg := c().(type) {
		case seekTickMsg:
			return msg
		case tea.BatchMsg:
			queue = append(queue, msg...)
		}
	}
	t.Fatal("no command returned a seekTickMsg")
	return seekTickMsg{}
}

// TestYTDLSeekEntryPointsRunAsync checks that no seek entry point restarts a
// yt-dlp pipeline on the Update goroutine.
func TestYTDLSeekEntryPointsRunAsync(t *testing.T) {
	track := playlist.Track{Title: "Video", Path: "https://www.youtube.com/watch?v=abc", Stream: true, DurationSecs: 3600}
	cases := []struct {
		name      string
		invoke    func(*Model) tea.Cmd
		wantDelta time.Duration
		check     func(*testing.T, *Model)
	}{
		{
			name:      "prev key rewinds",
			invoke:    func(m *Model) tea.Cmd { return m.handleKey(tea.KeyPressMsg{Code: '<', Text: "<"}) },
			wantDelta: -10 * time.Second,
		},
		{
			name: "prev message rewinds",
			invoke: func(m *Model) tea.Cmd {
				updated, cmd := m.Update(playback.PrevMsg{})
				*m = updated.(Model)
				return cmd
			},
			wantDelta: -10 * time.Second,
		},
		{
			name: "jump enter",
			invoke: func(m *Model) tea.Cmd {
				m.jump.active = true
				m.jump.input = "1:00"
				return m.handleJumpKey(tea.KeyPressMsg{Code: tea.KeyEnter})
			},
			wantDelta: 50 * time.Second,
			check: func(t *testing.T, m *Model) {
				t.Helper()
				if m.jump.active {
					t.Fatal("jump mode remained active after enter")
				}
			},
		},
		{
			name: "seek message",
			invoke: func(m *Model) tea.Cmd {
				updated, cmd := m.Update(playback.SeekMsg{Offset: 30 * time.Second})
				*m = updated.(Model)
				return cmd
			},
			wantDelta: 30 * time.Second,
		},
		{
			name: "set position message",
			invoke: func(m *Model) tea.Cmd {
				updated, cmd := m.Update(playback.SetPositionMsg{Position: 2 * time.Minute})
				*m = updated.(Model)
				return cmd
			},
			wantDelta: 110 * time.Second,
		},
		{
			name: "v2 seek",
			invoke: func(m *Model) tea.Cmd {
				updated, cmd := m.Update(v2Request(t, "seek", ipc.Request{Value: 30}))
				*m = updated.(Model)
				return cmd
			},
			wantDelta: 30 * time.Second,
		},
		{
			name: "v2 seek.absolute",
			invoke: func(m *Model) tea.Cmd {
				updated, cmd := m.Update(v2Request(t, "seek.absolute", ipc.Request{Value: 120}))
				*m = updated.(Model)
				return cmd
			},
			wantDelta: 110 * time.Second,
		},
		{
			name: "resume",
			invoke: func(m *Model) tea.Cmd {
				m.SetResume(track.Path, 90)
				return m.applyResume()
			},
			wantDelta: 80 * time.Second,
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			eng := &playbackFakeEngine{
				playing:  true,
				ytdlSeek: true,
				seekable: true,
				position: 10 * time.Second,
				duration: time.Hour,
			}
			pl := playlist.New()
			pl.Add(track)
			pl.SetIndex(0)
			m := Model{player: eng, playlist: pl, playingTrack: track, playingTrackActive: true}

			cmd := tt.invoke(&m)
			if len(eng.seekCalls) != 0 || len(eng.seekYTDLCalls) != 0 {
				t.Fatalf("inline seeks: Seek %v, SeekYTDL %v, want none", eng.seekCalls, eng.seekYTDLCalls)
			}
			if cmd == nil {
				t.Fatal("cmd = nil, want an async seek command")
			}
			if tt.check != nil {
				tt.check(t, &m)
			}
			msg := runSeekCmd(t, cmd)
			if msg.err != nil {
				t.Fatalf("seek error = %v", msg.err)
			}
			if len(eng.seekYTDLCalls) != 1 || eng.seekYTDLCalls[0] != tt.wantDelta {
				t.Fatalf("SeekYTDL calls = %v, want [%v]", eng.seekYTDLCalls, tt.wantDelta)
			}
		})
	}
}

// TestYTDLRelativeSeeksAccumulate checks that relative seeks sent before an
// in-flight yt-dlp seek reports back add up to one target.
func TestYTDLRelativeSeeksAccumulate(t *testing.T) {
	track := playlist.Track{Title: "Video", Path: "https://www.youtube.com/watch?v=abc", Stream: true, DurationSecs: 3600}
	seekMsg := func(m *Model) tea.Cmd {
		updated, cmd := m.Update(playback.SeekMsg{Offset: 30 * time.Second})
		*m = updated.(Model)
		return cmd
	}
	v2Seek := func(m *Model) tea.Cmd {
		updated, cmd := m.Update(v2Request(t, "seek", ipc.Request{Value: 30}))
		*m = updated.(Model)
		return cmd
	}
	keySeek := func(m *Model) tea.Cmd {
		return m.handleKey(tea.KeyPressMsg{Code: tea.KeyRight})
	}
	cases := []struct {
		name      string
		seeks     []func(*Model) tea.Cmd
		wantDelta time.Duration
	}{
		{name: "two seek messages", seeks: []func(*Model) tea.Cmd{seekMsg, seekMsg}, wantDelta: 60 * time.Second},
		{name: "two v2 seeks", seeks: []func(*Model) tea.Cmd{v2Seek, v2Seek}, wantDelta: 60 * time.Second},
		{name: "seek message extends a debounced key seek", seeks: []func(*Model) tea.Cmd{keySeek, seekMsg}, wantDelta: 35 * time.Second},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			eng := &playbackFakeEngine{
				playing:  true,
				ytdlSeek: true,
				seekable: true,
				position: 10 * time.Second,
				duration: time.Hour,
			}
			pl := playlist.New()
			pl.Add(track)
			pl.SetIndex(0)
			m := Model{player: eng, playlist: pl, playingTrack: track, playingTrackActive: true}

			var cmd tea.Cmd
			for _, seek := range tt.seeks {
				if c := seek(&m); c != nil && cmd == nil {
					cmd = c
				}
			}
			for i := 0; cmd != nil && i < len(tt.seeks); i++ {
				updated, next := m.Update(runSeekCmd(t, cmd))
				m = updated.(Model)
				if !m.seek.inFlight {
					break
				}
				cmd = next
			}
			if m.seek.inFlight || m.seek.pending {
				t.Fatalf("seek still running: inFlight %v, pending %v", m.seek.inFlight, m.seek.pending)
			}
			if n := len(eng.seekYTDLCalls); n == 0 || eng.seekYTDLCalls[n-1] != tt.wantDelta {
				t.Fatalf("SeekYTDL calls = %v, want the last one to be %v", eng.seekYTDLCalls, tt.wantDelta)
			}
		})
	}
}

// A second previous while the rewind of a yt-dlp track still runs goes to
// the previous track. The rewind has not moved Position yet, so previous
// reads the pending seek target, as the TUI clock does.
func TestPreviousDuringRewindGoesBack(t *testing.T) {
	prevMsg := func(t *testing.T, m *Model) {
		updated, _ := m.Update(playback.PrevMsg{})
		*m = updated.(Model)
	}
	prevKey := func(t *testing.T, m *Model) {
		updated, _ := m.Update(tea.KeyPressMsg{Code: '<', Text: "<"})
		*m = updated.(Model)
	}
	v2Prev := func(t *testing.T, m *Model) {
		updated, _ := m.Update(v2Request(t, "prev", ipc.Request{}))
		*m = updated.(Model)
	}
	for _, tc := range []struct {
		name    string
		presses []func(*testing.T, *Model)
	}{
		{name: "two prev messages", presses: []func(*testing.T, *Model){prevMsg, prevMsg}},
		{name: "two prev keys", presses: []func(*testing.T, *Model){prevKey, prevKey}},
		{name: "prev key then V2 prev", presses: []func(*testing.T, *Model){prevKey, v2Prev}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first := playlist.Track{Title: "First", Path: "/music/first.mp3"}
			video := playlist.Track{Title: "Video", Path: "https://www.youtube.com/watch?v=abc", Stream: true, DurationSecs: 3600}
			eng := &playbackFakeEngine{playing: true, ytdlSeek: true, seekable: true, position: time.Minute, duration: time.Hour}
			pl := playlist.New()
			pl.Add(first, video)
			pl.SetIndex(1)
			m := Model{player: eng, playlist: pl, playingTrack: video, playingTrackActive: true, playingTrackStarted: true}

			tc.presses[0](t, &m)
			if !m.seek.active || pl.Index() != 1 {
				t.Fatalf("first previous: seek active %v, index %d, want a rewind of index 1", m.seek.active, pl.Index())
			}
			tc.presses[1](t, &m)
			if pl.Index() != 0 || len(eng.playCalls) != 1 || eng.playCalls[0] != first.Path {
				t.Fatalf("second previous: index %d, plays %v, want %s", pl.Index(), eng.playCalls, first.Path)
			}
			if m.seek.active || m.seek.pending {
				t.Fatalf("seek state after the track change = active %v, pending %v", m.seek.active, m.seek.pending)
			}
		})
	}
}
