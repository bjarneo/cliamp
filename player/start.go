package player

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/gopxl/beep/v2"
	"github.com/gopxl/beep/v2/speaker"
)

// ErrRevoked reports that a ticket no longer identifies a live request: a newer
// request superseded it, Stop or ClearPreload revoked it, or the engine closed.
// Controllers treat it as silent; it never describes a source failure.
var ErrRevoked = errors.New("player: source revoked")

// ErrAlreadyPrepared reports a second Prepare for the same ticket.
var ErrAlreadyPrepared = errors.New("player: source already prepared")

// StartRequest describes one playback start.
type StartRequest struct {
	Path          string
	KnownDuration time.Duration // metadata duration; 0 when unknown
	Offset        time.Duration // position to begin at; ignored for yt-dlp pages
	YTDL          bool          // Path is a page URL for the yt-dlp | ffmpeg chain
}

// PlaybackStats identifies a source and snapshots its final playback state.
// Ticket is zero only when no source is loaded; every started source carries
// the ticket its request was reserved with.
type PlaybackStats struct {
	Ticket             uint64
	Position, Duration time.Duration
	Seekable           bool
	Playing, Paused    bool
}

// Advance identifies the exact preloaded source promoted by the audio callback.
type Advance struct {
	Ticket   uint64
	Finished PlaybackStats
}

type pendingSource struct {
	ticket    uint64
	ctx       context.Context
	cancel    context.CancelFunc
	preparing bool
	ready     *trackPipeline
}

// BeginStart reserves authority synchronously and cancels the previous candidate.
func (p *Player) BeginStart() (uint64, context.Context) {
	p.lifecycleMu.Lock()
	defer p.lifecycleMu.Unlock()
	return p.beginSource(&p.pendingStart)
}

// BeginPreload reserves a gapless candidate using the same source identity space.
func (p *Player) BeginPreload() (uint64, context.Context) {
	p.lifecycleMu.Lock()
	defer p.lifecycleMu.Unlock()
	return p.beginSource(&p.pendingPreload)
}

func (p *Player) beginSource(slot **pendingSource) (uint64, context.Context) {
	p.revokePending(slot)
	p.sequence++
	ctx, cancel := context.WithCancel(context.Background())
	if p.closed {
		cancel()
		return p.sequence, ctx
	}
	*slot = &pendingSource{ticket: p.sequence, ctx: ctx, cancel: cancel}
	return p.sequence, ctx
}

// revokePending is called with lifecycleMu held. Ready resources are reaped
// separately so reserving a new request never waits for process shutdown.
func (p *Player) revokePending(slot **pendingSource) {
	if pending := *slot; pending != nil {
		pending.cancel()
		p.closeLater(pending.ready)
		*slot = nil
	}
}

func (p *Player) pending(ticket uint64) *pendingSource {
	for _, candidate := range []*pendingSource{p.pendingStart, p.pendingPreload} {
		if candidate != nil && candidate.ticket == ticket {
			return candidate
		}
	}
	return nil
}

// Prepare opens a reserved source and stores its resources in the engine.
// It never alters active audio or registers a gapless source.
func (p *Player) Prepare(ticket uint64, req StartRequest) error {
	p.lifecycleMu.Lock()
	pending := p.pending(ticket)
	if pending == nil || pending.ctx.Err() != nil || p.closed {
		p.lifecycleMu.Unlock()
		return ErrRevoked
	}
	if pending.preparing || pending.ready != nil {
		p.lifecycleMu.Unlock()
		return ErrAlreadyPrepared
	}
	pending.preparing = true
	p.work.Add(1)
	p.lifecycleMu.Unlock()
	defer p.work.Done()

	tp, err := p.openStart(pending.ctx, req)
	p.lifecycleMu.Lock()
	defer p.lifecycleMu.Unlock()
	if p.pending(ticket) != pending || pending.ctx.Err() != nil {
		if tp != nil {
			tp.cancel = pending.cancel
			p.closeLater(tp)
		}
		return ErrRevoked
	}
	if err != nil {
		pending.cancel()
		return err
	}
	tp.ctx, tp.cancel, tp.ticket = pending.ctx, pending.cancel, ticket
	pending.ready = tp
	return nil
}

