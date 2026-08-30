package model

import (
	"encoding/json"
	"testing"

	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui"
)

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

// TestV2KeymapFollowsActiveScreen pins keymap.get and the state.get screen
// field to the screen that owns input: on the main screen only global rows
// come back, and opening a picker puts that screen's rows first.
func TestV2KeymapFollowsActiveScreen(t *testing.T) {
	tests := []struct {
		name         string
		open         func(*Model)
		wantScreenID string
		wantLabel    string
		wantFirst    string
	}{
		{name: "main screen", open: func(*Model) {}, wantScreenID: "playlist", wantLabel: "Playlist", wantFirst: "main"},
		{name: "file browser", open: func(m *Model) { m.fileBrowser.visible = true }, wantScreenID: "file_browser", wantLabel: "Files", wantFirst: "current"},
		{name: "queue", open: func(m *Model) { m.queue.visible = true }, wantScreenID: "queue", wantLabel: "Queue", wantFirst: "current"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine := &playbackFakeEngine{}
			m := Model{player: engine, playlist: playlist.New(), vis: ui.NewVisualizer(float64(engine.SampleRate()))}
			m.SetIPCBroker(ipc.NewBroker())
			tt.open(&m)

			reply := make(chan V2RequestResult, 1)
			updated, _ := m.Update(V2RequestMsg{Request: ipc.V2Request{Method: "state.get"}, Reply: reply})
			m = updated.(Model)
			state := <-reply
			if state.Error != nil || state.Result.Snapshot == nil || state.Result.Snapshot.Screen == nil {
				t.Fatalf("state result = %#v", state)
			}
			if got := *state.Result.Snapshot.Screen; got.ID != tt.wantScreenID || got.Label != tt.wantLabel {
				t.Fatalf("screen = %#v, want %s/%s", got, tt.wantScreenID, tt.wantLabel)
			}

			reply = make(chan V2RequestResult, 1)
			updated, _ = m.Update(V2RequestMsg{Request: ipc.V2Request{Method: "keymap.get"}, Reply: reply})
			m = updated.(Model)
			result := <-reply
			if result.Error != nil {
				t.Fatalf("keymap error = %#v", result.Error)
			}
			var entries []ipc.KeymapEntry
			if err := json.Unmarshal(result.Result.Result, &entries); err != nil {
				t.Fatal(err)
			}
			if len(entries) == 0 || entries[0].Section != tt.wantFirst {
				t.Fatalf("first entry = %#v, want section %q", entries, tt.wantFirst)
			}
			for _, entry := range entries {
				if len(entry.Keys) == 0 || entry.Label == "" || entry.Action == "" {
					t.Fatalf("incomplete entry %#v", entry)
				}
			}
			if overlay := m.buildKeymapEntries(); countRows(overlay) != len(entries) {
				t.Fatalf("overlay has %d rows, keymap.get has %d", countRows(overlay), len(entries))
			}
		})
	}
}

// countRows counts selectable overlay rows, skipping section dividers.
func countRows(entries []keymapEntry) int {
	n := 0
	for _, entry := range entries {
		if !entry.divider {
			n++
		}
	}
	return n
}
