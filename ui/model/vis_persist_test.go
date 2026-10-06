package model

import (
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/config"
	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/ui"
)

// recordingSaver captures config writes so a test can assert what was
// persisted without touching the real config file. saved holds the TOML
// text that config.SaveFunc writes for each key.
type recordingSaver struct {
	saved map[string]string
	err   error
}

func (s *recordingSaver) record(key, value string) error {
	if s.err != nil {
		return s.err
	}
	if s.saved == nil {
		s.saved = map[string]string{}
	}
	s.saved[key] = value
	return nil
}

func (s *recordingSaver) SaveString(key, value string) error {
	return s.record(key, config.QuoteString(value))
}

func (s *recordingSaver) SaveBool(key string, value bool) error {
	return s.record(key, strconv.FormatBool(value))
}

func (s *recordingSaver) SaveFloat(key string, value float64, prec int) error {
	return s.record(key, strconv.FormatFloat(value, 'f', prec, 64))
}

func (s *recordingSaver) SaveFloats(key string, values []float64) error {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = strconv.FormatFloat(v, 'f', -1, 64)
	}
	return s.record(key, "["+strings.Join(parts, ", ")+"]")
}

func visTestModel(saver ConfigSaver) *Model {
	return &Model{vis: ui.NewVisualizer(44100), configSaver: saver}
}

func newVisJob(t *testing.T) (*ipc.JobStore, string) {
	t.Helper()
	jobs := ipc.NewJobStore()
	job, err := jobs.Create("vis")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := jobs.Start(job.ID); err != nil {
		t.Fatalf("Start: %v", err)
	}
	return jobs, job.ID
}

// Setting a visualizer over IPC must persist it, the way the picker does.
// Without this it applied to the running player and vanished on next launch.
func TestVisualizerOverIPCPersists(t *testing.T) {
	saver := &recordingSaver{}
	m := visTestModel(saver)
	jobs, id := newVisJob(t)

	m.handleV2Visualizer(jobs, id, ipc.Request{Cmd: "vis", Name: "Scope"})

	if got := saver.saved["visualizer"]; got != `"Scope"` {
		t.Errorf("saved visualizer = %q, want %q", got, `"Scope"`)
	}
	if got := m.vis.ModeName(); got != "Scope" {
		t.Errorf("live mode = %q, want Scope", got)
	}
}

func TestVisualizerCycleOverIPCPersists(t *testing.T) {
	saver := &recordingSaver{}
	m := visTestModel(saver)
	m.SetVisualizer("Scope")
	jobs, id := newVisJob(t)

	m.handleV2Visualizer(jobs, id, ipc.Request{Cmd: "vis", Name: "next"})

	saved, ok := saver.saved["visualizer"]
	if !ok {
		t.Fatal("cycling over IPC saved nothing")
	}
	if saved == `"Scope"` {
		t.Errorf("saved visualizer = %q, want the mode after cycling", saved)
	}
	if want := `"` + m.vis.ModeName() + `"`; saved != want {
		t.Errorf("saved visualizer = %q, want %q", saved, want)
	}
}

// Listing must not write anything.
func TestVisualizerListDoesNotPersist(t *testing.T) {
	saver := &recordingSaver{}
	m := visTestModel(saver)
	jobs, id := newVisJob(t)

	m.handleV2Visualizer(jobs, id, ipc.Request{Cmd: "vis", Name: "list"})

	if _, ok := saver.saved["visualizer"]; ok {
		t.Error("listing the modes wrote to the config")
	}
}