// closeLater registers cleanup before relinquishing engine ownership. Close
// waits for this work as well as every still-running preparation.
func (p *Player) closeLater(pipelines ...*trackPipeline) {
	for _, tp := range pipelines {
		if tp == nil {
			continue
		}
		tp.interrupt()
		p.work.Add(1)
		go func() { defer p.work.Done(); tp.close() }()
	}
}

func pipelineStats(tp *trackPipeline) PlaybackStats {
	if tp == nil {
		return PlaybackStats{}
	}
	stats := PlaybackStats{Ticket: tp.ticket, Duration: tp.knownDuration,
		Seekable: tp.seekable || (tp.ytdlSeek && tp.knownDuration > 0)}
	if tp.livePrefetch != nil {
		stats.Position = tp.livePrefetch.Position() + tp.streamOffset
		if stats.Duration <= 0 {
			stats.Duration = tp.decodedDuration
		}
	} else if tp.decoder != nil && tp.format.SampleRate > 0 {
		stats.Position = tp.format.SampleRate.D(tp.decoder.Position()) + tp.streamOffset
		if n := tp.decoder.Len(); n > 0 {
			stats.Duration = tp.format.SampleRate.D(n)
		}
	}
	return stats
}

// openStart builds the pipeline for req without touching the speaker.
func (p *Player) openStart(ctx context.Context, req StartRequest) (*trackPipeline, error) {
	if req.YTDL {
		return p.openYTDLStart(ctx, req)
	}
	tp, err := p.buildPipeline(ctx, req.Path)
	if err != nil {
		return nil, fmt.Errorf("play at %v: %w", req.Offset, err)
	}
	tp.setKnownDuration(req.KnownDuration)
	if req.Offset > 0 && tp.seekable && !tp.ytdlSeek {
		if sample := relativeSeekSample(tp, req.Offset); sample > 0 {
			// Ignored deliberately: a failed seek should start the track from
			// the beginning, not refuse to play it.
			_ = tp.decoder.Seek(sample)
		}
	}
	return tp, nil
}

// openYTDLStart builds a yt-dlp | ffmpeg pipe chain for a page URL. Playback
// can begin as soon as the first PCM samples arrive (~1-3s). Not seekable.
func (p *Player) openYTDLStart(ctx context.Context, req StartRequest) (*trackPipeline, error) {
	knownDuration := req.KnownDuration
	probeCtx, stopProbe := context.WithCancel(ctx)
	defer stopProbe()
	probeCh := make(chan time.Duration, 1)
	if knownDuration == 0 {
		p.work.Add(1)
		go func() {
			defer p.work.Done()
			probeCh <- probeYTDLDuration(probeCtx, req.Path)
		}()
	}
	tp, err := p.buildYTDLPipeline(ctx, req.Path, 0)
	if err != nil {
		return nil, err
	}
	if knownDuration == 0 {
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()
		select {
		case knownDuration = <-probeCh:
		case <-timer.C:
		case <-ctx.Done():
			tp.close()
			return nil, ctx.Err()
		}
	}
	tp.knownDuration = knownDuration
	return tp, nil
}

