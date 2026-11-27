package player

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gopxl/beep/v2"
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
			decoder := &ytdlPipeStreamer{}
			decoder.setState(newPipeStreamState(300))
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
func (preparedSeekTestDecoder) commitPreparedSeek(*preparedFFmpegSeek) (*ffmpegPipe, bool) {
	return nil, true
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
		{name: "custom", blocked: blockedCustomPlayback},
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

// interruptibleTestDecoder is a custom streamer whose Stream blocks until
// Interrupt, as a network read on a dead connection does.
type interruptibleTestDecoder struct {
	playbackTestDecoder
	once        sync.Once
	interrupted chan struct{}
}

func (d *interruptibleTestDecoder) Stream([][2]float64) (int, bool) {
	<-d.interrupted
	return 0, false
}

func (d *interruptibleTestDecoder) Interrupt() {
	d.once.Do(func() { close(d.interrupted) })
}

// blockedCustomPlayback starts a custom streamer, such as the Spotify one,
// that blocks in Stream while the audio goroutine holds the speaker lock.
func blockedCustomPlayback(t *testing.T) (*trackPipeline, <-chan struct{}, <-chan struct{}, func()) {
	t.Helper()
	decoder := &interruptibleTestDecoder{
		playbackTestDecoder: playbackTestDecoder{closed: make(chan struct{})},
		interrupted:         make(chan struct{}),
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
	return tp, started, done, decoder.Interrupt
}

func blockedNavPlayback(t *testing.T) (*trackPipeline, <-chan struct{}, <-chan struct{}, func()) {
	t.Helper()
	reader, writer := io.Pipe()
	decoder := &navFFmpegStreamer{
		ffmpegPipe: ffmpegPipe{
			reader: bufio.NewReader(reader),
			pipe:   reader,
		},
		nb: newCompletedTestNavBuffer(t, nil),
		sr: 100,
	}
	decoder.setState(newPipeStreamState(0))
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

func TestTogglePauseRequiresPlayingAndSyncsState(t *testing.T) {
	tests := []struct {
		name        string
		playing     bool
		initPaused  bool
		wantPaused  bool
		wantCtrlPwd bool
	}{
		{name: "stopped ignores toggle", playing: false, initPaused: true, wantPaused: false, wantCtrlPwd: true},
		{name: "playing pauses", playing: true, initPaused: false, wantPaused: true, wantCtrlPwd: true},
		{name: "paused resumes", playing: true, initPaused: true, wantPaused: false, wantCtrlPwd: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newTestPlayer()
			p.gapless = &gaplessStreamer{}
			p.ctrl = &beep.Ctrl{Paused: tt.initPaused}
			p.suspended = true
			p.playing.Store(tt.playing)
			if tt.playing {
				p.paused.Store(tt.initPaused)
			}

			p.TogglePause()

			if got := p.IsPaused(); got != tt.wantPaused {
				t.Fatalf("IsPaused() = %v, want %v", got, tt.wantPaused)
			}
			speaker.Lock()
			gotCtrl := p.ctrl.Paused
			speaker.Unlock()
			if gotCtrl != tt.wantCtrlPwd {
				t.Fatalf("ctrl.Paused = %v, want %v", gotCtrl, tt.wantCtrlPwd)
			}
		})
	}
}

func TestClearPreloadConcurrentWithPreload(t *testing.T) {
	p := newTestPlayer()
	p.gapless = &gaplessStreamer{}
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			dec := newPlaybackTestDecoder()
			_ = p.preloadPipelineForGeneration(&trackPipeline{decoder: dec, stream: dec}, 0)
		}()
		go func() {
			defer wg.Done()
			p.ClearPreload()
		}()
	}
	wg.Wait()

	// Verify ClearPreload did not close an armed gapless next stream.
	p.mu.Lock()
	next := p.nextPipeline
	p.mu.Unlock()
	if next != nil {
		dec := next.decoder.(*playbackTestDecoder)
		select {
		case <-dec.closed:
			t.Fatal("live preloaded pipeline was closed while still armed")
		default:
		}
	}
	p.suspended = true
	p.Close()
}

