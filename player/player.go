package player

import (
	"context"
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
//	[Gapless] -> [Speed] -> [10x Biquad EQ] -> [Tap] -> [Volume + Mono] -> [Ctrl] -> speaker
//	     ↑
//	     ├─ current: [Decode A] → [Resample A]
//	     └─ next:    [Decode B] → [Resample B]  (preloaded)
//
// The tap sits before volume, so the visualizer sees the level before the
// volume gain and the mono downmix.
//
// Lock order: lifecycleMu, then the speaker lock, then mu, then
// gaplessStreamer.mu. A path can skip a lock, but it must not take them in a
// different order. The audio goroutine holds the speaker lock while it
// streams, and the gapless swap callback takes mu on that goroutine. So a
// path that holds mu must not call speaker.Lock. suspendMu is taken with no
// lock held or with only lifecycleMu held. navBuffer.mu is a leaf lock.
// Close pipelines and wait for processes only after these locks are released.
type Player struct {
	replayGain      replayGainState
	mu              sync.Mutex
	lifecycleMu     sync.Mutex // serializes source commits without covering setup or process waits
	sr              beep.SampleRate
	gapless         *gaplessStreamer
	current         *trackPipeline // active track's resources
	nextPipeline    *trackPipeline // preloaded track's resources
	started         bool           // true after first speaker.Play()
	suspendMu       sync.Mutex     // guards suspended and speaker suspend/resume calls
	suspended       bool           // true when speaker.Suspend() has been called
	ctrl            *beep.Ctrl
	volMin          atomic.Uint64     // dB floor stored as Float64bits, range [-90, 0]
	volume          atomic.Uint64     // dB stored as Float64bits, range [volMin, +6]
	speed           atomic.Uint64     // playback speed ratio as Float64bits; 1.0 = normal
	eqBands         [10]atomic.Uint64 // dB stored as math.Float64bits
	tap             *tap
	playing         atomic.Bool
	paused          atomic.Bool
	mono            atomic.Bool
	resampleQuality int
	bitDepth        int // 16 or 32
	tapBufferFrames int
	// speakerBufferFrames is the audio backend's buffer, in frames: the latency
	// between a frame reaching the tap and the same frame being heard.
	speakerBufferFrames int

	gaplessAdvance atomic.Bool   // set when gapless transition fires
	seekGen        atomic.Int64  // generation counter for yt-dlp seeks; incremented to cancel stale seeks
	playGen        atomic.Uint64 // current UI playback request; rejects stale asynchronous starts
	preloadGen     atomic.Uint64 // current preload request; rejects stale background preloads

	lastPlayedDuration time.Duration // real duration of the track finished by the last gapless swap

	streamTitle atomic.Value // stores string, set by ICY reader callback

	// The source registries are guarded by mu. Register writes them, and
	// buildPipeline reads them when a track starts.
	customFactories  map[string]StreamerFactory // URI scheme prefix -> factory (e.g. "spotify:" -> fn)
	bufferedURLMatch func(string) bool          // optional: returns true for URLs needing navBuffer pipeline
	sourceResolvers  map[string]SourceResolver  // URI scheme prefix -> play-time source resolver (e.g. "tidal://")
	ytdlURLMatch     func(string) bool          // optional: returns true for page URLs that play through yt-dlp

	streamMetaResolver StreamMetadataResolver // optional: API-based now-playing for streams without ICY
	metaCancel         context.CancelFunc     // cancels the active metadata poller; guarded by mu
}

// StreamMetadataResolver matches a stream URL to a now-playing fetcher for
// broadcasters that carry no inline ICY metadata (e.g. NTS, FIP) and instead
// publish the current track via a separate JSON API. It returns a fetch
// function, the poll interval, and ok=false when the URL is not recognized.
type StreamMetadataResolver func(streamURL string) (fetch func(ctx context.Context) (string, error), interval time.Duration, ok bool)

// maxAnalysisWindow is the largest window the visualizer asks for in one read
// (the classic peak meter's 4096-sample FFT). The package cannot import ui, so
// the value is duplicated here; it only needs to be an upper bound.
const maxAnalysisWindow = 4096

// tapRingFrames sizes the tap's ring buffer. The waveform read position sits
// speakerFrames behind the newest frame — that is where audio is actually
// audible — so the ring has to hold that lag *plus* a full analysis window on
// top of it. Sized to the backend buffer alone, the oldest end of the window
// would read frames that have already been overwritten.
func tapRingFrames(speakerFrames int) int {
	return max(4096, speakerFrames+maxAnalysisWindow)
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
	p := &Player{
		sr:                  sr,
		resampleQuality:     q.ResampleQuality,
		bitDepth:            bitDepth,
		tapBufferFrames:     tapRingFrames(sr.N(time.Duration(q.BufferMs) * time.Millisecond)),
		speakerBufferFrames: sr.N(time.Duration(q.BufferMs) * time.Millisecond),
	}
	p.volMin.Store(math.Float64bits(-50))
	p.speed.Store(math.Float64bits(1.0))
	p.gapless = &gaplessStreamer{}
	// Suspend the speaker immediately; the ALSA audio callback goroutine
	// burns ~2% CPU even on silence. Resume is called on every track start.
	//
	// Suspend is also where a failed device open first becomes visible: oto
	// opens the device on a background goroutine and only stores the error,
	// so speaker.Init above returns nil even when every candidate device
	// failed. Without this check playback silently no-ops while the UI and
	// `cliamp status` keep reporting "playing".
	if err := speaker.Suspend(); err != nil {
		return nil, fmt.Errorf("audio output unavailable: %w%s", err, audioOutputHint())
	}
	p.suspended = true
	p.gapless.onSwap = func(token uint64) {
		// Called from audio thread (goroutine) when gapless transition occurs.
		// Swap current ← nextPipeline and close the old one. The API metadata
		// poller is intentionally not restarted here: gapless advance is for
		// finite tracks, while resolver-backed streams (NTS, FIP) are infinite
		// live radio that is never preloaded as a gapless next track.
		p.handleGaplessSwap(token)
	}
	return p, nil
}

func (p *Player) handleGaplessSwap(token uint64) {
	p.mu.Lock()
	next := p.nextPipeline
	if next == nil || next.gaplessToken != token {
		p.mu.Unlock()
		return
	}
	old := p.current
	p.current = next
	p.nextPipeline = nil
	if old != nil {
		// Stash the finished track's real duration before closing its
		// pipeline: once the swap lands, p.current reports the next track.
		if n := old.decoder.Len(); n > 0 && old.format.SampleRate != 0 {
			p.lastPlayedDuration = old.format.SampleRate.D(n)
		} else {
			p.lastPlayedDuration = old.knownDuration
		}
	}
	p.mu.Unlock()
	if old != nil {
		go old.close()
	}
	p.gaplessAdvance.Store(true)
}

// LastPlayedDuration returns the real duration of the track finished by the
// most recent gapless transition. Meaningful right after GaplessAdvanced
// reports true; callers use it when playlist metadata lacks a duration.
func (p *Player) LastPlayedDuration() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastPlayedDuration
}

