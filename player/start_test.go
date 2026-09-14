package player

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gopxl/beep/v2"
	"github.com/gopxl/beep/v2/speaker"
)

func lifecyclePlayer(t *testing.T) *Player {
	t.Helper()
	p := newTestPlayer()
	p.sr = 100
	p.started = true
	p.ctrl = &beep.Ctrl{}
	p.gapless = &gaplessStreamer{onSwap: p.handleGaplessSwap}
	t.Cleanup(func() { p.suspended = true; p.Close() })
	return p
}

func prepareAsync(p *Player, ticket uint64, path string) <-chan error {
	done := make(chan error, 1)
	go func() { done <- p.Prepare(ticket, StartRequest{Path: path}) }()
	return done
}

func awaitPrepare(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("Prepare did not return")
	}
	return nil
}

func registerTestSource(p *Player, d beep.StreamSeekCloser) {
	p.RegisterStreamerFactory("test:", func(context.Context, string) (beep.StreamSeekCloser, beep.Format, time.Duration, error) {
		return d, beep.Format{SampleRate: 100, NumChannels: 2, Precision: 2}, 0, nil
	})
}

func TestPrepareCancellation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		preload bool
		revoke  func(*Player)
	}{
		{"new start", false, func(p *Player) { p.BeginStart() }},
		{"stop start", false, func(p *Player) { p.suspended = true; p.Stop() }},
		{"new preload", true, func(p *Player) { p.BeginPreload() }},
		{"clear preload", true, func(p *Player) { p.ClearPreload() }},
		{"stop preload", true, func(p *Player) { p.suspended = true; p.Stop() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := lifecyclePlayer(t)
			entered := make(chan struct{})
			p.RegisterStreamerFactory("test:", func(ctx context.Context, _ string) (beep.StreamSeekCloser, beep.Format, time.Duration, error) {
				close(entered)
				<-ctx.Done()
				return nil, beep.Format{}, 0, ctx.Err()
			})
			var ticket uint64
			if tc.preload {
				ticket, _ = p.BeginPreload()
			} else {
				ticket, _ = p.BeginStart()
			}
			done := prepareAsync(p, ticket, "test:blocked")
			<-entered
			tc.revoke(p)
			if err := awaitPrepare(t, done); !errors.Is(err, ErrRevoked) {
				t.Fatalf("Prepare = %v", err)
			}
			if _, ok := p.CommitStart(ticket); ok {
				t.Fatal("revoked start committed")
			}
			if p.CommitPreload(ticket) {
				t.Fatal("revoked preload committed")
			}
		})
	}
}

func TestPrepareAndCommitHaveSeparateAuthority(t *testing.T) {
	p := lifecyclePlayer(t)
	old := newPlaybackTestDecoder()
	p.current = &trackPipeline{ticket: 11, decoder: old, stream: old, format: beep.Format{SampleRate: 100}, seekable: true}
	p.gapless.Replace(old)
	d := newPlaybackTestDecoder()
	registerTestSource(p, d)
	ticket, ctx := p.BeginStart()
	if err := p.Prepare(ticket, StartRequest{Path: "test:new"}); err != nil {
		t.Fatal(err)
	}
	if p.current.decoder != old {
		t.Fatal("Prepare changed audio")
	}
	stats, ok := p.CommitStart(ticket)
	if !ok || stats.Ticket != 11 || stats.Duration != 10*time.Second || !stats.Seekable {
		t.Fatalf("CommitStart = %+v, %v", stats, ok)
	}
	if ctx.Err() != nil {
		t.Fatal("commit canceled active source")
	}
	if p.current.decoder != d {
		t.Fatal("commit did not transfer prepared source")
	}
	if _, ok := p.CommitStart(ticket); ok {
		t.Fatal("ticket committed twice")
	}
	p.BeginStart()
	if ctx.Err() != nil {
		t.Fatal("new pending start canceled committed source")
	}
}