func TestPreloadGenInvalidatedOnCommitAndSeek(t *testing.T) {
	tests := []struct {
		name string
		act  func(p *Player)
	}{
		{
			name: "playPipelineForGeneration",
			act: func(p *Player) {
				dec := newPlaybackTestDecoder()
				_ = p.playPipelineForGeneration(&trackPipeline{decoder: dec, stream: dec}, 0)
			},
		},
		{
			name: "native Seek",
			act: func(p *Player) {
				_ = p.Seek(time.Second)
			},
		},
		{
			name: "commitPreparedSeek",
			act: func(p *Player) {
				seeker := preparedSeekTestDecoder{playbackTestDecoder: newPlaybackTestDecoder()}
				cur := &trackPipeline{
					decoder:  seeker,
					stream:   seeker,
					format:   beep.Format{SampleRate: 100, NumChannels: 2, Precision: 2},
					seekable: true,
				}
				p.mu.Lock()
				p.current = cur
				p.gapless.Replace(cur.stream)
				p.mu.Unlock()
				_ = p.commitPreparedSeek(cur, seeker, &preparedFFmpegSeek{})
			},
		},
		{
			name: "commitYTDLSeek",
			act: func(p *Player) {
				cur := p.current
				repDec := newPlaybackTestDecoder()
				rep := &trackPipeline{decoder: repDec, stream: repDec, ytdlSeek: true}
				p.commitYTDLSeek(cur, rep, p.seekGen.Load())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newTestPlayer()
			p.sr = 100
			p.gapless = &gaplessStreamer{}
			p.ctrl = &beep.Ctrl{}
			p.started = true
			p.suspended = true
			curDec := newPlaybackTestDecoder()
			p.current = &trackPipeline{
				decoder:  curDec,
				stream:   curDec,
				format:   beep.Format{SampleRate: 100, NumChannels: 2, Precision: 2},
				seekable: true,
			}
			p.gapless.Replace(p.current.stream)

			inFlight := p.BeginPreload()
			tt.act(p)
			if p.preloadGen.Load() == inFlight {
				t.Fatalf("%s did not increment preloadGen", tt.name)
			}
			staleDec := newPlaybackTestDecoder()
			_ = p.preloadPipelineForGeneration(&trackPipeline{decoder: staleDec, stream: staleDec}, inFlight)
			if p.HasPreload() {
				t.Fatalf("%s allowed stale preload to arm", tt.name)
			}
			p.Close()
		})
	}
}

func TestPlayAndStopInterruptQueuedNextPipeline(t *testing.T) {
	tests := []struct {
		name string
		run  func(p *Player)
	}{
		{
			name: "Stop interrupts current and next",
			run: func(p *Player) {
				p.Stop()
			},
		},
		{
			name: "PlayAt interrupts current and next",
			run: func(p *Player) {
				dec := newPlaybackTestDecoder()
				_ = p.playPipelineForGeneration(&trackPipeline{decoder: dec, stream: dec}, 0)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			curDec := &interruptibleTestDecoder{
				playbackTestDecoder: playbackTestDecoder{closed: make(chan struct{})},
				interrupted:         make(chan struct{}),
			}
			nextDec := &interruptibleTestDecoder{
				playbackTestDecoder: playbackTestDecoder{closed: make(chan struct{})},
				interrupted:         make(chan struct{}),
			}
			p := newTestPlayer()
			p.sr = 100
			p.gapless = &gaplessStreamer{}
			p.ctrl = &beep.Ctrl{}
			p.started = true
			p.suspended = true
			p.current = &trackPipeline{decoder: curDec, stream: curDec}
			p.nextPipeline = &trackPipeline{decoder: nextDec, stream: nextDec}
			p.gapless.Replace(curDec)
			p.gapless.SetNext(nextDec)

			tt.run(p)

			select {
			case <-curDec.interrupted:
			default:
				t.Fatal("current pipeline was not interrupted")
			}
			select {
			case <-nextDec.interrupted:
			default:
				t.Fatal("queued next pipeline was not interrupted")
			}
			p.Close()
		})
	}
}