// PlayAt opens path and starts playing it at offset. On the first call it
// builds the long-lived speed → EQ → tap → volume → ctrl chain and starts the
// speaker. Subsequent calls swap only the track source via the gapless
// streamer. knownDuration is the metadata duration (use 0 if unknown); it is
// used as a fallback when the decoder cannot determine the length (e.g. HTTP
// streams). A seekable decoder is positioned before the pipeline reaches the
// speaker, so no audio plays from 0:00. A yt-dlp page starts at 0, and the
// caller seeks it by restart. buildSource picks the pipeline.
func (p *Player) PlayAt(path string, knownDuration, offset time.Duration) error {
	return p.playAt(path, knownDuration, offset, 0)
}

// SetPlaybackGeneration invalidates asynchronous playback starts from older
// UI requests. It waits for an in-progress source commit to finish so a new
// generation cannot race its final ownership check.
func (p *Player) SetPlaybackGeneration(generation uint64) {
	p.lifecycleMu.Lock()
	p.playGen.Store(generation)
	p.lifecycleMu.Unlock()
}

// PlayAtForGeneration is PlayAt that starts path only when generation is
// still current.
func (p *Player) PlayAtForGeneration(path string, knownDuration, offset time.Duration, generation uint64) error {
	return p.playAt(path, knownDuration, offset, generation)
}

