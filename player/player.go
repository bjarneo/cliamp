package player

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/gopxl/beep/v2"
	"github.com/gopxl/beep/v2/speaker"
)

// DefaultSampleRate is the CD-quality sample rate used for audio output.
const DefaultSampleRate = 44100

// Player is the audio engine managing the playback pipeline:
//
//	[Gapless] -> [10x Biquad EQ] -> [Volume] -> [Tap] -> [Ctrl] -> speaker
//	     ↑
//	     ├─ current: [Decode A] → [Resample A]
//	     └─ next:    [Decode B] → [Resample B]  (preloaded)
type Player struct {
	mu           sync.Mutex
	sr           beep.SampleRate
	gapless      *gaplessStreamer
	current      *trackPipeline // active track's resources
	nextPipeline *trackPipeline // preloaded track's resources
	started      bool           // true after first speaker.Play()
	ctrl         *beep.Ctrl
	volume       float64 // dB, range [-30, +6]
	eqBands      [10]float64
	tap          *Tap
	playing      bool
	paused       bool
	mono         bool

	gaplessAdvance atomic.Bool // set when gapless transition fires

	streamTitle atomic.Value // stores string, set by ICY reader callback
}

// New creates a Player and initializes the speaker at the given sample rate.
func New(sr beep.SampleRate) *Player {
	speaker.Init(sr, sr.N(time.Second/10))
	p := &Player{sr: sr}
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
	return p
}

// Play opens and starts playing an audio file. On the first call it builds
// the long-lived EQ → volume → tap → ctrl chain and starts the speaker.
// Subsequent calls swap only the track source via the gapless streamer.
func (p *Player) Play(path string) error {
	tp, err := p.buildPipeline(path)
	if err != nil {
		return err
	}

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

		for i := range 10 {
			s = newBiquad(s, EQFreqs[i], 1.4, &p.eqBands[i], float64(p.sr))
		}
		s = &volumeStreamer{s: s, vol: &p.volume, mono: &p.mono, mu: &p.mu}
		p.tap = NewTap(s, 4096)
		p.ctrl = &beep.Ctrl{Streamer: p.tap}
		p.started = true
		p.playing = true
		p.paused = false
		p.mu.Unlock()

		speaker.Play(p.ctrl)
		closePipelines(oldCurrent, oldNext)
		return nil
	}

	p.playing = true
	p.paused = false
	p.mu.Unlock()

	// Close old resources after all locks are released
	closePipelines(oldCurrent, oldNext)
	return nil
}

// Preload builds a pipeline for the next track and queues it for gapless transition.
func (p *Player) Preload(path string) error {
	tp, err := p.buildPipeline(path)
	if err != nil {
		return err
	}

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
	defer speaker.Unlock()
	if p.ctrl != nil {
		p.ctrl.Paused = !p.ctrl.Paused
		p.paused = p.ctrl.Paused
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
	p.playing = false
	p.paused = false
	p.mu.Unlock()

	closePipelines(oldCurrent, oldNext)
}

// Seek moves the playback position by the given duration (positive or negative).
// Returns nil immediately for non-seekable streams (e.g., HTTP without ffmpeg).
// The speaker lock is acquired first (outer), then p.mu briefly to snapshot
// the current pipeline, ensuring consistent lock ordering with the audio thread.
// Clears the preloaded next pipeline to prevent a stale gapless transition.
func (p *Player) Seek(d time.Duration) error {
	speaker.Lock()
	defer speaker.Unlock()
	p.mu.Lock()
	cur := p.current
	p.mu.Unlock()
	if cur == nil || !cur.seekable {
		return nil
	}
	curSample := cur.decoder.Position()
	curDur := cur.format.SampleRate.D(curSample)
	newSample := cur.format.SampleRate.N(curDur + d)
	if newSample < 0 {
		newSample = 0
	}
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

// Position returns the current playback position.
func (p *Player) Position() time.Duration {
	speaker.Lock()
	defer speaker.Unlock()
	p.mu.Lock()
	cur := p.current
	p.mu.Unlock()
	if cur == nil {
		return 0
	}
	return cur.format.SampleRate.D(cur.decoder.Position())
}

// Duration returns the total duration of the current track.
func (p *Player) Duration() time.Duration {
	speaker.Lock()
	defer speaker.Unlock()
	p.mu.Lock()
	cur := p.current
	p.mu.Unlock()
	if cur == nil {
		return 0
	}
	return cur.format.SampleRate.D(cur.decoder.Len())
}

// SetVolume sets the volume in dB, clamped to [-30, +6].
func (p *Player) SetVolume(db float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.volume = max(min(db, 6), -30)
}

// Volume returns the current volume in dB.
func (p *Player) Volume() float64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.volume
}

// ToggleMono switches between stereo and mono (L+R downmix) output.
func (p *Player) ToggleMono() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.mono = !p.mono
}

// Mono returns true if mono output is enabled.
func (p *Player) Mono() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.mono
}

// SetEQBand sets a single EQ band's gain in dB, clamped to [-12, +12].
func (p *Player) SetEQBand(band int, dB float64) {
	if band < 0 || band >= 10 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.eqBands[band] = max(min(dB, 12), -12)
}

// EQBands returns a copy of all 10 EQ band gains.
func (p *Player) EQBands() [10]float64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.eqBands
}

// IsPlaying returns true if a track is loaded and playing (possibly paused).
func (p *Player) IsPlaying() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.playing
}

// IsPaused returns true if playback is paused.
func (p *Player) IsPaused() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.paused
}

// Drained returns true if the current track ended with no preloaded next track.
func (p *Player) Drained() bool {
	return p.gapless.Drained()
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
func (p *Player) Seekable() bool {
	p.mu.Lock()
	cur := p.current
	p.mu.Unlock()
	return cur != nil && cur.seekable
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

// Samples returns the latest audio samples from the tap for FFT analysis.
func (p *Player) Samples() []float64 {
	p.mu.Lock()
	tap := p.tap
	p.mu.Unlock()
	if tap == nil {
		return nil
	}
	return tap.Samples(2048)
}

// Close fully stops the speaker and cleans up all resources.
func (p *Player) Close() {
	p.Stop()
	speaker.Clear()
}