type nonAtomicErrDecoder struct {
	playbackTestDecoder
	err error
}

func (d *nonAtomicErrDecoder) Stream(samples [][2]float64) (int, bool) {
	d.err = errors.New("stream error")
	clear(samples)
	return len(samples), true
}

func (d *nonAtomicErrDecoder) Err() error { return d.err }

func TestStreamErrSerializesDirectDecoderWithSpeakerLock(t *testing.T) {
	dec := &nonAtomicErrDecoder{playbackTestDecoder: playbackTestDecoder{closed: make(chan struct{})}}
	p := newTestPlayer()
	p.current = &trackPipeline{decoder: dec, stream: dec}

	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([][2]float64, 4)
		for range 200 {
			speaker.Lock()
			dec.Stream(buf)
			speaker.Unlock()
		}
	}()
	for range 200 {
		_ = p.StreamErr()
	}
	<-done
	if p.StreamErr() == nil {
		t.Fatal("StreamErr() = nil, want decoder error")
	}
}

func TestPlayerCloseWaitsForDetachedCloses(t *testing.T) {
	p := newTestPlayer()
	p.gapless = &gaplessStreamer{}
	p.suspended = true
	dec := &blockingCloseDecoder{
		playbackTestDecoder: newPlaybackTestDecoder(),
		release:             make(chan struct{}),
	}
	if err := p.preloadPipelineForGeneration(&trackPipeline{decoder: dec, stream: dec}, 0); err != nil {
		t.Fatalf("preloadPipelineForGeneration: %v", err)
	}
	p.ClearPreload()

	closedDone := make(chan struct{})
	go func() {
		p.Close()
		close(closedDone)
	}()

	select {
	case <-closedDone:
		t.Fatal("Player.Close returned before detached close finished")
	case <-time.After(30 * time.Millisecond):
	}

	close(dec.release)
	select {
	case <-closedDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Player.Close did not return after detached close completed")
	}
}

func TestSpeederResetOnReplaceClearAndSeek(t *testing.T) {
	seedSpeeder := func(ss *speedStreamer) {
		ss.tailValid = true
		ss.inN = 100
		ss.inPos = 50
		ss.outRd = 10
		ss.outWr = 40
		ss.tail[0] = [2]float64{1, 1}
	}
	assertCleared := func(t *testing.T, ss *speedStreamer) {
		t.Helper()
		if ss.tailValid || ss.inN != 0 || ss.inPos != 0 || ss.outRd != 0 || ss.outWr != 0 {
			t.Fatalf("speeder not reset: tailValid=%v inN=%d inPos=%v outRd=%d outWr=%d", ss.tailValid, ss.inN, ss.inPos, ss.outRd, ss.outWr)
		}
	}

	tests := []struct {
		name string
		act  func(p *Player)
	}{
		{
			name: "Replace via playPipelineForGeneration",
			act: func(p *Player) {
				dec := newPlaybackTestDecoder()
				_ = p.playPipelineForGeneration(&trackPipeline{decoder: dec, stream: dec}, 0)
			},
		},
		{
			name: "Clear via Stop",
			act: func(p *Player) {
				p.Stop()
			},
		},
		{
			name: "native Seek",
			act: func(p *Player) {
				_ = p.Seek(time.Second)
			},
		},
		{
			name: "prepared FFmpeg Seek",
			act: func(p *Player) {
				seeker := preparedSeekTestDecoder{playbackTestDecoder: newPlaybackTestDecoder()}
				cur := &trackPipeline{
					decoder:  seeker,
					stream:   seeker,
					format:   beep.Format{SampleRate: 100, NumChannels: 2, Precision: 2},
					seekable: true,
				}
				p.mu.Lock()
				p.current = cur
				p.gapless.Replace(cur.stream)
				p.mu.Unlock()
				_ = p.commitPreparedSeek(cur, seeker, &preparedFFmpegSeek{})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newTestPlayer()
			p.sr = 100
			p.gapless = &gaplessStreamer{}
			p.speeder = newSpeedStreamer(p.gapless, &p.speed)
			p.ctrl = &beep.Ctrl{}
			p.started = true
			p.suspended = true
			curDec := newPlaybackTestDecoder()
			p.current = &trackPipeline{
				decoder:  curDec,
				stream:   curDec,
				format:   beep.Format{SampleRate: 100, NumChannels: 2, Precision: 2},
				seekable: true,
			}
			p.gapless.Replace(curDec)

			seedSpeeder(p.speeder)
			tt.act(p)
			assertCleared(t, p.speeder)
			p.Close()
		})
	}
}

