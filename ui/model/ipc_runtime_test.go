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
