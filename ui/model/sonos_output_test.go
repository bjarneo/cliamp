package model

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/external/spotify"
	"github.com/bjarneo/cliamp/internal/playback"
	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/player"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui"
	"github.com/charmbracelet/x/ansi"
)

type sonosBridgeFake struct {
	deviceList   []spotify.SonosDevice
	current      spotify.SonosState
	reads        []string
	queueReads   int
	queuePlays   []int
	actions      []string
	volumes      []int
	err          error
	started      chan struct{}
	release      chan struct{}
	queueStarted chan struct{}
}

func (f *sonosBridgeFake) devices(context.Context) ([]spotify.SonosDevice, error) {
	return f.deviceList, f.err
}
func (f *sonosBridgeFake) state(_ context.Context, address string) (spotify.SonosState, error) {
	f.reads = append(f.reads, address)
	return f.current, f.err
}
func (f *sonosBridgeFake) queue(ctx context.Context, address string) (spotify.SonosState, error) {
	f.queueReads++
	f.reads = append(f.reads, "queue:"+address)
	if f.queueStarted != nil {
		f.queueStarted <- struct{}{}
		<-ctx.Done()
		return spotify.SonosState{}, ctx.Err()
	}
	return f.current, f.err
}
func (f *sonosBridgeFake) playQueue(ctx context.Context, _ string, index int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.queuePlays = append(f.queuePlays, index)
	return f.err
}
func (f *sonosBridgeFake) control(ctx context.Context, action, _ string) error {
	f.actions = append(f.actions, action)
	if f.started != nil {
		f.started <- struct{}{}
		select {
		case <-f.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return f.err
}
func (f *sonosBridgeFake) volume(_ context.Context, _ string, value int) error {
	f.volumes = append(f.volumes, value)
	return f.err
}

func sonosTestModel() (*Model, *sonosBridgeFake, *playbackFakeEngine) {
	engine := &playbackFakeEngine{playing: true}
	queue := playlist.New()
	queue.Add(playlist.Track{Title: "Local track", Path: "/tmp/local.flac"})
	volume := 35
	bridge := &sonosBridgeFake{current: spotify.SonosState{Transport: "PLAYING", Volume: &volume}}
	m := &Model{player: engine, playlist: queue, sonosBridge: bridge, playingTrackActive: true, playingTrack: playlist.Track{Title: "Local track"}}
	return m, bridge, engine
}

func executeSonosCmd(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected command")
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, nested := range batch {
			nested()
		}
	}
	return msg
}

func TestSonosAttachIsReadOnlyAndRetiresLocalPlayback(t *testing.T) {
	m, bridge, engine := sonosTestModel()
	cmd := m.selectOutput(player.AudioDevice{Name: "sonos:10.0.0.2", Description: "Sonos · Office"})
	if engine.playing || len(engine.playCalls) != 0 || engine.clearPreloadCalls == 0 {
		t.Fatalf("attach did not safely stop local playback: engine=%+v", engine)
	}
	if m.playlist.Len() != 1 {
		t.Fatalf("local queue changed: %d tracks", m.playlist.Len())
	}
	msg := executeSonosCmd(t, cmd).(sonosQueueMsg)
	m.handleSonosQueue(msg)
	if len(bridge.reads) != 1 || len(bridge.actions) != 0 || len(bridge.volumes) != 0 {
		t.Fatalf("attach was not read-only: reads=%v controls=%v volumes=%v", bridge.reads, bridge.actions, bridge.volumes)
	}
}

func TestSonosQueueSnapshotDoesNotMutateLocalQueueOrPollWithTransport(t *testing.T) {
	m, bridge, _ := sonosTestModel()
	m.layout.panelWidth = 120
	bridge.current = spotify.SonosState{Source: "Sonos virtual input", QueueTotal: 260, Queue: []spotify.SonosTrack{{Title: "saved"}}}
	local := m.playlist.Snapshot()
	cmd := m.selectOutput(player.AudioDevice{Name: "sonos:10.0.0.2", Description: "Sonos · Office"})
	if cmd == nil {
		t.Fatal("attach did not request initial state")
	}
	m.handleSonosQueue(cmd().(sonosQueueMsg))
	if bridge.queueReads != 1 || len(bridge.queuePlays) != 0 || !reflect.DeepEqual(local, m.playlist.Snapshot()) {
		t.Fatalf("attach queue reads=%d plays=%v local queue changed", bridge.queueReads, bridge.queuePlays)
	}
	cmd = m.refreshSonosQueue()
	if cmd == nil {
		t.Fatal("explicit queue read missing")
	}
	m.handleSonosQueue(cmd().(sonosQueueMsg))
	if m.sonosOutput.queue == nil || m.sonosOutput.queue.QueueTotal != 260 {
		t.Fatal("saved queue snapshot missing")
	}
	if header := ansi.Strip(m.renderPlaylistHeader()); !strings.Contains(header, "1 cached of 260") {
		t.Fatalf("queue label did not report snapshot and full total: %q", header)
	}
	cmd = m.refreshSonosState()
	if cmd == nil {
		t.Fatal("transport read missing")
	}
	m.handleSonosState(cmd().(sonosStateMsg))
	if bridge.queueReads != 2 {
		t.Fatalf("transport poll fetched full queue: %d reads", bridge.queueReads)
	}
	m.sonosOutput.queue = &spotify.SonosState{Source: "Sonos queue", Queue: []spotify.SonosTrack{{Title: "A"}, {Title: "B"}}}
	m.sonosOutput.queueCursor = 0
	m.sonosOutput.state = &spotify.SonosState{Source: "Sonos queue"}
	if cmd := m.playSonosQueueItem(2); cmd != nil {
		t.Fatal("out-of-range saved queue index was played")
	}
	m.handleMainKey(tea.KeyPressMsg{Text: "j"})
	cmd = m.handleMainKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil || !reflect.DeepEqual(local, m.playlist.Snapshot()) {
		t.Fatal("selection failed or changed local queue")
	}
	msg := cmd().(sonosControlMsg)
	if msg.err != nil || !reflect.DeepEqual(bridge.queuePlays, []int{1}) {
		t.Fatalf("queue play=%v err=%v", bridge.queuePlays, msg.err)
	}
}

func TestSonosTransportActionsKeepQueueSnapshotAndSelection(t *testing.T) {
	m, bridge, _ := sonosTestModel()
	m.sonosOutput.selected = "10.0.0.2"
	m.sonosOutput.queue = &spotify.SonosState{Source: "Sonos queue", Queue: []spotify.SonosTrack{{Title: "A"}, {Title: "B"}, {Title: "C"}}}
	m.sonosOutput.queueCursor = 2
	for _, action := range []string{"play", "pause", "next", "previous"} {
		cmd := m.controlSonos(action)
		msg := cmd().(sonosControlMsg)
		followup := m.handleSonosControl(msg)
		if followup == nil {
			t.Fatalf("%s did not request a transport refresh", action)
		}
		m.handleSonosState(followup().(sonosStateMsg))
		if bridge.queueReads != 0 || m.sonosOutput.queueCursor != 2 || len(m.sonosOutput.queue.Queue) != 3 {
			t.Fatalf("%s refreshed or moved the saved queue: reads=%d cursor=%d", action, bridge.queueReads, m.sonosOutput.queueCursor)
		}
	}
}

func TestSonosQueueReadCannotReleaseTransportControl(t *testing.T) {
	m, bridge, _ := sonosTestModel()
	m.sonosOutput.selected = "10.0.0.2"
	bridge.queueStarted = make(chan struct{}, 1)
	queueCmd := m.refreshSonosQueue()
	if cmd := m.refreshSonosState(); cmd != nil {
		t.Fatal("transport read overlapped a queue snapshot read")
	}
	queueResult := make(chan tea.Msg, 1)
	go func() { queueResult <- queueCmd() }()
	<-bridge.queueStarted
	controlCmd := m.controlSonos("pause")
	if !m.sonosOutput.busy || m.sonosOutput.queueLoading {
		t.Fatal("control did not cancel the pending snapshot read")
	}
	late := <-queueResult
	m.handleSonosQueue(late.(sonosQueueMsg))
	if !m.sonosOutput.busy || m.sonosOutput.cancel == nil {
		t.Fatal("late queue read released transport control")
	}
	result := controlCmd().(sonosControlMsg)
	followup := m.handleSonosControl(result)
	if followup == nil {
		t.Fatal("transport control did not schedule a fresh read")
	}
	m.handleSonosState(followup().(sonosStateMsg))
	if m.sonosOutput.busy || bridge.queueReads != 1 {
		t.Fatalf("control sequencing ended busy=%t with %d full reads", m.sonosOutput.busy, bridge.queueReads)
	}
}

func TestSonosFailedQueueRefreshKeepsCacheButDisablesSelection(t *testing.T) {
	m, bridge, _ := sonosTestModel()
	m.layout.panelWidth = 120
	m.sonosOutput.selected = "10.0.0.2"
	m.sonosOutput.state = &spotify.SonosState{Source: "Sonos queue"}
	cached := &spotify.SonosState{Source: "Sonos queue", Queue: []spotify.SonosTrack{{Title: "Cached"}}}
	m.sonosOutput.queue = cached
	bridge.err = errors.New("unreachable")
	cmd := m.refreshSonosQueue()
	m.handleSonosQueue(cmd().(sonosQueueMsg))
	if m.sonosOutput.queue != cached || !m.sonosOutput.queueStale {
		t.Fatal("failed refresh discarded the cache or left it marked current")
	}
	if cmd := m.playSonosQueueItem(0); cmd != nil {
		t.Fatal("selected an item from a stale snapshot")
	}
	if got := ansi.Strip(m.renderPlaylistHeader()); !strings.Contains(got, "stale; Enter disabled") {
		t.Fatalf("stale snapshot status missing: %q", got)
	}
}

func TestSonosQueueRejectsVirtualInputAndStaleRead(t *testing.T) {
	m, _, _ := sonosTestModel()
	m.sonosOutput.selected = "10.0.0.2"
	m.sonosOutput.queue = &spotify.SonosState{Source: "Sonos virtual input", Queue: []spotify.SonosTrack{{Title: "not active"}}}
	if cmd := m.playSonosQueueItem(0); cmd != nil || m.sonosOutput.busy {
		t.Fatal("virtual input snapshot treated as active queue")
	}
	m.sonosOutput.queue = nil
	cmd := m.refreshSonosQueue()
	if cmd == nil {
		t.Fatal("queue read missing")
	}
	stale := cmd().(sonosQueueMsg)
	local := m.playlist.Snapshot()
	m.selectOutput(player.AudioDevice{Name: "", Description: "Local (system default)"})
	m.handleSonosQueue(stale)
	if m.sonosOutput.queue != nil || m.sonosOutput.selected != "" || !reflect.DeepEqual(local, m.playlist.Snapshot()) {
		t.Fatal("stale queue read affected the newer Local output")
	}
	m.sonosOutput.selected = "10.0.0.2"
	m.sonosOutput.queue = &spotify.SonosState{Source: "Sonos queue", Queue: []spotify.SonosTrack{{Title: "saved"}}}
	m.sonosOutput.state = &spotify.SonosState{Source: "Sonos queue"}
	selection := m.playSonosQueueItem(0)
	m.selectOutput(player.AudioDevice{Name: "", Description: "Local (system default)"})
	result := selection().(sonosControlMsg)
	if !errors.Is(result.err, context.Canceled) || m.handleSonosControl(result) != nil || len(m.sonosBridge.(*sonosBridgeFake).queuePlays) != 0 {
		t.Fatal("stale queue selection affected the newer Local output")
	}
}

func TestSonosQueueUsesCurrentTransportAndKeepsPickerAndOverlayPriority(t *testing.T) {
	m, _, _ := sonosTestModel()
	m.layout.panelWidth = 100
	m.sonosOutput.selected = "10.0.0.2"
	m.sonosOutput.queue = &spotify.SonosState{Source: "Sonos queue", QueueIndex: 0, Queue: []spotify.SonosTrack{{Title: "Cached"}}}
	m.sonosOutput.state = &spotify.SonosState{Source: "Sonos virtual input", Track: spotify.SonosTrack{Title: "Input"}}
	if cmd := m.playSonosQueueItem(0); cmd != nil || m.sonosOutput.busy {
		t.Fatal("saved queue item played after transport changed to virtual input")
	}
	if got := ansi.Strip(m.renderPlaylist()); strings.Contains(got, "[playing]") {
		t.Fatalf("stale snapshot marked an item playing: %q", got)
	}
	m.devicePicker.visible = false
	if cmd := m.handleMainKey(tea.KeyPressMsg{Text: "d"}); cmd == nil || !m.devicePicker.visible {
		t.Fatal("d no longer opens the output picker")
	}
	m.devicePicker.visible = false
	m.sonosOutput.queueLoading = false
	if cmd := m.handleMainKey(tea.KeyPressMsg{Text: "A"}); cmd == nil || !m.sonosOutput.queueLoading {
		t.Fatal("A did not explicitly refresh the saved queue")
	}
	m.sonosOutput.queueLoading = false
	m.themePicker.visible = true
	if got := ansi.Strip(m.renderPlaylistHeader()); strings.Contains(got, "Sonos saved queue") {
		t.Fatalf("Sonos header overrode active overlay: %q", got)
	}
	m.themePicker.visible = false
	m.focus = focusProvider
	m.provider = &commandsTestProvider{name: "Test provider"}
	if got := ansi.Strip(m.renderPlaylistHeader()); !strings.Contains(got, "Test provider / Playlists") {
		t.Fatalf("Sonos header overrode provider browser: %q", got)
	}
	if got := ansi.Strip(m.renderPlaylist()); strings.Contains(got, "Cached") {
		t.Fatalf("Sonos queue overrode provider list: %q", got)
	}
}

func TestSonosStaleStateAndPickerDiscoveryAreIndependent(t *testing.T) {
	m, bridge, _ := sonosTestModel()
	m.sonosOutput.selected = "10.0.0.1"
	staleGen := m.sonosOutput.gen
	m.openOutputPicker()
	if m.sonosOutput.gen != staleGen {
		t.Fatal("opening picker invalidated selected-output state")
	}
	cmd := m.selectOutput(player.AudioDevice{Name: "sonos:10.0.0.2", Description: "Sonos · Den"})
	m.handleSonosState(sonosStateMsg{gen: staleGen, state: spotify.SonosState{Track: spotify.SonosTrack{Title: "stale"}}})
	if m.sonosOutput.state != nil {
		t.Fatal("stale room state appeared under new selection")
	}
	executeSonosCmd(t, cmd)
	if len(bridge.reads) != 1 || bridge.reads[0] != "queue:10.0.0.2" {
		t.Fatalf("selected room read = %v", bridge.reads)
	}
}

func TestSonosOutputPickerHandlesItsReturnedDiscoveryMessage(t *testing.T) {
	m, bridge, _ := sonosTestModel()
	bridge.deviceList = []spotify.SonosDevice{
		{ID: "room-1", Coordinator: "coordinator-1", Name: "Office", Bonded: true},
		{ID: "sub-1", Coordinator: "coordinator-1", Name: "Office Sub", Satellite: true},
		{ID: "room-2", Coordinator: "coordinator-1", Name: "Study"},
		{ID: "room-3", Coordinator: "192.0.2.12"},
	}
	m.sonosOutput.selected = "room-2"
	m.listAudioDevices = func() ([]player.AudioDevice, error) {
		return []player.AudioDevice{{Name: "sink", Description: "Speakers"}}, nil
	}
	cmd := m.openOutputPicker()
	msg := executeSonosCmd(t, cmd).(sonosDevicesMsg)
	m.handleSonosDevices(msg)
	devices := m.devicePicker.devices
	if m.devicePicker.loading || len(devices) != 5 || devices[0].Description != "Local (system default)" || devices[2].Name != "sonos:room-1" || devices[2].Description != "Sonos · Office" || devices[3].Name != "sonos:room-2" || devices[3].Description != "Sonos · Study" || !devices[3].Active || devices[2].Active || devices[4].Name != "sonos:room-3" || devices[4].Description != "Sonos · room-3" {
		t.Fatalf("picker discovery did not complete: loading=%t devices=%+v", m.devicePicker.loading, m.devicePicker.devices)
	}
	for _, device := range devices {
		if device.Name == "sonos:sub-1" {
			t.Fatal("satellite appeared as a selectable output")
		}
	}
}

func TestSonosControlsSupersedeReadsAndQueueConcurrentActions(t *testing.T) {
	m, bridge, _ := sonosTestModel()
	m.sonosOutput.selected = "10.0.0.2"
	read := m.refreshSonosState()
	if read == nil || !m.sonosOutput.busy {
		t.Fatal("state read did not start")
	}
	pause := m.controlSonos("pause")
	executeSonosCmd(t, pause)
	if got := bridge.actions; len(got) != 1 || got[0] != "pause" {
		t.Fatalf("pause was swallowed behind read: %v", got)
	}
	queued := m.controlSonos("next")
	if queued != nil || len(m.sonosOutput.pending) != 1 {
		t.Fatal("second control was not queued")
	}
	completion := m.handleSonosControl(sonosControlMsg{gen: m.sonosOutput.gen, request: sonosControlRequest{action: "pause"}})
	executeSonosCmd(t, completion)
	if len(bridge.actions) != 2 || bridge.actions[1] != "next" {
		t.Fatalf("queued next was not dispatched: %v", bridge.actions)
	}
}

func TestSonosIPCJobsWaitForMatchingControlAndQueuedCompletion(t *testing.T) {
	m, bridge, _ := sonosTestModel()
	m.sonosOutput.selected = "10.0.0.2"
	bridge.started, bridge.release = make(chan struct{}, 2), make(chan struct{})
	jobs := ipc.NewJobStore()
	first, _ := jobs.Create("pause")
	firstCmd := m.handleV2Request(V2RequestMsg{Jobs: jobs, JobID: first.ID, Request: ipc.V2Request{Version: 2, Operation: "pause"}})
	firstDone := make(chan tea.Msg, 1)
	go func() { firstDone <- firstCmd() }()
	<-bridge.started
	if job, _ := jobs.Get(first.ID); job.State != ipc.JobRunning {
		t.Fatalf("first job completed before bridge response: %s", job.State)
	}
	second, _ := jobs.Create("play")
	if cmd := m.handleV2Request(V2RequestMsg{Jobs: jobs, JobID: second.ID, Request: ipc.V2Request{Version: 2, Operation: "play"}}); cmd != nil {
		t.Fatal("queued control unexpectedly dispatched concurrently")
	}
	if job, _ := jobs.Get(second.ID); job.State != ipc.JobRunning {
		t.Fatalf("queued job completed early: %s", job.State)
	}
	bridge.release <- struct{}{}
	firstMsg := (<-firstDone).(sonosControlMsg)
	secondCmd := m.handleSonosControl(firstMsg)
	if job, _ := jobs.Get(first.ID); job.State != ipc.JobSucceeded {
		t.Fatalf("first job state = %s", job.State)
	}
	secondDone := make(chan tea.Msg, 1)
	go func() { secondDone <- secondCmd() }()
	<-bridge.started
	if job, _ := jobs.Get(second.ID); job.State != ipc.JobRunning {
		t.Fatalf("queued job completed before bridge response: %s", job.State)
	}
	bridge.release <- struct{}{}
	m.handleSonosControl((<-secondDone).(sonosControlMsg))
	if job, _ := jobs.Get(second.ID); job.State != ipc.JobSucceeded {
		t.Fatalf("queued job state = %s", job.State)
	}
}

func TestSonosIPCControlFailureAndOutputSwitchFailJobs(t *testing.T) {
	t.Run("bridge failure", func(t *testing.T) {
		m, bridge, _ := sonosTestModel()
		m.sonosOutput.selected = "10.0.0.2"
		bridge.err = errors.New("unreachable")
		jobs := ipc.NewJobStore()
		job, _ := jobs.Create("pause")
		cmd := m.handleV2Request(V2RequestMsg{Jobs: jobs, JobID: job.ID, Request: ipc.V2Request{Version: 2, Operation: "pause"}})
		m.handleSonosControl(executeSonosCmd(t, cmd).(sonosControlMsg))
		got, _ := jobs.Get(job.ID)
		if got.State != ipc.JobFailed || got.Error == nil {
			t.Fatalf("failed bridge job = %+v", got)
		}
	})
	t.Run("output switch", func(t *testing.T) {
		m, _, _ := sonosTestModel()
		m.sonosOutput.selected = "10.0.0.2"
		jobs := ipc.NewJobStore()
		first, _ := jobs.Create("pause")
		cmd := m.handleV2Request(V2RequestMsg{Jobs: jobs, JobID: first.ID, Request: ipc.V2Request{Version: 2, Operation: "pause"}})
		second, _ := jobs.Create("play")
		m.handleV2Request(V2RequestMsg{Jobs: jobs, JobID: second.ID, Request: ipc.V2Request{Version: 2, Operation: "play"}})
		m.selectOutput(player.AudioDevice{Name: "", Description: "Local (system default)"})
		for _, id := range []string{first.ID, second.ID} {
			got, _ := jobs.Get(id)
			if got.State != ipc.JobFailed {
				t.Fatalf("canceled job %s state = %s", id, got.State)
			}
		}
		_ = cmd
	})
}

func TestSonosCanceledQueuedIPCJobNeverReachesBridge(t *testing.T) {
	m, bridge, _ := sonosTestModel()
	m.sonosOutput.selected = "10.0.0.2"
	bridge.started, bridge.release = make(chan struct{}, 1), make(chan struct{})
	first := v2Request(t, "pause", ipc.Request{})
	firstCmd := m.handleV2Request(first)
	firstDone := make(chan tea.Msg, 1)
	go func() { firstDone <- firstCmd() }()
	<-bridge.started
	queued := v2Request(t, "next", ipc.Request{})
	m.handleV2Request(queued)
	if err := queued.Jobs.Cancel(queued.JobID); err != nil {
		t.Fatal(err)
	}
	bridge.release <- struct{}{}
	next := m.handleSonosControl((<-firstDone).(sonosControlMsg))
	if next != nil {
		executeSonosCmd(t, next)
	}
	job, _ := queued.Jobs.Get(queued.JobID)
	if job.State != ipc.JobCanceled || len(bridge.actions) != 1 || bridge.actions[0] != "pause" {
		t.Fatalf("canceled queued control was dispatched: job=%s actions=%v", job.State, bridge.actions)
	}
}

func TestSonosCancelingInFlightIPCJobCancelsBridgeContext(t *testing.T) {
	m, bridge, _ := sonosTestModel()
	m.sonosOutput.selected = "10.0.0.2"
	bridge.started, bridge.release = make(chan struct{}, 1), make(chan struct{})
	msg := v2Request(t, "pause", ipc.Request{})
	cmd := m.handleV2Request(msg)
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	<-bridge.started
	if err := msg.Jobs.Cancel(msg.JobID); err != nil {
		t.Fatal(err)
	}
	control := (<-done).(sonosControlMsg)
	if !errors.Is(control.err, context.Canceled) {
		t.Fatalf("bridge error = %v, want context canceled", control.err)
	}
	m.handleSonosControl(control)
	job, _ := msg.Jobs.Get(msg.JobID)
	if job.State != ipc.JobCanceled || len(bridge.actions) != 1 {
		t.Fatalf("cancelled request was retried or changed job state: job=%s actions=%v", job.State, bridge.actions)
	}
}

func TestSonosRelativeVolumeUsesPendingIntentAcrossTransportControls(t *testing.T) {
	m, bridge, _ := sonosTestModel()
	m.sonosOutput.selected = "10.0.0.2"
	m.sonosOutput.state = &bridge.current
	first := m.setSonosVolume(2)
	second := m.controlSonos("next")
	third := m.setSonosVolume(2)
	if second != nil || third != nil || len(m.sonosOutput.pending) != 2 {
		t.Fatalf("unexpected queued requests: %+v", m.sonosOutput.pending)
	}
	firstMsg := executeSonosCmd(t, first).(sonosControlMsg)
	nextCmd := m.handleSonosControl(firstMsg)
	if len(bridge.volumes) != 1 || bridge.volumes[0] != 37 {
		t.Fatalf("first volume request = %v", bridge.volumes)
	}
	nextMsg := executeSonosCmd(t, nextCmd).(sonosControlMsg)
	lastCmd := m.handleSonosControl(nextMsg)
	lastMsg := executeSonosCmd(t, lastCmd).(sonosControlMsg)
	m.handleSonosControl(lastMsg)
	if len(bridge.volumes) != 2 || bridge.volumes[1] != 39 {
		t.Fatalf("volume intent lost behind transport action: %v", bridge.volumes)
	}
}

func TestSonosVolumeDispatchesFromKeysMediaAndIPC(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(*Model) tea.Cmd
		want int
	}{
		{name: "key", run: func(m *Model) tea.Cmd { return m.handleMainKey(tea.KeyPressMsg{Text: "+"}) }, want: 36},
		{name: "media", run: func(m *Model) tea.Cmd { _, cmd := m.Update(playback.SetVolumeMsg{VolumeDB: 0}); return cmd }, want: 94},
		{name: "ipc", run: func(m *Model) tea.Cmd {
			jobs := ipc.NewJobStore()
			job, _ := jobs.Create("volume.adjust")
			params, _ := json.Marshal(map[string]float64{"value": 1})
			return m.handleV2Request(V2RequestMsg{Jobs: jobs, JobID: job.ID, Request: ipc.V2Request{Version: 2, Operation: "volume.adjust", Params: params}})
		}, want: 36},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, bridge, _ := sonosTestModel()
			m.sonosOutput.selected = "10.0.0.2"
			m.sonosOutput.state = &bridge.current
			cmd := tc.run(m)
			if cmd == nil {
				t.Fatal("no volume command returned")
			}
			executeSonosCmd(t, cmd)
			if len(bridge.volumes) != 1 || bridge.volumes[0] != tc.want {
				t.Fatalf("dispatched volumes = %v, want [%d]", bridge.volumes, tc.want)
			}
		})
	}
}

