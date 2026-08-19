package player

import (
	"context"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gopxl/beep/v2"
)

// Quality holds configurable audio output parameters.
type Quality struct {
	SampleRate      int // output sample rate in Hz (e.g. 44100, 48000)
	BufferMs        int // speaker buffer in milliseconds
	ResampleQuality int // beep resample quality factor (1–4)
	BitDepth        int // PCM bit depth for FFmpeg output: 16 or 32 (32 = lossless)

	// BitPerfect requests the bit-perfect output path: no resampling anywhere
	// in cliamp, and the output device reopened at each track's native rate.
	// SampleRate is then only the initial (and fallback) device rate. When the
	// platform has no bit-perfect backend the player falls back to the standard
	// path instead of failing.
	BitPerfect bool
	// Device is the output device for the bit-perfect backend (an ALSA device
	// name on Linux). Empty means the system default.
	Device string
	// Channels selects which physical output channels carry cliamp's stereo
	// signal, as "left,right" 0-based indices (e.g. "2,3") — needed for a
	// multichannel audio interface with no native stereo mode, whose one PCM
	// substream only accepts a fixed, larger channel count. Empty (the
	// default) is plain 2-channel negotiation, unaffected by this.
	Channels string
}

// StreamerFactory creates a beep.StreamSeekCloser for a custom URI scheme
// (e.g., spotify:track:xxx). Returns the streamer, its format, the track
// duration, and any error.
type StreamerFactory func(uri string) (beep.StreamSeekCloser, beep.Format, time.Duration, error)

// Player is the audio engine managing the playback pipeline:
//
//	[Gapless] -> [10x Biquad EQ] -> [Volume] -> [Tap] -> [Ctrl] -> speaker
//	     ↑
//	     ├─ current: [Decode A] → [Resample A]
//	     └─ next:    [Decode B] → [Resample B]  (preloaded)
type Player struct {
	mu sync.Mutex
	// out is the audio output backend. rate mirrors the rate it is currently
	// running at; it is atomic because the EQ filters read it from the audio
	// thread while a track change may be switching the device rate.
	out          sink
	rate         atomic.Int64
	bitPerfect   bool         // bit-perfect backend active (see Quality.BitPerfect)
	rateNote     atomic.Value // stores string: why the device rate request was not honoured
	gapless      *gaplessStreamer
	current      *trackPipeline // active track's resources
	nextPipeline *trackPipeline // preloaded track's resources
	// preloadDeferredPath/Cur remember a preload dropped by preloadPipeline for
	// a bit-perfect rate mismatch, so Preload's fast path (preloadStillDeferred)
	// can skip rebuilding it every tick until the current track changes.
	preloadDeferredPath string
	preloadDeferredCur  *trackPipeline
	started             bool       // true after the root streamer was handed to out
	suspendMu           sync.Mutex // guards suspended and the sink's suspend/resume calls
	suspended           bool       // true when the sink has been suspended
	ctrl                *beep.Ctrl
	volMin              atomic.Uint64     // dB floor stored as Float64bits, range [-90, 0]
	volume              atomic.Uint64     // dB stored as Float64bits, range [volMin, +6]
	speed               atomic.Uint64     // playback speed ratio as Float64bits; 1.0 = normal
	eqBands             [10]atomic.Uint64 // dB stored as math.Float64bits
	tap                 *tap
	playing             atomic.Bool
	paused              atomic.Bool
	mono                atomic.Bool
	resampleQuality     int
	bitDepth            int // 16 or 32

	gaplessAdvance atomic.Bool  // set when gapless transition fires
	seekGen        atomic.Int64 // generation counter for yt-dlp seeks; incremented to cancel stale seeks

	streamTitle      atomic.Value               // stores string, set by ICY reader callback
	customFactories  map[string]StreamerFactory // URI scheme prefix -> factory (e.g. "spotify:" -> fn)
	bufferedURLMatch func(string) bool          // optional: returns true for URLs needing navBuffer pipeline

	streamMetaResolver StreamMetadataResolver // optional: API-based now-playing for streams without ICY
	metaCancel         context.CancelFunc     // cancels the active metadata poller; guarded by mu
}

// StreamMetadataResolver matches a stream URL to a now-playing fetcher for
// broadcasters that carry no inline ICY metadata (e.g. NTS, FIP) and instead
// publish the current track via a separate JSON API. It returns a fetch
// function, the poll interval, and ok=false when the URL is not recognized.
type StreamMetadataResolver func(streamURL string) (fetch func(ctx context.Context) (string, error), interval time.Duration, ok bool)

