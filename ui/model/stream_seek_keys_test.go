package model

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

type fakeEngine struct {
	streamSeek bool
	seekCalls  []time.Duration
}

func (f *fakeEngine) Play(string, time.Duration) error                     { return nil }
func (f *fakeEngine) PlayYTDL(string, time.Duration) error                 { return nil }
func (f *fakeEngine) Preload(string, time.Duration) error                  { return nil }
func (f *fakeEngine) PreloadYTDL(string, time.Duration) error              { return nil }
func (f *fakeEngine) ClearPreload()                                        {}
func (f *fakeEngine) Stop()                                                {}
func (f *fakeEngine) Close()                                               {}
func (f *fakeEngine) TogglePause()                                         {}
func (f *fakeEngine) Seek(d time.Duration) error                           { f.seekCalls = append(f.seekCalls, d); return nil }
func (f *fakeEngine) SeekYTDL(time.Duration) error                         { return nil }
func (f *fakeEngine) CancelSeekYTDL()                                      {}
func (f *fakeEngine) IsPlaying() bool                                      { return true }
func (f *fakeEngine) IsPaused() bool                                       { return false }
func (f *fakeEngine) Drained() bool                                        { return false }
func (f *fakeEngine) HasPreload() bool                                     { return false }
func (f *fakeEngine) Seekable() bool                                       { return f.streamSeek }
func (f *fakeEngine) IsStreamSeek() bool                                   { return f.streamSeek }
func (f *fakeEngine) IsYTDLSeek() bool                                     { return false }
func (f *fakeEngine) GaplessAdvanced() bool                                { return false }
func (f *fakeEngine) Position() time.Duration                              { return 0 }
func (f *fakeEngine) Duration() time.Duration                              { return time.Hour }
func (f *fakeEngine) PositionAndDuration() (time.Duration, time.Duration)  { return 0, time.Hour }
func (f *fakeEngine) SetVolume(float64)                                    {}
func (f *fakeEngine) Volume() float64                                      { return 0 }
func (f *fakeEngine) SetSpeed(float64)                                     {}
func (f *fakeEngine) Speed() float64                                       { return 1 }
func (f *fakeEngine) ToggleMono()                                          {}
func (f *fakeEngine) Mono() bool                                           { return false }
func (f *fakeEngine) SetEQBand(int, float64)                               {}
func (f *fakeEngine) EQBands() [10]float64                                 { return [10]float64{} }
func (f *fakeEngine) StreamErr() error                                     { return nil }
func (f *fakeEngine) StreamTitle() string                                  { return "" }
func (f *fakeEngine) StreamBytes() (downloaded, total int64)               { return 0, 0 }
func (f *fakeEngine) SamplesInto([]float64) int                            { return 0 }
func (f *fakeEngine) SampleRate() int                                      { return 44100 }

func TestHandleKeyRightReturnsCmdForHTTPStreamSeek(t *testing.T) {
	eng := &fakeEngine{streamSeek: true}
	m := Model{player: eng}

	cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyRight})
	if cmd == nil {
		t.Fatal("handleKey(right) cmd = nil, want seek cmd for HTTP stream")
	}

	msg := cmd()
	if _, ok := msg.(seekTickMsg); !ok {
		t.Fatalf("cmd() msg = %T, want seekTickMsg", msg)
	}

	if len(eng.seekCalls) != 1 {
		t.Fatalf("Seek call count = %d, want 1", len(eng.seekCalls))
	}
	if got := eng.seekCalls[0]; got != 5*time.Second {
		t.Fatalf("Seek arg = %v, want %v", got, 5*time.Second)
	}
}