func TestSonosIPCTransportJobsWaitForTheirRemoteAction(t *testing.T) {
	for _, tc := range []struct {
		operation string
		transport string
		action    string
	}{
		{operation: "play", transport: "PAUSED_PLAYBACK", action: "play"},
		{operation: "pause", transport: "PLAYING", action: "pause"},
		{operation: "toggle", transport: "PLAYING", action: "toggle"},
		{operation: "toggle", transport: "PAUSED_PLAYBACK", action: "toggle"},
		{operation: "next", transport: "PLAYING", action: "next"},
		{operation: "prev", transport: "PLAYING", action: "previous"},
	} {
		t.Run(tc.operation+"/"+tc.transport, func(t *testing.T) {
			m, bridge, engine := sonosTestModel()
			m.sonosOutput.selected = "10.0.0.2"
			state := bridge.current
			state.Transport = tc.transport
			m.sonosOutput.state = &state
			msg := v2Request(t, tc.operation, ipc.Request{})
			cmd := m.handleV2Request(msg)
			if job, _ := msg.Jobs.Get(msg.JobID); job.State != ipc.JobRunning {
				t.Fatalf("job completed before remote action: %s", job.State)
			}
			control := executeSonosCmd(t, cmd).(sonosControlMsg)
			m.handleSonosControl(control)
			if job, _ := msg.Jobs.Get(msg.JobID); job.State != ipc.JobSucceeded {
				t.Fatalf("job state = %s, want succeeded", job.State)
			}
			if len(bridge.actions) != 1 || bridge.actions[0] != tc.action {
				t.Fatalf("remote actions = %v, want [%s]", bridge.actions, tc.action)
			}
			if len(engine.playCalls) != 0 || m.playlist.Len() != 1 {
				t.Fatal("remote transport mutated local playback or queue")
			}
		})
	}
}

