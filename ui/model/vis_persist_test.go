package model

import (
	"errors"
	"testing"

	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/ui"
)

// recordingSaver captures config writes so a test can assert what was
// persisted without touching the real config file.
type recordingSaver struct {
	saved map[string]string
	err   error
}

func (s *recordingSaver) Save(key, value string) error {
	if s.err != nil {
		return s.err
	}
	if s.saved == nil {
		s.saved = map[string]string{}
	}
	s.saved[key] = value
	return nil
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