func TestStartStreamMetadataCancelsPreviousPoller(t *testing.T) {
	p := newTestPlayer()
	p.gapless = &gaplessStreamer{}
	p.suspended = true

	var firstCancelled atomic.Bool
	p.mu.Lock()
	p.metaCancel = func() { firstCancelled.Store(true) }
	p.mu.Unlock()

	var secondCtx context.Context
	started := make(chan struct{}, 1)
	p.RegisterStreamMetadataResolver(func(string) (func(context.Context) (string, error), time.Duration, bool) {
		return func(ctx context.Context) (string, error) {
			secondCtx = ctx
			select {
			case started <- struct{}{}:
			default:
			}
			<-ctx.Done()
			return "", ctx.Err()
		}, time.Hour, true
	})

	p.startStreamMetadata("https://radio.example/live")
	if !firstCancelled.Load() {
		t.Fatal("startStreamMetadata did not cancel prior metaCancel")
	}
	<-started

	p.Stop()
	if secondCtx.Err() == nil {
		t.Fatal("Stop did not cancel active metadata poller")
	}
}

func TestSeekConcurrentWithStreamRaceFree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX shell fixtures")
	}
	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "ffmpeg"), "#!/bin/sh\ndd if=/dev/zero bs=4 count=2000 2>/dev/null\n")
	writeExecutable(t, filepath.Join(dir, "ffprobe"), "#!/bin/sh\nprintf '20\\n'\n")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	decoder, format, err := decodeFFmpegLocal(filepath.Join(dir, "track.m4a"), beep.SampleRate(100), 16)
	if err != nil {
		t.Fatalf("decodeFFmpegLocal() error = %v", err)
	}
	tp := &trackPipeline{decoder: decoder, stream: decoder, format: format, seekable: true}
	p := newTestPlayer()
	p.sr = 100
	p.gapless = &gaplessStreamer{}
	p.ctrl = &beep.Ctrl{}
	p.started = true
	p.suspended = true
	p.current = tp
	p.gapless.Replace(tp.stream)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := make([][2]float64, 16)
		for {
			select {
			case <-stop:
				return
			default:
				speaker.Lock()
				p.gapless.Stream(buf)
				speaker.Unlock()
			}
		}
	}()

	for range 5 {
		if err := p.Seek(time.Second); err != nil {
			close(stop)
			wg.Wait()
			p.Close()
			t.Fatalf("Seek() error = %v", err)
		}
		_, _ = p.PositionAndDuration()
	}

	close(stop)
	wg.Wait()
	p.Close()
}