func TestSonosToggleIntentSurvivesQueuedPauseForKeyboardMediaAndIPC(t *testing.T) {
	t.Run("keyboard", func(t *testing.T) {
		m, bridge, _ := sonosTestModel()
		m.sonosOutput.selected = "10.0.0.2"
		cmd := m.togglePlayPause()
		executeSonosCmd(t, cmd)
		if len(bridge.actions) != 1 || bridge.actions[0] != "toggle" {
			t.Fatalf("keyboard dispatched %v, want [toggle]", bridge.actions)
		}
	})
	t.Run("media", func(t *testing.T) {
		m, bridge, _ := sonosTestModel()
		m.sonosOutput.selected = "10.0.0.2"
		_, cmd := m.Update(playback.PlayPauseMsg{})
		executeSonosCmd(t, cmd)
		if len(bridge.actions) != 1 || bridge.actions[0] != "toggle" {
			t.Fatalf("media dispatched %v, want [toggle]", bridge.actions)
		}
	})
	t.Run("ipc queued behind pause", func(t *testing.T) {
		m, bridge, _ := sonosTestModel()
		m.sonosOutput.selected = "10.0.0.2"
		state := bridge.current
		state.Transport = "PLAYING"
		m.sonosOutput.state = &state
		bridge.started, bridge.release = make(chan struct{}, 2), make(chan struct{}, 2)
		pause := v2Request(t, "pause", ipc.Request{})
		pauseCmd := m.handleV2Request(pause)
		pauseDone := make(chan tea.Msg, 1)
		go func() { pauseDone <- pauseCmd() }()
		<-bridge.started
		toggles := []V2RequestMsg{v2Request(t, "toggle", ipc.Request{}), v2Request(t, "toggle", ipc.Request{})}
		for _, toggle := range toggles {
			if cmd := m.handleV2Request(toggle); cmd != nil {
				t.Fatal("toggle dispatched before in-flight pause completed")
			}
		}
		bridge.release <- struct{}{}
		next := m.handleSonosControl((<-pauseDone).(sonosControlMsg))
		for i := range toggles {
			done := make(chan tea.Msg, 1)
			go func(cmd tea.Cmd) { done <- cmd() }(next)
			<-bridge.started
			if len(bridge.actions) != i+2 || bridge.actions[i+1] != "toggle" {
				t.Fatalf("queued controls = %v, want pause then toggle", bridge.actions)
			}
			bridge.release <- struct{}{}
			next = m.handleSonosControl((<-done).(sonosControlMsg))
		}
		for _, toggle := range toggles {
			job, _ := toggle.Jobs.Get(toggle.JobID)
			if job.State != ipc.JobSucceeded {
				t.Fatalf("toggle job state = %s, want succeeded", job.State)
			}
		}
		if len(bridge.actions) != 3 {
			t.Fatalf("unexpected control sequence: %v", bridge.actions)
		}
	})
}