// playAt builds the pipeline for path and plays it. A generation of 0 plays
// it without the check.
func (p *Player) playAt(path string, knownDuration, offset time.Duration, generation uint64) error {
	tp, err := p.buildSource(path, knownDuration, offset, true)
	if err != nil {
		return err
	}
	return p.playPipelineForGeneration(tp, generation)
}

// playPipelineForGeneration wires a ready-to-play trackPipeline into the
// speaker chain, unless generation is not 0 and no longer current. On the
// first call it builds the long-lived speed → EQ → tap → volume → ctrl chain.
// Subsequent calls swap only the track source via the gapless streamer.
func (p *Player) playPipelineForGeneration(tp *trackPipeline, generation uint64) error {
	p.lifecycleMu.Lock()
	if generation != 0 && p.playGen.Load() != generation {
		p.lifecycleMu.Unlock()
		go tp.close()
		return nil
	}
	p.resumeSpeaker()

	// Collect old pipelines to close after releasing locks.
	var oldCurrent, oldNext *trackPipeline

	p.mu.Lock()
	started := p.started
	active := p.current
	p.mu.Unlock()

	if started {
		// A nav/pipe decoder may be blocked in Stream while the speaker mutex is
		// held. Interrupt it first so source replacement can acquire the mutex.
		if active != nil {
			p.gapless.SetNext(nil)
			active.interrupt()
		}
		// Lock the speaker so the goroutine finishes any in-progress Stream()
		// call before we swap the source and unpause. The ctrl.Paused write
		// must happen under the speaker lock because the audio thread reads it
		// on every Stream() call.
		speaker.Lock()
		p.gapless.Replace(tp.stream)
		p.gaplessAdvance.Store(false)
		p.ctrl.Paused = false
		p.mu.Lock()
		oldCurrent = p.current
		oldNext = p.nextPipeline
		p.current = tp
		p.nextPipeline = nil
		p.playing.Store(true)
		p.paused.Store(false)
		p.mu.Unlock()
		speaker.Unlock()
	} else {
		// TogglePause and Stop read p.ctrl under the speaker lock, so the
		// first start writes it under that lock too.
		speaker.Lock()
		p.mu.Lock()
		p.gapless.Replace(tp.stream)
		p.gaplessAdvance.Store(false)

		// Build the long-lived pipeline once
		var s beep.Streamer = p.gapless
		s = newSpeedStreamer(s, &p.speed)

		for i := range 10 {
			s = newBiquad(s, eqFreqs[i], 1.4, &p.eqBands[i], float64(p.sr))
		}

		p.tap = newTap(s, p.tapBufferFrames, int(p.sr), p.speakerBufferFrames)
		s = &volumeStreamer{s: p.tap, vol: &p.volume, mono: &p.mono, cachedDB: math.NaN()}
		ctrl := &beep.Ctrl{Streamer: s}
		p.ctrl = ctrl
		p.started = true
		p.current = tp
		p.nextPipeline = nil
		p.playing.Store(true)
		p.paused.Store(false)
		p.mu.Unlock()
		speaker.Unlock()
		// speaker.Play takes the speaker lock itself.
		speaker.Play(ctrl)
	}
	p.lifecycleMu.Unlock()
	// Start API-based now-playing polling for streams without ICY metadata
	// (no-op otherwise). Done here, not in buildPipeline, so preloaded
	// pipelines that may never play don't spawn pollers.
	p.startStreamMetadata(tp.path)
	// Close old resources asynchronously to avoid blocking the caller
	// (UI thread) on slow Close() operations (ffmpeg wait, HTTP teardown).
	go closePipelines(oldCurrent, oldNext)
	return nil
}

// BeginPreload invalidates older preload work and returns the current token.
func (p *Player) BeginPreload() uint64 {
	return p.preloadGen.Add(1)
}

// PreloadForGeneration builds the pipeline for the next track and queues it
// for gapless transition, only when generation is still current.
// knownDuration is the metadata duration (use 0 if unknown). buildSource
// picks the pipeline.
func (p *Player) PreloadForGeneration(path string, knownDuration time.Duration, generation uint64) error {
	tp, err := p.buildSource(path, knownDuration, 0, false)
	if err != nil {
		return err
	}
	return p.preloadPipelineForGeneration(tp, generation)
}