// The list names every mode that a client can select by name, the Lua
// visualizers too. It also names the active mode and gives its row, because
// a Lua visualizer can have the name of a built-in mode.
func TestVisualizerListIncludesLuaModes(t *testing.T) {
	tests := []struct {
		name string
		mode ui.VisMode
	}{
		{"built-in Bars", ui.VisBars},
		{"Lua Bars", ui.VisCount},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := visTestModel(&recordingSaver{})
			m.RegisterLuaVisualizers([]string{"Bars"}, nil)
			m.vis.SetMode(tt.mode)
			jobs, id := newVisJob(t)

			m.handleV2Visualizer(jobs, id, ipc.Request{Cmd: "vis", Name: "list"})

			job, _ := jobs.Get(id)
			var response ipc.Response
			if err := json.Unmarshal(job.Result, &response); err != nil {
				t.Fatalf("result %s: %v", job.Result, err)
			}
			if want := append(ui.VisModeNames(), "Bars"); !slices.Equal(response.Items, want) {
				t.Fatalf("items = %v, want %v", response.Items, want)
			}
			if response.Visualizer != "Bars" || response.Index != int(tt.mode) {
				t.Fatalf("visualizer, index = %q, %d; want Bars, %d", response.Visualizer, response.Index, tt.mode)
			}
		})
	}
}

// An unknown mode changes nothing, so it must not be persisted either.
func TestVisualizerUnknownModeDoesNotPersist(t *testing.T) {
	saver := &recordingSaver{}
	m := visTestModel(saver)
	jobs, id := newVisJob(t)

	m.handleV2Visualizer(jobs, id, ipc.Request{Cmd: "vis", Name: "NotAMode"})

	if _, ok := saver.saved["visualizer"]; ok {
		t.Error("an unknown mode was persisted")
	}
}

func TestVisualizerSaveFailureFailsTheJob(t *testing.T) {
	saver := &recordingSaver{err: errors.New("disk full")}
	m := visTestModel(saver)
	jobs, id := newVisJob(t)

	m.handleV2Visualizer(jobs, id, ipc.Request{Cmd: "vis", Name: "Scope"})

	job, ok := jobs.Get(id)
	if !ok {
		t.Fatal("job missing")
	}
	if job.State != ipc.JobFailed {
		t.Errorf("job state = %v, want failed when the config write fails", job.State)
	}
}

// Each config write of the Model goes through a typed saver. A string such
// as an audio device name is quoted, a failed write shows in the status
// line, and a Model with no saver writes nothing.
func TestConfigWritesUseTypedSavers(t *testing.T) {
	for _, tc := range []struct {
		name      string
		run       func(m *Model)
		key, want string
	}{
		{name: "v key", run: func(m *Model) { m.handleKey(tea.KeyPressMsg{Text: "v"}) }, key: "visualizer", want: `"BarsDot"`},
		{name: "device switch", run: func(m *Model) {
			next, _ := m.Update(deviceSwitchedMsg{name: `USB "Pro" #2`})
			*m = next.(Model)
		}, key: "audio_device", want: `"USB \"Pro\" #2"`},
		{name: "V2 device switch", run: func(m *Model) { m.applyV2DeviceResponse(ipc.Response{OK: true, Device: "usb"}) }, key: "audio_device", want: `"usb"`},
		{name: "help bar", run: func(m *Model) { m.toggleHelpBar() }, key: "hide_help_bar", want: "true"},
		{name: "speed", run: func(m *Model) { m.player.SetSpeed(1.25); m.saveSpeed() }, key: "speed", want: "1.25"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			saver := &recordingSaver{}
			m := newColumnTestModel(100, 30)
			m.configSaver = saver
			tc.run(&m)
			if got := saver.saved[tc.key]; got != tc.want {
				t.Fatalf("saved %s = %q, want %q", tc.key, got, tc.want)
			}

			m = newColumnTestModel(100, 30)
			m.configSaver = &recordingSaver{err: errors.New("disk full")}
			tc.run(&m)
			if !strings.Contains(m.status.text, "Config save failed: disk full") {
				t.Fatalf("status = %q, want the config save error", m.status.text)
			}

			m = newColumnTestModel(100, 30)
			tc.run(&m)
		})
	}
}
