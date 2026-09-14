package main

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/history"
	"github.com/bjarneo/cliamp/internal/playback"
	"github.com/bjarneo/cliamp/internal/resume"
	"github.com/bjarneo/cliamp/player"
	"github.com/bjarneo/cliamp/playlist"
)

type daemonPlaybackFake struct {
	player.Engine
	playing      bool
	paused       bool
	drained      bool
	runtimeLive  bool
	playErr      error
	playCalls    []string
	stopCalls    int
	toggleCalls  int
	ticket       uint64
	activeTicket uint64
	request      player.StartRequest
}

func (f *daemonPlaybackFake) BeginStart() (uint64, context.Context) {
	f.ticket++
	return f.ticket, context.Background()
}

func (f *daemonPlaybackFake) Prepare(_ uint64, req player.StartRequest) error {
	f.playCalls = append(f.playCalls, req.Path)
	f.request = req
	return f.playErr
}

func (f *daemonPlaybackFake) CommitStart(ticket uint64) (player.PlaybackStats, bool) {
	if ticket != f.ticket {
		return player.PlaybackStats{}, false
	}
	f.playing, f.paused, f.drained = true, false, false
	f.activeTicket = ticket
	return player.PlaybackStats{}, true
}

func (f *daemonPlaybackFake) Stop() player.PlaybackStats {
	f.ticket++
	f.stopCalls++
	f.playing = false
	f.paused = false
	f.activeTicket = 0
	return player.PlaybackStats{}
}

func (f *daemonPlaybackFake) Snapshot() player.PlaybackStats {
	return player.PlaybackStats{Ticket: f.activeTicket, Playing: f.playing, Paused: f.paused}
}

func (f *daemonPlaybackFake) TogglePause() {
	f.toggleCalls++
	f.paused = !f.paused
}

func (f *daemonPlaybackFake) IsPlaying() bool         { return f.playing }
func (f *daemonPlaybackFake) IsPaused() bool          { return f.paused }
func (f *daemonPlaybackFake) Drained() bool           { return f.drained }
func (f *daemonPlaybackFake) IsLiveStream() bool      { return f.runtimeLive }
func (f *daemonPlaybackFake) Duration() time.Duration { return 0 }
func (f *daemonPlaybackFake) Position() time.Duration { return 0 }
func (f *daemonPlaybackFake) PositionAndDuration() (time.Duration, time.Duration) {
	return 0, 0
}
func (f *daemonPlaybackFake) Volume() float64 { return 0 }
func (f *daemonPlaybackFake) Seekable() bool  { return false }

func TestDaemonResumeRestartsLiveStation(t *testing.T) {
	fake := &daemonPlaybackFake{playing: true, paused: true, runtimeLive: true}
	pl := playlist.New()
	pl.Add(
		playlist.Track{Path: "https://radio.example.com/one", Stream: true},
		playlist.Track{Path: "https://radio.example.com/two", Stream: true},
	)
	pl.SetIndex(0)
	d := &daemon{player: fake, playlist: pl}

	d.Send(playback.PlayMsg{})

	if got := pl.Index(); got != 0 {
		t.Fatalf("playlist index = %d, want current station 0", got)
	}
	if fake.stopCalls != 1 || len(fake.playCalls) != 1 || fake.playCalls[0] != "https://radio.example.com/one" {
		t.Fatalf("stop/play calls = %d/%v, want one restart of current station", fake.stopCalls, fake.playCalls)
	}
	if fake.toggleCalls != 0 {
		t.Fatalf("TogglePause calls = %d, want a fresh live connection", fake.toggleCalls)
	}
}

func TestDaemonDrainedLiveStationDoesNotAdvance(t *testing.T) {
	tests := []struct {
		name    string
		playErr error
	}{
		{name: "restart succeeds"},
		{name: "restart fails without repeated advance", playErr: errors.New("offline")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &daemonPlaybackFake{
				playing:     true,
				drained:     true,
				runtimeLive: true,
				playErr:     tt.playErr,
			}
			pl := playlist.New()
			pl.Add(
				playlist.Track{Path: "https://radio.example.com/one", Stream: true},
				playlist.Track{Path: "https://radio.example.com/two", Stream: true},
			)
			pl.SetIndex(0)
			d := &daemon{player: fake, playlist: pl}

			d.tick()

			if got := pl.Index(); got != 0 {
				t.Fatalf("playlist index = %d, want current station 0", got)
			}
			if len(fake.playCalls) != 1 || fake.playCalls[0] != "https://radio.example.com/one" {
				t.Fatalf("play calls = %v, want current station restart", fake.playCalls)
			}
			if tt.playErr != nil && fake.playing {
				t.Fatal("player remains active after failed live restart")
			}
		})
	}
}

