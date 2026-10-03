package model

import (
	"context"
	"encoding/json"
	"maps"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/internal/playback"
	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/player"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
	"github.com/bjarneo/cliamp/theme"
	"github.com/bjarneo/cliamp/ui"
)

// v2Request returns the V2RequestMsg that the TUI dispatcher in main.go sends
// for op, with a new job in its own JobStore.
func v2Request(t *testing.T, op string, params ipc.Request) V2RequestMsg {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	jobs := ipc.NewJobStore()
	t.Cleanup(jobs.CancelAll)
	job, err := jobs.Create(op)
	if err != nil {
		t.Fatal(err)
	}
	return V2RequestMsg{Request: ipc.V2Request{Operation: op, Params: raw}, Jobs: jobs, JobID: job.ID}
}

// runV2 sends op through the live V2 path of m and returns the job result. A
// failed job gives OK false with the error code and detail in Error. A
// deferred operation finishes through the messages of the commands that it
// returns, so runV2 runs these commands and sends their messages to m.
func runV2(t *testing.T, m *Model, op string, params ipc.Request) ipc.Response {
	t.Helper()
	msg := v2Request(t, op, params)
	updated, cmd := m.Update(msg)
	*m = updated.(Model)
	running := func() bool {
		job, _ := msg.Jobs.Get(msg.JobID)
		return job.State == ipc.JobRunning
	}
	results := make(chan tea.Msg, 16)
	pending := 0
	run := func(c tea.Cmd) {
		pending++
		go func() { results <- c() }()
	}
	if cmd != nil && running() {
		run(cmd)
	}
	for pending > 0 && running() {
		select {
		case result := <-results:
			pending--
			if batch, ok := result.(tea.BatchMsg); ok {
				for _, c := range batch {
					run(c)
				}
			} else if result != nil {
				updated, _ = m.Update(result)
				*m = updated.(Model)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: the job did not finish", op)
		}
	}
	job, _ := msg.Jobs.Get(msg.JobID)
	switch job.State {
	case ipc.JobSucceeded:
		var response ipc.Response
		if err := json.Unmarshal(job.Result, &response); err != nil {
			t.Fatalf("%s: result %s: %v", op, job.Result, err)
		}
		return response
	case ipc.JobFailed:
		response := ipc.Response{Error: job.Error.Code}
		if job.Error.Detail != "" {
			response.Error += ": " + job.Error.Detail
		}
		return response
	}
	t.Fatalf("%s: job state = %s, want a finished job", op, job.State)
	return ipc.Response{}
}

func TestV2StateRequestReturnsRetainedGUIState(t *testing.T) {
	engine := &playbackFakeEngine{playing: true}
	pl := playlist.New()
	pl.Add(playlist.Track{
		Path:         "/music/song.flac",
		Title:        "Song",
		ProviderMeta: map[string]string{"provider.id": "track-1"},
	})
	broker := ipc.NewBroker()
	m := Model{
		player:   engine,
		playlist: pl,
		vis:      ui.NewVisualizer(float64(engine.SampleRate())),
	}
	m.SetIPCBroker(broker)

	reply := make(chan V2RequestResult, 1)
	updated, _ := m.Update(V2RequestMsg{Request: ipc.V2Request{Method: "state.get"}, Reply: reply})
	m = updated.(Model)
	result := <-reply
	if result.Error != nil || result.Result.Snapshot == nil {
		t.Fatalf("state result = %#v", result)
	}
	snapshot := result.Result.Snapshot
	if snapshot.Track == nil || snapshot.Track.ProviderMeta["provider.id"] != "track-1" || snapshot.LogicalTrack == nil {
		t.Fatalf("snapshot tracks = %#v / %#v", snapshot.Track, snapshot.LogicalTrack)
	}

	sub, err := broker.Subscribe([]string{ipcRuntimeEventState})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	event := <-sub.Events()
	if !event.Retained {
		t.Fatal("runtime state was not retained")
	}
	var retained ipc.RuntimeSnapshot
	if err := json.Unmarshal(event.Data, &retained); err != nil {
		t.Fatal(err)
	}
	if retained.Revision == 0 || retained.PlaylistRevision != pl.Revision() {
		t.Fatalf("retained state = %#v", retained)
	}
}

func TestV2QueueAndPlayNextUseSeparateIndexes(t *testing.T) {
	engine := &playbackFakeEngine{}
	pl := playlist.New()
	pl.Add(
		playlist.Track{Path: "/music/one.flac", Title: "One"},
		playlist.Track{Path: "/music/two.flac", Title: "Two"},
	)
	m := Model{player: engine, playlist: pl, vis: ui.NewVisualizer(float64(engine.SampleRate()))}
	jobs := ipc.NewJobStore()

	job, err := jobs.Create("queue.enqueue")
	if err != nil {
		t.Fatal(err)
	}
	updated, _ := m.Update(V2RequestMsg{
		Request: ipc.V2Request{Operation: "queue.enqueue", Params: json.RawMessage(`{"index":1}`)},
		Jobs:    jobs,
		JobID:   job.ID,
	})
	m = updated.(Model)
	completed, ok := jobs.Get(job.ID)
	if !ok || completed.State != ipc.JobSucceeded || pl.QueueLen() != 1 || pl.Len() != 2 {
		t.Fatalf("queue job=%#v queue=%d playlist=%d", completed, pl.QueueLen(), pl.Len())
	}

	job, err = jobs.Create("playnext.remove")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = m.Update(V2RequestMsg{
		Request: ipc.V2Request{Operation: "playnext.remove", Params: json.RawMessage(`{"index":0}`)},
		Jobs:    jobs,
		JobID:   job.ID,
	})
	completed, ok = jobs.Get(job.ID)
	if !ok || completed.State != ipc.JobSucceeded || pl.QueueLen() != 0 || pl.Len() != 2 {
		t.Fatalf("play-next job=%#v queue=%d playlist=%d", completed, pl.QueueLen(), pl.Len())
	}
}

func TestV2RevisionConflictPreventsMutation(t *testing.T) {
	engine := &playbackFakeEngine{}
	pl := playlist.New()
	pl.Add(playlist.Track{Path: "/music/one.flac", Title: "One"})
	m := Model{player: engine, playlist: pl, vis: ui.NewVisualizer(float64(engine.SampleRate()))}
	jobs := ipc.NewJobStore()
	job, err := jobs.Create("queue.clear")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = m.Update(V2RequestMsg{
		Request: ipc.V2Request{Operation: "queue.clear", Params: json.RawMessage(`{"if_revision":999}`)},
		Jobs:    jobs,
		JobID:   job.ID,
	})
	completed, ok := jobs.Get(job.ID)
	if !ok || completed.State != ipc.JobFailed || completed.Error == nil || completed.Error.Code != ipc.V2ErrorCodeConflict {
		t.Fatalf("conflict job=%#v", completed)
	}
	if pl.Len() != 1 {
		t.Fatalf("playlist mutated after conflict: %d tracks", pl.Len())
	}
}

func TestV2QueueRemoveStopsActivePlayback(t *testing.T) {
	engine := &playbackFakeEngine{playing: true}
	pl := playlist.New()
	pl.Add(
		playlist.Track{Path: "/music/one.flac", Title: "One"},
		playlist.Track{Path: "/music/two.flac", Title: "Two"},
	)
	m := Model{player: engine, playlist: pl, vis: ui.NewVisualizer(float64(engine.SampleRate()))}
	jobs := ipc.NewJobStore()
	job, err := jobs.Create("queue.remove")
	if err != nil {
		t.Fatal(err)
	}

	_, _ = m.Update(V2RequestMsg{
		Request: ipc.V2Request{Operation: "queue.remove", Params: json.RawMessage(`{"index":0}`)},
		Jobs:    jobs,
		JobID:   job.ID,
	})
	completed, ok := jobs.Get(job.ID)
	if !ok || completed.State != ipc.JobSucceeded {
		t.Fatalf("queue job = %#v", completed)
	}
	if engine.stopCalls != 1 || pl.Len() != 1 || pl.Tracks()[0].Title != "Two" {
		t.Fatalf("stops=%d tracks=%#v", engine.stopCalls, pl.Tracks())
	}
}

// The settings operations report the new state and save it the way the
// matching TUI keys do.
func TestV2SettingsOperations(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Cleanup(func() { applyThemeAll(theme.Default()) })
	on, off := true, false
	for _, tc := range []struct {
		name      string
		op        string
		params    ipc.Request
		setup     func(*Model)
		want      ipc.Response
		wantSaved map[string]string
	}{
		{name: "shuffle on", op: "shuffle", params: ipc.Request{Name: "on"}, want: ipc.Response{OK: true, Shuffle: &on}, wantSaved: map[string]string{"shuffle": "true"}},
		{name: "shuffle toggle", op: "shuffle", want: ipc.Response{OK: true, Shuffle: &on}, wantSaved: map[string]string{"shuffle": "true"}},
		{name: "shuffle off", op: "shuffle", params: ipc.Request{Name: "off"}, setup: func(m *Model) { m.playlist.ToggleShuffle() }, want: ipc.Response{OK: true, Shuffle: &off}, wantSaved: map[string]string{"shuffle": "false"}},
		{name: "repeat one", op: "repeat", params: ipc.Request{Name: "one"}, want: ipc.Response{OK: true, Repeat: "One"}, wantSaved: map[string]string{"repeat": `"One"`}},
		{name: "repeat cycle", op: "repeat", want: ipc.Response{OK: true, Repeat: "All"}, wantSaved: map[string]string{"repeat": `"All"`}},
		{name: "mono on", op: "mono", params: ipc.Request{Name: "on"}, want: ipc.Response{OK: true, Mono: &on}},
		{name: "mono off stays off", op: "mono", params: ipc.Request{Name: "off"}, want: ipc.Response{OK: true, Mono: &off}},
		{name: "shuffle toggle by name", op: "shuffle", params: ipc.Request{Name: "Toggle"}, want: ipc.Response{OK: true, Shuffle: &on}, wantSaved: map[string]string{"shuffle": "true"}},
		{name: "repeat cycle by name", op: "repeat", params: ipc.Request{Name: "cycle"}, want: ipc.Response{OK: true, Repeat: "All"}, wantSaved: map[string]string{"repeat": `"All"`}},
		{name: "mono toggle", op: "mono", params: ipc.Request{Name: "toggle"}, want: ipc.Response{OK: true, Mono: &on}},
		{name: "mono toggle with no name", op: "mono", want: ipc.Response{OK: true, Mono: &on}},
		{name: "shuffle unknown", op: "shuffle", params: ipc.Request{Name: "bogus"}, want: ipc.Response{Error: ipc.V2ErrorCodeInvalidParams}},
		{name: "repeat unknown", op: "repeat", params: ipc.Request{Name: "bogus"}, want: ipc.Response{Error: ipc.V2ErrorCodeInvalidParams}},
		{name: "repeat toggle is unknown", op: "repeat", params: ipc.Request{Name: "toggle"}, want: ipc.Response{Error: ipc.V2ErrorCodeInvalidParams}},
		{name: "mono unknown", op: "mono", params: ipc.Request{Name: "bogus"}, want: ipc.Response{Error: ipc.V2ErrorCodeInvalidParams}},
		{name: "volume", op: "volume", params: ipc.Request{Value: -6}, want: ipc.Response{OK: true, Volume: -6}},
		{name: "volume adjust", op: "volume.adjust", params: ipc.Request{Value: 2}, setup: func(m *Model) { m.player.SetVolume(-6) }, want: ipc.Response{OK: true, Volume: -4}},
		{name: "speed", op: "speed", params: ipc.Request{Value: 1.5}, want: ipc.Response{OK: true, Speed: 1.5}, wantSaved: map[string]string{"speed": "1.50"}},
		{name: "speed zero", op: "speed", want: ipc.Response{Error: ipc.V2ErrorCodeInvalidParams}},
		{name: "speed adjust", op: "speed.adjust", params: ipc.Request{Value: 0.25}, want: ipc.Response{OK: true, Speed: 1.25}, wantSaved: map[string]string{"speed": "1.25"}},
		{name: "eq band", op: "eq", params: ipc.Request{Band: 3, Value: 4}, want: ipc.Response{OK: true, EQPreset: "Custom"}, wantSaved: map[string]string{"eq_preset": `"Custom"`, "eq": "[0, 0, 0, 4, 0, 0, 0, 0, 0, 0]"}},
		{name: "eq band out of range", op: "eq", params: ipc.Request{Band: eqBandCount, Value: 4}, want: ipc.Response{Error: ipc.V2ErrorCodeInvalidParams}},
		{name: "eq preset", op: "eq", params: ipc.Request{Name: "rock"}, want: ipc.Response{OK: true, EQPreset: "Rock"}, wantSaved: map[string]string{"eq_preset": `"Rock"`, "eq": "[0, 0, 0, 0, 0, 0, 0, 0, 0, 0]"}},
		{name: "eq custom curve", op: "eq", params: ipc.Request{Name: "custom"}, want: ipc.Response{OK: true, EQPreset: "Custom"}, wantSaved: map[string]string{"eq_preset": `"Custom"`, "eq": "[0, 0, 0, 0, 0, 0, 0, 0, 0, 0]"}},
		{name: "eq unknown preset", op: "eq", params: ipc.Request{Name: "rokc"}, want: ipc.Response{Error: ipc.V2ErrorCodeNotFound}},
		{name: "theme", op: "theme", params: ipc.Request{Name: "dracula"}, want: ipc.Response{OK: true}, wantSaved: map[string]string{"theme": `"dracula"`}},
		{name: "theme default", op: "theme", params: ipc.Request{Name: "default"}, want: ipc.Response{OK: true}, wantSaved: map[string]string{"theme": `""`}},
		{name: "theme empty", op: "theme", params: ipc.Request{Name: ""}, want: ipc.Response{OK: true}, wantSaved: map[string]string{"theme": `""`}},
		{name: "theme listed default name", op: "theme", params: ipc.Request{Name: theme.DefaultName}, want: ipc.Response{OK: true}, wantSaved: map[string]string{"theme": `""`}},
		{name: "theme default name any case", op: "theme", params: ipc.Request{Name: strings.ToUpper(theme.DefaultName)}, want: ipc.Response{OK: true}, wantSaved: map[string]string{"theme": `""`}},
		{name: "theme unknown", op: "theme", params: ipc.Request{Name: "no-such-theme"}, want: ipc.Response{Error: ipc.V2ErrorCodeNotFound}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pl := playlist.New()
			pl.Add(
				playlist.Track{Path: "/music/one.flac", Title: "One"},
				playlist.Track{Path: "/music/two.flac", Title: "Two"},
				playlist.Track{Path: "/music/three.flac", Title: "Three"},
			)
			saver := &recordingSaver{}
			m := Model{player: &playbackFakeEngine{}, playlist: pl, configSaver: saver, vis: ui.NewVisualizer(44100)}
			if tc.setup != nil {
				tc.setup(&m)
			}

			if got := runV2(t, &m, tc.op, tc.params); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("response = %+v, want %+v", got, tc.want)
			}
			m.flushPendingEQSave() // The EQ save waits for a debounce.
			if !maps.Equal(saver.saved, tc.wantSaved) {
				t.Fatalf("saved = %v, want %v", saver.saved, tc.wantSaved)
			}
		})
	}
}