// preloadPipelineForGeneration queues a ready trackPipeline for gapless
// transition, unless generation is not 0 and no longer current.
func (p *Player) preloadPipelineForGeneration(tp *trackPipeline, generation uint64) error {
	// Lock speaker to atomically swap the gapless next stream, ensuring no
	// in-flight transition reads from the old pipeline we're about to close.
	speaker.Lock()
	p.mu.Lock()
	if generation != 0 && p.preloadGen.Load() != generation {
		p.mu.Unlock()
		speaker.Unlock()
		go tp.close()
		return nil
	}
	old := p.nextPipeline
	p.nextPipeline = tp
	// Keep Player's pipeline state and gapless' token registration atomic with
	// respect to the asynchronous transition callback.
	tp.gaplessToken = p.gapless.SetNext(tp.stream)
	p.mu.Unlock()
	speaker.Unlock()

	if old != nil {
		old.close()
	}
	return nil
}

// ClearPreload discards the preloaded next track (e.g., when shuffle/repeat changes).
// Speaker is locked to ensure no in-flight gapless transition can reference the
// pipeline we're about to close. The old pipeline closes asynchronously, as in
// playPipelineForGeneration, because ClearPreload runs on the UI goroutine and
// a close can wait for an ffmpeg or yt-dlp process to exit.
func (p *Player) ClearPreload() {
	p.preloadGen.Add(1)
	speaker.Lock()
	p.gapless.SetNext(nil)
	speaker.Unlock()

	p.mu.Lock()
	old := p.nextPipeline
	p.nextPipeline = nil
	p.mu.Unlock()

	if old != nil {
		go old.close()
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
	speaker.Lock()
	if p.ctrl != nil {
		p.ctrl.Paused = !p.ctrl.Paused
		paused := p.ctrl.Paused
		speaker.Unlock()
		p.paused.Store(paused)
		if paused {
			p.suspendSpeaker()
		} else {
			p.resumeSpeaker()
		}
	} else {
		speaker.Unlock()
	}
}

// Stop halts playback and releases resources. The speaker is suspended so
// the ALSA audio callback goroutine blocks (zero CPU) instead of streaming
// silence. Resume is called automatically on the next track start.
func (p *Player) Stop() {
	// Reject a preload still loading in the background, as ClearPreload does,
	// so it cannot arm a next track on the stopped player.
	p.preloadGen.Add(1)
	p.lifecycleMu.Lock()
	p.mu.Lock()
	active := p.current
	p.mu.Unlock()
	if active != nil {
		p.gapless.SetNext(nil)
		active.interrupt()
	}

	// Lock speaker to ensure the goroutine finishes any in-progress Stream()
	// call, then clear the source and pause. After unlock, the speaker will
	// only see silence from the gapless streamer (paused ctrl).
	speaker.Lock()
	p.gapless.Clear()
	p.gaplessAdvance.Store(false)
	if p.ctrl != nil {
		p.ctrl.Paused = true
	}
	p.mu.Lock()
	oldCurrent := p.current
	oldNext := p.nextPipeline
	p.current = nil
	p.nextPipeline = nil
	p.playing.Store(false)
	p.paused.Store(false)
	p.mu.Unlock()
	speaker.Unlock()
	p.lifecycleMu.Unlock()

	// Now safe to close decoder resources: speaker cannot be reading them.

	p.stopStreamMetadata()
	closePipelines(oldCurrent, oldNext)

	p.suspendSpeaker()
}

// Seek moves the playback position by the given duration (positive or negative).
// For seekable local files, the decoder's Seek method is used directly.
// Returns nil immediately for non-seekable streams (e.g., Icecast radio).
// FFmpeg replacements are prepared before the speaker lock is acquired. The
// lock only covers decoder commit and preload invalidation.
// Clears the preloaded next pipeline to prevent a stale gapless transition.
func (p *Player) Seek(d time.Duration) error {
	p.mu.Lock()
	cur := p.current
	p.mu.Unlock()
	if cur == nil {
		return nil
	}

	// yt-dlp seek-by-restart: handled outside the speaker lock via SeekYTDL.
	if cur.ytdlSeek {
		return p.SeekYTDL(d)
	}

	if seeker, ok := cur.decoder.(preparedFFmpegSeeker); ok {
		newSample := relativeSeekSample(cur, d)
		prepared, err := seeker.prepareSeek(newSample)
		if err != nil {
			return err
		}
		return p.commitPreparedSeek(cur, seeker, prepared)
	}

	// Other local decoders use their native Seek implementation.
	if !cur.seekable {
		return nil
	}

	p.lifecycleMu.Lock()
	speaker.Lock()
	p.mu.Lock()
	if p.current != cur {
		p.mu.Unlock()
		speaker.Unlock()
		p.lifecycleMu.Unlock()
		return nil
	}
	p.mu.Unlock()
	newSample := relativeSeekSample(cur, d)
	if err := cur.decoder.Seek(newSample); err != nil {
		speaker.Unlock()
		p.lifecycleMu.Unlock()
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
	speaker.Unlock()
	p.lifecycleMu.Unlock()
	if old != nil {
		// Seek runs on the UI goroutine, and a close can wait for an ffmpeg
		// or yt-dlp process to exit, as in ClearPreload.
		go old.close()
	}
	return nil
}

type preparedFFmpegSeeker interface {
	prepareSeek(int) (*preparedFFmpegSeek, error)
	seekMatches(*preparedFFmpegSeek) bool
	commitPreparedSeek(*preparedFFmpegSeek) (ffmpegPipe, bool)
	interrupt()
}

func relativeSeekSample(cur *trackPipeline, d time.Duration) int {
	curDur := cur.format.SampleRate.D(cur.decoder.Position())
	newSample := max(cur.format.SampleRate.N(curDur+d), 0)
	if length := cur.decoder.Len(); length > 0 && newSample >= length {
		return length - 1
	}
	return newSample
}

func (p *Player) commitPreparedSeek(cur *trackPipeline, seeker preparedFFmpegSeeker, prepared *preparedFFmpegSeek) error {
	p.lifecycleMu.Lock()
	p.mu.Lock()
	current := p.current == cur
	p.mu.Unlock()
	if !current || !seeker.seekMatches(prepared) {
		p.lifecycleMu.Unlock()
		_ = prepared.close()
		return nil
	}

	// Prevent interruption-driven EOF from advancing to a stale preload while
	// the speaker finishes its active Stream call.
	p.gapless.SetNext(nil)
	seeker.interrupt()

	speaker.Lock()
	p.mu.Lock()
	current = p.current == cur && seeker.seekMatches(prepared)
	if !current {
		p.mu.Unlock()
		speaker.Unlock()
		p.lifecycleMu.Unlock()
		_ = prepared.close()
		return nil
	}

	oldPipe, _ := seeker.commitPreparedSeek(prepared)
	oldNext := p.nextPipeline
	p.nextPipeline = nil
	p.mu.Unlock()
	p.gapless.Replace(cur.stream)
	p.gaplessAdvance.Store(false)
	speaker.Unlock()
	p.lifecycleMu.Unlock()

	_ = oldPipe.stop()
	// A local track seeks on the UI goroutine, and the preload close can wait
	// for an ffmpeg or yt-dlp process to exit, as in ClearPreload.
	go closePipelines(oldNext)
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
	curPos, ok := p.muteYTDLSeekSource(cur, gen)
	if !ok {
		// Another track or a newer seek replaced cur after the snapshot.
		return nil
	}

	newPos := max(curPos+d, 0)
	if cur.knownDuration > 0 && newPos >= cur.knownDuration {
		newPos = cur.knownDuration - time.Second
	}
	startSec := int(newPos.Seconds())

	// Build pipeline WITHOUT speaker lock (this is the slow part — spawns yt-dlp).
	tp, err := p.buildYTDLPipeline(cur.path, startSec)
	if err != nil {
		p.restoreYTDLSeekSource(cur, gen)
		return fmt.Errorf("yt-dlp seek: %w", err)
	}
	tp.knownDuration = cur.knownDuration
	tp.ytdlSeek = true

	if !p.commitYTDLSeek(cur, tp, gen) {
		// The seek was cancelled or another track started while the new pipeline
		// was being built.
		go closePipelines(tp)
	}
	return nil
}

// muteYTDLSeekSource reads the position of cur and silences it while SeekYTDL
// builds the replacement. It returns false and leaves the source alone when
// another track or a newer seek replaced cur after the snapshot. It checks
// under the same locks that commitYTDLSeek uses.
func (p *Player) muteYTDLSeekSource(cur *trackPipeline, gen int64) (time.Duration, bool) {
	speaker.Lock()
	defer speaker.Unlock()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.current != cur || p.seekGen.Load() != gen {
		return 0, false
	}
	p.gapless.Replace(nil)
	p.gaplessAdvance.Store(false)
	// The decoder of a prefetched pipeline reads ahead of the speaker. Seek
	// from the audio that played, as the Position that the caller read does.
	pos, _ := cur.positionAndDuration()
	return pos, true
}

// commitYTDLSeek swaps in a rebuilt seek pipeline only when it still belongs
// to the active track. It must validate under the same locks used for the swap
// because playback can change while yt-dlp is starting.
func (p *Player) commitYTDLSeek(cur, replacement *trackPipeline, gen int64) bool {
	speaker.Lock()
	p.mu.Lock()
	if p.seekGen.Load() != gen || p.current != cur {
		p.mu.Unlock()
		speaker.Unlock()
		return false
	}
	p.gapless.Replace(replacement.stream)
	p.gapless.SetNext(nil)
	p.gaplessAdvance.Store(false)
	old := p.current
	oldNext := p.nextPipeline
	p.current = replacement
	p.nextPipeline = nil
	p.mu.Unlock()
	speaker.Unlock()
	// Clean up old pipelines async to avoid blocking on process wait.
	go closePipelines(old, oldNext)
	return true
}

// restoreYTDLSeekSource puts the original stream back after a replacement
// pipeline fails to start. The generation and current-pipeline checks prevent
// an obsolete seek from overwriting a newer seek or track change.
func (p *Player) restoreYTDLSeekSource(cur *trackPipeline, gen int64) {
	if cur == nil || p.seekGen.Load() != gen {
		return
	}
	speaker.Lock()
	p.mu.Lock()
	stillCurrent := p.current == cur && p.seekGen.Load() == gen
	if stillCurrent {
		p.gapless.Replace(cur.stream)
		p.gaplessAdvance.Store(false)
	}
	p.mu.Unlock()
	speaker.Unlock()
}

// IsYTDLSeek reports whether the current track uses yt-dlp seek-by-restart.
func (p *Player) IsYTDLSeek() bool {
	p.mu.Lock()
	cur := p.current
	p.mu.Unlock()
	return cur != nil && cur.ytdlSeek
}

// Position returns the current playback position.
// streamOffset is added for yt-dlp streams restarted at a time offset.
func (p *Player) Position() time.Duration {
	speaker.Lock()
	defer speaker.Unlock()
	pos, _ := p.positionAndDurationLocked()
	return pos
}

// Duration returns the total duration of the current track.
// For seekable local files it is derived from the decoder's sample count.
// For HTTP streams where the decoder reports Len()==0, the metadata hint
// stored at pipeline build time (knownDuration) is returned instead.
func (p *Player) Duration() time.Duration {
	speaker.Lock()
	defer speaker.Unlock()
	_, dur := p.positionAndDurationLocked()
	return dur
}

// PositionAndDuration returns both position and duration under a single
// speaker lock, avoiding two separate lock acquisitions per tick.
func (p *Player) PositionAndDuration() (time.Duration, time.Duration) {
	speaker.Lock()
	defer speaker.Unlock()
	return p.positionAndDurationLocked()
}

// positionAndDurationLocked is the rule of Position, Duration and
// PositionAndDuration: the clock of the current pipeline. The caller holds
// the speaker lock.
func (p *Player) positionAndDurationLocked() (time.Duration, time.Duration) {
	p.mu.Lock()
	cur := p.current
	p.mu.Unlock()
	if cur == nil {
		return 0, 0
	}
	return cur.positionAndDuration()
}

// SetVolumeMin sets the minimum volume floor in dB, clamped to [-90, 0].
// If the current volume is below the new floor it is immediately raised to match.
// A NaN floor is ignored.
func (p *Player) SetVolumeMin(db float64) {
	if math.IsNaN(db) {
		return
	}
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
// A NaN volume is ignored.
func (p *Player) SetVolume(db float64) {
	if math.IsNaN(db) {
		return
	}
	p.volume.Store(math.Float64bits(max(min(db, 6), p.VolumeMin())))
}

// Volume returns the current volume in dB.
func (p *Player) Volume() float64 {
	return math.Float64frombits(p.volume.Load())
}

// SetSpeed sets the playback speed ratio, clamped to [0.25, 2.0].
// 1.0 is normal speed, 2.0 is double speed, etc. A NaN ratio is ignored.
func (p *Player) SetSpeed(ratio float64) {
	if math.IsNaN(ratio) {
		return
	}
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
// A NaN gain is ignored.
func (p *Player) SetEQBand(band int, dB float64) {
	if band < 0 || band >= 10 || math.IsNaN(dB) {
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

// IsLiveStream reports whether ICY headers identify the current response as
// live radio. A known duration takes precedence over transport metadata.
func (p *Player) IsLiveStream() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.current != nil && p.current.live && p.current.knownDuration <= 0 && p.current.decodedDuration <= 0
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
// Returns true for local files, buffered FFmpeg tracks, and yt-dlp streams
// with a known duration.
func (p *Player) Seekable() bool {
	p.mu.Lock()
	cur := p.current
	p.mu.Unlock()
	if cur == nil {
		return false
	}
	return cur.seekable || (cur.ytdlSeek && cur.knownDuration > 0)
}

// StreamErr returns the current streamer error, if any (e.g., connection drops).
func (p *Player) StreamErr() error {
	p.mu.Lock()
	cur := p.current
	p.mu.Unlock()
	if cur == nil {
		return nil
	}
	if cur.livePrefetch != nil {
		return cur.livePrefetch.Err()
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

// WaveformSamplesInto copies audio samples at the current position within the
// output buffer for smooth raw visualizer rendering.
func (p *Player) WaveformSamplesInto(dst []float64) int {
	p.mu.Lock()
	tap := p.tap
	p.mu.Unlock()
	if tap == nil {
		return 0
	}
	return tap.WaveformSamplesInto(dst)
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

// RegisterYTDLMatcher registers a function that identifies page URLs, such
// as YouTube or SoundCloud pages, that play through the yt-dlp | ffmpeg
// pipe chain.
func (p *Player) RegisterYTDLMatcher(match func(string) bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ytdlURLMatch = match
}

// ResolvedSource is a playable source produced by a SourceResolver at play
// time: either a direct HTTP URL, or an ordered list of media segment URLs
// whose concatenated bytes form one progressive stream (e.g. unencrypted
// DASH fMP4 segments). Buffered sends URL to the buffered download and
// ffmpeg pipeline, for a finite file at a signed URL that no buffered-URL
// matcher can recognize.
type ResolvedSource struct {
	URL      string
	Segments []string
	Buffered bool
}

// SourceResolver turns a custom URI (e.g. "tidal://track/123") into a
// ResolvedSource when playback starts. Resolving at play time keeps
// short-lived signed URLs fresh no matter how long a track sat in the queue.
type SourceResolver func(uri string) (ResolvedSource, error)

// RegisterSourceResolver registers a resolver for a custom URI scheme prefix.
// Unlike RegisterStreamerFactory, the provider only supplies bytes to fetch;
// decoding stays in the player's buffered ffmpeg pipeline.
func (p *Player) RegisterSourceResolver(scheme string, r SourceResolver) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.sourceResolvers == nil {
		p.sourceResolvers = make(map[string]SourceResolver)
	}
	p.sourceResolvers[scheme] = r
}

// HasSourceResolver reports whether a registered SourceResolver claims path.
// Such a path opens over the network when playback starts.
func (p *Player) HasSourceResolver(path string) bool {
	return p.matchSourceResolver(path) != nil
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
	if err := speaker.Suspend(); err != nil {
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
	if err := speaker.Resume(); err != nil {
		return
	}
	p.suspended = false
}

// Close fully stops the speaker and cleans up all resources.
func (p *Player) Close() {
	p.Stop()
	speaker.Clear()
}
