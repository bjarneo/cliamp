package model

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"cliamp/playlist"
	"cliamp/ui"
)

type playbackFakeEngine struct {
	playing   bool
	playCalls []string
}

func (f *playbackFakeEngine) Play(path string, _ time.Duration) error {
	f.playing = true
	f.playCalls = append(f.playCalls, path)
	return nil
}
func (f *playbackFakeEngine) PlayYTDL(string, time.Duration) error    { return nil }
func (f *playbackFakeEngine) Preload(string, time.Duration) error     { return nil }
func (f *playbackFakeEngine) PreloadYTDL(string, time.Duration) error { return nil }
func (f *playbackFakeEngine) ClearPreload()                           {}
func (f *playbackFakeEngine) Stop()                                   { f.playing = false }
func (f *playbackFakeEngine) Close()                                  {}
func (f *playbackFakeEngine) TogglePause()                            {}
func (f *playbackFakeEngine) Seek(time.Duration) error                { return nil }
func (f *playbackFakeEngine) SeekYTDL(time.Duration) error            { return nil }
func (f *playbackFakeEngine) CancelSeekYTDL()                         {}
func (f *playbackFakeEngine) IsPlaying() bool                         { return f.playing }
func (f *playbackFakeEngine) IsPaused() bool                          { return false }
func (f *playbackFakeEngine) Drained() bool                           { return false }
func (f *playbackFakeEngine) HasPreload() bool                        { return false }
func (f *playbackFakeEngine) Seekable() bool                          { return false }
func (f *playbackFakeEngine) IsStreamSeek() bool                      { return false }
func (f *playbackFakeEngine) IsYTDLSeek() bool                        { return false }
func (f *playbackFakeEngine) GaplessAdvanced() bool                   { return false }
func (f *playbackFakeEngine) Position() time.Duration                 { return 0 }
func (f *playbackFakeEngine) Duration() time.Duration                 { return 0 }
func (f *playbackFakeEngine) PositionAndDuration() (time.Duration, time.Duration) {
	return 0, 0
}
func (f *playbackFakeEngine) SetVolume(float64)                      {}
func (f *playbackFakeEngine) Volume() float64                        { return 0 }
func (f *playbackFakeEngine) SetSpeed(float64)                       {}
func (f *playbackFakeEngine) Speed() float64                         { return 1 }
func (f *playbackFakeEngine) ToggleMono()                            {}
func (f *playbackFakeEngine) Mono() bool                             { return false }
func (f *playbackFakeEngine) SetEQBand(int, float64)                 {}
func (f *playbackFakeEngine) EQBands() [10]float64                   { return [10]float64{} }
func (f *playbackFakeEngine) StreamErr() error                       { return nil }
func (f *playbackFakeEngine) StreamTitle() string                    { return "" }
func (f *playbackFakeEngine) StreamBytes() (downloaded, total int64) { return 0, 0 }
func (f *playbackFakeEngine) SamplesInto([]float64) int              { return 0 }
func (f *playbackFakeEngine) SampleRate() int                        { return 44100 }

func TestNavTrackListQueueStartsQueuedTrackWhenStopped(t *testing.T) {
	player := &playbackFakeEngine{}
	p := playlist.New()
	p.Replace([]playlist.Track{
		{Title: "Existing", Path: "https://example.com/existing", Stream: true},
		{Title: "Other", Path: "https://example.com/other", Stream: true},
	})
	p.SetIndex(0)

	m := Model{
		player:   player,
		playlist: p,
		vis:      ui.NewVisualizer(float64(player.SampleRate())),
		navBrowser: navBrowserState{
			tracks: []playlist.Track{
				{Title: "Queued", Path: "https://example.com/queued", Stream: true},
			},
		},
	}

	cmd := m.handleNavTrackListKey(tea.KeyPressMsg{Text: "q"})
	if cmd == nil {
		t.Fatal("handleNavTrackListKey(q) = nil, want command")
	}
	if current, idx := m.playlist.Current(); current.Title != "Queued" || idx != 2 {
		t.Fatalf("current = (%q,%d), want (\"Queued\",2)", current.Title, idx)
	}
	if m.plCursor != 2 {
		t.Fatalf("plCursor = %d, want 2", m.plCursor)
	}
	if p.QueueLen() != 0 {
		t.Fatalf("QueueLen() = %d, want 0 after starting queued track", p.QueueLen())
	}
}