// load is provider.load on the local provider. It replaces the playlist and
// remembers the name of the loaded playlist.
func TestV2LoadReadsLocalPlaylist(t *testing.T) {
	tracks := []playlist.Track{{Path: "/music/a.mp3", Title: "A"}, {Path: "/music/b.mp3", Title: "B"}}
	local := fixedTracksProvider{commandsTestProvider{name: "Local"}, tracks}
	pl := playlist.New()
	pl.Add(playlist.Track{Path: "/music/old.mp3", Title: "Old"})
	m := Model{
		player:        &playbackFakeEngine{},
		playlist:      pl,
		vis:           ui.NewVisualizer(44100),
		localProvider: local,
		providers:     []provider.Entry{{Key: "local", Name: "Local", Provider: local}},
	}

	response := runV2(t, &m, "load", ipc.Request{Playlist: "Mix"})
	if !response.OK || response.Playlist != "Mix" || response.Total != len(tracks) {
		t.Fatalf("response = %+v, want the 2 tracks of Mix", response)
	}
	if !reflect.DeepEqual(m.playlist.Tracks(), tracks) || m.loadedPlaylist != "Mix" {
		t.Fatalf("playlist = %+v, loaded %q; want the tracks of Mix", m.playlist.Tracks(), m.loadedPlaylist)
	}
}