// New creates a Player and initializes the audio output with the given quality
// settings. When q.BitPerfect is set it tries the bit-perfect backend first and
// falls back to the standard speaker if the platform or device cannot provide
// it; the reason is then reported through BitPerfect().
func New(q Quality) (*Player, error) {
	if q.SampleRate <= 0 || q.BufferMs <= 0 || q.ResampleQuality <= 0 {
		return nil, fmt.Errorf("invalid quality settings: SampleRate=%d, BufferMs=%d, ResampleQuality=%d",
			q.SampleRate, q.BufferMs, q.ResampleQuality)
	}
	bitDepth := q.BitDepth
	if bitDepth != 32 {
		bitDepth = 16
	}

	p := &Player{resampleQuality: q.ResampleQuality}
	if q.BitPerfect {
		channels, chErr := parseChannelLayout(q.Channels)
		if chErr != nil {
			p.rateNote.Store(fmt.Sprintf("bit-perfect output unavailable: %v", chErr))
		} else if out, err := newBitPerfectSink(q.Device, q.SampleRate, q.BufferMs, channels); err != nil {
			p.rateNote.Store(fmt.Sprintf("bit-perfect output unavailable: %v", err))
		} else {
			p.out = out
			p.bitPerfect = true
			// FFmpeg decodes to float PCM in bit-perfect mode: a 16-bit
			// intermediate would truncate 24-bit sources before they ever
			// reach the device.
			bitDepth = 32
		}
	}
	if p.out == nil {
		out, err := newBeepSink(q.SampleRate, q.BufferMs)
		if err != nil {
			return nil, err
		}
		p.out = out
	}
	p.bitDepth = bitDepth
	p.rate.Store(int64(p.out.SampleRate()))
	p.volMin.Store(math.Float64bits(-50))
	p.speed.Store(math.Float64bits(1.0))
	p.gapless = &gaplessStreamer{}
	// Suspend the output immediately; the audio callback goroutine burns
	// ~2% CPU even on silence. Resume is called on every Play().
	_ = p.out.Suspend()
	p.suspended = true
	p.gapless.onSwap = func() {
		// Called from audio thread (goroutine) when gapless transition occurs.
		// Swap current ← nextPipeline and close the old one. The API metadata
		// poller is intentionally not restarted here: gapless advance is for
		// finite tracks, while resolver-backed streams (NTS, FIP) are infinite
		// live radio that is never preloaded as a gapless next track.
		p.mu.Lock()
		old := p.current
		p.current = p.nextPipeline
		p.nextPipeline = nil
		p.mu.Unlock()
		if old != nil {
			old.close()
		}
		p.gaplessAdvance.Store(true)
	}
	return p, nil
}

// Play opens and starts playing an audio file. On the first call it builds
// the long-lived EQ → volume → tap → ctrl chain and starts the speaker.
// Subsequent calls swap only the track source via the gapless streamer.
// knownDuration is the metadata duration (use 0 if unknown); it is used as a
// fallback when the decoder cannot determine the length (e.g. HTTP streams).
func (p *Player) Play(path string, knownDuration time.Duration) error {
	tp, err := p.buildPipeline(path)
	if err != nil {
		return err
	}
	tp.setKnownDuration(knownDuration)
	return p.playPipeline(tp)
}

// PlayYTDL starts playing a yt-dlp page URL via a piped yt-dlp | ffmpeg chain.
// Playback starts as soon as the first PCM samples arrive (~1-3s). Not seekable.
func (p *Player) PlayYTDL(pageURL string, knownDuration time.Duration) error {
	// Probe duration concurrently with pipeline setup so it doesn't delay playback.
	probeCh := make(chan time.Duration, 1)
	if knownDuration == 0 {
		go func() { probeCh <- probeYTDLDuration(pageURL) }()
	}
	tp, err := p.buildYTDLPipeline(pageURL, 0)
	if err != nil {
		return err
	}
	if knownDuration == 0 {
		// The probe ran concurrently with buildYTDLPipeline. Try to
		// collect the result, but don't block playback for more than 2s.
		// A hung probeYTDLDuration (e.g. yt-dlp zombie keeping pipes
		// open) previously blocked here forever, leaving the UI stuck
		// at "Buffering...".
		select {
		case d := <-probeCh:
			if d > 0 {
				knownDuration = d
			}
		case <-time.After(2 * time.Second):
			// Probe still running — start playback without duration.
			// The seek bar won't show progress but audio plays immediately.
		}
	}
	tp.knownDuration = knownDuration
	return p.playPipeline(tp)
}

// playPipeline wires a ready-to-play trackPipeline into the speaker chain.
// On the first call it builds the long-lived EQ → volume → tap → ctrl chain.
// Subsequent calls swap only the track source via the gapless streamer.
func (p *Player) playPipeline(tp *trackPipeline) error {
	// Retune the device to the track before anything reads tp.stream: in
	// bit-perfect mode this may reopen the device, and it decides whether the
	// stream needs a resampler after all.
	p.alignOutput(tp)
	p.resumeSpeaker()

	// Collect old pipelines to close after releasing locks.
	var oldCurrent, oldNext *trackPipeline

	if p.started {
		// Lock the speaker so the goroutine finishes any in-progress Stream()
		// call before we swap the source and unpause. The ctrl.Paused write
		// must happen under the speaker lock because the audio thread reads it
		// on every Stream() call.
		p.out.Lock()
		p.gapless.Replace(tp.stream)
		p.ctrl.Paused = false
		p.out.Unlock()
	}

	p.mu.Lock()

	oldCurrent = p.current
	oldNext = p.nextPipeline
	p.current = tp
	p.nextPipeline = nil

	firstPlay := !p.started
	if firstPlay {
		p.gapless.Replace(tp.stream)

		// Build the long-lived pipeline once
		var s beep.Streamer = p.gapless
		s = newSpeedStreamer(s, &p.speed)

		for i := range 10 {
			s = newBiquad(s, eqFreqs[i], 1.4, &p.eqBands[i], &p.rate)
		}

		p.tap = newTap(s, 4096)
		s = &volumeStreamer{s: p.tap, vol: &p.volume, mono: &p.mono, cachedDB: math.NaN()}
		p.ctrl = &beep.Ctrl{Streamer: s}
		p.started = true
	}
	p.playing.Store(true)
	p.paused.Store(false)
	p.mu.Unlock()

	if firstPlay {
		p.out.Play(p.ctrl)
	}
	// Start API-based now-playing polling for streams without ICY metadata
	// (no-op otherwise). Done here, not in buildPipelineAt, so preloaded
	// pipelines that may never play don't spawn pollers.
	p.startStreamMetadata(tp.path)
	// Close old resources asynchronously to avoid blocking the caller
	// (UI thread) on slow Close() operations (ffmpeg wait, HTTP teardown).
	go closePipelines(oldCurrent, oldNext)
	return nil
}