// CommitStart transfers a prepared source to the speaker when its ticket is current.
// On the first start it builds the long-lived EQ → volume → tap → ctrl chain;
// later starts swap only the track source via the gapless streamer.
//
// It runs on the caller's goroutine, normally the UI thread, and takes the
// speaker lock. The wait is bounded by one audio callback: the active source is
// interrupted first, so a read blocked in Stream returns and the callback
// releases the lock. Every decoder kind must therefore be releasable either by
// its interrupt hook or by cancelling its context.
func (p *Player) CommitStart(ticket uint64) (PlaybackStats, bool) {
	p.lifecycleMu.Lock()
	defer p.lifecycleMu.Unlock()
	pending := p.pendingStart
	if p.closed || pending == nil || pending.ticket != ticket || pending.ready == nil || pending.ctx.Err() != nil {
		return PlaybackStats{}, false
	}
	tp := pending.ready
	p.pendingStart = nil
	p.revokePending(&p.pendingPreload)
	p.cancelSeekLocked()
	var finished PlaybackStats
	p.resumeSpeaker()

	// Collect old pipelines to close after releasing locks.
	var oldCurrent, oldNext *trackPipeline

	p.mu.Lock()
	started := p.started
	active, queued := p.current, p.nextPipeline
	p.mu.Unlock()

	p.gapless.SetNext(nil)
	if queued != nil {
		queued.interrupt()
	}
	if started {
		// A nav/pipe decoder may be blocked in Stream while the speaker mutex is
		// held. Interrupt it first so source replacement can acquire the mutex.
		if active != nil {
			active.interrupt()
		}
		// Lock the speaker so the goroutine finishes any in-progress Stream()
		// call before we swap the source and unpause. The ctrl.Paused write
		// must happen under the speaker lock because the audio thread reads it
		// on every Stream() call.
		speaker.Lock()
		p.gapless.Replace(tp.stream)
		p.ctrl.Paused = false
		p.mu.Lock()
		finished = pipelineStats(p.current)
		oldCurrent = p.current
		oldNext = p.nextPipeline
		p.current = tp
		p.nextPipeline = nil
		p.playing.Store(true)
		p.paused.Store(false)
		p.mu.Unlock()
		speaker.Unlock()
	} else {
		p.mu.Lock()
		finished = pipelineStats(p.current)
		oldCurrent, oldNext = p.current, p.nextPipeline
		p.gapless.Replace(tp.stream)

		// Build the long-lived pipeline once
		var s beep.Streamer = p.gapless
		s = newSpeedStreamer(s, &p.speed)

		for i := range 10 {
			s = newBiquad(s, eqFreqs[i], 1.4, &p.eqBands[i], float64(p.sr))
		}

		p.tap = newTap(s, p.tapBufferFrames, int(p.sr), p.speakerBufferFrames)
		s = &volumeStreamer{s: p.tap, vol: &p.volume, mono: &p.mono, cachedDB: math.NaN()}
		p.ctrl = &beep.Ctrl{Streamer: s}
		p.started = true
		p.current = tp
		p.nextPipeline = nil
		p.playing.Store(true)
		p.paused.Store(false)
		p.mu.Unlock()
	}

	if !started {
		speaker.Play(p.ctrl)
	}
	// Start API-based now-playing polling for streams without ICY metadata
	// (no-op otherwise). Done here, not in buildPipeline, so preloaded
	// pipelines that may never play don't spawn pollers.
	p.startStreamMetadata(tp.path)
	// Close old resources asynchronously to avoid blocking the caller
	// (UI thread) on slow Close() operations (ffmpeg wait, HTTP teardown).
	p.closeLater(oldCurrent, oldNext)
	return finished, true
}

// CommitPreload transfers a ready source into the gapless slot.
func (p *Player) CommitPreload(ticket uint64) bool {
	p.lifecycleMu.Lock()
	defer p.lifecycleMu.Unlock()
	pending := p.pendingPreload
	if p.closed || pending == nil || pending.ticket != ticket || pending.ready == nil || pending.ctx.Err() != nil {
		return false
	}
	tp := pending.ready
	p.pendingPreload = nil
	p.gapless.SetNext(nil)
	speaker.Lock()
	p.mu.Lock()
	old := p.nextPipeline
	p.nextPipeline = tp
	tp.gaplessToken = p.gapless.SetNext(tp.stream)
	p.mu.Unlock()
	speaker.Unlock()
	p.closeLater(old)
	return true
}