// A device job records the output device for the snapshot. Only a switch
// saves the device in the config. The list text feeds cliamp device list.
func TestV2DeviceResponseRecordsDevice(t *testing.T) {
	devices := []player.AudioDevice{{Name: "speakers"}, {Name: "headphones", Active: true}}
	for _, tc := range []struct {
		name       string
		response   ipc.Response
		wantDevice string
		wantSaved  map[string]string
	}{
		{name: "list", response: deviceListResponse(devices), wantDevice: "headphones"},
		{name: "switch", response: ipc.Response{OK: true, Device: "usb"}, wantDevice: "usb", wantSaved: map[string]string{"audio_device": `"usb"`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			saver := &recordingSaver{}
			m := Model{player: &playbackFakeEngine{}, playlist: playlist.New(), configSaver: saver}
			m.applyV2DeviceResponse(tc.response)
			if got := m.runtimeSnapshot().Device; got != tc.wantDevice {
				t.Fatalf("snapshot device = %q, want %q", got, tc.wantDevice)
			}
			if !maps.Equal(saver.saved, tc.wantSaved) {
				t.Fatalf("saved = %v, want %v", saver.saved, tc.wantSaved)
			}
		})
	}
	list := deviceListResponse(devices)
	if want := "  speakers\n* headphones"; list.Device != want || len(list.Devices) != 2 || !list.Devices[1].Active {
		t.Fatalf("device list = %q %+v, want %q", list.Device, list.Devices, want)
	}
}

