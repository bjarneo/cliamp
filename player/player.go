package player

import (
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gopxl/beep/v2"
	"github.com/gopxl/beep/v2/speaker"
)

// Quality holds configurable audio output parameters.
type Quality struct {
	SampleRate      int // output sample rate in Hz (e.g. 44100, 48000)
	BufferMs        int // speaker buffer in milliseconds
	ResampleQuality int // beep resample quality factor (1–4)
	BitDepth        int // PCM bit depth for FFmpeg output: 16 or 32 (32 = lossless)
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
	mu              sync.Mutex
	sr              beep.SampleRate
	gapless         *gaplessStreamer
	current         *trackPipeline // active track's resources
	nextPipeline    *trackPipeline // preloaded track's resources
	started         bool           // true after first speaker.Play()
	ctrl            *beep.Ctrl
	volume          atomic.Uint64     // dB stored as Float64bits, range [-30, +6]
	speed           atomic.Uint64     // playback speed ratio as Float64bits; 1.0 = normal
	eqBands         [10]atomic.Uint64 // dB stored as math.Float64bits
	tap             *tap
	playing         atomic.Bool
	paused          atomic.Bool
	mono            atomic.Bool
	resampleQuality int
	bitDepth        int // 16 or 32

	gaplessAdvance atomic.Bool  // set when gapless transition fires
	seekGen        atomic.Int64 // generation counter for yt-dlp seeks; incremented to cancel stale seeks

	streamTitle      atomic.Value               // stores string, set by ICY reader callback
	customFactories  map[string]StreamerFactory // URI scheme prefix -> factory (e.g. "spotify:" -> fn)
	bufferedURLMatch func(string) bool          // optional: returns true for URLs needing navBuffer pipeline
}

