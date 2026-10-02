package model

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/applog"
	"github.com/bjarneo/cliamp/history"
	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/player"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
	"github.com/bjarneo/cliamp/ui"
)

// headlessEngine is a player fake for headless Model tests. It fills the
// audio taps with a tone.
type headlessEngine struct {
	playbackFakeEngine
	tone bool
}

func (e *headlessEngine) SamplesInto(dst []float64) int         { return e.fill(dst) }
func (e *headlessEngine) WaveformSamplesInto(dst []float64) int { return e.fill(dst) }

func (e *headlessEngine) fill(dst []float64) int {
	if !e.tone {
		return 0
	}
	for i := range dst {
		dst[i] = 0.5 * math.Sin(2*math.Pi*440*float64(i)/44100)
	}
	return len(dst)
}

// countingProvider counts the calls that load its playlists.
type countingProvider struct {
	commandsTestProvider
	calls *atomic.Int32
}

func (p countingProvider) Playlists() ([]playlist.PlaylistInfo, error) {
	p.calls.Add(1)
	return nil, nil
}

// newHeadlessModel builds a Model the way cliamp --daemon does: through New,
// then SetHeadless, with no WindowSizeMsg. History goes to a temp directory.
func newHeadlessModel(t *testing.T, engine player.Engine, providers []provider.Entry, tracks ...playlist.Track) Model {
	t.Helper()
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
	pl := playlist.New()
	pl.Add(tracks...)
	m := New(engine, pl, providers, "", nil, nil, history.New(), nil, nil, nil)
	m.SetHeadless(true)
	return m
}

// initMessages runs the Init command of m and the commands that it batches.
// The tick fires at once.
func initMessages(t *testing.T, m Model) []tea.Msg {
	t.Helper()
	prev := teaTick
	t.Cleanup(func() { teaTick = prev })
	teaTick = func(_ time.Duration, fn func(time.Time) tea.Msg) tea.Cmd {
		return func() tea.Msg { return fn(time.Now()) }
	}
	return runCmd(m.Init())
}

// A headless Model has no screen, so Init neither asks for the window size
// nor loads the rows of the provider pane.
func TestHeadlessInitSkipsScreenCommands(t *testing.T) {
	for _, tc := range []struct {
		name          string
		headless      bool
		wantMessages  int
		wantPlaylists int32
	}{
		{name: "TUI", wantMessages: 3, wantPlaylists: 1},
		{name: "headless", headless: true, wantMessages: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := &atomic.Int32{}
			prov := countingProvider{commandsTestProvider{name: "Counting"}, calls}
			m := newHeadlessModel(t, &headlessEngine{}, []provider.Entry{{Key: "counting", Name: "Counting", Provider: prov}})
			m.headless = tc.headless

			msgs := initMessages(t, m)
			if len(msgs) != tc.wantMessages {
				t.Fatalf("Init messages = %T, want %d", msgs, tc.wantMessages)
			}
			if !slices.ContainsFunc(msgs, func(msg tea.Msg) bool { _, ok := msg.(tickMsg); return ok }) {
				t.Fatalf("Init messages = %T, want a tick", msgs)
			}
			if got := calls.Load(); got != tc.wantPlaylists {
				t.Fatalf("provider playlist loads = %d, want %d", got, tc.wantPlaylists)
			}
		})
	}
}

func TestHeadlessViewIsEmpty(t *testing.T) {
	m := newHeadlessModel(t, &headlessEngine{}, nil, playlist.Track{Path: "/music/one.flac", Title: "One"})
	if view := m.View(); view.Content != "" || view.AltScreen || view.WindowTitle != "" {
		t.Fatalf("headless view = %+v, want an empty view", view)
	}
}

// A headless Model with no window size, or with the zero size that a program
// with no terminal sends, plays, skips and advances after a drained track
// like the TUI. It records each track in the history when the track starts,
// and the tick keeps the low-power cadence.
func TestHeadlessZeroSizePlayNextDrain(t *testing.T) {
	tracks := []playlist.Track{
		{Path: "/music/one.flac", Title: "One"},
		{Path: "/music/two.flac", Title: "Two"},
		{Path: "/music/three.flac", Title: "Three"},
	}
	for _, tc := range []struct {
		name string
		size []tea.Msg
	}{
		{name: "no window size"},
		{name: "zero window size", size: []tea.Msg{tea.WindowSizeMsg{}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine := &headlessEngine{}
			m := newHeadlessModel(t, engine, nil, tracks...)
			for _, msg := range tc.size {
				updated, _ := m.Update(msg)
				m = updated.(Model)
			}

			if response := runV2(t, &m, "play", ipc.Request{}); !response.OK {
				t.Fatalf("play = %+v", response)
			}
			if response := runV2(t, &m, "next", ipc.Request{}); !response.OK {
				t.Fatalf("next = %+v", response)
			}
			if got := m.tickInterval(); got != ui.TickLowPowerPlaying {
				t.Fatalf("tick interval while playing = %v, want %v", got, ui.TickLowPowerPlaying)
			}

			engine.drained = true
			updated, _ := m.Update(tickMsg(time.Now()))
			m = updated.(Model)
			want := []string{tracks[0].Path, tracks[1].Path, tracks[2].Path}
			if !slices.Equal(engine.playCalls, want) || m.playlist.Index() != 2 {
				t.Fatalf("play calls = %v at index %d, want %v at index 2", engine.playCalls, m.playlist.Index(), want)
			}

			// The last track drains, so the queue ends.
			engine.drained = true
			updated, _ = m.Update(tickMsg(time.Now()))
			m = updated.(Model)
			if engine.playing || m.runtimeSnapshot().State != "stopped" {
				t.Fatalf("playing = %v, state %q after the queue ended", engine.playing, m.runtimeSnapshot().State)
			}

			entries, err := history.New().Recent(0)
			if err != nil {
				t.Fatal(err)
			}
			var recorded []string
			for _, entry := range entries {
				recorded = append(recorded, entry.Track.Path)
			}
			if wantHistory := []string{tracks[2].Path, tracks[1].Path, tracks[0].Path}; !slices.Equal(recorded, wantHistory) {
				t.Fatalf("history = %v, want %v", recorded, wantHistory)
			}
			if view := m.View(); view.Content != "" {
				t.Fatalf("view = %q, want empty", view.Content)
			}
		})
	}
}