// Preload builds a pipeline for the next track and queues it for gapless transition.
// knownDuration is the metadata duration (use 0 if unknown).
func (p *Player) Preload(path string, knownDuration time.Duration) error {
	if p.preloadStillDeferred(path) {
		// Already tried and dropped for a bit-perfect rate mismatch under the
		// current track (see preloadPipeline); the UI retries preloading on
		// every tick until HasPreload() is true, so without this the same
		// probe/build would repeat every tick until the track ends.
		return nil
	}
	// A cheap ffprobe first, before the expensive build (ffmpeg process,
	// network connection, provider request) that preloadPipeline's own
	// mismatch check further down would otherwise immediately throw away for
	// the exact same reason: a gapless transition can't retune the device, so
	// a next track at a different native rate is always going to be deferred
	// in bit-perfect mode. This only ever short-circuits — a failed or
	// inconclusive probe (native == 0) falls through to the normal build,
	// same as if this check weren't here.
	if p.bitPerfect {
		if native := probeNativeRate(path); native > 0 && native != p.out.SampleRate() {
			p.deferPreload(path)
			return nil
		}
	}
	tp, err := p.buildPipeline(path)
	if err != nil {
		return err
	}
	tp.setKnownDuration(knownDuration)
	return p.preloadPipeline(path, tp)
}

// preloadStillDeferred reports whether path was already dropped by
// preloadPipeline for a bit-perfect rate mismatch and the current track
// hasn't changed since — i.e. retrying would just repeat the same result.
func (p *Player) preloadStillDeferred(path string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.preloadDeferredPath != "" && p.preloadDeferredPath == path && p.preloadDeferredCur == p.current
}

// deferPreload records path as dropped for a bit-perfect rate mismatch under
// the current track, so preloadStillDeferred can skip retrying it every tick.
func (p *Player) deferPreload(path string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.preloadDeferredPath = path
	p.preloadDeferredCur = p.current
}

// PreloadYTDL builds a yt-dlp pipe pipeline and queues it for gapless transition.
func (p *Player) PreloadYTDL(pageURL string, knownDuration time.Duration) error {
	tp, err := p.buildYTDLPipeline(pageURL, 0)
	if err != nil {
		return err
	}
	tp.knownDuration = knownDuration
	return p.preloadPipeline(pageURL, tp)
}

// preloadPipeline queues a ready trackPipeline for gapless transition. path
// is the caller's own request (a local path, stream URL, or yt-dlp page
// URL) — used as the deferral key instead of tp.path, which isn't set by
// every trackPipeline construction site (a custom URI streamer factory, or
// an ffmpeg-decode fallback after a native decoder failed, both leave it
// empty). Preload's fast path matches on this same string, so a dropped
// pathless pipeline would otherwise never be recorded as deferred and get
// rebuilt from scratch on every tick until the current track changes.
func (p *Player) preloadPipeline(path string, tp *trackPipeline) error {
	// A gapless transition happens inside a single device buffer, so it cannot
	// change the device's sample rate — it never calls alignOutput/
	// SetSampleRate at all. In bit-perfect mode a next track at a different
	// rate is therefore dropped here and opened normally (reopening the
	// device) once the current one drains. Remember the deferral (when the
	// pipeline has a path to key on) so Preload's fast path can skip rebuilding
	// it every tick until the current track changes.
	//
	// !p.out.RateExact() also defers even when the rate already matches: the
	// current device can be sitting at this exact number for an unrelated
	// reason (a fallback from an earlier track's unsupported rate — see
	// alsaDevice.requestedRate) without ever having been verified exact
	// *for* it. Gapless reusing it as-is would silently carry that stale
	// non-exact state into a track that would genuinely earn the bit-perfect
	// badge if actually reopened — which deferring here achieves, by forcing
	// the eventual non-gapless Play() through alignOutput's real
	// verification instead.
	if p.bitPerfect && tp.format.SampleRate > 0 && (!p.out.RateExact() || int(tp.format.SampleRate) != p.out.SampleRate()) {
		if path != "" {
			p.deferPreload(path)
		}
		go closePipelines(tp)
		return nil
	}
	p.mu.Lock()
	p.preloadDeferredPath = ""
	p.mu.Unlock()

	// Lock speaker to atomically swap the gapless next stream, ensuring no
	// in-flight transition reads from the old pipeline we're about to close.
	p.out.Lock()
	p.gapless.SetNext(tp.stream)
	p.out.Unlock()

	p.mu.Lock()
	old := p.nextPipeline
	p.nextPipeline = tp
	p.mu.Unlock()

	if old != nil {
		old.close()
	}
	return nil
}