// New creates a Player and initializes the speaker with the given quality settings.
func New(q Quality) (*Player, error) {
	if q.SampleRate <= 0 || q.BufferMs <= 0 || q.ResampleQuality <= 0 {
		return nil, fmt.Errorf("invalid quality settings: SampleRate=%d, BufferMs=%d, ResampleQuality=%d",
			q.SampleRate, q.BufferMs, q.ResampleQuality)
	}
	sr := beep.SampleRate(q.SampleRate)
	if err := speaker.Init(sr, sr.N(time.Duration(q.BufferMs)*time.Millisecond)); err != nil {
		return nil, fmt.Errorf("speaker init: %w", err)
	}
	bitDepth := q.BitDepth
	if bitDepth != 32 {
		bitDepth = 16
	}
	p := &Player{sr: sr, resampleQuality: q.ResampleQuality, bitDepth: bitDepth}
	p.speed.Store(math.Float64bits(1.0))
	p.gapless = &gaplessStreamer{}
	p.gapless.onSwap = func() {
		// Called from audio thread (goroutine) when gapless transition occurs.
		// Swap current ← nextPipeline and close the old one.
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
	// Collect old pipelines to close after releasing locks.
	var oldCurrent, oldNext *trackPipeline

	if p.started {
		// Lock the speaker so the goroutine finishes any in-progress Stream()
		// call before we swap the source and unpause. The ctrl.Paused write
		// must happen under the speaker lock because the audio thread reads it
		// on every Stream() call.
		speaker.Lock()
		p.gapless.Replace(tp.stream)
		p.ctrl.Paused = false
		speaker.Unlock()
	}

	p.mu.Lock()

	oldCurrent = p.current
	oldNext = p.nextPipeline
	p.current = tp
	p.nextPipeline = nil

	if !p.started {
		p.gapless.Replace(tp.stream)

		// Build the long-lived pipeline once
		var s beep.Streamer = p.gapless
		s = newSpeedStreamer(s, &p.speed)

		for i := range 10 {
			s = newBiquad(s, eqFreqs[i], 1.4, &p.eqBands[i], float64(p.sr))
		}

		s = &volumeStreamer{s: s, vol: &p.volume, mono: &p.mono, cachedDB: math.NaN()}
		p.tap = newTap(s, 4096)
		p.ctrl = &beep.Ctrl{Streamer: p.tap}
		p.started = true
		p.playing.Store(true)
		p.paused.Store(false)
		p.mu.Unlock()

		speaker.Play(p.ctrl)
		go closePipelines(oldCurrent, oldNext)
		return nil
	}

	p.playing.Store(true)
	p.paused.Store(false)
	p.mu.Unlock()

	// Close old resources asynchronously to avoid blocking the caller
	// (UI thread) on slow Close() operations (ffmpeg wait, HTTP teardown).
	go closePipelines(oldCurrent, oldNext)
	return nil
}

// Preload builds a pipeline for the next track and queues it for gapless transition.
// knownDuration is the metadata duration (use 0 if unknown).
func (p *Player) Preload(path string, knownDuration time.Duration) error {
	tp, err := p.buildPipeline(path)
	if err != nil {
		return err
	}
	tp.setKnownDuration(knownDuration)
	return p.preloadPipeline(tp)
}

// PreloadYTDL builds a yt-dlp pipe pipeline and queues it for gapless transition.
func (p *Player) PreloadYTDL(pageURL string, knownDuration time.Duration) error {
	tp, err := p.buildYTDLPipeline(pageURL, 0)
	if err != nil {
		return err
	}
	tp.knownDuration = knownDuration
	return p.preloadPipeline(tp)
}

// preloadPipeline queues a ready trackPipeline for gapless transition.
func (p *Player) preloadPipeline(tp *trackPipeline) error {
	// Lock speaker to atomically swap the gapless next stream, ensuring no
	// in-flight transition reads from the old pipeline we're about to close.
	speaker.Lock()
	p.gapless.SetNext(tp.stream)
	speaker.Unlock()

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
	speaker.Lock()
	p.gapless.SetNext(nil)
	speaker.Unlock()

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
func (p *Player) TogglePause() {
	speaker.Lock()
	if p.ctrl != nil {
		p.ctrl.Paused = !p.ctrl.Paused
		paused := p.ctrl.Paused
		speaker.Unlock()
		p.paused.Store(paused)
	} else {
		speaker.Unlock()
	}
}

// Stop halts playback and releases resources. The speaker continues running
// (outputting silence via the gapless streamer) so it can be restarted without
// rebuilding the pipeline.
func (p *Player) Stop() {
	// Lock speaker to ensure the goroutine finishes any in-progress Stream()
	// call, then clear the source and pause. After unlock, the speaker will
	// only see silence from the gapless streamer (paused ctrl).
	speaker.Lock()
	p.gapless.Clear()
	if p.ctrl != nil {
		p.ctrl.Paused = true
	}
	speaker.Unlock()

	// Now safe to close decoder resources — speaker can't be reading them.
	p.mu.Lock()
	oldCurrent := p.current
	oldNext := p.nextPipeline
	p.current = nil
	p.nextPipeline = nil
	p.playing.Store(false)
	p.paused.Store(false)
	p.mu.Unlock()

	closePipelines(oldCurrent, oldNext)
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
	speaker.Lock()
	defer speaker.Unlock()
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
		speaker.Unlock()

		// Build a new pipeline starting at the computed byte offset.
		// Speaker lock is NOT held — HTTP I/O can take seconds on slow networks.
		tp, err := p.buildPipelineAt(path, byteOffset, newPos)

		speaker.Lock() // re-acquire for defer
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
		speaker.Unlock()
		err := p.SeekYTDL(d)
		speaker.Lock() // re-acquire so defer Unlock works
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
	speaker.Lock()
	curPos := cur.format.SampleRate.D(cur.decoder.Position()) + cur.streamOffset
	p.gapless.Replace(nil)
	speaker.Unlock()

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
	speaker.Lock()
	p.gapless.Replace(tp.stream)
	p.gapless.SetNext(nil)
	speaker.Unlock()

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
	speaker.Lock()
	defer speaker.Unlock()
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
	speaker.Lock()
	defer speaker.Unlock()
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
	speaker.Lock()
	defer speaker.Unlock()
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

// SetVolume sets the volume in dB, clamped to [-30, +6].
func (p *Player) SetVolume(db float64) {
	p.volume.Store(math.Float64bits(max(min(db, 6), -30)))
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

// SampleRate returns the output sample rate in Hz.
func (p *Player) SampleRate() int {
	return int(p.sr)
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

// Close fully stops the speaker and cleans up all resources.
func (p *Player) Close() {
	p.Stop()
	speaker.Clear()
}