func TestPositionAndDurationAndStreamErrSkipSpeakerLock(t *testing.T) {
	wantErr := errors.New("pipe error")
	format := beep.Format{SampleRate: 100, NumChannels: 2, Precision: 2}

	makeState := func(pos int, err error) *pipeStreamState {
		st := newPipeStreamState(pos)
		st.err.publish(err)
		return st
	}

	localPipe := &localFFmpegStreamer{sr: format.SampleRate}
	localPipe.setState(makeState(250, wantErr))
	localPipe.setTotal(1000)

	navPipe := &navFFmpegStreamer{sr: format.SampleRate}
	navPipe.setState(makeState(150, wantErr))
	navPipe.probed.Store(800)

	streamPipe := &ffmpegPipeStreamer{}
	streamPipe.setState(makeState(300, wantErr))

	ytdlPipe := &ytdlPipeStreamer{}
	ytdlPipe.setState(makeState(200, wantErr))

	prefetch := &livePrefetchStreamer{sampleRate: format.SampleRate, consumed: 400, err: wantErr}

	tests := []struct {
		name    string
		tp      *trackPipeline
		wantPos time.Duration
		wantDur time.Duration
	}{
		{
			name:    "livePrefetch",
			tp:      &trackPipeline{decoder: newPlaybackTestDecoder(), format: format, livePrefetch: prefetch, knownDuration: 30 * time.Second},
			wantPos: 4 * time.Second,
			wantDur: 30 * time.Second,
		},
		{
			name:    "localFFmpegStreamer",
			tp:      &trackPipeline{decoder: localPipe, format: format},
			wantPos: 2500 * time.Millisecond,
			wantDur: 10 * time.Second,
		},
		{
			name:    "navFFmpegStreamer",
			tp:      &trackPipeline{decoder: navPipe, format: format},
			wantPos: 1500 * time.Millisecond,
			wantDur: 8 * time.Second,
		},
		{
			name:    "ffmpegPipeStreamer",
			tp:      &trackPipeline{decoder: streamPipe, format: format, knownDuration: 20 * time.Second},
			wantPos: 3 * time.Second,
			wantDur: 20 * time.Second,
		},
		{
			name:    "ytdlPipeStreamer",
			tp:      &trackPipeline{decoder: ytdlPipe, format: format, streamOffset: time.Minute, knownDuration: 2 * time.Minute},
			wantPos: time.Minute + 2*time.Second,
			wantDur: 2 * time.Minute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newTestPlayer()
			p.current = tt.tp

			// Hold the speaker lock so any path that tries to lock it blocks.
			speaker.Lock()
			defer speaker.Unlock()

			done := make(chan struct{})
			var pos, dur, posOnly, durOnly time.Duration
			var streamErr error
			go func() {
				defer close(done)
				pos, dur = p.PositionAndDuration()
				posOnly = p.Position()
				durOnly = p.Duration()
				streamErr = p.StreamErr()
			}()

			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatalf("%s blocked on speaker.Lock()", tt.name)
			}

			if pos != tt.wantPos || dur != tt.wantDur || posOnly != tt.wantPos || durOnly != tt.wantDur {
				t.Fatalf("got pos=(%v,%v) dur=(%v,%v), want (%v, %v)", pos, posOnly, dur, durOnly, tt.wantPos, tt.wantDur)
			}
			if !errors.Is(streamErr, wantErr) {
				t.Fatalf("StreamErr() = %v, want %v", streamErr, wantErr)
			}
		})
	}
}