func TestSonosIPCUnsupportedControlVariantsFailWithoutLocalMutation(t *testing.T) {
	for _, tc := range []struct {
		operation string
		params    ipc.Request
	}{
		{operation: "stop"},
		{operation: "seek", params: ipc.Request{Value: 10}},
		{operation: "seek.absolute", params: ipc.Request{Value: 10}},
		{operation: "speed", params: ipc.Request{Value: 1.5}},
		{operation: "speed.adjust", params: ipc.Request{Value: 0.25}},
		{operation: "eq", params: ipc.Request{Band: 2, Value: 3}},
		{operation: "shuffle", params: ipc.Request{Name: "on"}},
		{operation: "repeat", params: ipc.Request{Name: "one"}},
		{operation: "mono", params: ipc.Request{Name: "on"}},
		{operation: "vis", params: ipc.Request{Name: "next"}},
	} {
		t.Run(tc.operation, func(t *testing.T) {
			m, _, engine := sonosTestModel()
			m.sonosOutput.selected = "10.0.0.2"
			m.sonosOutput.state = &spotify.SonosState{Transport: "PLAYING"}
			oldSpeed, oldVolume := engine.Speed(), engine.Volume()
			oldRepeat, oldShuffle := m.playlist.Repeat(), m.playlist.Shuffled()
			oldBands, oldPreset := engine.EQBands(), m.eqPresetIdx
			msg := v2Request(t, tc.operation, tc.params)
			if cmd := m.handleV2Request(msg); cmd != nil {
				t.Fatalf("unsupported operation returned command %s", tc.operation)
			}
			job, _ := msg.Jobs.Get(msg.JobID)
			if job.State != ipc.JobFailed || job.Error == nil || job.Error.Code != ipc.V2ErrorCodeUnavailable {
				t.Fatalf("unsupported job = %+v", job)
			}
			if engine.Speed() != oldSpeed || engine.Volume() != oldVolume || m.playlist.Repeat() != oldRepeat || m.playlist.Shuffled() != oldShuffle || engine.EQBands() != oldBands || m.eqPresetIdx != oldPreset {
				t.Fatalf("unsupported operation mutated local state: %s", tc.operation)
			}
		})
	}
}

