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
type StreamerFactory func(context.Context, string) (beep.StreamSeekCloser, beep.Format, time.Duration, error)

// Player is the audio engine managing the playback pipeline:
//
//	[Gapless] -> [10x Biquad EQ] -> [Volume] -> [Tap] -> [Ctrl] -> speaker
//	     ↑
//	     ├─ current: [Decode A] → [Resample A]
//	     └─ next:    [Decode B] → [Resample B]  (preloaded)
type Player struct {
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

	seekGen        atomic.Int64
	seekCancel     context.CancelFunc // guarded by lifecycleMu
	sequence       uint64
	pendingStart   *pendingSource
	pendingPreload *pendingSource
	closed         bool
	work           sync.WaitGroup // builders and detached resource cleanup; registered before authority is released
	advances       []Advance      // guarded by mu

	customFactories  map[string]StreamerFactory // URI scheme prefix -> factory (e.g. "spotify:" -> fn)
	bufferedURLMatch func(string) bool          // optional: returns true for URLs needing navBuffer pipeline
	sourceResolvers  map[string]SourceResolver  // URI scheme prefix -> play-time source resolver (e.g. "tidal://")

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
	// burns ~2% CPU even on silence. Resume is called on every committed start.
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
	p.advances = append(p.advances, Advance{Ticket: next.ticket, Finished: pipelineStats(old)})
	if old != nil {
		// The exhausted source no longer needs interrupting to release the audio
		// callback. Keep all teardown off that callback, but register it before
		// releasing ownership so Close still waits for it.
		p.work.Add(1)
		go func() { defer p.work.Done(); old.close() }()
	}
	p.mu.Unlock()
}

// TakeAdvance consumes the next completed gapless transition with its source identity.
func (p *Player) TakeAdvance() (Advance, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.advances) == 0 {
		return Advance{}, false
	}
	a := p.advances[0]
	p.advances = p.advances[1:]
	return a, true
}

// ClearPreload revokes preparation and detaches the registered next source.
func (p *Player) ClearPreload() {
	p.lifecycleMu.Lock()
	defer p.lifecycleMu.Unlock()
	p.revokePending(&p.pendingPreload)
	// Removing next before taking the speaker lock also prevents an interrupted
	// decoder from advancing into a source that is being discarded.
	removed := p.gapless.RemoveNext()
	p.mu.Lock()
	old := p.nextPipeline
	p.mu.Unlock()
	if old != nil && removed != 0 && old.gaplessToken == removed {
		old.interrupt()
	}
	speaker.Lock()
	p.mu.Lock()
	old = p.nextPipeline
	p.nextPipeline = nil
	p.mu.Unlock()
	speaker.Unlock()
	p.closeLater(old)
}