func TestReadySourceRevocationAndCloseJoin(t *testing.T) {
	p := lifecyclePlayer(t)
	d := &blockingCloseDecoder{playbackTestDecoder: newPlaybackTestDecoder(), closing: make(chan struct{}), release: make(chan struct{})}
	registerTestSource(p, d)
	ticket, ctx := p.BeginStart()
	if err := p.Prepare(ticket, StartRequest{Path: "test:ready"}); err != nil {
		t.Fatal(err)
	}
	p.BeginStart()
	<-ctx.Done()
	<-d.closing
	done := make(chan struct{})
	p.suspended = true
	go func() { p.Close(); close(done) }()
	select {
	case <-done:
		t.Fatal("Close returned before discarded ready resource was reaped")
	default:
	}
	close(d.release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close failed to join cleanup")
	}
}

type blockingCloseDecoder struct {
	*playbackTestDecoder
	closing, release chan struct{}
}

func (d *blockingCloseDecoder) Close() error {
	close(d.closing)
	<-d.release
	return d.playbackTestDecoder.Close()
}

func TestCloseJoinsRevokedPreparation(t *testing.T) {
	p := lifecyclePlayer(t)
	entered, release := make(chan struct{}), make(chan struct{})
	d := newPlaybackTestDecoder()
	p.RegisterStreamerFactory("test:", func(context.Context, string) (beep.StreamSeekCloser, beep.Format, time.Duration, error) {
		close(entered)
		<-release
		return d, beep.Format{SampleRate: 100}, 0, nil
	})
	ticket, ctx := p.BeginStart()
	prepared := prepareAsync(p, ticket, "test:late")
	<-entered
	done := make(chan struct{})
	p.suspended = true
	go func() { p.Close(); close(done) }()
	<-ctx.Done()
	select {
	case <-done:
		t.Fatal("Close returned before preparation completed")
	default:
	}
	close(release)
	if err := awaitPrepare(t, prepared); !errors.Is(err, ErrRevoked) {
		t.Fatalf("Prepare = %v", err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close failed to join preparation")
	}
	select {
	case <-d.closed:
	default:
		t.Fatal("late source leaked")
	}
}

func TestRevokedTicketNeverOpensAndTicketsAreUnique(t *testing.T) {
	p := lifecyclePlayer(t)
	var calls atomic.Int32
	p.RegisterStreamerFactory("test:", func(context.Context, string) (beep.StreamSeekCloser, beep.Format, time.Duration, error) {
		calls.Add(1)
		return nil, beep.Format{}, 0, errors.New("failed")
	})
	first, _ := p.BeginStart()
	second, _ := p.BeginPreload()
	third, _ := p.BeginStart()
	if !(first < second && second < third) {
		t.Fatalf("tickets not unique: %d %d %d", first, second, third)
	}
	if err := p.Prepare(first, StartRequest{Path: "test:old"}); !errors.Is(err, ErrRevoked) {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("revoked source opened")
	}
	if err := p.Prepare(third, StartRequest{Path: "test:failed"}); err == nil || errors.Is(err, ErrRevoked) {
		t.Fatalf("failure = %v", err)
	}
}

func TestGaplessAdvanceCarriesExactTicketAndFinishedStats(t *testing.T) {
	p := lifecyclePlayer(t)
	old := &finiteStatsDecoder{position: 375, total: 500}
	p.current = &trackPipeline{ticket: 9, decoder: old, stream: old, format: beep.Format{SampleRate: 100}, seekable: true}
	p.gapless.Replace(old)
	registerTestSource(p, newPlaybackTestDecoder())
	ticket, ctx := p.BeginPreload()
	if err := p.Prepare(ticket, StartRequest{Path: "test:next"}); err != nil {
		t.Fatal(err)
	}
	if p.HasPreload() {
		t.Fatal("Prepare registered gapless audio")
	}
	if !p.CommitPreload(ticket) {
		t.Fatal("preload commit failed")
	}
	speaker.Lock()
	p.gapless.Stream(make([][2]float64, 10))
	speaker.Unlock()
	// Later reservations cannot relabel an already completed transition.
	later, _ := p.BeginPreload()
	a, ok := p.TakeAdvance()
	if !ok || a.Ticket != ticket || a.Ticket == later || a.Finished.Ticket != 9 || a.Finished.Position != 3750*time.Millisecond || a.Finished.Duration != 5*time.Second {
		t.Fatalf("advance = %+v, %v", a, ok)
	}
	if ctx.Err() != nil {
		t.Fatal("promotion canceled active preload")
	}
	if _, ok := p.TakeAdvance(); ok {
		t.Fatal("advance delivered twice")
	}
}

type finiteStatsDecoder struct{ position, total int }

func (d *finiteStatsDecoder) Stream([][2]float64) (int, bool) { return 0, false }
func (d *finiteStatsDecoder) Err() error                      { return nil }
func (d *finiteStatsDecoder) Len() int                        { return d.total }
func (d *finiteStatsDecoder) Position() int                   { return d.position }
func (d *finiteStatsDecoder) Seek(n int) error                { d.position = n; return nil }
func (d *finiteStatsDecoder) Close() error                    { return nil }

func TestStopReturnsPromotedSourceAndPreservesAdvance(t *testing.T) {
	p := lifecyclePlayer(t)
	old := &finiteStatsDecoder{position: 100, total: 100}
	p.current = &trackPipeline{ticket: 10, decoder: old, stream: old, format: beep.Format{SampleRate: 100}}
	p.gapless.Replace(old)
	registerTestSource(p, newPlaybackTestDecoder())
	ticket, _ := p.BeginPreload()
	if err := p.Prepare(ticket, StartRequest{Path: "test:next"}); err != nil {
		t.Fatal(err)
	}
	if !p.CommitPreload(ticket) {
		t.Fatal("preload commit failed")
	}
	speaker.Lock()
	p.gapless.Stream(make([][2]float64, 10))
	speaker.Unlock()
	p.suspended = true
	stopped := p.Stop()
	if stopped.Ticket != ticket || stopped.Duration != 10*time.Second {
		t.Fatalf("stopped = %+v", stopped)
	}
	a, ok := p.TakeAdvance()
	if !ok || a.Ticket != ticket || a.Finished.Ticket != 10 {
		t.Fatalf("advance after Stop = %+v, %v", a, ok)
	}
}

func TestSeekReplacementTransfersCancellationAuthority(t *testing.T) {
	p := lifecyclePlayer(t)
	oldCtx, oldCancel := context.WithCancel(context.Background())
	oldDecoder := newPlaybackTestDecoder()
	old := &trackPipeline{ctx: oldCtx, cancel: oldCancel, ticket: 42, decoder: oldDecoder, stream: oldDecoder}
	p.current = old
	p.gapless.Replace(oldDecoder)
	ctx, cancel := context.WithCancel(context.WithoutCancel(oldCtx))
	stopParent := context.AfterFunc(oldCtx, cancel)
	replacementDecoder := newPlaybackTestDecoder()
	replacement := &trackPipeline{ctx: ctx, cancel: cancel, ticket: 42, decoder: replacementDecoder, stream: replacementDecoder}
	if !p.commitYTDLSeek(old, replacement, 0, stopParent) {
		t.Fatal("seek replacement rejected")
	}
	if oldCtx.Err() == nil {
		t.Fatal("old source was not canceled")
	}
	if ctx.Err() != nil {
		t.Fatal("old source cancellation killed replacement")
	}
	p.suspended = true
	p.Stop()
	if ctx.Err() == nil {
		t.Fatal("Stop did not cancel replacement lifetime")
	}
}

func TestPreparedMetadataCannotChangeActiveSource(t *testing.T) {
	p := lifecyclePlayer(t)
	old := newPlaybackTestDecoder()
	activeTitle := new(atomic.Value)
	activeTitle.Store("active")
	p.current = &trackPipeline{decoder: old, stream: old, streamTitle: activeTitle}
	p.gapless.Replace(old)
	registerTestSource(p, newPlaybackTestDecoder())
	ticket, _ := p.BeginPreload()
	if err := p.Prepare(ticket, StartRequest{Path: "test:next"}); err != nil {
		t.Fatal(err)
	}
	p.pendingPreload.ready.streamTitle.Store("candidate")
	if got := p.StreamTitle(); got != "active" {
		t.Fatalf("active title = %q", got)
	}
	p.BeginPreload()
	if got := p.StreamTitle(); got != "active" {
		t.Fatalf("revocation changed title = %q", got)
	}
}

func TestClearPreloadPreservesSourceAlreadyReadByAudio(t *testing.T) {
	p := lifecyclePlayer(t)
	old := &finiteStatsDecoder{}
	p.current = &trackPipeline{ticket: 9, decoder: old, stream: old}
	p.gapless.Replace(old)
	next := &blockingPlaybackDecoder{playbackTestDecoder: newPlaybackTestDecoder(), entered: make(chan struct{}), release: make(chan struct{})}
	registerTestSource(p, next)
	ticket, ctx := p.BeginPreload()
	if err := p.Prepare(ticket, StartRequest{Path: "test:next"}); err != nil {
		t.Fatal(err)
	}
	if !p.CommitPreload(ticket) {
		t.Fatal("preload commit failed")
	}
	audioDone := make(chan struct{})
	go func() { speaker.Lock(); p.gapless.Stream(make([][2]float64, 10)); speaker.Unlock(); close(audioDone) }()
	<-next.entered
	p.mu.Lock()
	activeTicket := p.current.ticket
	p.mu.Unlock()
	if activeTicket != ticket {
		close(next.release)
		<-audioDone
		t.Fatal("audio read source before publishing its identity")
	}
	cleared := make(chan struct{})
	go func() { p.ClearPreload(); close(cleared) }()
	close(next.release)
	<-audioDone
	select {
	case <-cleared:
	case <-time.After(5 * time.Second):
		t.Fatal("ClearPreload did not complete")
	}
	if ctx.Err() != nil {
		t.Fatal("ClearPreload canceled an already active source")
	}
}

type blockingPlaybackDecoder struct {
	*playbackTestDecoder
	entered, release chan struct{}
}

func (d *blockingPlaybackDecoder) Stream(samples [][2]float64) (int, bool) {
	close(d.entered)
	<-d.release
	return d.playbackTestDecoder.Stream(samples)
}

func TestDelayedSeekCannotAffectReplacementSource(t *testing.T) {
	for _, tc := range []struct {
		name string
		seek func(*Player, uint64, time.Duration) error
	}{
		{"native", (*Player).Seek},
		{"yt-dlp", (*Player).SeekYTDL},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := lifecyclePlayer(t)
			registerTestSource(p, newPlaybackTestDecoder())
			oldTicket, _ := p.BeginStart()
			if err := p.Prepare(oldTicket, StartRequest{Path: "test:old"}); err != nil {
				t.Fatal(err)
			}
			if _, ok := p.CommitStart(oldTicket); !ok {
				t.Fatal("initial commit failed")
			}
			delayed := func() error { return tc.seek(p, oldTicket, time.Second) }

			current := &finiteStatsDecoder{position: 17, total: 1000}
			registerTestSource(p, current)
			currentTicket, _ := p.BeginStart()
			if err := p.Prepare(currentTicket, StartRequest{Path: "test:current"}); err != nil {
				t.Fatal(err)
			}
			if _, ok := p.CommitStart(currentTicket); !ok {
				t.Fatal("replacement commit failed")
			}
			// Even the yt-dlp path must reject stale identity before building a source.
			p.current.ytdlSeek = tc.name == "yt-dlp"
			if err := delayed(); !errors.Is(err, ErrRevoked) {
				t.Fatalf("delayed seek = %v", err)
			}
			if got := p.Snapshot(); got.Ticket != currentTicket || got.Position != 170*time.Millisecond {
				t.Fatalf("replacement was changed: %+v", got)
			}
		})
	}
}

