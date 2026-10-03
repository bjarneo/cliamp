package model

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
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

// detachedEngine is a player fake for detached session tests. It fills the
// audio taps with a tone.
type detachedEngine struct {
	playbackFakeEngine
	tone bool
}

func (e *detachedEngine) SamplesInto(dst []float64) int         { return e.fill(dst) }
func (e *detachedEngine) WaveformSamplesInto(dst []float64) int { return e.fill(dst) }

func (e *detachedEngine) fill(dst []float64) int {
	if !e.tone {
		return 0
	}
	for i := range dst {
		dst[i] = 0.5 * math.Sin(2*math.Pi*440*float64(i)/44100)
	}
	return len(dst)
}

// newDetachedModel builds a Model the way cliamp --daemon does: through New,
// then SetDetached, at the size of the virtual terminal of the session.
// History goes to a temp directory.
func newDetachedModel(t *testing.T, engine player.Engine, providers []provider.Entry, tracks ...playlist.Track) Model {
	t.Helper()
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
	pl := playlist.New()
	pl.Add(tracks...)
	m := New(engine, pl, providers, "", nil, nil, history.New(), nil, nil, nil)
	m.SetDetached(true)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return updated.(Model)
}

// With no client attached a session has no terminal to paint.
func TestDetachedViewIsEmpty(t *testing.T) {
	m := newDetachedModel(t, &detachedEngine{}, nil, playlist.Track{Path: "/music/one.flac", Title: "One"})
	if view := m.View(); view.Content != "" || view.AltScreen || view.WindowTitle != "" {
		t.Fatalf("detached view = %+v, want an empty view", view)
	}
}

// A detached session plays, skips and advances after a drained track like
// the TUI. It records each track in the history when the track starts, and
// the tick keeps the bookkeeping cadence.
func TestDetachedPlayNextDrain(t *testing.T) {
	tracks := []playlist.Track{
		{Path: "/music/one.flac", Title: "One"},
		{Path: "/music/two.flac", Title: "Two"},
		{Path: "/music/three.flac", Title: "Three"},
	}
	engine := &detachedEngine{}
	m := newDetachedModel(t, engine, nil, tracks...)

	if response := runV2(t, &m, "play", ipc.Request{}); !response.OK {
		t.Fatalf("play = %+v", response)
	}
	if response := runV2(t, &m, "next", ipc.Request{}); !response.OK {
		t.Fatalf("next = %+v", response)
	}
	if got := m.tickInterval(); got != ui.TickDetached {
		t.Fatalf("tick interval while playing = %v, want %v", got, ui.TickDetached)
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
}

// No visualizer ticks in a detached session, so it analyzes the audio on
// each spectrum.get request. An attached Model reports the bands of its last
// tick.
func TestDetachedSpectrumAnalyzesOnRequest(t *testing.T) {
	for _, tc := range []struct {
		name          string
		detached      bool
		providerFirst bool
		wantBands     bool
	}{
		{name: "detached", detached: true, wantBands: true},
		{name: "detached after a start in the provider pane", detached: true, providerFirst: true, wantBands: true},
		{name: "attached without a tick"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine := &detachedEngine{tone: true}
			engine.playing = true
			providers := []provider.Entry{{Key: "local", Name: "Local", Provider: commandsTestProvider{name: "Local"}}}
			m := newDetachedModel(t, engine, providers, playlist.Track{Path: "/music/one.flac", Title: "One"})
			if tc.providerFirst {
				m.StartInProvider()
			}
			m.detached = tc.detached

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

// A failed start goes to the log, because a detached session shows no error.
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
			m := newDetachedModel(t, &detachedEngine{playbackFakeEngine: playbackFakeEngine{playErr: failure}}, nil, tc.track)

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
// goes to the log, because a detached session shows no error.
func TestURLResolveFailureIsLogged(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "cliamp.log")
	closeLog, err := applog.Init(logPath, applog.LevelWarn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = closeLog() })
	m := newDetachedModel(t, &detachedEngine{}, nil)

	m.Update(feedsLoadedMsg{err: errors.New("feed offline")})
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if log := string(data); !strings.Contains(log, "level=WARN") || !strings.Contains(log, "feed offline") {
		t.Fatalf("log = %q, want a warning with the error", log)
	}
}