// The snapshot names the loaded saved playlist, and a change of the
// playlist or the device publishes a new runtime state.
func TestV2SnapshotReportsLoadedPlaylist(t *testing.T) {
	pl := playlist.New()
	pl.Add(playlist.Track{Path: "/music/one.flac", Title: "One"})
	m := Model{player: &playbackFakeEngine{}, playlist: pl, configSaver: &recordingSaver{}}
	m.SetIPCBroker(ipc.NewBroker())
	m.publishIPCRuntimeState()
	revision := m.ipcRuntime.revision

	m.loadedPlaylist = "Lofi"
	m.publishIPCRuntimeState()
	if got := m.runtimeSnapshot().Playlist; got != "Lofi" || m.ipcRuntime.revision != revision+1 {
		t.Fatalf("playlist = %q at revision %d, want Lofi at %d", got, m.ipcRuntime.revision, revision+1)
	}
	m.applyV2DeviceResponse(ipc.Response{OK: true, Device: "usb"})
	m.publishIPCRuntimeState()
	if m.ipcRuntime.revision != revision+2 {
		t.Fatalf("revision = %d after a device change, want %d", m.ipcRuntime.revision, revision+2)
	}
}

// writableTestProvider records the saved-playlist writes that IPC forwards.
type writableTestProvider struct {
	commandsTestProvider
	created, renamed, deleted string
	added                     []string
	removed                   int
}