type daemonPreparation struct {
	ticket uint64
	path   string
	ctx    context.Context
}

type daemonAsyncEngine struct {
	*daemonV2Engine
	mu          sync.Mutex
	contexts    map[uint64]context.Context
	cancel      context.CancelFunc
	gates       map[string]chan error
	started     chan daemonPreparation
	commits     []uint64
	delivered   chan uint64
	drained     bool
	seekStarted chan daemonSeekRequest
	seekRelease chan struct{}
	seekDone    chan error
}

type daemonSeekRequest struct {
	ticket uint64
	offset time.Duration
}

func (e *daemonAsyncEngine) Seek(ticket uint64, offset time.Duration) error {
	e.seekStarted <- daemonSeekRequest{ticket, offset}
	<-e.seekRelease
	err := e.daemonV2Engine.Seek(ticket, offset)
	e.seekDone <- err
	return err
}

func (e *daemonAsyncEngine) Drained() bool { return e.drained }

func (e *daemonAsyncEngine) BeginStart() (uint64, context.Context) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cancel != nil {
		e.cancel()
	}
	e.ticket++
	ctx, cancel := context.WithCancel(context.Background())
	e.cancel = cancel
	e.contexts[e.ticket] = ctx
	return e.ticket, ctx
}

func (e *daemonAsyncEngine) Prepare(ticket uint64, req player.StartRequest) error {
	e.mu.Lock()
	ctx := e.contexts[ticket]
	e.mu.Unlock()
	e.started <- daemonPreparation{ticket, req.Path, ctx}
	// Deliberately permit a success after cancellation: the controller must
	// reject a stale completion independently of a cooperative source reader.
	return <-e.gates[req.Path]
}

func (e *daemonAsyncEngine) CommitStart(ticket uint64) (player.PlaybackStats, bool) {
	e.commits = append(e.commits, ticket)
	finished := player.PlaybackStats{Position: e.position, Duration: e.duration}
	if _, ok := e.daemonV2Engine.CommitStart(ticket); !ok {
		return player.PlaybackStats{}, false
	}
	e.position = 0
	return finished, true
}

func (e *daemonAsyncEngine) Stop() player.PlaybackStats {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cancel != nil {
		e.cancel()
	}
	return e.daemonV2Engine.Stop()
}

type daemonBarrier chan struct{}
type daemonTick struct{}

func newAsyncPlaybackDaemon(t *testing.T) (*daemon, *daemonAsyncEngine) {
	t.Helper()
	old := playlist.Track{Path: "old.mp3", Title: "Old", DurationSecs: 100}
	pl := playlist.New()
	pl.Add(old, playlist.Track{Path: "slow.mp3", Title: "Slow"}, playlist.Track{Path: "new.mp3", Title: "New"})
	e := &daemonAsyncEngine{
		daemonV2Engine: &daemonV2Engine{ticket: 1, activeTicket: 1, playing: true, position: 55 * time.Second, duration: 100 * time.Second},
		contexts:       make(map[uint64]context.Context),
		gates:          map[string]chan error{"slow.mp3": make(chan error, 1), "new.mp3": make(chan error, 1)},
		started:        make(chan daemonPreparation, 4),
		delivered:      make(chan uint64, 4),
		seekStarted:    make(chan daemonSeekRequest, 1),
		seekRelease:    make(chan struct{}, 1),
		seekDone:       make(chan error, 1),
	}
	d := &daemon{player: e, playbackTicket: 1, playlist: pl, playbackTrack: old, hasPlaybackTrack: true,
		control: make(chan any, daemonControlQueueCapacity), done: make(chan struct{})}
	loopDone := make(chan struct{})
	go func() {
		defer close(loopDone)
		for {
			select {
			case <-d.done:
				return
			case msg := <-d.control:
				switch m := msg.(type) {
				case daemonBarrier:
					close(m)
				case daemonTick:
					d.tick()
				default:
					d.handleMessage(msg)
					if prepared, ok := msg.(daemonSourcePrepared); ok {
						e.delivered <- prepared.ticket
					}
				}
			}
		}
	}()
	t.Cleanup(func() {
		d.Send(playback.StopMsg{})
		flushDaemon(t, d)
		for _, gate := range e.gates {
			select {
			case gate <- player.ErrRevoked:
			default:
			}
		}
		select {
		case e.seekRelease <- struct{}{}:
		default:
		}
		close(d.done)
		<-loopDone
	})
	return d, e
}