func TestSnapshotIncludesSourceAndPlaybackState(t *testing.T) {
	p := lifecyclePlayer(t)
	registerTestSource(p, &finiteStatsDecoder{position: 125, total: 500})
	ticket, _ := p.BeginStart()
	if err := p.Prepare(ticket, StartRequest{Path: "test:source"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := p.CommitStart(ticket); !ok {
		t.Fatal("commit failed")
	}
	got := p.Snapshot()
	if got.Ticket != ticket || got.Position != 1250*time.Millisecond || got.Duration != 5*time.Second || !got.Seekable || !got.Playing || got.Paused {
		t.Fatalf("playing snapshot = %+v", got)
	}
	p.suspended = true
	p.TogglePause()
	got = p.Snapshot()
	if !got.Playing || !got.Paused || got.Ticket != ticket {
		t.Fatalf("paused snapshot = %+v", got)
	}
	p.Stop()
	if got := p.Snapshot(); got != (PlaybackStats{}) {
		t.Fatalf("stopped snapshot = %+v", got)
	}
}

// ctxBlockingDecoder stands in for a provider streamer such as Spotify: it has
// no interrupt hook, so only its context can release a read blocked in Stream.
type ctxBlockingDecoder struct {
	ctx       context.Context
	started   chan struct{}
	startOnce sync.Once
}

func (d *ctxBlockingDecoder) Stream([][2]float64) (int, bool) {
	d.startOnce.Do(func() { close(d.started) })
	<-d.ctx.Done()
	return 0, false
}
func (d *ctxBlockingDecoder) Err() error     { return nil }
func (d *ctxBlockingDecoder) Len() int       { return 0 }
func (d *ctxBlockingDecoder) Position() int  { return 0 }
func (d *ctxBlockingDecoder) Seek(int) error { return nil }
func (d *ctxBlockingDecoder) Close() error   { return nil }

// CommitStart runs on the UI thread and must not wait behind an audio callback
// stuck in a source that only context cancellation can release.
func TestCommitStartReleasesCustomSourceBlockedInStream(t *testing.T) {
	p := lifecyclePlayer(t)
	blocked := &ctxBlockingDecoder{started: make(chan struct{})}
	p.RegisterStreamerFactory("block:", func(ctx context.Context, _ string) (beep.StreamSeekCloser, beep.Format, time.Duration, error) {
		blocked.ctx = ctx
		return blocked, beep.Format{SampleRate: 100, NumChannels: 2, Precision: 2}, 0, nil
	})
	registerTestSource(p, newPlaybackTestDecoder())

	first, _ := p.BeginStart()
	if err := p.Prepare(first, StartRequest{Path: "block:one"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := p.CommitStart(first); !ok {
		t.Fatal("first commit refused")
	}
	audioDone := make(chan struct{})
	go func() {
		speaker.Lock()
		p.gapless.Stream(make([][2]float64, 4))
		speaker.Unlock()
		close(audioDone)
	}()
	<-blocked.started

	second, _ := p.BeginStart()
	if err := p.Prepare(second, StartRequest{Path: "test:two"}); err != nil {
		t.Fatal(err)
	}
	committed := make(chan bool, 1)
	go func() { _, ok := p.CommitStart(second); committed <- ok }()
	select {
	case ok := <-committed:
		if !ok {
			t.Fatal("replacement commit refused")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("CommitStart waited behind a source blocked in Stream")
	}
	select {
	case <-audioDone:
	case <-time.After(time.Second):
		t.Fatal("blocked custom source was not released")
	}
	if got := p.Snapshot(); got.Ticket != second {
		t.Fatalf("current ticket = %d, want %d", got.Ticket, second)
	}
}
