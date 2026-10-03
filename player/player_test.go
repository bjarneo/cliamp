package player

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gopxl/beep/v2"
	"github.com/gopxl/beep/v2/speaker"
)

// newTestPlayer returns a Player with only the atomic accessors wired up.
// We deliberately skip speaker.Init (which would open a real audio device)
// by constructing the struct directly — this limits us to testing the
// lock-free getters/setters, not the audio pipeline itself.
func newTestPlayer() *Player {
	p := &Player{}
	p.volMin.Store(math.Float64bits(-50))
	p.speed.Store(math.Float64bits(1.0))
	return p
}

func TestSetVolumeClamps(t *testing.T) {
	p := newTestPlayer()

	tests := []struct {
		in   float64
		want float64
	}{
		{-60, -50}, // below min
		{-50, -50},
		{0, 0},
		{6, 6},
		{12, 6},         // above max
		{math.NaN(), 6}, // NaN keeps the current volume
	}
	for _, tt := range tests {
		p.SetVolume(tt.in)
		if got := p.Volume(); got != tt.want {
			t.Errorf("SetVolume(%v) → Volume() = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestRestoreYTDLSeekSource(t *testing.T) {
	original := newPlaybackTestDecoder()
	replacement := newPlaybackTestDecoder()
	cur := &trackPipeline{decoder: original, stream: original, ytdlSeek: true}
	p := &Player{gapless: &gaplessStreamer{}, current: cur}
	p.gapless.Replace(nil) // SeekYTDL mutes playback while rebuilding.
	p.gaplessAdvance.Store(true)

	p.restoreYTDLSeekSource(cur, 0)

	p.gapless.mu.Lock()
	got := p.gapless.current
	p.gapless.mu.Unlock()
	if got != original {
		t.Fatal("failed seek did not restore the original stream")
	}
	if p.GaplessAdvanced() {
		t.Fatal("failed seek retained a stale gapless-advance notification")
	}

	p.gapless.Replace(replacement)
	p.seekGen.Add(1)
	p.restoreYTDLSeekSource(cur, 0)
	p.gapless.mu.Lock()
	got = p.gapless.current
	p.gapless.mu.Unlock()
	if got != replacement {
		t.Fatal("stale failed seek overwrote a newer stream")
	}
}

func TestCommitYTDLSeekDoesNotReplaceNewTrack(t *testing.T) {
	old := &trackPipeline{}
	current := &trackPipeline{}
	p := &Player{gapless: &gaplessStreamer{}, current: current}

	if p.commitYTDLSeek(old, &trackPipeline{}, 0) {
		t.Fatal("commitYTDLSeek() = true, want false for replaced track")
	}
	if p.current != current {
		t.Fatal("stale seek replaced the current pipeline")
	}
}

// SeekYTDL snapshots the current pipeline and mutes it later. A skip or a
// newer seek can land between the two, and the mute must then leave the new
// source alone.
func TestMuteYTDLSeekSource(t *testing.T) {
	tests := []struct {
		name     string
		change   func(p *Player, newer *trackPipeline)
		wantMute bool
	}{
		{name: "snapshot still current", wantMute: true},
		{name: "another track started", change: func(p *Player, newer *trackPipeline) {
			p.current = newer
			p.gapless.Replace(newer.stream)
		}},
		{name: "newer seek started", change: func(p *Player, newer *trackPipeline) {
			p.CancelSeekYTDL()
			p.gapless.Replace(newer.stream)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snapshot := newPlaybackTestDecoder()
			newerDecoder := newPlaybackTestDecoder()
			cur := &trackPipeline{
				decoder:  snapshot,
				stream:   snapshot,
				format:   beep.Format{SampleRate: 100, NumChannels: 2, Precision: 2},
				ytdlSeek: true,
			}
			newer := &trackPipeline{decoder: newerDecoder, stream: newerDecoder}
			p := &Player{gapless: &gaplessStreamer{}, current: cur}
			p.gapless.Replace(cur.stream)
			gen := p.seekGen.Load()
			want := beep.Streamer(cur.stream)
			if tt.change != nil {
				tt.change(p, newer)
				want = newer.stream
			}
			if tt.wantMute {
				want = nil
			}

			if _, muted := p.muteYTDLSeekSource(cur, gen); muted != tt.wantMute {
				t.Fatalf("muteYTDLSeekSource() = %v, want %v", muted, tt.wantMute)
			}
			p.gapless.mu.Lock()
			got := p.gapless.current
			p.gapless.mu.Unlock()
			if got != want {
				t.Fatalf("gapless source = %v, want %v", got, want)
			}
		})
	}
}

// The decoder of a prefetched yt-dlp page reads ahead of the speaker. A seek
// by restart must start from the audio that played, as Position reports it.
func TestMuteYTDLSeekSourceReadsPlayedPosition(t *testing.T) {
	tests := []struct {
		name     string
		prefetch *livePrefetchStreamer
		want     time.Duration
	}{
		{name: "direct decoder", want: 3*time.Second + time.Minute},
		{name: "prefetched decoder", prefetch: &livePrefetchStreamer{sampleRate: 100, consumed: 100}, want: time.Second + time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The decoder has read 3 s of audio.
			decoder := &ytdlPipeStreamer{pipeReport: pipeReport{state: newPipeStreamState(300)}}
			cur := &trackPipeline{
				decoder:      decoder,
				stream:       decoder,
				format:       beep.Format{SampleRate: 100, NumChannels: 2, Precision: 2},
				ytdlSeek:     true,
				streamOffset: time.Minute,
				livePrefetch: tt.prefetch,
			}
			p := &Player{gapless: &gaplessStreamer{}, current: cur}

			got, ok := p.muteYTDLSeekSource(cur, p.seekGen.Load())
			if !ok || got != tt.want {
				t.Fatalf("muteYTDLSeekSource() = (%v, %v), want (%v, true)", got, ok, tt.want)
			}
			if pos := p.Position(); pos != tt.want {
				t.Fatalf("Position() = %v, want %v", pos, tt.want)
			}
		})
	}
}

func TestPlayPipelineForGenerationDiscardsStaleStart(t *testing.T) {
	p := newTestPlayer()
	p.SetPlaybackGeneration(2)

	if err := p.playPipelineForGeneration(&trackPipeline{}, 1); err != nil {
		t.Fatalf("playPipelineForGeneration: %v", err)
	}
	if p.current != nil {
		t.Fatal("stale playback start replaced the current pipeline")
	}
}

func TestPreloadPipelineForGenerationDiscardsStalePreload(t *testing.T) {
	p := newTestPlayer()
	p.gapless = &gaplessStreamer{}
	stale := p.BeginPreload()
	p.BeginPreload()

	if err := p.preloadPipelineForGeneration(&trackPipeline{}, stale); err != nil {
		t.Fatalf("preloadPipelineForGeneration: %v", err)
	}
	if p.nextPipeline != nil {
		t.Fatal("stale preload replaced the next pipeline")
	}
}

// A preload still loading when playback stops must not arm a next track on the
// stopped player.
func TestStopDiscardsInFlightPreload(t *testing.T) {
	p := newTestPlayer()
	p.gapless = &gaplessStreamer{}
	p.suspended = true // Avoid a real speaker context.
	inFlight := p.BeginPreload()
	p.Stop()

	if err := p.preloadPipelineForGeneration(&trackPipeline{}, inFlight); err != nil {
		t.Fatalf("preloadPipelineForGeneration: %v", err)
	}
	if p.nextPipeline != nil {
		t.Fatal("preload started before Stop armed the stopped player")
	}
}

// blockingCloseDecoder holds Close until the test closes release, as an
// ffmpeg or yt-dlp process does while it exits.
type blockingCloseDecoder struct {
	*playbackTestDecoder
	release chan struct{}
}

func (d *blockingCloseDecoder) Close() error {
	<-d.release
	return d.playbackTestDecoder.Close()
}

// ClearPreload runs on the UI goroutine, so it must not wait while the old
// pipeline closes.
func TestClearPreloadDoesNotWaitForClose(t *testing.T) {
	tests := []struct {
		name    string
		preload bool
	}{
		{name: "no preload"},
		{name: "preload with a slow close", preload: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newTestPlayer()
			p.gapless = &gaplessStreamer{}
			var decoder *blockingCloseDecoder
			if tt.preload {
				decoder = &blockingCloseDecoder{playbackTestDecoder: newPlaybackTestDecoder(), release: make(chan struct{})}
				if err := p.preloadPipelineForGeneration(&trackPipeline{decoder: decoder, stream: decoder}, 0); err != nil {
					t.Fatalf("preloadPipelineForGeneration: %v", err)
				}
			}
			generation := p.preloadGen.Load()

			done := make(chan struct{})
			go func() {
				p.ClearPreload()
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				if decoder != nil {
					close(decoder.release)
					<-done
				}
				t.Fatal("ClearPreload waited for the old pipeline to close")
			}

			if p.HasPreload() {
				t.Fatal("ClearPreload kept the preloaded pipeline")
			}
			if p.preloadGen.Load() == generation {
				t.Fatal("ClearPreload did not reject a preload in flight")
			}
			p.gapless.mu.Lock()
			next := p.gapless.next
			p.gapless.mu.Unlock()
			if next != nil {
				t.Fatal("ClearPreload left the gapless next stream armed")
			}
			if decoder == nil {
				return
			}
			close(decoder.release)
			select {
			case <-decoder.closed:
			case <-time.After(2 * time.Second):
				t.Fatal("ClearPreload did not close the old pipeline")
			}
		})
	}
}

// preparedSeekTestDecoder seeks through the prepared ffmpeg path, as a
// local ffmpeg decoder does, without a process.
type preparedSeekTestDecoder struct {
	*playbackTestDecoder
}

func (preparedSeekTestDecoder) prepareSeek(int) (*preparedFFmpegSeek, error) {
	return &preparedFFmpegSeek{}, nil
}
func (preparedSeekTestDecoder) seekMatches(*preparedFFmpegSeek) bool { return true }
func (preparedSeekTestDecoder) commitPreparedSeek(*preparedFFmpegSeek) (ffmpegPipe, bool) {
	return ffmpegPipe{}, true
}
func (preparedSeekTestDecoder) interrupt() {}

// A seek of a local track runs on the UI goroutine and drops the preload,
// because the gapless boundary moved. It must not wait while that preload
// closes.
func TestSeekDoesNotWaitForPreloadClose(t *testing.T) {
	native := func() beep.StreamSeekCloser { return newPlaybackTestDecoder() }
	prepared := func() beep.StreamSeekCloser {
		return preparedSeekTestDecoder{playbackTestDecoder: newPlaybackTestDecoder()}
	}
	tests := []struct {
		name    string
		current func() beep.StreamSeekCloser
		preload bool
	}{
		{name: "native seek without a preload", current: native},
		{name: "native seek with a slow preload close", current: native, preload: true},
		{name: "prepared ffmpeg seek without a preload", current: prepared},
		{name: "prepared ffmpeg seek with a slow preload close", current: prepared, preload: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newTestPlayer()
			p.gapless = &gaplessStreamer{}
			current := tt.current()
			p.current = &trackPipeline{
				decoder:  current,
				stream:   current,
				format:   beep.Format{SampleRate: 100, NumChannels: 2, Precision: 2},
				seekable: true,
			}
			var decoder *blockingCloseDecoder
			if tt.preload {
				decoder = &blockingCloseDecoder{playbackTestDecoder: newPlaybackTestDecoder(), release: make(chan struct{})}
				if err := p.preloadPipelineForGeneration(&trackPipeline{decoder: decoder, stream: decoder}, 0); err != nil {
					t.Fatalf("preloadPipelineForGeneration: %v", err)
				}
			}

			done := make(chan error, 1)
			go func() { done <- p.Seek(time.Second) }()
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("Seek: %v", err)
				}
			case <-time.After(2 * time.Second):
				if decoder != nil {
					close(decoder.release)
					<-done
				}
				t.Fatal("Seek waited for the old preload to close")
			}

			if p.HasPreload() {
				t.Fatal("Seek kept the preloaded pipeline")
			}
			if decoder == nil {
				return
			}
			close(decoder.release)
			select {
			case <-decoder.closed:
			case <-time.After(2 * time.Second):
				t.Fatal("Seek did not close the old preload")
			}
		})
	}
}