// ClearPreload discards the preloaded next track (e.g., when shuffle/repeat changes).
// Speaker is locked to ensure no in-flight gapless transition can reference the
// pipeline we're about to close.
func (p *Player) ClearPreload() {
	p.out.Lock()
	p.gapless.SetNext(nil)
	p.out.Unlock()

	p.mu.Lock()
	old := p.nextPipeline
	p.nextPipeline = nil
	p.mu.Unlock()

	if old != nil {
		old.close()
	}
}

// GaplessAdvanced returns true (once) when a gapless transition happened.
func (p *Player) GaplessAdvanced() bool {
	return p.gaplessAdvance.CompareAndSwap(true, false)
}

// TogglePause toggles between paused and playing states.
// When pausing, the speaker is suspended to save CPU; when unpausing
// it is resumed so the audio callback drains the queued samples.
func (p *Player) TogglePause() {
	p.out.Lock()
	if p.ctrl != nil {
		p.ctrl.Paused = !p.ctrl.Paused
		paused := p.ctrl.Paused
		p.out.Unlock()
		p.paused.Store(paused)
		if paused {
			p.suspendSpeaker()
		} else {
			p.resumeSpeaker()
		}
	} else {
		p.out.Unlock()
	}
}

// Stop halts playback and releases resources. The speaker is suspended so
// the ALSA audio callback goroutine blocks (zero CPU) instead of streaming
// silence. Resume is called automatically on the next Play().
func (p *Player) Stop() {
	// Lock speaker to ensure the goroutine finishes any in-progress Stream()
	// call, then clear the source and pause. After unlock, the speaker will
	// only see silence from the gapless streamer (paused ctrl).
	p.out.Lock()
	p.gapless.Clear()
	if p.ctrl != nil {
		p.ctrl.Paused = true
	}
	p.out.Unlock()

	// Now safe to close decoder resources — speaker can't be reading them.
	p.mu.Lock()
	oldCurrent := p.current
	oldNext := p.nextPipeline
	p.current = nil
	p.nextPipeline = nil
	p.playing.Store(false)
	p.paused.Store(false)
	p.mu.Unlock()

	p.stopStreamMetadata()
	closePipelines(oldCurrent, oldNext)

	p.suspendSpeaker()
}

// Seek moves the playback position by the given duration (positive or negative).
// For seekable local files, the decoder's Seek method is used directly.
// For HTTP streams with a known Content-Length and duration (seekableStream),
// seek is implemented by reconnecting with a Range: bytes=N- header and
// rebuilding the decoder at the computed byte offset. This is known as
// seek-by-reconnect.
// Returns nil immediately for non-seekable streams (e.g., Icecast radio).
// The speaker lock is acquired first (outer), then p.mu briefly to snapshot
// the current pipeline, ensuring consistent lock ordering with the audio thread.
// Clears the preloaded next pipeline to prevent a stale gapless transition.
func (p *Player) Seek(d time.Duration) error {
	p.out.Lock()
	defer p.out.Unlock()
	p.mu.Lock()
	cur := p.current
	p.mu.Unlock()
	if cur == nil {
		return nil
	}

	// seekableStream: HTTP stream with Content-Length — reconnect at byte offset.
	// Release the speaker lock before the slow HTTP reconnect so the audio
	// thread and UI tick handler aren't blocked during the request.
	if cur.seekableStream && cur.knownDuration > 0 && cur.contentLength > 0 {
		// Compute new absolute position.
		curPos := cur.format.SampleRate.D(cur.decoder.Position()) + cur.streamOffset
		newPos := max(curPos+d, 0)
		if newPos >= cur.knownDuration {
			newPos = cur.knownDuration - time.Second
		}
		// Map position to byte offset: offset = newPos/duration * contentLength.
		// Use floating-point to avoid int64 overflow on large files.
		ratio := float64(newPos) / float64(cur.knownDuration)
		byteOffset := int64(ratio * float64(cur.contentLength))

		// Snapshot values and mute audio while reconnecting — prevents the
		// old stream from playing at the pre-seek position during the rebuild.
		path := cur.path
		knownDuration := cur.knownDuration
		contentLength := cur.contentLength
		p.gapless.Replace(nil)
		p.out.Unlock()

		// Build a new pipeline starting at the computed byte offset.
		// Speaker lock is NOT held — HTTP I/O can take seconds on slow networks.
		tp, err := p.buildPipelineAt(path, byteOffset, newPos)

		p.out.Lock() // re-acquire for defer
		if err != nil {
			// Restore the old stream on failure if the pipeline hasn't changed.
			p.mu.Lock()
			if p.current == cur {
				p.gapless.Replace(cur.stream)
			}
			p.mu.Unlock()
			return fmt.Errorf("seek reconnect: %w", err)
		}
		tp.knownDuration = knownDuration
		// seekableStream / contentLength / path are set by buildPipelineAt when
		// contentLength > 0, but byteOffset shifts the origin, so we keep the
		// original full-file contentLength and mark seekableStream explicitly.
		tp.seekableStream = true
		tp.contentLength = contentLength

		// Verify the current pipeline hasn't changed while we were unlocked
		// (e.g. track skip or another seek). If it changed, discard our work.
		p.mu.Lock()
		if p.current != cur {
			p.mu.Unlock()
			go closePipelines(tp)
			return nil
		}
		p.mu.Unlock()

		p.gapless.Replace(tp.stream)

		// Clear any preloaded next pipeline — its transition point is now stale.
		p.gapless.SetNext(nil)
		p.mu.Lock()
		old := p.current
		oldNext := p.nextPipeline
		p.current = tp
		p.nextPipeline = nil
		p.mu.Unlock()
		go closePipelines(old, oldNext)
		return nil
	}

	// yt-dlp seek-by-restart: handled outside the speaker lock via SeekYTDL.
	if cur.ytdlSeek {
		// Release speaker lock, then do the slow seek.
		p.out.Unlock()
		err := p.SeekYTDL(d)
		p.out.Lock() // re-acquire so defer Unlock works
		return err
	}

	// Local file (or ffmpeg-buffered PCM): use the decoder's native Seek.
	if !cur.seekable {
		return nil
	}
	curSample := cur.decoder.Position()
	curDur := cur.format.SampleRate.D(curSample)
	newSample := max(cur.format.SampleRate.N(curDur+d), 0)
	if newSample >= cur.decoder.Len() {
		newSample = cur.decoder.Len() - 1
	}
	if err := cur.decoder.Seek(newSample); err != nil {
		return err
	}
	// Invalidate the preloaded next pipeline — the gapless transition point
	// has moved and the old preload may be stale. The speaker lock is already
	// held, so we can safely clear the gapless next stream.
	p.gapless.SetNext(nil)
	p.mu.Lock()
	old := p.nextPipeline
	p.nextPipeline = nil
	p.mu.Unlock()
	if old != nil {
		old.close()
	}
	return nil
}