// TogglePause toggles between paused and playing states.
// When pausing, the speaker is suspended to save CPU; when unpausing
// it is resumed so the audio callback drains the queued samples.
func (p *Player) TogglePause() {
	speaker.Lock()
	if p.ctrl != nil {
		p.ctrl.Paused = !p.ctrl.Paused
		paused := p.ctrl.Paused
		p.paused.Store(paused)
		speaker.Unlock()
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
// silence. The returned snapshot identifies the stopped source; queued advances
// remain available through TakeAdvance. A committed start resumes the speaker.
func (p *Player) Stop() PlaybackStats {
	p.lifecycleMu.Lock()
	defer p.lifecycleMu.Unlock()
	return p.stopLocked()
}

func (p *Player) stopLocked() PlaybackStats {
	p.revokePending(&p.pendingStart)
	p.revokePending(&p.pendingPreload)
	p.cancelSeekLocked()
	p.gapless.SetNext(nil)
	p.mu.Lock()
	active, next := p.current, p.nextPipeline
	p.mu.Unlock()
	if active != nil {
		active.interrupt()
	}
	if next != nil {
		next.interrupt()
	}
	speaker.Lock()
	p.gapless.Clear()
	if p.ctrl != nil {
		p.ctrl.Paused = true
	}
	p.mu.Lock()
	finished := pipelineStats(p.current)
	oldCurrent, oldNext := p.current, p.nextPipeline
	p.current, p.nextPipeline = nil, nil
	p.playing.Store(false)
	p.paused.Store(false)
	p.mu.Unlock()
	speaker.Unlock()
	p.stopStreamMetadata()
	p.closeLater(oldCurrent, oldNext)
	p.suspendSpeaker()
	return finished
}

// Seek moves the ticketed source by the given duration (positive or negative).
// A delayed command for a replaced source fails with ErrRevoked before opening
// resources.
// For seekable local files, the decoder's Seek method is used directly.
// Returns nil immediately for non-seekable streams (e.g., Icecast radio).
// FFmpeg replacements are prepared before the speaker lock is acquired. The
// lock only covers decoder commit and preload invalidation.
// Clears the preloaded next pipeline to prevent a stale gapless transition.
func (p *Player) Seek(ticket uint64, d time.Duration) error {
	p.lifecycleMu.Lock()
	p.mu.Lock()
	cur := p.current
	p.mu.Unlock()
	if p.closed || cur == nil || cur.ticket != ticket {
		p.lifecycleMu.Unlock()
		return ErrRevoked
	}
	p.work.Add(1)
	p.lifecycleMu.Unlock()
	defer p.work.Done()

	// yt-dlp seek-by-restart: handled outside the speaker lock via SeekYTDL.
	if cur.ytdlSeek {
		return p.SeekYTDL(ticket, d)
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
	p.revokePending(&p.pendingPreload)
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
		old.close()
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

	p.revokePending(&p.pendingPreload)
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
	speaker.Unlock()
	p.lifecycleMu.Unlock()

	_ = oldPipe.stop()
	closePipelines(oldNext)
	return nil
}

// CancelSeekYTDL revokes and interrupts a pending seek replacement.
func (p *Player) CancelSeekYTDL() {
	p.lifecycleMu.Lock()
	defer p.lifecycleMu.Unlock()
	p.cancelSeekLocked()
}

func (p *Player) cancelSeekLocked() {
	p.seekGen.Add(1)
	if p.seekCancel != nil {
		p.seekCancel()
		p.seekCancel = nil
	}
}

// SeekYTDL prepares a replacement scoped to the ticketed source lifetime.
func (p *Player) SeekYTDL(ticket uint64, d time.Duration) error {
	p.lifecycleMu.Lock()
	p.mu.Lock()
	cur := p.current
	p.mu.Unlock()
	if p.closed || cur == nil || cur.ticket != ticket {
		p.lifecycleMu.Unlock()
		return ErrRevoked
	}
	if !cur.ytdlSeek {
		p.lifecycleMu.Unlock()
		return nil
	}
	p.cancelSeekLocked()
	gen := p.seekGen.Load()
	parent := cur.ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(context.WithoutCancel(parent))
	stopParent := context.AfterFunc(parent, cancel)
	defer stopParent()
	p.seekCancel = cancel
	p.work.Add(1)
	// yt-dlp exposes an atomic sample position, so snapshot it without waiting
	// for a possibly blocked audio read. The old source plays until commit.
	curPos := cur.format.SampleRate.D(cur.decoder.Position()) + cur.streamOffset
	p.lifecycleMu.Unlock()
	defer p.work.Done()
	newPos := max(curPos+d, 0)
	if cur.knownDuration > 0 && newPos >= cur.knownDuration {
		newPos = max(cur.knownDuration-time.Second, 0)
	}
	tp, err := p.buildYTDLPipeline(ctx, cur.path, int(newPos.Seconds()))
	if err != nil {
		cancel()
		return fmt.Errorf("yt-dlp seek: %w", err)
	}
	tp.ctx, tp.cancel, tp.ticket = ctx, cancel, cur.ticket
	tp.knownDuration, tp.ytdlSeek = cur.knownDuration, true
	if !p.commitYTDLSeek(cur, tp, gen, stopParent) {
		tp.close()
		return ErrRevoked
	}
	return nil
}

func (p *Player) commitYTDLSeek(cur, replacement *trackPipeline, gen int64, stopParent func() bool) bool {
	p.lifecycleMu.Lock()
	defer p.lifecycleMu.Unlock()
	p.mu.Lock()
	current := p.current == cur
	p.mu.Unlock()
	if p.closed || p.seekGen.Load() != gen || !current || (replacement.ctx != nil && replacement.ctx.Err() != nil) {
		return false
	}
	p.revokePending(&p.pendingPreload)
	p.gapless.SetNext(nil)
	// Transfer cancellation authority before interrupting the source being
	// replaced. A canceled source cannot transfer a still-running child.
	if !stopParent() {
		return false
	}
	cur.interrupt()
	speaker.Lock()
	p.mu.Lock()
	if p.current != cur {
		p.mu.Unlock()
		speaker.Unlock()
		return false
	}
	p.gapless.Replace(replacement.stream)
	oldNext := p.nextPipeline
	p.current = replacement
	p.nextPipeline = nil
	p.seekCancel = nil
	p.mu.Unlock()
	speaker.Unlock()
	p.closeLater(cur, oldNext)
	return true
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
	p.mu.Lock()
	cur := p.current
	p.mu.Unlock()
	if cur == nil {
		return 0
	}
	if cur.livePrefetch != nil {
		return cur.livePrefetch.Position() + cur.streamOffset
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
	if cur.livePrefetch != nil {
		if cur.knownDuration > 0 {
			return cur.knownDuration
		}
		return cur.decodedDuration
	}
	if n := cur.decoder.Len(); n > 0 {
		return cur.format.SampleRate.D(n)
	}
	return cur.knownDuration
}

// Snapshot reads source identity, progress, and playback state together.
// The speaker lock prevents a gapless promotion midway through the snapshot.
func (p *Player) Snapshot() PlaybackStats {
	speaker.Lock()
	defer speaker.Unlock()
	p.mu.Lock()
	defer p.mu.Unlock()
	stats := pipelineStats(p.current)
	stats.Playing, stats.Paused = p.playing.Load(), p.paused.Load()
	return stats
}

// PositionAndDuration returns both position and duration from one snapshot.
func (p *Player) PositionAndDuration() (time.Duration, time.Duration) {
	stats := p.Snapshot()
	return stats.Position, stats.Duration
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
	p.mu.Lock()
	cur := p.current
	p.mu.Unlock()
	if cur == nil || cur.streamTitle == nil {
		return ""
	}
	title, _ := cur.streamTitle.Load().(string)
	return title
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

	p.mu.Lock()
	parent := context.Background()
	var title *atomic.Value
	if p.current != nil {
		title = p.current.streamTitle
		if p.current.ctx != nil {
			parent = p.current.ctx
		}
	}
	ctx, cancel := context.WithCancel(parent)
	p.metaCancel = cancel
	p.mu.Unlock()

	p.work.Add(1)
	go func() { defer p.work.Done(); p.pollStreamMetadata(ctx, title, fetch, interval) }()
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
// interval tick until ctx is cancelled. Each poller writes only to the source
// that created it, so a late fetch cannot change another track's metadata.
func (p *Player) pollStreamMetadata(ctx context.Context, target *atomic.Value, fetch func(context.Context) (string, error), interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		title, err := fetch(ctx)
		if ctx.Err() != nil {
			return
		}
		if err == nil && title != "" && target != nil {
			target.Store(title)
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

// ResolvedSource is a playable source produced by a SourceResolver at play
// time: either a direct HTTP URL, or an ordered list of media segment URLs
// whose concatenated bytes form one progressive stream (e.g. unencrypted
// DASH fMP4 segments).
type ResolvedSource struct {
	URL      string
	Segments []string
}

// SourceResolver turns a custom URI (e.g. "tidal://track/123") into a
// ResolvedSource when playback starts. Resolving at play time keeps
// short-lived signed URLs fresh no matter how long a track sat in the queue.
type SourceResolver func(context.Context, string) (ResolvedSource, error)

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
	p.lifecycleMu.Lock()
	if !p.closed {
		p.closed = true
		p.stopLocked()
	}
	p.lifecycleMu.Unlock()
	p.work.Wait()
	speaker.Clear()
}
