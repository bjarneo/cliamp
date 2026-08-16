package player

import (
	"math"
	"sync"
	"testing"
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

func TestSampleRate(t *testing.T) {
	p := &Player{}
	p.rate.Store(44100)
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