// CancelSeekYTDL increments the seek generation, causing any in-flight
// SeekYTDL to discard its result instead of swapping streams.
func (p *Player) CancelSeekYTDL() {
	p.seekGen.Add(1)
}

// SeekYTDL seeks a yt-dlp stream by restarting the pipeline at the target
// position. Must NOT be called with the speaker lock held.
// If a newer seek is requested (via CancelSeekYTDL) while this one is
// building, the result is discarded.
func (p *Player) SeekYTDL(d time.Duration) error {
	gen := p.seekGen.Load()

	// Snapshot current state without speaker lock.
	p.mu.Lock()
	cur := p.current
	p.mu.Unlock()
	if cur == nil || !cur.ytdlSeek {
		return nil
	}

	// Read position, then mute the current stream so the speaker outputs
	// silence while the new pipeline is being built (which blocks on Peek
	// waiting for yt-dlp data). Without this, the old audio keeps playing
	// at the pre-seek position during the rebuild.
	p.out.Lock()
	curPos := cur.format.SampleRate.D(cur.decoder.Position()) + cur.streamOffset
	p.gapless.Replace(nil)
	p.out.Unlock()

	newPos := max(curPos+d, 0)
	if cur.knownDuration > 0 && newPos >= cur.knownDuration {
		newPos = cur.knownDuration - time.Second
	}
	startSec := int(newPos.Seconds())

	// Build pipeline WITHOUT speaker lock (this is the slow part — spawns yt-dlp).
	tp, err := p.buildYTDLPipeline(cur.path, startSec)
	if err != nil {
		return fmt.Errorf("yt-dlp seek: %w", err)
	}
	tp.knownDuration = cur.knownDuration
	tp.ytdlSeek = true

	// Check if this seek was cancelled while we were building.
	if p.seekGen.Load() != gen {
		// A newer seek was requested — discard this result.
		go closePipelines(tp)
		return nil
	}

	// Now acquire speaker lock to swap streams.
	p.out.Lock()
	p.gapless.Replace(tp.stream)
	p.gapless.SetNext(nil)
	p.out.Unlock()

	p.mu.Lock()
	old := p.current
	oldNext := p.nextPipeline
	p.current = tp
	p.nextPipeline = nil
	p.mu.Unlock()
	// Clean up old pipelines async to avoid blocking on process wait.
	go closePipelines(old, oldNext)
	return nil
}

// IsYTDLSeek reports whether the current track uses yt-dlp seek-by-restart.
func (p *Player) IsYTDLSeek() bool {
	p.mu.Lock()
	cur := p.current
	p.mu.Unlock()
	return cur != nil && cur.ytdlSeek
}

// IsStreamSeek reports whether the current track uses HTTP seek-by-reconnect.
// When true, Seek() releases the speaker lock during the HTTP request, so
// callers should dispatch seeks asynchronously to avoid blocking the UI.
func (p *Player) IsStreamSeek() bool {
	p.mu.Lock()
	cur := p.current
	p.mu.Unlock()
	return cur != nil && cur.seekableStream && cur.knownDuration > 0 && cur.contentLength > 0
}

// Position returns the current playback position.
// For ranged HTTP streams (seek-by-reconnect), streamOffset is added to the
// decoder's sample-based position so the reported time is absolute within
// the track, not relative to the reconnect point.
func (p *Player) Position() time.Duration {
	p.out.Lock()
	defer p.out.Unlock()
	p.mu.Lock()
	cur := p.current
	p.mu.Unlock()
	if cur == nil {
		return 0
	}
	return cur.format.SampleRate.D(cur.decoder.Position()) + cur.streamOffset
}