func TestSonosIPCVolumeUsesDBResponseAndSharedPercentMapping(t *testing.T) {
	for _, tc := range []struct {
		name      string
		operation string
		value     float64
		wantPct   int
		wantDB    float64
	}{
		{name: "absolute", operation: "volume", value: 0, wantPct: 94, wantDB: sonosVolumeDB(94)},
		{name: "relative", operation: "volume.adjust", value: 1, wantPct: 36, wantDB: sonosVolumeDB(36)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, bridge, _ := sonosTestModel()
			m.sonosOutput.selected = "10.0.0.2"
			m.sonosOutput.state = &bridge.current
			msg := v2Request(t, tc.operation, ipc.Request{Value: tc.value})
			cmd := m.handleV2Request(msg)
			control := executeSonosCmd(t, cmd).(sonosControlMsg)
			m.handleSonosControl(control)
			if len(bridge.volumes) != 1 || bridge.volumes[0] != tc.wantPct {
				t.Fatalf("dispatched group volume = %v, want [%d]", bridge.volumes, tc.wantPct)
			}
			job, _ := msg.Jobs.Get(msg.JobID)
			var response ipc.Response
			if err := json.Unmarshal(job.Result, &response); err != nil {
				t.Fatal(err)
			}
			if job.State != ipc.JobSucceeded || math.Abs(response.Volume-tc.wantDB) > 0.001 {
				t.Fatalf("IPC volume response = %+v (job %s), want %.2f dB", response, job.State, tc.wantDB)
			}
		})
	}
}