func flushDaemon(t *testing.T, d *daemon) {
	t.Helper()
	done := make(daemonBarrier)
	d.Send(done)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("daemon control loop is blocked")
	}
}

func awaitDaemonPreparation(t *testing.T, e *daemonAsyncEngine) daemonPreparation {
	t.Helper()
	select {
	case started := <-e.started:
		return started
	case <-time.After(2 * time.Second):
		t.Fatal("source preparation did not begin")
		return daemonPreparation{}
	}
}

func completeDaemonPreparation(t *testing.T, d *daemon, e *daemonAsyncEngine, source daemonPreparation, err error) {
	t.Helper()
	e.gates[source.path] <- err
	select {
	case ticket := <-e.delivered:
		if ticket != source.ticket {
			t.Fatalf("delivered source %d, want %d", ticket, source.ticket)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("prepared source was not delivered through the control loop")
	}
}

func TestDaemonAsyncSupersededPreparationCannotCommit(t *testing.T) {
	d, e := newAsyncPlaybackDaemon(t)
	d.Send(playback.NextMsg{})
	slow := awaitDaemonPreparation(t, e)
	flushDaemon(t, d)
	if got := d.snapshotState().Track.URL; got != "old.mp3" {
		t.Fatalf("metadata while opening = %q, want actual old source", got)
	}
	d.Send(playback.NextMsg{})
	newer := awaitDaemonPreparation(t, e)
	select {
	case <-slow.ctx.Done():
	default:
		t.Fatal("new source did not cancel older preparation")
	}
	completeDaemonPreparation(t, d, e, newer, nil)
	completeDaemonPreparation(t, d, e, slow, nil)
	flushDaemon(t, d)
	if got := d.playbackTrack.Path; got != "new.mp3" {
		t.Fatalf("actual playback = %q, want newer source", got)
	}
	if len(e.commits) != 1 || e.commits[0] != newer.ticket {
		t.Fatalf("commit calls = %v, want only newer ticket %d", e.commits, newer.ticket)
	}
}

func TestDaemonAsyncStopRevokesPreparedSource(t *testing.T) {
	d, e := newAsyncPlaybackDaemon(t)
	d.Send(playback.NextMsg{})
	slow := awaitDaemonPreparation(t, e)
	d.Send(playback.StopMsg{})
	flushDaemon(t, d)
	select {
	case <-slow.ctx.Done():
	default:
		t.Fatal("stop did not cancel source preparation")
	}
	completeDaemonPreparation(t, d, e, slow, nil)
	flushDaemon(t, d)
	if d.hasPlaybackTrack || e.playing || len(e.commits) != 0 {
		t.Fatalf("stale result restarted stopped daemon: active=%v playing=%v commits=%v", d.hasPlaybackTrack, e.playing, e.commits)
	}
}

func TestDaemonAsyncFailureKeepsActualPlayback(t *testing.T) {
	d, e := newAsyncPlaybackDaemon(t)
	d.Send(playback.NextMsg{})
	slow := awaitDaemonPreparation(t, e)
	completeDaemonPreparation(t, d, e, slow, errors.New("source unavailable"))
	if got := d.snapshotState().Track.URL; got != "old.mp3" || !e.playing {
		t.Fatalf("failed replacement metadata = %q, playing=%v", got, e.playing)
	}
	if len(e.commits) != 0 {
		t.Fatalf("failed preparation attempted commit: %v", e.commits)
	}
}

func TestDaemonTickDoesNotAdvancePastPendingSource(t *testing.T) {
	d, e := newAsyncPlaybackDaemon(t)
	e.drained = true
	d.Send(playback.NextMsg{})
	slow := awaitDaemonPreparation(t, e)
	d.Send(daemonTick{})
	flushDaemon(t, d)
	if got := d.playlist.Index(); got != 1 || d.pendingStart != slow.ticket {
		t.Fatalf("tick advanced past pending source: index=%d ticket=%d", got, d.pendingStart)
	}
	select {
	case started := <-e.started:
		t.Fatalf("tick opened an unwanted source: %+v", started)
	default:
	}
}

func TestDaemonPendingSourceKeepsHistoryAndResumeOnActualTrack(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLIAMP_CONFIG_DIR", dir)
	d, e := newAsyncPlaybackDaemon(t)
	d.historyStore = history.NewAt(filepath.Join(dir, "history.toml"))
	d.Send(playback.NextMsg{})
	slow := awaitDaemonPreparation(t, e)
	flushDaemon(t, d)
	d.saveResume()
	if state := resume.Load(); state.Path != "old.mp3" || state.PositionSec != 55 {
		t.Fatalf("resume during preparation = %+v, want old source at 55 seconds", state)
	}
	completeDaemonPreparation(t, d, e, slow, nil)
	entries, err := d.historyStore.Recent(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Track.Path != "old.mp3" {
		t.Fatalf("history after commit = %+v, want finished old source", entries)
	}
}

func TestDaemonDeferredSeekRetainsOriginalSourceTicket(t *testing.T) {
	d, e := newAsyncPlaybackDaemon(t)
	d.Send(playback.SetPositionMsg{Position: 60 * time.Second})
	select {
	case seek := <-e.seekStarted:
		if seek.ticket != 1 || seek.offset != 5*time.Second {
			t.Fatalf("scheduled seek = %+v, want source 1 at +5 seconds", seek)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("seek was not scheduled")
	}
	// Neither the active seek nor accumulated input may reach a replacement.
	d.Send(playback.SeekMsg{Offset: 10 * time.Second})
	d.Send(playback.NextMsg{})
	newer := awaitDaemonPreparation(t, e)
	completeDaemonPreparation(t, d, e, newer, nil)
	e.seekRelease <- struct{}{}
	select {
	case err := <-e.seekDone:
		if !errors.Is(err, player.ErrRevoked) {
			t.Fatalf("stale seek = %v, want revocation", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("deferred seek did not finish")
	}
	flushDaemon(t, d)
	if e.position != 0 || d.playbackTicket != newer.ticket {
		t.Fatalf("stale seek altered replacement: position=%v active=%d", e.position, d.playbackTicket)
	}
	select {
	case request := <-e.seekStarted:
		t.Fatalf("pending seek survived source replacement: %+v", request)
	default:
	}
}

func TestDaemonFailedReplacementStopsDrainedPlayback(t *testing.T) {
	for _, repeat := range []playlist.RepeatMode{playlist.RepeatOff, playlist.RepeatOne, playlist.RepeatAll} {
		t.Run(repeat.String(), func(t *testing.T) {
			d, e := newAsyncPlaybackDaemon(t)
			d.Send(playback.NextMsg{})
			pending := awaitDaemonPreparation(t, e)
			flushDaemon(t, d)
			e.drained = true
			d.playlist.SetRepeat(repeat)
			completeDaemonPreparation(t, d, e, pending, errors.New("unavailable"))
			d.Send(daemonTick{})
			flushDaemon(t, d)
			if e.playing || d.hasPlaybackTrack || d.pendingStart != 0 {
				t.Fatalf("failed drained replacement remains active: playing=%v track=%v pending=%d", e.playing, d.hasPlaybackTrack, d.pendingStart)
			}
			select {
			case next := <-e.started:
				t.Fatalf("tick retried failed source: %+v", next)
			default:
			}
		})
	}
}

func TestDaemonSeeksSerializeAndCombinePendingInput(t *testing.T) {
	d, e := newAsyncPlaybackDaemon(t)
	awaitSeek := func(want time.Duration) {
		t.Helper()
		select {
		case request := <-e.seekStarted:
			if request.offset != want {
				t.Fatalf("seek offset = %v, want %v", request.offset, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("seek did not start")
		}
	}
	finishSeek := func() {
		t.Helper()
		e.seekRelease <- struct{}{}
		select {
		case err := <-e.seekDone:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("seek did not finish")
		}
	}
	d.Send(playback.SetPositionMsg{Position: 60 * time.Second})
	awaitSeek(5 * time.Second)
	d.Send(playback.SetPositionMsg{Position: 70 * time.Second})
	for range 100 {
		d.Send(playback.SeekMsg{Offset: time.Millisecond})
	}
	flushDaemon(t, d)
	select {
	case request := <-e.seekStarted:
		t.Fatalf("concurrent seek: %+v", request)
	default:
	}
	finishSeek()
	awaitSeek(10*time.Second + 100*time.Millisecond)
	finishSeek()
	flushDaemon(t, d)
	if e.position != 70*time.Second+100*time.Millisecond {
		t.Fatalf("final position = %v", e.position)
	}
}