func TestTogglePlayPauseRestartsQueuedCurrentTrack(t *testing.T) {
	player := &playbackFakeEngine{}
	p := playlist.New()
	p.Replace([]playlist.Track{
		{Title: "Base", Path: "base.mp3", DurationSecs: 180},
		{Title: "Queued", Path: "queued.mp3", DurationSecs: 180},
	})
	p.SetIndex(0)
	p.Queue(1)
	if track, ok := p.Next(); !ok || track.Title != "Queued" {
		t.Fatalf("Next() = (%q,%t), want (\"Queued\",true)", track.Title, ok)
	}
	if !p.CurrentIsQueued() {
		t.Fatal("CurrentIsQueued() = false, want true")
	}

	m := Model{
		player:   player,
		playlist: p,
		vis:      ui.NewVisualizer(float64(player.SampleRate())),
	}

	if cmd := m.togglePlayPause(); cmd != nil {
		_ = cmd()
	}

	if len(player.playCalls) != 1 || player.playCalls[0] != "queued.mp3" {
		t.Fatalf("playCalls = %v, want [queued.mp3]", player.playCalls)
	}
	if current, idx := m.playlist.Current(); current.Title != "Queued" || idx != 1 {
		t.Fatalf("current = (%q,%d), want (\"Queued\",1)", current.Title, idx)
	}
}

func TestPlayCurrentTrackUnplayableUsesSelectionOrder(t *testing.T) {
	player := &playbackFakeEngine{}
	p := playlist.New()
	p.Replace([]playlist.Track{
		{Title: "Queued", Path: "https://example.com/queued", Stream: true},
		{Title: "Missing", Unplayable: true},
		{Title: "Replacement", Path: "https://example.com/replacement", Stream: true},
	})
	p.SetIndex(1)
	p.Queue(0)

	m := Model{
		player:   player,
		playlist: p,
		vis:      ui.NewVisualizer(float64(player.SampleRate())),
	}

	cmd := m.playCurrentTrack()
	if cmd == nil {
		t.Fatal("playCurrentTrack() = nil, want command")
	}
	if idx := m.playlist.Index(); idx != 2 {
		t.Fatalf("playlist.Index() = %d, want 2", idx)
	}
	if m.plCursor != 2 {
		t.Fatalf("plCursor = %d, want 2", m.plCursor)
	}
	if m.status.text != "Track unavailable, skipping..." {
		t.Fatalf("status.text = %q, want %q", m.status.text, "Track unavailable, skipping...")
	}
	if p.QueueLen() != 1 {
		t.Fatalf("QueueLen() = %d, want 1", p.QueueLen())
	}
}

func TestPlayCurrentTrackUnplayableStopsWhenNoReplacementExists(t *testing.T) {
	player := &playbackFakeEngine{playing: true}
	p := playlist.New()
	p.Replace([]playlist.Track{
		{Title: "Playing", Path: "playing.mp3", DurationSecs: 2},
		{Title: "Missing", Unplayable: true},
	})
	p.SetIndex(1)

	m := Model{
		player:   player,
		playlist: p,
		vis:      ui.NewVisualizer(float64(player.SampleRate())),
	}

	if cmd := m.playCurrentTrack(); cmd != nil {
		t.Fatalf("playCurrentTrack() = %v, want nil", cmd)
	}
	if len(player.playCalls) != 0 {
		t.Fatalf("playCalls = %v, want none", player.playCalls)
	}
	if player.IsPlaying() {
		t.Fatal("player.IsPlaying() = true, want false")
	}
	if _, idx := m.playlist.Current(); idx != 1 {
		t.Fatalf("current index = %d, want 1", idx)
	}
	if m.status.text != "No available tracks" {
		t.Fatalf("status.text = %q, want %q", m.status.text, "No available tracks")
	}
}