func (p *writableTestProvider) Tracks(string) ([]playlist.Track, error) {
	return []playlist.Track{{Path: "/music/one.flac", Title: "One"}}, nil
}
func (p *writableTestProvider) CreatePlaylist(_ context.Context, name string) (string, error) {
	p.created = name
	return name, nil
}
func (p *writableTestProvider) RenamePlaylist(oldName, newName string) error {
	p.renamed = oldName + ":" + newName
	return nil
}
func (p *writableTestProvider) DeletePlaylist(name string) error {
	p.deleted = name
	return nil
}
func (p *writableTestProvider) RemoveTrack(_ string, index int) error {
	p.removed = index
	return nil
}
func (p *writableTestProvider) AddTrackToPlaylist(_ context.Context, name string, track playlist.Track) error {
	p.added = append(p.added, name+":"+track.Path)
	return nil
}

// provider.tracks names the playlist that it lists, and playlist.add_many
// counts the tracks that a provider without a batch write added one by one.
func TestV2ProviderResponsesNameThePlaylistAndCount(t *testing.T) {
	prov := &writableTestProvider{commandsTestProvider: commandsTestProvider{name: "Writable"}}
	m := Model{
		player:    &playbackFakeEngine{},
		playlist:  playlist.New(),
		providers: []provider.Entry{{Key: "writable", Name: "Writable", Provider: prov}},
	}

	if response := runV2(t, &m, "provider.tracks", ipc.Request{Provider: "writable", Playlist: "Mix"}); !response.OK || response.Playlist != "Mix" || response.Total != 1 {
		t.Fatalf("provider.tracks = %+v, want playlist Mix and 1 track", response)
	}
	tracks := []ipc.TrackInfo{{Path: "/a.flac"}, {Path: "/b.flac"}}
	if response := runV2(t, &m, "playlist.add_many", ipc.Request{Provider: "writable", Playlist: "Mix", Tracks: tracks}); !response.OK || response.Total != 2 {
		t.Fatalf("playlist.add_many = %+v, want 2 added", response)
	}
	if want := []string{"Mix:/a.flac", "Mix:/b.flac"}; !reflect.DeepEqual(prov.added, want) {
		t.Fatalf("added = %v, want %v", prov.added, want)
	}
}