func TestSonosVolumePercentRoundTripsAndRelativeStepsAreSymmetric(t *testing.T) {
	for percent := 0; percent <= 100; percent++ {
		if got := sonosVolumePercent(sonosVolumeDB(percent)); got != percent {
			t.Fatalf("percent %d round-tripped to %d", percent, got)
		}
	}
	for _, tc := range []struct {
		delta int
		want  int
	}{{delta: 1, want: 36}, {delta: -1, want: 34}} {
		m, bridge, _ := sonosTestModel()
		m.sonosOutput.selected = "10.0.0.2"
		m.sonosOutput.state = &bridge.current
		cmd := m.adjustVolume(float64(tc.delta))
		msg := executeSonosCmd(t, cmd).(sonosControlMsg)
		m.handleSonosControl(msg)
		if len(bridge.volumes) != 1 || bridge.volumes[0] != tc.want {
			t.Fatalf("delta %+d dB dispatched %v, want [%d]", tc.delta, bridge.volumes, tc.want)
		}
	}
}

func TestSonosVolumeUnavailableAndBridgeErrorsAreReported(t *testing.T) {
	m, bridge, _ := sonosTestModel()
	m.sonosOutput.selected = "10.0.0.2"
	m.sonosOutput.state = &spotify.SonosState{}
	if cmd := m.adjustVolume(1); cmd != nil || m.status.text == "" {
		t.Fatalf("missing volume was not reported: cmd=%v status=%q", cmd, m.status.text)
	}
	volume := 30
	m.sonosOutput.state.Volume = &volume
	bridge.err = errors.New("unreachable")
	cmd := m.adjustVolume(1)
	msg := executeSonosCmd(t, cmd).(sonosControlMsg)
	m.handleSonosControl(msg)
	if m.status.text == "" {
		t.Fatal("remote control error was not reported")
	}
}