func TestPlayerSetTapEnabledAndLockFreeReads(t *testing.T) {
	p := newTestPlayer()
	p.sr = 44100
	p.gapless = &gaplessStreamer{}
	p.tapBufferFrames = 64
	p.suspended = true
	t.Cleanup(speaker.Clear)

	// A call to SetTapEnabled(false) before first play propagates to the new tap.
	p.SetTapEnabled(false)
	dec := &levelDecoder{level: 0.5, left: -1}
	if err := p.playPipelineForGeneration(&trackPipeline{decoder: dec, stream: dec}, 0); err != nil {
		t.Fatalf("playPipelineForGeneration: %v", err)
	}
	tp := p.tap.Load()
	if tp == nil {
		t.Fatal("tap was not published")
	}
	if !tp.disabled.Load() {
		t.Fatal("tap started enabled despite SetTapEnabled(false) before play")
	}

	// Enable tap, feed frames, and read while p.mu is held.
	p.SetTapEnabled(true)
	if tp.disabled.Load() {
		t.Fatal("SetTapEnabled(true) did not enable tap")
	}
	tp.Stream(make([][2]float64, 8))

	p.mu.Lock()
	done := make(chan struct{})
	var nMono, nWave, nStereo int
	go func() {
		defer close(done)
		nMono = p.SamplesInto(make([]float64, 4))
		nWave = p.WaveformSamplesInto(make([]float64, 4))
		nStereo = p.StereoSamplesInto(make([][2]float64, 4))
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		p.mu.Unlock()
		t.Fatal("tap sample readers blocked on p.mu")
	}
	p.mu.Unlock()

	if nMono != 4 || nWave != 4 || nStereo != 4 {
		t.Fatalf("sample counts = (%d, %d, %d), want (4, 4, 4)", nMono, nWave, nStereo)
	}
	p.Close()
}

type interruptTrackingPipeline struct {
	*playbackTestDecoder
	interrupted atomic.Bool
}

func TestCommitPreparedSeekAbortsWhenGaplessTransitioned(t *testing.T) {
	p := newTestPlayer()
	p.sr = 100
	p.gapless = &gaplessStreamer{}
	p.speeder = newSpeedStreamer(p.gapless, &p.speed)
	p.ctrl = &beep.Ctrl{}
	p.started = true
	p.suspended = true

	seeker := preparedSeekTestDecoder{playbackTestDecoder: newPlaybackTestDecoder()}
	cur := &trackPipeline{
		decoder:  seeker,
		stream:   seeker,
		format:   beep.Format{SampleRate: 100, NumChannels: 2, Precision: 2},
		seekable: true,
	}
	nextDec := &interruptTrackingPipeline{playbackTestDecoder: newPlaybackTestDecoder()}
	next := &trackPipeline{
		decoder:         nextDec,
		stream:          nextDec,
		sourceInterrupt: func() { nextDec.interrupted.Store(true) },
	}

	// Simulate gapless having already transitioned g.current to next while
	// handleGaplessSwap has not yet acquired p.mu to update p.current.
	p.mu.Lock()
	p.current = cur
	p.nextPipeline = next
	p.gapless.Replace(next.stream)
	p.mu.Unlock()

	if err := p.commitPreparedSeek(cur, seeker, &preparedFFmpegSeek{}); err != nil {
		t.Fatalf("commitPreparedSeek() error = %v", err)
	}
	if nextDec.interrupted.Load() {
		t.Fatal("commitPreparedSeek interrupted newly active next track")
	}
	p.Close()
}

type seekableRampDecoder struct {
	samples [][2]float64
	pos     int
}

func (d *seekableRampDecoder) Stream(out [][2]float64) (int, bool) {
	if d.pos >= len(d.samples) {
		return 0, false
	}
	n := copy(out, d.samples[d.pos:])
	d.pos += n
	return n, true
}

func (d *seekableRampDecoder) Err() error    { return nil }
func (d *seekableRampDecoder) Len() int      { return len(d.samples) }
func (d *seekableRampDecoder) Position() int { return d.pos }
func (d *seekableRampDecoder) Close() error  { return nil }
func (d *seekableRampDecoder) Seek(p int) error {
	if p < 0 || p > len(d.samples) {
		return errors.New("out of range")
	}
	d.pos = p
	return nil
}