// Every runtime.* alias of the registry reaches an operation that the Model
// serves. The server rejects the names that are not registered, so the Model
// keeps no alias for them.
func TestNormalizeV2OperationCoversRegistryAliases(t *testing.T) {
	registry := ipc.DefaultOperationRegistry()
	for _, operation := range registry.Operations() {
		name := operation.Name
		if !strings.HasPrefix(name, "runtime.") || name == "runtime.snapshot" || name == "runtime.status" {
			continue
		}
		canonical := normalizeV2Operation(name)
		if _, ok := registry.Lookup(canonical); !ok || strings.HasPrefix(canonical, "runtime.") {
			t.Errorf("%s maps to %q, want a registered operation", name, canonical)
		}
	}
	for _, name := range []string{"player.play", "player.previous", "player.volume.set", "runtime.playlist.get"} {
		if _, ok := registry.Lookup(name); ok {
			t.Errorf("%s is registered, want an unknown operation", name)
		}
		if got := normalizeV2Operation(name); got != name {
			t.Errorf("%s maps to %q, want no alias", name, got)
		}
	}

	pl := playlist.New()
	pl.Add(playlist.Track{Path: "/music/one.flac", Title: "One"})
	engine := &playbackFakeEngine{}
	m := Model{player: engine, playlist: pl}
	if response := runV2(t, &m, "runtime.queue.list", ipc.Request{}); !response.OK || response.Total != 1 {
		t.Fatalf("runtime.queue.list = %+v, want the live playlist", response)
	}
	if response := runV2(t, &m, "runtime.volume", ipc.Request{Value: -3}); !response.OK || engine.volume != -3 {
		t.Fatalf("runtime.volume = %+v, volume %v; want -3", response, engine.volume)
	}
}

// While a skipped-to stream buffers, the runtime state reports its start
// and the duration of its metadata, not the clock of the old pipeline. The
// start of the stream then publishes a new revision with the new clock.
func TestRuntimeStateFollowsAStreamStart(t *testing.T) {
	engine := &playbackFakeEngine{playing: true, seekable: true, position: 30 * time.Second, duration: 180 * time.Second}
	pl := playlist.New()
	pl.Add(
		playlist.Track{Path: "/music/a.mp3", Title: "A", DurationSecs: 180},
		playlist.Track{Path: "https://radio.example/show.mp3", Title: "Show", Stream: true, DurationSecs: 240},
	)
	pl.SetIndex(0)
	m := Model{player: engine, playlist: pl, vis: ui.NewVisualizer(float64(engine.SampleRate()))}
	m.SetIPCBroker(ipc.NewBroker())

	updated, _ := m.Update(playback.NextMsg{})
	m = updated.(Model)
	if !m.buffering {
		t.Fatal("the stream does not buffer after next")
	}
	snapshot := m.runtimeSnapshot()
	if snapshot.Position != 0 || snapshot.Duration != 240 {
		t.Fatalf("buffering snapshot clock = %v/%v, want 0/240", snapshot.Position, snapshot.Duration)
	}
	if _, state := m.playbackState(); state.Position != 0 || state.Track.Duration != 240*time.Second {
		t.Fatalf("buffering playback state clock = %v/%v, want 0/4m0s", state.Position, state.Track.Duration)
	}
	buffered := snapshot.Revision

	engine.position, engine.duration, engine.seekable = 0, 300*time.Second, false
	updated, _ = m.Update(streamPlayedMsg{path: "https://radio.example/show.mp3", gen: m.requests.stream})
	m = updated.(Model)
	snapshot = m.runtimeSnapshot()
	if snapshot.Revision == buffered {
		t.Fatalf("revision = %d after the stream start, want a new one", snapshot.Revision)
	}
	if snapshot.Duration != 300 || snapshot.Seekable {
		t.Fatalf("started snapshot = duration %v, seekable %v, want 300, false", snapshot.Duration, snapshot.Seekable)
	}
}
