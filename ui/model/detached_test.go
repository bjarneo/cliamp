package model

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/ipc"
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

// Attaching and detaching must not schedule a tick. Each tick schedules the
// next, so a second chain started here would never end, and every attach and
// detach would leave one more behind.
func TestDetachedTransitionsScheduleNoExtraTick(t *testing.T) {
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
	if cmd != nil {
		t.Error("attaching scheduled a tick, want the running one to carry on")
	}
	// The cadence the running tick reads has already changed, which is what
	// makes scheduling one here unnecessary.
	if got := next.tickInterval(); got != ui.TickFast {
		t.Errorf("attached tickInterval() = %v, want %v", got, ui.TickFast)
	}

	updated, cmd = next.Update(SetDetachedMsg{Detached: true})
	if cmd != nil {
		t.Error("detaching scheduled a tick")
	}
	next, _ = updated.(Model)
	if got := next.tickInterval(); got != ui.TickDetached {
		t.Errorf("detached tickInterval() = %v, want %v", got, ui.TickDetached)
	}
	if _, cmd := next.Update(SetDetachedMsg{Detached: true}); cmd != nil {
		t.Error("a repeated state change returned a command")
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

// In a session the quit key hands the terminal back and the music keeps
// playing; only an explicit shutdown ends it.
func TestQuitKeyDetachesInASession(t *testing.T) {
	detached := 0
	m := Model{
		player:   &playbackFakeEngine{playing: true},
		playlist: playlist.New(),
		vis:      ui.NewVisualizer(44100),
	}
	m.SetSessionDetach(func() { detached++ })

	if cmd := m.quit(); cmd != nil {
		t.Error("the quit key returned a command in a session, want a plain detach")
	}
	if detached != 1 {
		t.Errorf("detach calls = %d, want 1", detached)
	}
	if m.quitting {
		t.Error("the quit key stopped the session")
	}

	if cmd := m.shutDown(); cmd == nil {
		t.Error("the shutdown returned no command, want tea.Quit")
	}
	if !m.quitting {
		t.Error("the shutdown did not quit")
	}
	if detached != 1 {
		t.Errorf("detach calls = %d after the shutdown, want it left alone", detached)
	}
}

// Without a session there is nowhere to detach to, so the quit key quits.
func TestQuitKeyQuitsWithoutASession(t *testing.T) {
	m := Model{
		player:   &playbackFakeEngine{playing: true},
		playlist: playlist.New(),
		vis:      ui.NewVisualizer(44100),
	}

	if cmd := m.quit(); cmd == nil {
		t.Error("the quit key returned no command, want tea.Quit")
	}
	if !m.quitting {
		t.Error("the quit key did not quit")
	}
}

// Nothing on the keyboard ends a session -- under a service manager a
// keystroke must not stop the unit -- so the quit operation is what does it,
// and it answers before it goes.
func TestQuitOperationShutsDownAndAnswersFirst(t *testing.T) {
	m := Model{
		player:   &playbackFakeEngine{playing: true},
		playlist: playlist.New(),
		vis:      ui.NewVisualizer(44100),
	}
	m.SetSessionDetach(func() { t.Error("the quit operation detached instead of quitting") })

	jobs := ipc.NewJobStore()
	job, err := jobs.Create("quit")
	if err != nil {
		t.Fatal(err)
	}
	cmd := m.handleV2Request(V2RequestMsg{
		Request: ipc.V2Request{Method: "operation.submit", Operation: "quit"},
		Jobs:    jobs,
		JobID:   job.ID,
	})

	if !m.quitting {
		t.Error("the quit operation did not quit")
	}
	if cmd == nil {
		t.Fatal("the quit operation returned no command, want tea.Quit")
	}
	done, ok := jobs.Get(job.ID)
	if !ok {
		t.Fatal("the quit job is gone")
	}
	if done.State != ipc.JobSucceeded {
		t.Errorf("job state = %q, want %q before the shutdown", done.State, ipc.JobSucceeded)
	}
}

// The keymap has to say what the key does in the mode the user is in.
func TestKeymapLabelsDetachInASession(t *testing.T) {
	m := Model{player: &playbackFakeEngine{}, playlist: playlist.New()}
	var quit commandSpec
	for _, command := range commandRegistry {
		if command.KeyLabel == "q" {
			quit = command
			break
		}
	}
	if quit.KeyLabel != "q" {
		t.Fatal("no command registered for q")
	}
	if label := quit.label(m); label != "Quit" {
		t.Errorf("label without a session = %q, want %q", label, "Quit")
	}
	m.SetSessionDetach(func() {})
	if label := quit.label(m); label == "Quit" {
		t.Errorf("label in a session = %q, want it to mention detaching", label)
	}
}

// A detached session keeps reporting listening progress: it is the same tick
// loop, so providers that track position (Audiobookshelf and friends) see a
// session driven only over IPC exactly as they see the TUI. Issue #377.
func TestDetachedSessionReportsListeningProgress(t *testing.T) {
	prov := &progressProv{reports: make(chan time.Duration, 2)}
	engine := &playbackFakeEngine{playing: true, position: 42 * time.Second}
	m := Model{
		player:             engine,
		playlist:           playlist.New(),
		vis:                ui.NewVisualizer(float64(engine.SampleRate())),
		provider:           prov,
		providers:          []ProviderEntry{{Key: "stub", Name: "Plain", Provider: prov}},
		playingTrack:       stubTracks()[0],
		playingTrackActive: true,
		detached:           true,
	}

	m.Update(tickMsg(time.Now()))

	select {
	case position := <-prov.reports:
		if position != 42*time.Second {
			t.Errorf("reported position = %v, want %v", position, 42*time.Second)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a detached session reported no progress")
	}
}

var _ tea.Msg = SetDetachedMsg{}