// No screen ticks the visualizer at the frame rate, so a headless Model
// analyzes the audio on each spectrum.get request. A TUI Model reports the
// bands of its last tick.
func TestHeadlessSpectrumAnalyzesOnRequest(t *testing.T) {
	for _, tc := range []struct {
		name          string
		headless      bool
		providerFirst bool
		wantBands     bool
	}{
		{name: "headless", headless: true, wantBands: true},
		{name: "headless after a start in the provider pane", headless: true, providerFirst: true, wantBands: true},
		{name: "TUI without a tick"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine := &headlessEngine{tone: true}
			engine.playing = true
			providers := []provider.Entry{{Key: "local", Name: "Local", Provider: commandsTestProvider{name: "Local"}}}
			m := newHeadlessModel(t, engine, providers, playlist.Track{Path: "/music/one.flac", Title: "One"})
			if tc.providerFirst {
				// run starts an empty queue in the provider pane before it
				// makes the Model headless.
				m.headless = false
				m.StartInProvider()
				m.SetHeadless(true)
			}
			m.headless = tc.headless

			reply := make(chan V2RequestResult, 1)
			updated, _ := m.Update(V2RequestMsg{Request: ipc.V2Request{Method: "spectrum.get"}, Reply: reply})
			m = updated.(Model)
			result := <-reply
			if result.Error != nil {
				t.Fatal(result.Error)
			}
			var response ipc.Response
			if err := json.Unmarshal(result.Result.Result, &response); err != nil {
				t.Fatal(err)
			}
			if !response.OK || response.Visualizer == "" || len(response.Bands) == 0 {
				t.Fatalf("spectrum = %+v", response)
			}
			if got := slices.Max(response.Bands) > 0; got != tc.wantBands {
				t.Fatalf("bands = %v, want sound %v", response.Bands, tc.wantBands)
			}
		})
	}
}

// A track start refreshes the Local pane in the TUI, because it lists
// Recently Played. A headless Model has no pane, so it makes no provider
// call.
func TestHeadlessTrackStartSkipsProviderPaneRefresh(t *testing.T) {
	for _, tc := range []struct {
		name     string
		headless bool
		want     int32
	}{
		{name: "TUI", want: 1},
		{name: "headless", headless: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := &atomic.Int32{}
			prov := countingProvider{commandsTestProvider{name: "Local"}, calls}
			m := newHeadlessModel(t, &headlessEngine{}, []provider.Entry{{Key: providerKeyLocal, Name: "Local", Provider: prov}},
				playlist.Track{Path: "/music/one.flac", Title: "One"})
			m.headless = tc.headless

			runCmd(m.playCurrentTrack())
			if got := calls.Load(); got != tc.want {
				t.Fatalf("provider playlist loads = %d, want %d", got, tc.want)
			}
		})
	}
}

// A failed start goes to the log, because a headless Model shows no error.
func TestPlayFailureIsLogged(t *testing.T) {
	for _, tc := range []struct {
		name  string
		track playlist.Track
	}{
		{name: "local file", track: playlist.Track{Path: "/music/broken.flac", Title: "Broken"}},
		{name: "stream", track: playlist.Track{Path: "https://radio.example.com/offline", Title: "Offline", Stream: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logPath := filepath.Join(t.TempDir(), "cliamp.log")
			closeLog, err := applog.Init(logPath, applog.LevelWarn)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = closeLog() })
			failure := errors.New("device busy")
			m := newHeadlessModel(t, &headlessEngine{playbackFakeEngine: playbackFakeEngine{playErr: failure}}, nil, tc.track)

			m.playCurrentTrack()
			if tc.track.Stream {
				updated, _ := m.Update(streamPlayedMsg{path: tc.track.Path, gen: m.requests.stream, err: failure})
				m = updated.(Model)
			}
			if !errors.Is(m.err, failure) {
				t.Fatalf("model error = %v, want %v", m.err, failure)
			}
			data, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatal(err)
			}
			if log := string(data); !strings.Contains(log, "level=WARN") || !strings.Contains(log, tc.track.Path) || !strings.Contains(log, "device busy") {
				t.Fatalf("log = %q, want a warning with the path and the error", log)
			}
		})
	}
}

// The feeds and pages on the command line resolve after the start. A failure
// goes to the log, because a headless Model shows no error.
func TestURLResolveFailureIsLogged(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "cliamp.log")
	closeLog, err := applog.Init(logPath, applog.LevelWarn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = closeLog() })
	m := newHeadlessModel(t, &headlessEngine{}, nil)

	m.Update(feedsLoadedMsg{err: errors.New("feed offline")})
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if log := string(data); !strings.Contains(log, "level=WARN") || !strings.Contains(log, "feed offline") {
		t.Fatalf("log = %q, want a warning with the error", log)
	}
}