// Duration returns the total duration of the current track.
// For seekable local files it is derived from the decoder's sample count.
// For HTTP streams where the decoder reports Len()==0, the metadata hint
// stored at pipeline build time (knownDuration) is returned instead.
func (p *Player) Duration() time.Duration {
	p.out.Lock()
	defer p.out.Unlock()
	p.mu.Lock()
	cur := p.current
	p.mu.Unlock()
	if cur == nil {
		return 0
	}
	if n := cur.decoder.Len(); n > 0 {
		return cur.format.SampleRate.D(n)
	}
	return cur.knownDuration
}

// PositionAndDuration returns both position and duration under a single
// speaker lock, avoiding two separate lock acquisitions per tick.
func (p *Player) PositionAndDuration() (time.Duration, time.Duration) {
	p.out.Lock()
	defer p.out.Unlock()
	p.mu.Lock()
	cur := p.current
	p.mu.Unlock()
	if cur == nil {
		return 0, 0
	}
	pos := cur.format.SampleRate.D(cur.decoder.Position()) + cur.streamOffset
	var dur time.Duration
	if n := cur.decoder.Len(); n > 0 {
		dur = cur.format.SampleRate.D(n)
	} else {
		dur = cur.knownDuration
	}
	return pos, dur
}

// SetVolumeMin sets the minimum volume floor in dB, clamped to [-90, 0].
// If the current volume is below the new floor it is immediately raised to match.
func (p *Player) SetVolumeMin(db float64) {
	newMin := max(min(db, 0), -90)
	p.volMin.Store(math.Float64bits(newMin))
	for {
		cur := p.volume.Load()
		curDB := math.Float64frombits(cur)
		if curDB >= newMin {
			break
		}
		if p.volume.CompareAndSwap(cur, math.Float64bits(newMin)) {
			break
		}
	}
}

// VolumeMin returns the current minimum volume floor in dB.
func (p *Player) VolumeMin() float64 {
	return math.Float64frombits(p.volMin.Load())
}

// SetVolume sets the volume in dB, clamped to [VolumeMin, +6].
func (p *Player) SetVolume(db float64) {
	p.volume.Store(math.Float64bits(max(min(db, 6), p.VolumeMin())))
}

// Volume returns the current volume in dB.
func (p *Player) Volume() float64 {
	return math.Float64frombits(p.volume.Load())
}

// SetSpeed sets the playback speed ratio, clamped to [0.25, 2.0].
// 1.0 is normal speed, 2.0 is double speed, etc.
func (p *Player) SetSpeed(ratio float64) {
	p.speed.Store(math.Float64bits(max(min(ratio, 2.0), 0.25)))
}

// Speed returns the current playback speed ratio.
func (p *Player) Speed() float64 {
	return math.Float64frombits(p.speed.Load())
}

// ToggleMono switches between stereo and mono (L+R downmix) output.
func (p *Player) ToggleMono() {
	p.mono.Store(!p.mono.Load())
}

// Mono returns true if mono output is enabled.
func (p *Player) Mono() bool {
	return p.mono.Load()
}

// SetEQBand sets a single EQ band's gain in dB, clamped to [-12, +12].
func (p *Player) SetEQBand(band int, dB float64) {
	if band < 0 || band >= 10 {
		return
	}
	p.eqBands[band].Store(math.Float64bits(max(min(dB, 12), -12)))
}

// EQBands returns a copy of all 10 EQ band gains.
func (p *Player) EQBands() [10]float64 {
	var bands [10]float64
	for i := range 10 {
		bands[i] = math.Float64frombits(p.eqBands[i].Load())
	}
	return bands
}

// IsPlaying returns true if a track is loaded and playing (possibly paused).
func (p *Player) IsPlaying() bool {
	return p.playing.Load()
}

// IsPaused returns true if playback is paused.
func (p *Player) IsPaused() bool {
	return p.paused.Load()
}

// Drained returns true if the current track ended with no preloaded next track.
func (p *Player) Drained() bool {
	return p.gapless.Drained()
}

// HasPreload returns true if a next track is already queued for gapless transition.
func (p *Player) HasPreload() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.nextPipeline != nil
}

// StreamTitle returns the current ICY stream title (e.g., "Artist - Song").
// Returns "" when no ICY metadata has been received.
func (p *Player) StreamTitle() string {
	v, _ := p.streamTitle.Load().(string)
	return v
}

// setStreamTitle is the ICY onMeta callback, called from the reader goroutine.
func (p *Player) setStreamTitle(title string) {
	p.streamTitle.Store(title)
}

// RegisterStreamMetadataResolver installs a resolver used to pull now-playing
// metadata for streams that lack inline ICY metadata. Pass nil to disable.
func (p *Player) RegisterStreamMetadataResolver(r StreamMetadataResolver) {
	p.mu.Lock()
	p.streamMetaResolver = r
	p.mu.Unlock()
}

// startStreamMetadata (re)starts background now-playing polling for streamURL,
// cancelling any previous poller. It is a no-op unless a registered resolver
// recognizes the URL, so streams that carry ICY metadata (or local files) are
// unaffected. The poller feeds titles through the same path as ICY metadata.
func (p *Player) startStreamMetadata(streamURL string) {
	p.stopStreamMetadata()

	p.mu.Lock()
	resolver := p.streamMetaResolver
	p.mu.Unlock()
	if resolver == nil || streamURL == "" || !isURL(streamURL) {
		return
	}
	fetch, interval, ok := resolver(streamURL)
	if !ok || fetch == nil {
		return
	}
	if interval <= 0 {
		interval = 15 * time.Second
	}

	ctx, cancel := context.WithCancel(context.Background())
	p.mu.Lock()
	p.metaCancel = cancel
	p.mu.Unlock()

	go p.pollStreamMetadata(ctx, fetch, interval)
}

