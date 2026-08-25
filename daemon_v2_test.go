package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/internal/playback"
	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/player"
	"github.com/bjarneo/cliamp/playlist"
)

type daemonV2Engine struct {
	player.Engine

	playing  bool
	paused   bool
	position time.Duration
	duration time.Duration
	seekable bool
	volume   float64
	speed    float64
	mono     bool
	eq       [10]float64
}

func (e *daemonV2Engine) Play(string, time.Duration) error {
	e.playing = true
	e.paused = false
	return nil
}

func (e *daemonV2Engine) PlayYTDL(path string, duration time.Duration) error {
	return e.Play(path, duration)
}

func (e *daemonV2Engine) Stop()                   { e.playing, e.paused = false, false }
func (e *daemonV2Engine) TogglePause()            { e.paused = !e.paused }
func (e *daemonV2Engine) IsPlaying() bool         { return e.playing }
func (e *daemonV2Engine) IsPaused() bool          { return e.paused }
func (*daemonV2Engine) Drained() bool             { return false }
func (e *daemonV2Engine) Position() time.Duration { return e.position }
func (e *daemonV2Engine) Duration() time.Duration { return e.duration }
func (e *daemonV2Engine) Seekable() bool          { return e.seekable }
func (e *daemonV2Engine) Volume() float64         { return e.volume }
func (e *daemonV2Engine) Speed() float64          { return e.speed }
func (e *daemonV2Engine) Mono() bool              { return e.mono }
func (e *daemonV2Engine) EQBands() [10]float64    { return e.eq }
func (*daemonV2Engine) StreamTitle() string       { return "" }
func (*daemonV2Engine) StreamErr() error          { return nil }
func (e *daemonV2Engine) PositionAndDuration() (time.Duration, time.Duration) {
	return e.position, e.duration
}
func (e *daemonV2Engine) SetVolume(value float64) { e.volume = value }
func (e *daemonV2Engine) SetSpeed(value float64)  { e.speed = value }
func (e *daemonV2Engine) ToggleMono()             { e.mono = !e.mono }
func (e *daemonV2Engine) SetEQBand(band int, value float64) {
	if band >= 0 && band < len(e.eq) {
		e.eq[band] = value
	}
}
func (e *daemonV2Engine) Seek(offset time.Duration) error {
	e.position += offset
	return nil
}

func newDaemonV2TestDaemon() (*daemon, *daemonV2Engine) {
	engine := &daemonV2Engine{
		playing:  true,
		position: 12 * time.Second,
		duration: 3 * time.Minute,
		seekable: true,
		volume:   -6,
		speed:    1,
	}
	pl := playlist.New()
	pl.Add(
		playlist.Track{Path: "https://example.com/one", Title: "One", Stream: true},
		playlist.Track{Path: "https://example.com/two", Title: "Two", Stream: true},
	)
	pl.Queue(1)
	return &daemon{player: engine, playlist: pl, eqPreset: "Custom", runtimeRevision: 9}, engine
}

func TestDaemonV2StateSnapshot(t *testing.T) {
	d, _ := newDaemonV2TestDaemon()
	d.control = make(chan any, 1)
	dispatcher := newDaemonV2Dispatcher(d, ipc.NewJobStore())
	go func() { d.handleMessage(<-d.control) }()

	result, protocolErr := dispatcher.DispatchV2(context.Background(), ipc.V2Request{Method: "state.get"})
	if protocolErr != nil {
		t.Fatal(protocolErr)
	}
	if result.Snapshot == nil {
		t.Fatal("state.get returned no snapshot")
	}
	snapshot := result.Snapshot
	if snapshot.Revision != 9 || snapshot.PlaylistRevision != d.playlist.Revision() {
		t.Fatalf("revisions = %d/%d, want 9/%d", snapshot.Revision, snapshot.PlaylistRevision, d.playlist.Revision())
	}
	if snapshot.State != "playing" || !snapshot.Seekable || snapshot.Total != 2 || snapshot.PlayNextTotal != 1 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if snapshot.Track == nil || snapshot.LogicalTrack == nil || snapshot.Track.Path != "https://example.com/one" || snapshot.LogicalTrack.Path != "https://example.com/one" {
		t.Fatalf("tracks = %#v/%#v", snapshot.Track, snapshot.LogicalTrack)
	}
}