func TestSonosLocalReturnAndStaleLocalCompletionDoNotResumeQueue(t *testing.T) {
	m, _, engine := sonosTestModel()
	m.buffering = true
	m.requests.stream = 8
	oldStream := m.requests.stream
	selectCmd := m.selectOutput(player.AudioDevice{Name: "sonos:10.0.0.2", Description: "Sonos · Office"})
	executeSonosCmd(t, selectCmd)
	if m.playlist.Len() != 1 || engine.playing {
		t.Fatal("Sonos attach changed or resumed the local queue")
	}
	m.handleStreamPlayed(streamPlayedMsg{path: "/tmp/local.flac", gen: oldStream})
	if engine.playing || len(engine.playCalls) != 0 {
		t.Fatal("stale local stream completion started playback")
	}
	localCmd := m.selectOutput(player.AudioDevice{Name: "", Description: "Local (system default)"})
	if localCmd == nil || m.sonosOutput.selected != "" || engine.playing || m.playlist.Len() != 1 {
		t.Fatal("return to Local did not preserve the stopped queue")
	}
}

func TestSonosOutputChangesInvalidatePendingAutoplayButKeepExplicitPlay(t *testing.T) {
	t.Run("resolved URL", func(t *testing.T) {
		m, _, engine := sonosTestModel()
		gen := m.requests.output
		m.selectOutput(player.AudioDevice{Name: "sonos:10.0.0.2", Description: "Sonos · Office"})
		m.selectOutput(player.AudioDevice{Name: "", Description: "Local (system default)"})
		tracks := []playlist.Track{{Title: "Loaded URL", Path: "/tmp/url.flac"}}
		cmd := m.handleFeedsLoaded(feedsLoadedMsg{tracks: tracks, autoPlay: true, outputGen: gen})
		if cmd != nil || engine.playing || len(engine.playCalls) != 0 || m.playlist.Len() != 2 {
			t.Fatalf("stale URL completion auto-played or lost browsing results: playing=%t calls=%v queue=%d", engine.playing, engine.playCalls, m.playlist.Len())
		}
		_, _ = m.Update(autoPlayMsg{outputGen: gen})
		if engine.playing {
			t.Fatal("stale initial auto-play started local audio")
		}
		if cmd := m.playCurrentTrack(); cmd == nil {
			t.Fatal("fresh explicit local play did not start")
		} else {
			executeSonosCmd(t, cmd)
		}
		if !engine.playing || len(engine.playCalls) == 0 {
			t.Fatal("fresh explicit local play did not reach the player")
		}
	})
	t.Run("IPC URL play", func(t *testing.T) {
		m, _, engine := sonosTestModel()
		gen := m.requests.output
		reply := make(chan ipc.Response, 1)
		m.selectOutput(player.AudioDevice{Name: "sonos:10.0.0.2", Description: "Sonos · Office"})
		m.selectOutput(player.AudioDevice{Name: "", Description: "Local (system default)"})
		cmd := m.handleIPCURLResult(ipcURLLoadResult{request: ipcURLRequest{Play: true, Reply: reply, OutputGen: gen}, tracks: []playlist.Track{{Title: "URL", Path: "/tmp/url.flac"}}})
		if cmd != nil || engine.playing || len(engine.playCalls) != 0 || len(m.playlist.Tracks()) != 2 {
			t.Fatalf("stale IPC URL completion started audio or lost tracks: cmd=%v playing=%t queue=%d", cmd, engine.playing, m.playlist.Len())
		}
	})
	t.Run("IPC provider load", func(t *testing.T) {
		m, _, engine := sonosTestModel()
		gen := m.requests.output
		reply := make(chan ipc.Response, 1)
		m.selectOutput(player.AudioDevice{Name: "sonos:10.0.0.2", Description: "Sonos · Office"})
		m.selectOutput(player.AudioDevice{Name: "", Description: "Local (system default)"})
		cmd := m.handleIPCProviderLoad(ipcProviderLoadResult{request: ipcLibraryRequest{Reply: reply, OutputGen: gen}, tracks: []playlist.Track{{Title: "Provider", Path: "/tmp/provider.flac"}}})
		if cmd != nil || engine.playing || len(engine.playCalls) != 0 || m.playlist.Len() != 1 {
			t.Fatalf("stale provider completion started audio or lost browsed tracks: cmd=%v playing=%t queue=%d", cmd, engine.playing, m.playlist.Len())
		}
	})
	t.Run("file browser load", func(t *testing.T) {
		m, _, engine := sonosTestModel()
		gen := m.requests.output
		m.selectOutput(player.AudioDevice{Name: "sonos:10.0.0.2", Description: "Sonos · Office"})
		m.selectOutput(player.AudioDevice{Name: "", Description: "Local (system default)"})
		cmd := m.handleFBTracksResolved(fbTracksResolvedMsg{replace: true, queue: m.requests.queue, outputGen: gen, tracks: []playlist.Track{{Title: "File", Path: "/tmp/file.flac"}}})
		if cmd != nil || engine.playing || len(engine.playCalls) != 0 || m.playlist.Len() != 1 {
			t.Fatalf("stale file browser completion started audio or lost tracks: cmd=%v playing=%t queue=%d", cmd, engine.playing, m.playlist.Len())
		}
	})
	t.Run("subscription play", func(t *testing.T) {
		m, _, engine := sonosTestModel()
		gen := m.requests.output
		m.selectOutput(player.AudioDevice{Name: "sonos:10.0.0.2", Description: "Sonos · Office"})
		m.selectOutput(player.AudioDevice{Name: "", Description: "Local (system default)"})
		cmd := m.handleSubsEpisodes(subsEpisodesMsg{mode: subsLoadPlay, outputGen: gen, name: "Show", tracks: []playlist.Track{{Title: "Episode", Path: "/tmp/episode.flac"}}})
		if cmd != nil || engine.playing || len(engine.playCalls) != 0 || m.playlist.Len() != 2 {
			t.Fatalf("stale subscription completion started audio or lost tracks: cmd=%v playing=%t queue=%d", cmd, engine.playing, m.playlist.Len())
		}
	})
}

