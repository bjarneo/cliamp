package player

import (
	"bufio"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
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
		{12, 6}, // above max
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
		{5, 0}, // above max (must be ≤ 0)
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

func TestPlayerBlockedNavStreamCanBeInterruptedBeforeSpeakerLock(t *testing.T) {
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
				return p.playPipeline(tp)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			old, audioStarted, audioDone, closeInput := blockedNavPlayback(t)
			defer closeInput()
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
				old.interrupt()
				<-audioDone
				t.Fatal("playback operation deadlocked behind blocked nav Stream")
			}
			select {
			case <-audioDone:
			case <-time.After(time.Second):
				t.Fatal("blocked nav Stream was not released")
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

func blockedNavPlayback(t *testing.T) (*trackPipeline, <-chan struct{}, <-chan struct{}, func()) {
	t.Helper()
	reader, writer := io.Pipe()
	decoder := &navFFmpegStreamer{
		ffmpegPipe: ffmpegPipe{
			reader: bufio.NewReader(reader),
			pipe:   reader,
			state:  newPipeStreamState(0),
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
				decoder, _, err = decodeNavFFmpeg(nb, 100, 16, 1000)
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
			default:
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