func TestDaemonV2AsyncJobResult(t *testing.T) {
	d, engine := newDaemonV2TestDaemon()
	d.control = make(chan any, 1)
	jobs := ipc.NewJobStore()
	dispatcher := newDaemonV2Dispatcher(d, jobs)

	result, protocolErr := dispatcher.DispatchV2(context.Background(), ipc.V2Request{
		Operation: "volume",
		Params:    json.RawMessage(`{"value":-18}`),
	})
	if protocolErr != nil {
		t.Fatal(protocolErr)
	}
	if result.Job == nil || result.Job.State != ipc.JobQueued {
		t.Fatalf("returned job = %#v", result.Job)
	}
	drainDaemonControl(t, d)

	job, ok := jobs.Get(result.Job.ID)
	if !ok || job.State != ipc.JobSucceeded {
		t.Fatalf("job = %#v, found=%v", job, ok)
	}
	var response ipc.Response
	if err := json.Unmarshal(job.Result, &response); err != nil {
		t.Fatal(err)
	}
	if !response.OK || response.Volume != -18 || engine.volume != -18 {
		t.Fatalf("response=%#v volume=%v", response, engine.volume)
	}
	if job.Snapshot == nil || job.Snapshot.Volume != -18 {
		t.Fatalf("job snapshot = %#v", job.Snapshot)
	}
}

func TestDaemonV2QueueAndPlayNextAreSeparate(t *testing.T) {
	d, _ := newDaemonV2TestDaemon()
	d.playlist.Replace(nil)
	d.control = make(chan any, 1)
	jobs := ipc.NewJobStore()
	dispatcher := newDaemonV2Dispatcher(d, jobs)

	queueJob := dispatchDaemonV2Job(t, d, dispatcher, jobs, "queue", `{"path":"https://example.com/queued"}`)
	if queueJob.Snapshot == nil || queueJob.Snapshot.Total != 1 || queueJob.Snapshot.PlayNextTotal != 0 {
		t.Fatalf("queue snapshot = %#v", queueJob.Snapshot)
	}

	enqueueJob := dispatchDaemonV2Job(t, d, dispatcher, jobs, "queue.enqueue", `{"index":0}`)
	if enqueueJob.Snapshot == nil || enqueueJob.Snapshot.Total != 1 || enqueueJob.Snapshot.PlayNextTotal != 1 {
		t.Fatalf("enqueue snapshot = %#v", enqueueJob.Snapshot)
	}

	playNextJob := dispatchDaemonV2Job(t, d, dispatcher, jobs, "playnext.list", `{}`)
	var playNext ipc.Response
	if err := json.Unmarshal(playNextJob.Result, &playNext); err != nil {
		t.Fatal(err)
	}
	if len(playNext.Tracks) != 1 || playNext.Total != 1 || playNext.Tracks[0].QueuePosition != 1 {
		t.Fatalf("play-next response = %#v", playNext)
	}

	liveQueueJob := dispatchDaemonV2Job(t, d, dispatcher, jobs, "queue.list", `{}`)
	var liveQueue ipc.Response
	if err := json.Unmarshal(liveQueueJob.Result, &liveQueue); err != nil {
		t.Fatal(err)
	}
	if len(liveQueue.Tracks) != 1 || liveQueue.Total != 1 || liveQueue.Tracks[0].Path != playNext.Tracks[0].Path {
		t.Fatalf("live queue response = %#v", liveQueue)
	}
}

func TestDaemonSendUsesControlQueueInOrder(t *testing.T) {
	d, engine := newDaemonV2TestDaemon()
	d.control = make(chan any, 2)

	d.Send(playback.SetVolumeMsg{VolumeDB: -10})
	d.Send(playback.SetVolumeMsg{VolumeDB: -20})
	if engine.volume != -6 {
		t.Fatalf("volume changed before control loop handled messages: %v", engine.volume)
	}

	drainDaemonControl(t, d)
	if engine.volume != -10 {
		t.Fatalf("first control message set volume to %v, want -10", engine.volume)
	}
	drainDaemonControl(t, d)
	if engine.volume != -20 {
		t.Fatalf("second control message set volume to %v, want -20", engine.volume)
	}
}

func dispatchDaemonV2Job(t *testing.T, d *daemon, dispatcher ipc.V2Dispatcher, jobs *ipc.JobStore, operation, params string) ipc.Job {
	t.Helper()
	result, protocolErr := dispatcher.DispatchV2(context.Background(), ipc.V2Request{Operation: operation, Params: json.RawMessage(params)})
	if protocolErr != nil {
		t.Fatal(protocolErr)
	}
	if result.Job == nil {
		t.Fatalf("%s returned no job", operation)
	}
	drainDaemonControl(t, d)
	job, ok := jobs.Get(result.Job.ID)
	if !ok || job.State != ipc.JobSucceeded {
		t.Fatalf("%s job = %#v, found=%v", operation, job, ok)
	}
	return job
}

func drainDaemonControl(t *testing.T, d *daemon) {
	t.Helper()
	select {
	case message := <-d.control:
		d.handleMessage(message)
	default:
		t.Fatal("daemon control queue is empty")
	}
}
