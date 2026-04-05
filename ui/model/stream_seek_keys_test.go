package model

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"cliamp/internal/playback"
	"cliamp/ipc"
)

type fakeEngine struct {
	streamSeek bool
	seekCalls  []time.Duration
	position   time.Duration
}

func (f *fakeEngine) Play(string, time.Duration) error                    { return nil }
func (f *fakeEngine) PlayYTDL(string, time.Duration) error                { return nil }
func (f *fakeEngine) Preload(string, time.Duration) error                 { return nil }
func (f *fakeEngine) PreloadYTDL(string, time.Duration) error             { return nil }
func (f *fakeEngine) ClearPreload()                                       {}
func (f *fakeEngine) Stop()                                               {}
func (f *fakeEngine) Close()                                              {}
func (f *fakeEngine) TogglePause()                                        {}
func (f *fakeEngine) Seek(d time.Duration) error                          { f.seekCalls = append(f.seekCalls, d); return nil }
func (f *fakeEngine) SeekYTDL(time.Duration) error                        { return nil }
func (f *fakeEngine) CancelSeekYTDL()                                     {}
func (f *fakeEngine) IsPlaying() bool                                     { return true }
func (f *fakeEngine) IsPaused() bool                                      { return false }
func (f *fakeEngine) Drained() bool                                       { return false }
func (f *fakeEngine) HasPreload() bool                                    { return false }
func (f *fakeEngine) Seekable() bool                                      { return f.streamSeek }
func (f *fakeEngine) IsStreamSeek() bool                                  { return f.streamSeek }
func (f *fakeEngine) IsYTDLSeek() bool                                    { return false }
func (f *fakeEngine) GaplessAdvanced() bool                               { return false }
func (f *fakeEngine) Position() time.Duration                             { return f.position }
func (f *fakeEngine) Duration() time.Duration                             { return time.Hour }
func (f *fakeEngine) PositionAndDuration() (time.Duration, time.Duration) { return 0, time.Hour }
func (f *fakeEngine) SetVolume(float64)                                   {}
func (f *fakeEngine) Volume() float64                                     { return 0 }
func (f *fakeEngine) SetSpeed(float64)                                    {}
func (f *fakeEngine) Speed() float64                                      { return 1 }
func (f *fakeEngine) ToggleMono()                                         {}
func (f *fakeEngine) Mono() bool                                          { return false }
func (f *fakeEngine) SetEQBand(int, float64)                              {}
func (f *fakeEngine) EQBands() [10]float64                                { return [10]float64{} }
func (f *fakeEngine) StreamErr() error                                    { return nil }
func (f *fakeEngine) StreamTitle() string                                 { return "" }
func (f *fakeEngine) StreamBytes() (downloaded, total int64)              { return 0, 0 }
func (f *fakeEngine) SamplesInto([]float64) int                           { return 0 }
func (f *fakeEngine) SampleRate() int                                     { return 44100 }

func assertStreamSeekCmd(t *testing.T, eng *fakeEngine, cmd tea.Cmd, want time.Duration) {
	t.Helper()

	if cmd == nil {
		t.Fatal("cmd = nil, want seek cmd for HTTP stream")
	}

	msg := cmd()
	if _, ok := msg.(seekTickMsg); !ok {
		t.Fatalf("cmd() msg = %T, want seekTickMsg", msg)
	}

	if len(eng.seekCalls) != 1 {
		t.Fatalf("Seek call count after cmd() = %d, want 1", len(eng.seekCalls))
	}
	if got := eng.seekCalls[0]; got != want {
		t.Fatalf("Seek arg = %v, want %v", got, want)
	}
}

func assertDeferredStreamSeek(t *testing.T, eng *fakeEngine, cmd tea.Cmd, position, want time.Duration) {
	t.Helper()

	if len(eng.seekCalls) != 0 {
		t.Fatalf("Seek call count before cmd() = %d, want 0", len(eng.seekCalls))
	}

	eng.position = position
	assertStreamSeekCmd(t, eng, cmd, want)
}

func assertImmediateStreamSeek(t *testing.T, eng *fakeEngine, cmd tea.Cmd, want time.Duration) {
	t.Helper()

	if cmd != nil {
		t.Fatalf("cmd = %v, want nil for synchronous seek", cmd)
	}
	if len(eng.seekCalls) != 1 {
		t.Fatalf("Seek call count = %d, want 1", len(eng.seekCalls))
	}
	if got := eng.seekCalls[0]; got != want {
		t.Fatalf("Seek arg = %v, want %v", got, want)
	}
}

func TestDeferredHTTPStreamSeek(t *testing.T) {
	cases := []struct {
		name       string
		initialPos time.Duration
		settlePos  time.Duration
		want       time.Duration
		invoke     func(*Model) tea.Cmd
	}{
		{
			name:      "right key",
			settlePos: 8 * time.Second,
			want:      5 * time.Second,
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
				_, cmd := m.Update(playback.SetPositionMsg{Position: 10 * time.Second})
				return cmd
			},
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			eng := &fakeEngine{streamSeek: true, position: tt.initialPos}
			m := Model{player: eng}

			cmd := tt.invoke(&m)
			assertDeferredStreamSeek(t, eng, cmd, tt.settlePos, tt.want)
		})
	}
}

func TestImmediateHTTPStreamSeek(t *testing.T) {
	cases := []struct {
		name   string
		want   time.Duration
		invoke func(*Model) tea.Cmd
		check  func(*testing.T, *Model)
	}{
		{
			name: "jump enter",
			want: 7 * time.Second,
			invoke: func(m *Model) tea.Cmd {
				m.jumping = true
				m.jumpInput = "10"
				return m.handleJumpKey(tea.KeyPressMsg{Code: tea.KeyEnter})
			},
			check: func(t *testing.T, m *Model) {
				t.Helper()
				if m.jumping {
					t.Fatal("jump mode remained active after enter")
				}
				if m.jumpInput != "" {
					t.Fatalf("jump input = %q, want empty", m.jumpInput)
				}
			},
		},
		{
			name: "ipc seek",
			want: 4 * time.Second,
			invoke: func(m *Model) tea.Cmd {
				_, cmd := m.Update(ipc.SeekMsg{Offset: 4 * time.Second})
				return cmd
			},
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			eng := &fakeEngine{streamSeek: true, position: 3 * time.Second}
			m := Model{player: eng}

			cmd := tt.invoke(&m)
			if tt.check != nil {
				tt.check(t, &m)
			}
			assertImmediateStreamSeek(t, eng, cmd, tt.want)
		})
	}
}
