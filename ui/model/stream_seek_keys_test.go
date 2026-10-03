package model

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/internal/playback"
	"github.com/bjarneo/cliamp/ui"
)

// TestStreamSeekEntryPoints checks that each way to seek a seekable stream
// restarts its decoder in a command, never in Update. A seek key waits for
// the debounce. The command seeks to the target from the position that the
// player reports when the command runs.
func TestStreamSeekEntryPoints(t *testing.T) {
	cases := []struct {
		name       string
		initialPos time.Duration
		settlePos  time.Duration
		want       time.Duration
		invoke     func(*Model) tea.Cmd
		check      func(*testing.T, *Model)
	}{
		{
			name:       "right key",
			initialPos: 3 * time.Second,
			settlePos:  5 * time.Second,
			want:       3 * time.Second,
			invoke: func(m *Model) tea.Cmd {
				return m.handleKey(tea.KeyPressMsg{Code: tea.KeyRight})
			},
		},
		{
			name:       "set position update",
			initialPos: 3 * time.Second,
			settlePos:  5 * time.Second,
			want:       5 * time.Second,
			invoke: func(m *Model) tea.Cmd {
				updated, cmd := m.Update(playback.SetPositionMsg{Position: 10 * time.Second})
				*m = updated.(Model)
				return cmd
			},
		},
		{
			name:       "jump enter",
			initialPos: 3 * time.Second,
			settlePos:  5 * time.Second,
			want:       5 * time.Second,
			invoke: func(m *Model) tea.Cmd {
				m.jump.active = true
				m.jump.input = "10"
				return m.handleJumpKey(tea.KeyPressMsg{Code: tea.KeyEnter})
			},
			check: func(t *testing.T, m *Model) {
				t.Helper()
				if m.jump.active {
					t.Fatal("jump mode remained active after enter")
				}
				if m.jump.input != "" {
					t.Fatalf("jump input = %q, want empty", m.jump.input)
				}
			},
		},
		{
			name:       "seek message",
			initialPos: 3 * time.Second,
			settlePos:  5 * time.Second,
			want:       2 * time.Second,
			invoke: func(m *Model) tea.Cmd {
				updated, cmd := m.Update(playback.SeekMsg{Offset: 4 * time.Second})
				*m = updated.(Model)
				return cmd
			},
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			eng := &playbackFakeEngine{playing: true, seekable: true, duration: time.Hour, position: tt.initialPos}
			m := streamSeekModel(eng)

			cmd := tt.invoke(&m)
			if tt.check != nil {
				tt.check(t, &m)
			}
			if cmd == nil {
				cmd = m.tickSeek(time.Duration(seekDebounceTicks) * ui.TickFast)
			}
			if len(eng.seekCalls) != 0 {
				t.Fatalf("Seek calls in Update = %v, want none", eng.seekCalls)
			}
			if cmd == nil {
				t.Fatal("no seek command, want the seek to run in a command")
			}

			eng.position = tt.settlePos
			if _, ok := cmd().(seekTickMsg); !ok {
				t.Fatal("seek command did not return seekTickMsg")
			}
			if len(eng.seekCalls) != 1 || eng.seekCalls[0] != tt.want {
				t.Fatalf("Seek calls = %v, want [%v]", eng.seekCalls, tt.want)
			}
		})
	}
}