// stopStreamMetadata cancels the active metadata poller, if any.
func (p *Player) stopStreamMetadata() {
	p.mu.Lock()
	cancel := p.metaCancel
	p.metaCancel = nil
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// pollStreamMetadata fetches the current title immediately and then on each
// interval tick until ctx is cancelled, publishing non-empty titles via
// setStreamTitle. A title fetched after cancellation is discarded so a stale
// poller cannot clobber the next stream's metadata.
func (p *Player) pollStreamMetadata(ctx context.Context, fetch func(context.Context) (string, error), interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		title, err := fetch(ctx)
		if ctx.Err() != nil {
			return
		}
		if err == nil && title != "" {
			p.setStreamTitle(title)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Seekable reports whether the current track supports seeking.
// Returns true for local files (decoder-native seek) and for HTTP streams
// with a known Content-Length and duration (seek-by-reconnect).
func (p *Player) Seekable() bool {
	p.mu.Lock()
	cur := p.current
	p.mu.Unlock()
	if cur == nil {
		return false
	}
	return cur.seekable || (cur.seekableStream && cur.knownDuration > 0) || (cur.ytdlSeek && cur.knownDuration > 0)
}

// StreamErr returns the current streamer error, if any (e.g., connection drops).
func (p *Player) StreamErr() error {
	p.mu.Lock()
	cur := p.current
	p.mu.Unlock()
	if cur == nil {
		return nil
	}
	return cur.decoder.Err()
}

// OutputErr returns the reason the output device stopped working, if any.
// Unlike StreamErr (an input-side failure that stops decoding), this is
// output-side: on Linux the bit-perfect sink can hit a write failure it
// can't recover from mid-track (e.g. the device was unplugged) and it does
// not always mean playback has stopped — the sink may have already fallen
// back to a working device on its own. Either way this is worth surfacing:
// see alsaSink.handleFatal.
func (p *Player) OutputErr() error {
	return p.out.Err()
}

// SamplesInto copies the latest audio samples into dst, avoiding allocation.
// Returns the number of samples written.
func (p *Player) SamplesInto(dst []float64) int {
	p.mu.Lock()
	tap := p.tap
	p.mu.Unlock()
	if tap == nil {
		return 0
	}
	return tap.SamplesInto(dst)
}

// StereoSamplesInto copies the latest stereo audio frames into dst.
// Returns the number of frames written.
func (p *Player) StereoSamplesInto(dst [][2]float64) int {
	p.mu.Lock()
	tap := p.tap
	p.mu.Unlock()
	if tap == nil {
		return 0
	}
	return tap.StereoSamplesInto(dst)
}

// SampleRate returns the output sample rate in Hz. In bit-perfect mode this
// follows the current track's native rate.
func (p *Player) SampleRate() int {
	return int(p.rate.Load())
}

// outRate returns the rate new pipelines should decode to: the rate the output
// device is currently running at.
func (p *Player) outRate() beep.SampleRate {
	return beep.SampleRate(p.rate.Load())
}

// alignOutput points the output device at tp's native sample rate. When the
// device cannot run at that rate the stream is resampled after all, so
// playback still works — just not bit-perfectly. It is a no-op outside
// bit-perfect mode, where pipelines are already built at the device rate.
func (p *Player) alignOutput(tp *trackPipeline) {
	if !p.bitPerfect || tp == nil || tp.format.SampleRate <= 0 {
		return
	}
	native := int(tp.format.SampleRate)
	actual, err := p.out.SetSampleRate(native)
	switch {
	case err != nil && actual == 0:
		// The device is gone; leave the pipeline alone and let the stream
		// error surface through the normal playback path.
		p.rateNote.Store(fmt.Sprintf("output device unavailable: %v", err))
		return
	case err != nil:
		p.rateNote.Store(fmt.Sprintf("device refused %d Hz: %v", native, err))
	default:
		p.rateNote.Store("")
	}

	p.rate.Store(int64(actual))
	if rt, ok := tp.decoder.(rateRetargetable); ok {
		// A decoder that resamples internally (chained OGG/Icecast radio
		// resamples per chain segment, since a later segment's native rate
		// can differ from an earlier one's — see chainedOggStreamer.chain)
		// must be told the device's real rate directly: wrapping it in
		// another outer resampler here, like the branch below does for an
		// ordinary decoder, would double-resample whatever it's already
		// producing internally.
		rt.SetTargetRate(beep.SampleRate(actual))
		return
	}
	tp.stream = p.wrapResample(tp.format.SampleRate, beep.SampleRate(actual), tp.stream)
}

// rateRetargetable is implemented by a decoder that resamples internally and
// needs to be told when the device's actual rate changes, rather than being
// wrapped in an outer resampler the way an ordinary decoder is — see
// alignOutput. Currently only chainedOggStreamer implements it.
type rateRetargetable interface {
	SetTargetRate(beep.SampleRate)
}

// liveSourceReporter is implemented by a decoder whose own source rate/bit
// depth can change mid-stream (chained OGG re-initializes per logical
// bitstream boundary — see chainedOggStreamer.chain) — BitPerfect() prefers
// its live values over trackPipeline's verifiedSourceRate/verifiedSourceBits,
// which are frozen at pipeline-build time and would otherwise go stale after
// a chain boundary. Currently only chainedOggStreamer implements it.
type liveSourceReporter interface {
	CurrentSourceRate() int
	CurrentSourceBits() int
}

// BitPerfect reports whether audio currently reaches the output device
// unaltered, and what stands in the way when it does not.
func (p *Player) BitPerfect() BitPerfectStatus {
	p.mu.Lock()
	cur := p.current
	p.mu.Unlock()

	// Prefer an independently confirmed hardware rate for reporting (e.g.
	// past a plughw: conversion layer, which can silently resample without
	// this) — never for the app-facing SampleRate() that pipeline decisions
	// use elsewhere, only for what's shown/compared here.
	deviceRate := p.out.SampleRate()
	if real := p.out.RealRate(); real > 0 {
		deviceRate = real
	}
	in := bitPerfectInputs{
		enabled:    p.bitPerfect,
		playing:    p.playing.Load(),
		deviceRate: deviceRate,
		rateExact:  p.out.RateExact(),
		encoding:   p.out.Encoding(),
		volumeDB:   p.Volume(),
		eq:         p.EQBands(),
		mono:       p.mono.Load(),
		speed:      p.Speed(),
	}
	in.note, _ = p.rateNote.Load().(string)
	if outErr := p.out.Err(); outErr != nil {
		in.note = outErr.Error()
	}
	if cur != nil {
		// verifiedSourceRate, not format.SampleRate: the latter is the rate
		// PCM frames actually arrive at from the decoder, which for an
		// unprobed ffmpeg transcode (radio, HLS, yt-dlp, or a local file
		// whose probe failed) is just whatever rate ffmpeg was asked to
		// target — it says nothing about the source's real native rate, so
		// treating it as one would let those cases earn the badge on a
		// coincidental match instead of a verified one. See trackPipeline's
		// doc comment.
		in.sourceRate = cur.verifiedSourceRate
		in.sourceBytes = cur.format.Precision
		in.verifiedSourceBits = cur.verifiedSourceBits
		if live, ok := cur.decoder.(liveSourceReporter); ok {
			// Chained OGG's source rate/bits can change mid-stream (see
			// chain()); trackPipeline's verified* fields are frozen at
			// pipeline-build time and would go stale after a chain
			// boundary otherwise. Locking matches writeLoop's own access to
			// the same streamer — see CurrentSourceRate's doc comment.
			p.out.Lock()
			in.sourceRate = live.CurrentSourceRate()
			in.verifiedSourceBits = live.CurrentSourceBits()
			p.out.Unlock()
		}
	}
	return in.eval()
}

// StreamBytes returns the bytes downloaded and total content length for the
// current HTTP stream. Returns (0, 0) for local files or when no counter exists.
func (p *Player) StreamBytes() (downloaded, total int64) {
	p.mu.Lock()
	cur := p.current
	p.mu.Unlock()
	if cur == nil {
		return 0, 0
	}
	if cur.bytesRead != nil {
		downloaded = cur.bytesRead.Load()
	}
	total = cur.contentLength
	return downloaded, total
}

// RegisterStreamerFactory registers a factory for a custom URI scheme prefix
// (e.g., "spotify:"). When buildPipeline encounters a path starting with this
// prefix, it calls the factory to create the decoder instead of the normal
// file/HTTP pipeline.
func (p *Player) RegisterStreamerFactory(scheme string, f StreamerFactory) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.customFactories == nil {
		p.customFactories = make(map[string]StreamerFactory)
	}
	p.customFactories[scheme] = f
}

// RegisterBufferedURLMatcher registers a function that identifies HTTP URLs
// requiring the buffered download + ffmpeg pipeline (e.g. Subsonic stream
// endpoints). This replaces hardcoded URL pattern checks.
func (p *Player) RegisterBufferedURLMatcher(match func(string) bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.bufferedURLMatch = match
}

// suspendSpeaker suspends the ALSA audio callback goroutine so it blocks
// on a condition variable instead of busy-looping. Safe to call multiple
// times; subsequent calls are no-ops.
func (p *Player) suspendSpeaker() {
	p.suspendMu.Lock()
	defer p.suspendMu.Unlock()

	if p.suspended {
		return
	}
	if err := p.out.Suspend(); err != nil {
		// Non-fatal: the ALSA driver may return an error if the context
		// has already hit a terminal error. Continue without tracking
		// the suspended state so we don't try to resume a dead context.
		return
	}
	p.suspended = true
}

// resumeSpeaker resumes the ALSA audio callback goroutine. Safe to call
// multiple times; subsequent calls are no-ops.
func (p *Player) resumeSpeaker() {
	p.suspendMu.Lock()
	defer p.suspendMu.Unlock()

	if !p.suspended {
		return
	}
	if err := p.out.Resume(); err != nil {
		return
	}
	p.suspended = false
}

// Close fully stops the speaker and cleans up all resources.
func (p *Player) Close() {
	p.Stop()
	p.out.Close()
}