func TestNativeSeekResetsResamplerAndClearsDrained(t *testing.T) {
	p := newTestPlayer()
	p.sr = 48000
	p.gapless = &gaplessStreamer{}
	p.speeder = newSpeedStreamer(p.gapless, &p.speed)
	p.ctrl = &beep.Ctrl{}
	p.started = true
	p.suspended = true

	// Source at 44100 Hz so polyphaseResampler wraps the decoder.
	samples := make([][2]float64, 44100)
	for i := range samples {
		if i < 22050 {
			samples[i] = [2]float64{0.25, 0.25}
		} else {
			samples[i] = [2]float64{0.75, 0.75}
		}
	}
	dec := &seekableRampDecoder{samples: samples}
	stream := resampleWithHeadroom(4, 44100, 48000, dec)
	cur := &trackPipeline{
		decoder:  dec,
		stream:   stream,
		format:   beep.Format{SampleRate: 44100, NumChannels: 2, Precision: 2},
		seekable: true,
	}
	p.mu.Lock()
	p.current = cur
	p.gapless.Replace(cur.stream)
	p.mu.Unlock()

	// Drain the stream completely so polyphaseResampler sets eof=true and gapless drains.
	buf := make([][2]float64, 4096)
	for !p.Drained() {
		p.gapless.Stream(buf)
	}

	// Seek back to start.
	if err := p.Seek(-2 * time.Second); err != nil {
		t.Fatalf("Seek() error = %v", err)
	}
	if p.Drained() {
		t.Fatal("Drained() = true after backward seek")
	}
	// Stream after seek must produce non-zero samples from the beginning (~0.25) and not drain.
	out := make([][2]float64, 2048)
	p.gapless.Stream(out)
	if p.Drained() {
		t.Fatal("stream prematurely drained right after backward seek")
	}
	var sum float64
	for _, s := range out[512:] {
		sum += s[0]
	}
	avg := sum / float64(len(out[512:]))
	if avg < 0.1 || avg > 0.35 {
		t.Fatalf("average sample after backward seek = %v, want ~0.25 (stale buffer or silence)", avg)
	}
	p.Close()
}

func TestMuteAndRestoreYTDLSeekClearPreload(t *testing.T) {
	p := newTestPlayer()
	p.sr = 48000
	p.gapless = &gaplessStreamer{}
	p.speeder = newSpeedStreamer(p.gapless, &p.speed)
	p.ctrl = &beep.Ctrl{}
	p.started = true
	p.suspended = true

	curDec := newPlaybackTestDecoder()
	cur := &trackPipeline{decoder: curDec, stream: curDec, ytdlSeek: true}
	nextDec := newPlaybackTestDecoder()
	next := &trackPipeline{decoder: nextDec, stream: nextDec}

	p.mu.Lock()
	p.current = cur
	p.nextPipeline = next
	p.gapless.Replace(cur.stream)
	p.gapless.SetNext(next.stream)
	p.mu.Unlock()

	gen := p.seekGen.Load()
	beforePreloadGen := p.preloadGen.Load()
	if _, ok := p.muteYTDLSeekSource(cur, gen); !ok {
		t.Fatal("muteYTDLSeekSource() = false, want true")
	}
	if p.HasPreload() {
		t.Fatal("HasPreload() = true after muteYTDLSeekSource")
	}
	if p.preloadGen.Load() == beforePreloadGen {
		t.Fatal("preloadGen did not increment on muteYTDLSeekSource")
	}

	// Re-arm a preload while muted, then restore and ensure it is cleared.
	nextDec2 := newPlaybackTestDecoder()
	next2 := &trackPipeline{decoder: nextDec2, stream: nextDec2}
	p.mu.Lock()
	p.nextPipeline = next2
	p.gapless.SetNext(next2.stream)
	p.mu.Unlock()

	beforeRestoreGen := p.preloadGen.Load()
	p.restoreYTDLSeekSource(cur, gen)
	if p.HasPreload() {
		t.Fatal("HasPreload() = true after restoreYTDLSeekSource")
	}
	if p.preloadGen.Load() == beforeRestoreGen {
		t.Fatal("preloadGen did not increment on restoreYTDLSeekSource")
	}
	p.Close()
}
