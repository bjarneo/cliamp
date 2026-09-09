package model

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui"
)

// A detached session renders into a stream nobody reads, so it pays for
// neither the visualizer nor a playback-speed cadence -- only for the
// bookkeeping the tick drives (drain detection, gapless, preload, resume).
func TestDetachedSessionDropsToBookkeepingCadence(t *testing.T) {
	p := &playbackFakeEngine{playing: true}
	m := Model{
		player:   p,
		vis:      ui.NewVisualizer(float64(p.SampleRate())),
		playlist: playlist.New(),
		width:    80,
		height:   24,
	}
	m.recomputeLayout()

	if !m.visualizerVisible() {
		t.Fatal("visualizer hidden while attached, want visible for this setup")
	}
	if got := m.tickInterval(); got != ui.TickFast {
		t.Fatalf("attached tickInterval() = %v, want %v", got, ui.TickFast)
	}

	m.SetDetached(true)

	if m.visualizerVisible() {
		t.Error("visualizer still visible with no client attached")
	}
	if got := m.tickInterval(); got != ui.TickDetached {
		t.Errorf("detached tickInterval() = %v, want %v", got, ui.TickDetached)
	}
}

// Stopped and detached is the state a session started at login sits in for
// hours: it must reach the fully-idle cadence, not the bookkeeping one.
func TestDetachedIdleSessionUsesIdleCadence(t *testing.T) {
	m := Model{
		player:   &playbackFakeEngine{},
		playlist: playlist.New(),
		vis:      ui.NewVisualizer(44100),
		detached: true,
	}

	if got := m.tickInterval(); got != ui.TickIdle {
		t.Errorf("tickInterval() = %v, want %v", got, ui.TickIdle)
	}
}

// Attaching has to restart the tick: a detached session sitting at the idle
// cadence would otherwise take up to TickIdle to draw its first frame.
func TestAttachingRestartsTheTick(t *testing.T) {
	p := &playbackFakeEngine{playing: true}
	m := Model{
		player:   p,
		vis:      ui.NewVisualizer(float64(p.SampleRate())),
		playlist: playlist.New(),
		width:    80,
		height:   24,
		detached: true,
	}
	m.recomputeLayout()

	updated, cmd := m.Update(SetDetachedMsg{Detached: false})
	next, ok := updated.(Model)
	if !ok {
		t.Fatalf("Update returned %T, want Model", updated)
	}
	if next.Detached() {
		t.Error("model still detached after attaching")
	}
	if cmd == nil {
		t.Fatal("attaching returned no command, want a tick")
	}
	if _, ok := cmd().(tickMsg); !ok {
		t.Errorf("command produced %T, want tickMsg", cmd())
	}

	// Detaching again is the same story in reverse, and repeating the state
	// change must not queue redundant ticks.
	updated, cmd = next.Update(SetDetachedMsg{Detached: false})
	if cmd != nil {
		t.Error("attaching an already-attached session queued another tick")
	}
	next, _ = updated.(Model)
	if _, cmd := next.Update(SetDetachedMsg{Detached: true}); cmd == nil {
		t.Error("detaching returned no command, want a tick")
	}
}

// A status bar polling `cliamp vis` against a detached session must get a
// live spectrum, not the frame that was current when the last client left.
func TestDetachedSpectrumIsAnalyzedOnDemand(t *testing.T) {
	engine := &samplingFakeEngine{playbackFakeEngine: &playbackFakeEngine{playing: true}}
	m := Model{
		player:   engine,
		playlist: playlist.New(),
		vis:      ui.NewVisualizer(float64(engine.SampleRate())),
		detached: true,
	}

	response := m.v2BandsResponse()
	if !response.OK {
		t.Fatalf("response = %+v, want ok", response)
	}
	if engine.sampleCalls == 0 {
		t.Error("no samples were read for a detached spectrum request")
	}
	energy := 0.0
	for _, band := range response.Bands {
		energy += band
	}
	if energy <= 0 {
		t.Errorf("bands = %v, want a live spectrum", response.Bands)
	}
}

var _ tea.Msg = SetDetachedMsg{}