func TestSetVolumeMinClamps(t *testing.T) {
	p := newTestPlayer()

	tests := []struct {
		in   float64
		want float64
	}{
		{-100, -90}, // below absolute floor
		{-90, -90},
		{-50, -50},
		{0, 0},
		{5, 0},          // above max (must be ≤ 0)
		{math.NaN(), 0}, // NaN keeps the current floor
	}
	for _, tt := range tests {
		p.SetVolumeMin(tt.in)
		if got := p.VolumeMin(); got != tt.want {
			t.Errorf("SetVolumeMin(%v) → VolumeMin() = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestSetVolumeMinReconcilesCurrent(t *testing.T) {
	p := newTestPlayer()
	p.SetVolume(-40)
	p.SetVolumeMin(-30) // raise floor above current volume
	if got := p.Volume(); got != -30 {
		t.Errorf("Volume after SetVolumeMin raise = %v, want -30", got)
	}
}

func TestVolumeClampedToCustomMin(t *testing.T) {
	tests := []struct {
		name      string
		volumeMin float64
		volume    float64
		want      float64
	}{
		{"below floor", -30, -40, -30},
		{"at floor", -30, -30, -30},
		{"above floor", -30, -10, -10},
		{"custom floor -60", -60, -70, -60},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newTestPlayer()
			p.SetVolumeMin(tt.volumeMin)
			p.SetVolume(tt.volume)
			if got := p.Volume(); got != tt.want {
				t.Errorf("Volume = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSetSpeedClamps(t *testing.T) {
	p := newTestPlayer()

	tests := []struct {
		in   float64
		want float64
	}{
		{0.1, 0.25},
		{0.25, 0.25},
		{1.0, 1.0},
		{2.0, 2.0},
		{3.0, 2.0},
		{math.NaN(), 2.0}, // NaN keeps the current speed
	}
	for _, tt := range tests {
		p.SetSpeed(tt.in)
		if got := p.Speed(); got != tt.want {
			t.Errorf("SetSpeed(%v) → Speed() = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestToggleMono(t *testing.T) {
	p := newTestPlayer()

	if p.Mono() {
		t.Fatal("Mono() should start false")
	}
	p.ToggleMono()
	if !p.Mono() {
		t.Error("ToggleMono should flip to true")
	}
	p.ToggleMono()
	if p.Mono() {
		t.Error("ToggleMono should flip back to false")
	}
}

func TestSetEQBandClamps(t *testing.T) {
	p := newTestPlayer()

	tests := []struct {
		band int
		in   float64
		want float64
	}{
		{0, 20.0, 12.0},
		{0, -20.0, -12.0},
		{5, 6.5, 6.5},
		{9, 0.0, 0.0},
		{5, math.NaN(), 6.5}, // NaN keeps the current gain
	}
	for _, tt := range tests {
		p.SetEQBand(tt.band, tt.in)
		bands := p.EQBands()
		if bands[tt.band] != tt.want {
			t.Errorf("SetEQBand(%d, %v) → %v, want %v", tt.band, tt.in, bands[tt.band], tt.want)
		}
	}
}

func TestSetEQBandIgnoresInvalidIndex(t *testing.T) {
	p := newTestPlayer()
	// Setting an out-of-range band should be a no-op, not panic.
	p.SetEQBand(-1, 5)
	p.SetEQBand(10, 5)
	p.SetEQBand(100, 5)

	for i, b := range p.EQBands() {
		if b != 0 {
			t.Errorf("EQBands[%d] = %v, want 0 after invalid writes", i, b)
		}
	}
}

func TestEQBandsReturnsCopy(t *testing.T) {
	p := newTestPlayer()
	p.SetEQBand(0, 6)

	bands := p.EQBands()
	bands[0] = 999 // mutate local copy

	if p.EQBands()[0] != 6 {
		t.Error("EQBands() should return a copy — mutation leaked back")
	}
}

func TestIsPlayingDefaultsFalse(t *testing.T) {
	p := newTestPlayer()
	if p.IsPlaying() {
		t.Error("IsPlaying() should start false")
	}
	if p.IsPaused() {
		t.Error("IsPaused() should start false")
	}
}

func TestIsLiveStream(t *testing.T) {
	p := newTestPlayer()
	if p.IsLiveStream() {
		t.Fatal("IsLiveStream() = true without a current pipeline")
	}
	p.current = &trackPipeline{live: true}
	if !p.IsLiveStream() {
		t.Fatal("IsLiveStream() = false for a live HTTP pipeline")
	}
}

func TestHasSourceResolver(t *testing.T) {
	p := newTestPlayer()
	p.RegisterSourceResolver("qobuz://track/", func(string) (ResolvedSource, error) {
		return ResolvedSource{}, nil
	})
	tests := []struct {
		path string
		want bool
	}{
		{path: "qobuz://track/42", want: true},
		{path: "tidal://track/42", want: false},
		{path: "/music/song.flac", want: false},
	}
	for _, tt := range tests {
		if got := p.HasSourceResolver(tt.path); got != tt.want {
			t.Errorf("HasSourceResolver(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestSampleRate(t *testing.T) {
	p := &Player{sr: 44100}
	if p.SampleRate() != 44100 {
		t.Errorf("SampleRate() = %d, want 44100", p.SampleRate())
	}
}

func TestStreamTitleEmpty(t *testing.T) {
	p := newTestPlayer()
	if got := p.StreamTitle(); got != "" {
		t.Errorf("StreamTitle() on fresh player = %q, want empty", got)
	}
}

func TestSetStreamTitle(t *testing.T) {
	p := newTestPlayer()
	p.setStreamTitle("Artist - Song")
	if got := p.StreamTitle(); got != "Artist - Song" {
		t.Errorf("StreamTitle() = %q, want 'Artist - Song'", got)
	}
}

func TestRegisterStreamerFactory(t *testing.T) {
	p := newTestPlayer()
	var noop StreamerFactory // nil factory is fine for this storage-only test
	p.RegisterStreamerFactory("spotify:", noop)
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.customFactories) != 1 {
		t.Errorf("len(customFactories) = %d, want 1", len(p.customFactories))
	}
	if _, ok := p.customFactories["spotify:"]; !ok {
		t.Error("factory not stored under 'spotify:'")
	}
}

func TestRegisterBufferedURLMatcher(t *testing.T) {
	p := newTestPlayer()
	match := func(u string) bool { return u == "foo" }
	p.RegisterBufferedURLMatcher(match)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.bufferedURLMatch == nil {
		t.Error("matcher not stored")
	}
}

// The registries are read while a track starts and written by Register
// calls. Run with -race: the reads must hold the lock that the writes hold.
func TestRegistryReadsHoldTheLock(t *testing.T) {
	factory := func(string) (beep.StreamSeekCloser, beep.Format, time.Duration, error) {
		return nil, beep.Format{}, 0, nil
	}
	resolver := func(string) (ResolvedSource, error) { return ResolvedSource{}, nil }
	tests := []struct {
		name     string
		register func(p *Player, i int)
		match    func(p *Player) bool
	}{
		{
			name:     "streamer factory",
			register: func(p *Player, i int) { p.RegisterStreamerFactory(fmt.Sprintf("s%d:", i), factory) },
			match:    func(p *Player) bool { return p.matchCustomURI("s0:track") != nil },
		},
		{
			name:     "source resolver",
			register: func(p *Player, i int) { p.RegisterSourceResolver(fmt.Sprintf("r%d://", i), resolver) },
			match:    func(p *Player) bool { return p.matchSourceResolver("r0://track") != nil },
		},
		{
			name:     "buffered url matcher",
			register: func(p *Player, _ int) { p.RegisterBufferedURLMatcher(func(string) bool { return true }) },
			match:    func(p *Player) bool { return p.isBufferedURL("https://example.com/stream") },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newTestPlayer()
			const writes = 200
			done := make(chan struct{})
			go func() {
				defer close(done)
				for i := range writes {
					tt.register(p, i)
				}
			}()
			for {
				select {
				case <-done:
					if !tt.match(p) {
						t.Fatal("registered entry does not match after the writes")
					}
					return
				default:
					tt.match(p)
				}
			}
		})
	}
}

func TestConcurrentVolumeSetRead(t *testing.T) {
	p := newTestPlayer()
	var wg sync.WaitGroup
	const N = 100

	wg.Add(N * 2)
	for i := 0; i < N; i++ {
		go func(i int) {
			defer wg.Done()
			p.SetVolume(float64(-i % 30))
		}(i)
		go func() {
			defer wg.Done()
			_ = p.Volume()
		}()
	}
	wg.Wait()
	// If the race detector didn't flag anything, we're happy.
}

// Ensure the atomic uint64 storage actually wraps dB as expected.
func TestVolumeStoragePrecision(t *testing.T) {
	p := newTestPlayer()
	p.SetVolume(-3.14)
	v := math.Float64frombits(p.volume.Load())
	if math.Abs(v-(-3.14)) > 1e-12 {
		t.Errorf("stored volume = %v, want -3.14", v)
	}
}

type playbackTestDecoder struct {
	closeOnce sync.Once
	closed    chan struct{}
}

func newPlaybackTestDecoder() *playbackTestDecoder {
	return &playbackTestDecoder{closed: make(chan struct{})}
}

func (d *playbackTestDecoder) Stream(samples [][2]float64) (int, bool) {
	clear(samples)
	return len(samples), true
}

func (*playbackTestDecoder) Err() error     { return nil }
func (*playbackTestDecoder) Len() int       { return 1000 }
func (*playbackTestDecoder) Position() int  { return 0 }
func (*playbackTestDecoder) Seek(int) error { return nil }
func (d *playbackTestDecoder) Close() error {
	d.closeOnce.Do(func() { close(d.closed) })
	return nil
}

// A pipe decoder can block in Stream while the audio goroutine holds the
// speaker lock. Stop and a source replacement must interrupt it first.
func TestPlayerBlockedPipeStreamCanBeInterruptedBeforeSpeakerLock(t *testing.T) {
	sources := []struct {
		name    string
		blocked func(*testing.T) (*trackPipeline, <-chan struct{}, <-chan struct{}, func())
	}{
		{name: "nav", blocked: blockedNavPlayback},
		{name: "yt-dlp", blocked: blockedYTDLPlayback},
	}
	tests := []struct {
		name string
		run  func(*Player) error
	}{
		{
			name: "stop",
			run: func(p *Player) error {
				p.suspended = true // Avoid a real speaker context in this lifecycle test.
				p.Stop()
				return nil
			},
		},
		{
			name: "source replacement",
			run: func(p *Player) error {
				decoder := newPlaybackTestDecoder()
				tp := &trackPipeline{
					decoder: decoder,
					stream:  decoder,
					format:  beep.Format{SampleRate: 100, NumChannels: 2, Precision: 2},
				}
				return p.playPipelineForGeneration(tp, 0)
			},
		},
	}

	for _, src := range sources {
		for _, tt := range tests {
			t.Run(src.name+"/"+tt.name, func(t *testing.T) {
				old, audioStarted, audioDone, release := src.blocked(t)
				defer release()
				queuedDecoder := newPlaybackTestDecoder()
				queued := &trackPipeline{decoder: queuedDecoder, stream: queuedDecoder}
				p := &Player{
					sr:           100,
					gapless:      &gaplessStreamer{},
					current:      old,
					nextPipeline: queued,
					started:      true,
					ctrl:         &beep.Ctrl{},
					suspended:    false,
				}
				p.gapless.Replace(old.stream)
				p.gapless.SetNext(queued.stream)
				<-audioStarted

				done := make(chan error, 1)
				go func() { done <- tt.run(p) }()
				select {
				case err := <-done:
					if err != nil {
						t.Fatalf("playback operation error = %v", err)
					}
				case <-time.After(2 * time.Second):
					release()
					<-audioDone
					t.Fatalf("playback operation deadlocked behind blocked %s Stream", src.name)
				}
				select {
				case <-audioDone:
				case <-time.After(time.Second):
					t.Fatalf("blocked %s Stream was not released", src.name)
				}
				select {
				case <-queuedDecoder.closed:
				case <-time.After(time.Second):
					t.Fatal("playback operation retained the queued preload")
				}

				if tt.name == "source replacement" {
					p.mu.Lock()
					current := p.current
					p.mu.Unlock()
					if current == nil || current == old {
						t.Fatal("late gapless advance overwrote source replacement")
					}
					p.suspended = true
					p.Stop()
				}
			})
		}
	}
}

// blockedYTDLPlayback starts a yt-dlp | ffmpeg chain whose yt-dlp sends one
// frame and then stalls, as on a network stall. The audio goroutine then
// blocks in Stream while it holds the speaker lock.
func blockedYTDLPlayback(t *testing.T) (*trackPipeline, <-chan struct{}, <-chan struct{}, func()) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX process fixtures")
	}
	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "yt-dlp"), "#!/bin/sh\nprintf '\\000\\100\\000\\300'\nexec sleep 30\n")
	writeExecutable(t, filepath.Join(dir, "ffmpeg"), "#!/bin/sh\nexec cat\n")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	decoder, format, err := decodeYTDLPipe("https://www.youtube.com/watch?v=stall", 100, 16, 0)
	if err != nil {
		t.Fatalf("decodeYTDLPipe() error = %v", err)
	}
	if err := prefillYTDLPipe(decoder); err != nil {
		t.Fatalf("prefillYTDLPipe() error = %v", err)
	}
	tp := &trackPipeline{decoder: decoder, stream: decoder, format: format, ytdlSeek: true}
	started := make(chan struct{})
	done := make(chan struct{})
	go func() {
		speaker.Lock()
		close(started)
		// One frame is in the pipe. The read waits for the second one.
		decoder.Stream(make([][2]float64, 2))
		speaker.Unlock()
		close(done)
	}()
	return tp, started, done, func() { _ = decoder.Close() }
}

func blockedNavPlayback(t *testing.T) (*trackPipeline, <-chan struct{}, <-chan struct{}, func()) {
	t.Helper()
	reader, writer := io.Pipe()
	decoder := &navFFmpegStreamer{
		ffmpegPipe: ffmpegPipe{
			pipeReport: pipeReport{state: newPipeStreamState(0)},
			reader:     bufio.NewReader(reader),
			pipe:       reader,
		},
		nb: newCompletedTestNavBuffer(t, nil),
		sr: 100,
	}
	tp := &trackPipeline{
		decoder:  decoder,
		stream:   decoder,
		format:   beep.Format{SampleRate: 100, NumChannels: 2, Precision: 2},
		seekable: true,
	}
	started := make(chan struct{})
	done := make(chan struct{})
	go func() {
		speaker.Lock()
		close(started)
		decoder.Stream(make([][2]float64, 1))
		speaker.Unlock()
		close(done)
	}()
	return tp, started, done, func() { _ = writer.Close() }
}

func TestPlayerFFmpegSeekPreparesOutsideSpeakerLock(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX shell fixtures")
	}
	for _, kind := range []string{"local", "nav"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			countPath := filepath.Join(dir, "ffmpeg-count")
			readyPath := filepath.Join(dir, "replacement-ready")
			writeExecutable(t, filepath.Join(dir, "ffmpeg"), `#!/bin/sh
n=0
if [ -f "$FFMPEG_COUNT" ]; then n=$(cat "$FFMPEG_COUNT"); fi
n=$((n + 1))
printf '%s' "$n" > "$FFMPEG_COUNT"
if [ "$n" -gt 1 ]; then
  sleep 0.2
  : > "$FFMPEG_READY"
fi
printf '\000\100\000\300'
exec sleep 30
`)
			writeExecutable(t, filepath.Join(dir, "ffprobe"), `#!/bin/sh
printf '10\n'
`)
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("FFMPEG_COUNT", countPath)
			t.Setenv("FFMPEG_READY", readyPath)

			var decoder beep.StreamSeekCloser
			var err error
			switch kind {
			case "local":
				decoder, _, err = decodeFFmpegLocal(filepath.Join(dir, "track.m4a"), 100, 16)
			case "nav":
				nb := newCompletedTestNavBuffer(t, []byte("HEADpayload"))
				decoder, _, err = decodeNavFFmpeg(nb, 100, 16)
				waitForFileValue(t, countPath, "1")
			}
			if err != nil {
				t.Fatalf("build %s decoder: %v", kind, err)
			}
			defer decoder.Close()

			tp := &trackPipeline{
				decoder:  decoder,
				stream:   decoder,
				format:   beep.Format{SampleRate: 100, NumChannels: 2, Precision: 2},
				seekable: true,
			}
			preloadedDecoder := newPlaybackTestDecoder()
			preloaded := &trackPipeline{decoder: preloadedDecoder, stream: preloadedDecoder}
			p := &Player{sr: 100, gapless: &gaplessStreamer{}, current: tp, nextPipeline: preloaded}
			p.gapless.Replace(tp.stream)
			p.gapless.SetNext(preloaded.stream)

			speaker.Lock()
			seekDone := make(chan error, 1)
			go func() { seekDone <- p.Seek(time.Second) }()
			if !waitForPath(readyPath) {
				speaker.Unlock()
				t.Fatalf("timed out waiting for %s", readyPath)
			}
			select {
			case err := <-seekDone:
				speaker.Unlock()
				t.Fatalf("Seek completed before speaker commit lock was available: %v", err)
			default:
			}
			speaker.Unlock()

			select {
			case err := <-seekDone:
				if err != nil {
					t.Fatalf("Seek() error = %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("Seek did not complete after speaker lock was released")
			}
			if got := decoder.Position(); got != 100 {
				t.Fatalf("Position() = %d, want relative seek target 100", got)
			}
			if p.HasPreload() {
				t.Fatal("successful Seek retained stale preload")
			}
			select {
			case <-preloadedDecoder.closed:
			case <-time.After(2 * time.Second):
				t.Fatal("successful Seek did not close stale preload")
			}
		})
	}
}

func waitForPath(path string) bool {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func waitForFileValue(t *testing.T, path, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got, err := os.ReadFile(path); err == nil && string(got) == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s to contain %q", path, want)
}

// TestNewRejectsInvalidQuality covers the validation branch of New, which
// returns before speaker.Init and therefore needs no audio device.
func TestNewRejectsInvalidQuality(t *testing.T) {
	tests := []struct {
		name string
		q    Quality
	}{
		{"zero sample rate", Quality{SampleRate: 0, BufferMs: 100, ResampleQuality: 4}},
		{"negative sample rate", Quality{SampleRate: -1, BufferMs: 100, ResampleQuality: 4}},
		{"zero buffer", Quality{SampleRate: 44100, BufferMs: 0, ResampleQuality: 4}},
		{"negative buffer", Quality{SampleRate: 44100, BufferMs: -1, ResampleQuality: 4}},
		{"zero resample quality", Quality{SampleRate: 44100, BufferMs: 100, ResampleQuality: 0}},
		{"negative resample quality", Quality{SampleRate: 44100, BufferMs: 100, ResampleQuality: -1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := New(tt.q)
			if err == nil {
				t.Fatal("New() error = nil, want non-nil")
			}
			if p != nil {
				t.Errorf("New() player = %v, want nil on error", p)
			}
		})
	}
}

// TestAudioOutputHint guards the actionable part of the "audio output
// unavailable" error: on Linux the failure is almost always a missing ALSA
// bridge, so the hint must name the packages that fix it.
func TestAudioOutputHint(t *testing.T) {
	hint := audioOutputHint()
	if runtime.GOOS != "linux" {
		if hint != "" {
			t.Errorf("audioOutputHint() = %q, want empty on %s", hint, runtime.GOOS)
		}
		return
	}
	for _, want := range []string{"libasound2-plugins", "pipewire-alsa", "pulseaudio-alsa", "ALSA"} {
		if !strings.Contains(hint, want) {
			t.Errorf("audioOutputHint() missing %q, got:\n%s", want, hint)
		}
	}
}

// frameDecoder reports a fixed position and length in frames.
type frameDecoder struct {
	playbackTestDecoder
	pos, n int
}

func (d *frameDecoder) Position() int { return d.pos }
func (d *frameDecoder) Len() int      { return d.n }

// TestPositionAndDurationRule checks that Position, Duration and
// PositionAndDuration report the same values for each pipeline shape.
func TestPositionAndDurationRule(t *testing.T) {
	format := beep.Format{SampleRate: 1000, NumChannels: 2, Precision: 2}
	prefetch := newLivePrefetchStreamer(&gatedLiveStreamer{}, format.SampleRate)
	defer prefetch.Close()
	tests := []struct {
		name    string
		current *trackPipeline
		wantPos time.Duration
		wantDur time.Duration
	}{
		{name: "no track"},
		{
			name:    "decoder length wins over metadata",
			current: &trackPipeline{decoder: &frameDecoder{pos: 500, n: 2000}, format: format, knownDuration: time.Minute},
			wantPos: 500 * time.Millisecond,
			wantDur: 2 * time.Second,
		},
		{
			name:    "metadata when the decoder has no length",
			current: &trackPipeline{decoder: &frameDecoder{pos: 1000}, format: format, knownDuration: time.Minute},
			wantPos: time.Second,
			wantDur: time.Minute,
		},
		{
			name:    "yt-dlp restart adds its offset",
			current: &trackPipeline{decoder: &frameDecoder{pos: 1000}, format: format, streamOffset: 30 * time.Second, knownDuration: time.Hour},
			wantPos: 31 * time.Second,
			wantDur: time.Hour,
		},
		{
			name:    "live prefetch with metadata",
			current: &trackPipeline{decoder: &frameDecoder{pos: 9000, n: 9000}, format: format, livePrefetch: prefetch, knownDuration: 2 * time.Minute, decodedDuration: time.Minute},
			wantDur: 2 * time.Minute,
		},
		{
			name:    "live prefetch falls back to the decoded length",
			current: &trackPipeline{decoder: &frameDecoder{pos: 9000, n: 9000}, format: format, livePrefetch: prefetch, streamOffset: time.Second, decodedDuration: time.Minute},
			wantPos: time.Second,
			wantDur: time.Minute,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Player{current: tt.current}
			pos, dur := p.PositionAndDuration()
			got := fmt.Sprintf("Position=%v Duration=%v PositionAndDuration=%v,%v", p.Position(), p.Duration(), pos, dur)
			want := fmt.Sprintf("Position=%v Duration=%v PositionAndDuration=%v,%v", tt.wantPos, tt.wantDur, tt.wantPos, tt.wantDur)
			if got != want {
				t.Fatalf("got %s\nwant %s", got, want)
			}
		})
	}
}

// TestFirstPlayWritesCtrlUnderSpeakerLock checks, under -race, that the
// first start publishes p.ctrl under the speaker lock. TogglePause and Stop
// read it under that lock.
func TestFirstPlayWritesCtrlUnderSpeakerLock(t *testing.T) {
	p := &Player{sr: 1000, gapless: &gaplessStreamer{}, tapBufferFrames: 4096}
	t.Cleanup(speaker.Clear)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			speaker.Lock()
			ctrl := p.ctrl
			speaker.Unlock()
			if ctrl != nil {
				return
			}
			select {
			case <-stop:
				return
			default:
			}
		}
	}()
	decoder := newPlaybackTestDecoder()
	err := p.playPipelineForGeneration(&trackPipeline{decoder: decoder, stream: decoder}, 0)
	close(stop)
	<-done
	if err != nil {
		t.Fatalf("playPipelineForGeneration: %v", err)
	}
	speaker.Lock()
	ctrl := p.ctrl
	speaker.Unlock()
	if ctrl == nil || !p.IsPlaying() {
		t.Fatalf("after the first start: ctrl %v, playing %v", ctrl, p.IsPlaying())
	}
	p.suspended = true // Avoid a real speaker context in this lifecycle test.
	p.Stop()
}