func TestSonosVisualizerPickerRejectsRemoteOwnership(t *testing.T) {
	m, _, _ := sonosTestModel()
	m.vis = ui.NewVisualizer(44100)
	m.sonosOutput.selected = "10.0.0.2"
	oldMode := m.vis.Mode
	m.openVisPicker()
	if m.visPicker.visible || m.vis.Mode != oldMode {
		t.Fatal("visualizer picker opened or changed mode while Sonos owned output")
	}
	m.visPicker.visible = true
	m.visPicker.modes = m.vis.AllModeNames()
	m.visPicker.cursor = int(ui.VisNone)
	m.visPicker.savedMode = int(oldMode)
	saver := &recordingSaver{}
	m.configSaver = saver
	m.handleVisPickerKey(tea.KeyPressMsg{Text: "enter"})
	if m.vis.Mode != oldMode || m.visPicker.visible || m.status.text == "" || len(saver.saved) != 0 {
		t.Fatalf("remote visualizer selection changed local mode or saved: mode=%v visible=%t status=%q", m.vis.Mode, m.visPicker.visible, m.status.text)
	}
}

func TestSonosStatePollFailureIsThrottledFromAttemptTime(t *testing.T) {
	m, bridge, _ := sonosTestModel()
	m.sonosOutput.selected = "10.0.0.2"
	bridge.err = errors.New("unreachable")
	read := m.refreshSonosState()
	m.handleSonosState(executeSonosCmd(t, read).(sonosStateMsg))
	attempted := m.sonosOutput.attempted
	if !m.sonosOutput.refreshed.IsZero() {
		t.Fatal("failed read changed last-success timestamp")
	}
	m.handleTick(tickMsg(attempted.Add(time.Second)))
	if m.sonosOutput.busy || len(bridge.reads) != 1 {
		t.Fatalf("failed read retried before throttle interval: busy=%t reads=%v", m.sonosOutput.busy, bridge.reads)
	}
	m.handleTick(tickMsg(attempted.Add(4 * time.Second)))
	if m.sonosOutput.busy || len(bridge.reads) != 1 {
		t.Fatalf("failed read retried before five-second interval: busy=%t reads=%v", m.sonosOutput.busy, bridge.reads)
	}
	m.handleTick(tickMsg(attempted.Add(5 * time.Second)))
	if !m.sonosOutput.busy || len(bridge.reads) != 1 {
		t.Fatalf("poll did not retry after interval: busy=%t reads=%v", m.sonosOutput.busy, bridge.reads)
	}
}

func TestSonosRemoteSnapshotAndRenderingUseRemoteStateOnly(t *testing.T) {
	m, _, _ := sonosTestModel()
	m.sonosOutput.selected = "10.0.0.2"
	m.sonosOutput.name = "Office"
	volume := 42
	m.sonosOutput.state = &spotify.SonosState{
		Transport: "PLAYING", Position: "00:01:23", Duration: "00:04:56", Volume: &volume,
		Track: spotify.SonosTrack{Title: "Remote track", Artist: "Remote artist"},
	}
	snapshot := m.runtimeSnapshot()
	if snapshot.Track == nil || snapshot.Track.Title != "Remote track" || snapshot.Position != 83 || snapshot.Playlist != "" || snapshot.Total != 0 {
		t.Fatalf("remote snapshot exposed wrong state: %+v", snapshot)
	}
	if m.renderTrackInfo() == "" || strings.Contains(m.renderTrackInfo(), "Local track") || m.visualizerVisible() {
		t.Fatalf("remote rendering exposed local playback: %q", m.renderTrackInfo())
	}
}

func TestSonosUnsupportedLocalControlsDoNotMutateLocalState(t *testing.T) {
	m, _, engine := sonosTestModel()
	m.sonosOutput.selected = "10.0.0.2"
	engine.playing = false
	oldRepeat, oldShuffle, oldSpeed := m.playlist.Repeat(), m.playlist.Shuffled(), engine.Speed()
	m.setRepeat(playlist.RepeatOne)
	m.setShuffle(true)
	m.setSpeed(1.5)
	m.seekRelative(time.Second, 0)
	m.stopByUser()
	if m.playlist.Repeat() != oldRepeat || m.playlist.Shuffled() != oldShuffle || engine.Speed() != oldSpeed || engine.playing {
		t.Fatalf("unsupported local control mutated local state while Sonos owned output: repeat=%v shuffle=%t speed=%v playing=%t", m.playlist.Repeat(), m.playlist.Shuffled(), engine.Speed(), engine.playing)
	}
	if m.status.text == "" {
		t.Fatal("unsupported controls were not reported")
	}
}
